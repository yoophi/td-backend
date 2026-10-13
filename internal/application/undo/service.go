// Package undo applies conditional inverses in the original operation's project.
package undo

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type Service struct{ Runner operation.Runner }
type Command struct {
	Reason string `json:"reason"`
}
type Result struct{ AffectedIssues []domain.Issue }
type plan func(operation.CommandContext) (*domain.Issue, error)

func (s *Service) Run(ctx context.Context, c domain.Caller, m port.Mutation, key string, cmd Command) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "operation.undo", func(command operation.CommandContext) (any, error) {
		if !domain.ValidOperationKey(key) {
			return nil, &domain.ValidationError{Fields: []domain.FieldError{{Field: "operation_id", Rule: "invalid", Message: "UUIDv4 operation key required"}}}
		}
		if strings.TrimSpace(cmd.Reason) == "" {
			return nil, &domain.ValidationError{Fields: []domain.FieldError{{Field: "reason", Rule: "invalid", Message: "nonblank reason required"}}}
		}
		tx := command.Tx
		receipt, e := tx.Operations().AuditGet(tx.Context(), c.ProjectID, key)
		if e != nil {
			return nil, e
		}
		if receipt.Kind == "operation.undo" || receipt.Kind == "project.create" || receipt.Kind == "session.create" || receipt.Kind == "session.heartbeat" {
			return nil, domain.ErrConflict
		}
		effects, e := tx.Operations().Effects(tx.Context(), c.ProjectID, receipt.RowID)
		if e != nil {
			return nil, e
		}
		if len(effects) == 0 {
			return nil, domain.ErrConflict
		}
		plans := []plan{}
		// Prepare every aggregate before writing any inverse. The transaction also
		// rolls back all writes if an inverse, receipt or deferred FK later fails.
		for _, effect := range effects {
			p, e := prepare(tx, c, effect, key, strings.TrimSpace(cmd.Reason))
			if e != nil {
				return nil, e
			}
			plans = append(plans, p)
		}
		out := Result{AffectedIssues: []domain.Issue{}}
		for _, p := range plans {
			i, e := p(command)
			if e != nil {
				return nil, e
			}
			if i != nil {
				out.AffectedIssues = append(out.AffectedIssues, *i)
			}
		}
		if e = validateGraph(tx, c, effects); e != nil {
			return nil, e
		}
		if e = tx.OperationWrites().RecordUndo(tx.Context(), c.ProjectID, command.RowID, receipt.RowID, strings.TrimSpace(cmd.Reason)); e != nil {
			return nil, e
		}
		return out, nil
	})
}
func observed(effect port.Effect, revision int64) error {
	if effect.AfterRevision != domain.Revision(revision) {
		return domain.ErrConflict
	}
	return nil
}
func decode(data []byte, target any) error {
	if e := json.Unmarshal(data, target); e != nil {
		return domain.ErrConflict
	}
	return nil
}
func inverse(command operation.CommandContext, c domain.Caller, effect port.Effect, before, after any, revision int64) error {
	old, e := json.Marshal(before)
	if e != nil {
		return e
	}
	next, e := json.Marshal(after)
	if e != nil {
		return e
	}
	effect.OperationRowID = command.RowID
	effect.AfterRevision = domain.Revision(revision)
	effect.BeforeJSON = old
	effect.AfterJSON = next
	return command.Tx.OperationWrites().SaveEffect(command.Tx.Context(), effect)
}
