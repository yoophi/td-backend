package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yoophi/td-backend/internal/application/focus"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

// ReleaseCommand lets a supervisor release another Session's claim. The
// selector is checked again inside the write transaction, after If-Match.
type ReleaseCommand struct {
	Holder     string `json:"holder"`
	Mode       string `json:"mode"` // session or stale
	StaleNanos int64  `json:"stale_nanos,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type ReleaseResult struct {
	Issue domain.Issue
}

func (s *Service) ReleaseClaim(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd ReleaseCommand) (port.StoredReply, error) {
	if strings.TrimSpace(cmd.Holder) == "" || cmd.Holder != strings.TrimSpace(cmd.Holder) {
		return port.StoredReply{}, invalid("holder", "expected one implementer session ID")
	}
	if cmd.Mode != "session" && cmd.Mode != "stale" {
		return port.StoredReply{}, invalid("mode", "expected session or stale")
	}
	if cmd.Mode == "stale" && cmd.StaleNanos <= 0 || cmd.Mode == "session" && cmd.StaleNanos != 0 {
		return port.StoredReply{}, invalid("stale_nanos", "positive threshold required only for stale mode")
	}
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	return s.Runner.Run(ctx, c, m, "workflow.release", func(command operation.CommandContext) (any, error) {
		facts, err := observed(command, c, id, revision)
		if err != nil {
			return nil, err
		}
		issue := facts.Issue
		if issue.ImplementerSession != cmd.Holder || issue.DeletedAt != nil || (issue.Status != domain.StatusInProgress && issue.Status != domain.StatusOpen) {
			return nil, domain.ErrConflict
		}
		if cmd.Mode == "stale" {
			if err := verifyStaleClaim(command, c, issue, cmd); err != nil {
				return nil, err
			}
		}
		before := issue
		issue.Status = domain.StatusOpen
		issue.ImplementerSession = ""
		if err := s.history(command, c, issue, domain.ActionSessionUnstarted); err != nil {
			return nil, err
		}
		message := fmt.Sprintf("claim released: session %s (released by %s)", cmd.Holder, c.SessionID)
		if cmd.Mode == "stale" {
			message = fmt.Sprintf("claim released: implementer session %s idle longer than %s", cmd.Holder, time.Duration(cmd.StaleNanos))
		}
		if cmd.Reason != "" {
			message += "; " + cmd.Reason
		}
		activity, err := s.activity(command, c, issue, "log", message, nil)
		if err != nil {
			return nil, err
		}
		activity.SnapshotEvent = "unstart"
		if err := command.Tx.WorkflowWrites().AppendActivity(command.Tx.Context(), c.ProjectID, activity); err != nil {
			return nil, err
		}
		if err := persist(command, c, before, &issue); err != nil {
			return nil, err
		}
		if err := clearReleasedFocus(command, c, issue.ID); err != nil {
			return nil, err
		}
		return ReleaseResult{Issue: issue}, nil
	})
}

func clearReleasedFocus(command operation.CommandContext, c domain.Caller, id string) error {
	ctx := command.Tx.Context()
	before, err := command.Tx.Focus().Get(ctx, c.ProjectID, c.SessionID)
	if err != nil || before.IssueID == nil || *before.IssueID != id {
		return err
	}
	after := domain.SessionFocus{SessionID: c.SessionID, Revision: before.Revision + 1}
	if err := command.Tx.FocusWrites().Save(ctx, c.ProjectID, after, before.Revision); err != nil {
		return err
	}
	previous, err := json.Marshal(focus.State{Focus: before})
	if err != nil {
		return err
	}
	next, err := json.Marshal(focus.State{Focus: after})
	if err != nil {
		return err
	}
	return command.Tx.OperationWrites().SaveEffect(ctx, port.Effect{ProjectID: c.ProjectID, OperationRowID: command.RowID, EntityKind: "focus", EntityID: c.SessionID, AfterRevision: domain.Revision(after.Revision), StateVersion: 1, BeforeJSON: previous, AfterJSON: next})
}

func verifyStaleClaim(command operation.CommandContext, c domain.Caller, issue domain.Issue, cmd ReleaseCommand) error {
	ctx := command.Tx.Context()
	sessions, err := command.Tx.Sessions().List(ctx, c.ProjectID)
	if err != nil {
		return err
	}
	kin := map[string]bool{c.SessionID: true}
	for changed := true; changed; {
		changed = false
		for _, session := range sessions {
			if session.PreviousSessionID == nil {
				continue
			}
			previous := *session.PreviousSessionID
			if kin[session.ID] && !kin[previous] {
				kin[previous], changed = true, true
			}
			if kin[previous] && !kin[session.ID] {
				kin[session.ID], changed = true, true
			}
		}
	}
	if kin[cmd.Holder] {
		return domain.ErrConflict
	}
	last := issue.UpdatedAt
	for _, session := range sessions {
		if session.ID == cmd.Holder && session.LastActivity.After(last) {
			last = session.LastActivity
		}
	}
	activity, err := command.Tx.Workflow().Activities(ctx, c.ProjectID, issue.ID, true)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	for _, row := range activity {
		if row.CreatedAt.After(last) {
			last = row.CreatedAt
		}
	}
	if last.IsZero() || command.Now.Sub(last) <= time.Duration(cmd.StaleNanos) {
		return domain.ErrConflict
	}
	return nil
}
