// Package focus owns project/Session focus and its internal undo revision.
package focus

import (
	"context"
	"encoding/json"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type Service struct {
	Reads  port.ReadStore
	Runner operation.Runner
}
type SetCommand struct {
	IssueID *string `json:"issue_id"`
}
type Result struct{ Focus domain.SessionFocus }

// State version 1 retains cleared focus rows so later undo can detect new edits.
type State struct {
	Focus domain.SessionFocus `json:"focus"`
}

func (s *Service) Get(ctx context.Context, c domain.Caller) (domain.SessionFocus, error) {
	var out domain.SessionFocus
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		var e error
		out, e = r.Focus().Get(r.Context(), c.ProjectID, c.SessionID)
		return e
	})
	return out, e
}
func (s *Service) Set(ctx context.Context, c domain.Caller, m port.Mutation, cmd SetCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "focus.set", func(command operation.CommandContext) (any, error) {
		tx := command.Tx
		if cmd.IssueID != nil {
			if *cmd.IssueID == "" {
				return nil, &domain.ValidationError{Fields: []domain.FieldError{{Field: "issue_id", Rule: "invalid", Message: "nonempty issue ID or null required"}}}
			}
			if _, e := tx.Issues().Get(tx.Context(), c.ProjectID, *cmd.IssueID, false); e != nil {
				return nil, e
			}
		}
		before, e := tx.Focus().Get(tx.Context(), c.ProjectID, c.SessionID)
		if e != nil {
			return nil, e
		}
		after := domain.SessionFocus{SessionID: c.SessionID, IssueID: cmd.IssueID, Revision: before.Revision + 1}
		if e = tx.FocusWrites().Save(tx.Context(), c.ProjectID, after, before.Revision); e != nil {
			return nil, e
		}
		var previous []byte
		if before.Revision > 0 {
			if previous, e = json.Marshal(State{before}); e != nil {
				return nil, e
			}
		}
		next, e := json.Marshal(State{after})
		if e != nil {
			return nil, e
		}
		if e = tx.OperationWrites().SaveEffect(tx.Context(), port.Effect{ProjectID: c.ProjectID, OperationRowID: command.RowID, EntityKind: "focus", EntityID: c.SessionID, AfterRevision: domain.Revision(after.Revision), StateVersion: 1, BeforeJSON: previous, AfterJSON: next}); e != nil {
			return nil, e
		}
		return Result{after}, nil
	})
}
