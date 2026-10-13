package workflow

import (
	"context"
	"slices"
	"strings"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type ActivityCommand struct {
	Kind          string         `json:"kind"`
	Message       string         `json:"message"`
	LogType       domain.LogType `json:"log_type"`
	WorkSessionID string         `json:"work_session_id"`
	Done          []string       `json:"done"`
	Remaining     []string       `json:"remaining"`
	Decisions     []string       `json:"decisions"`
	Uncertain     []string       `json:"uncertain"`
	Snapshot      *port.Snapshot `json:"snapshot"`
}
type CommentCommand struct {
	Text string `json:"text"`
}
type LogCommand struct {
	Message       string         `json:"message"`
	Type          domain.LogType `json:"type"`
	WorkSessionID string         `json:"work_session_id"`
}
type ActivityResult struct {
	Issue    domain.Issue
	Activity port.Activity
}

func (s *Service) Comment(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd CommentCommand) (port.StoredReply, error) {
	return s.appendActivity(ctx, c, m, id, revision, "activity.comment", ActivityCommand{Kind: "comment", Message: cmd.Text})
}
func (s *Service) Log(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd LogCommand) (port.StoredReply, error) {
	return s.appendActivity(ctx, c, m, id, revision, "activity.log", ActivityCommand{Kind: "log", Message: cmd.Message, LogType: cmd.Type, WorkSessionID: cmd.WorkSessionID})
}
func (s *Service) AppendActivity(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd ActivityCommand) (port.StoredReply, error) {
	return s.appendActivity(ctx, c, m, id, revision, "activity.append", cmd)
}
func (s *Service) appendActivity(ctx context.Context, c domain.Caller, m port.Mutation, id, revision, replyKind string, cmd ActivityCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, replyKind, func(command operation.CommandContext) (any, error) {
		if !slices.Contains([]string{"comment", "log", "handoff"}, cmd.Kind) {
			return nil, invalid("kind", "invalid activity kind")
		}
		if cmd.Kind != "handoff" && strings.TrimSpace(cmd.Message) == "" {
			return nil, invalid("message", "nonempty message required")
		}
		if cmd.Kind == "log" && !slices.Contains([]domain.LogType{domain.LogTypeProgress, domain.LogTypeSecurity, domain.LogTypeBlocker, domain.LogTypeDecision, domain.LogTypeHypothesis, domain.LogTypeTried, domain.LogTypeResult, domain.LogTypeOrchestration}, cmd.LogType) {
			return nil, invalid("log_type", "valid log type required")
		}
		if cmd.Kind != "log" && cmd.LogType != "" {
			return nil, invalid("log_type", "only log accepts log_type")
		}
		if cmd.Kind != "handoff" && (len(cmd.Done)+len(cmd.Remaining)+len(cmd.Decisions)+len(cmd.Uncertain) > 0) {
			return nil, invalid("kind", "handoff fields require handoff kind")
		}
		if e := validateSnapshot(cmd.Snapshot); e != nil {
			return nil, e
		}
		f, e := observed(command, c, id, revision)
		if e != nil {
			return nil, e
		}
		i := f.Issue
		before := i
		tx := command.Tx
		ctx := tx.Context()
		if cmd.WorkSessionID != "" {
			ws, e := tx.Workflow().WorkSessionAccess(ctx, c.ProjectID, cmd.WorkSessionID)
			if e != nil {
				return nil, e
			}
			if ws.SessionID != c.SessionID {
				return nil, domain.ErrForbidden
			}
			if ws.Ended {
				return nil, domain.ErrConflict
			}
		}
		if cmd.Kind == "handoff" {
			if i.Status == domain.StatusClosed {
				return nil, domain.ErrConflict
			}
			if e = invalidate(command, c, &i); e != nil {
				return nil, e
			}
		}
		a, e := s.activity(command, c, i, cmd.Kind, cmd.Message, cmd.Snapshot)
		if e != nil {
			return nil, e
		}
		a.OperationID = m.Key
		a.WorkSessionID = cmd.WorkSessionID
		a.LogType = cmd.LogType
		a.Done = cmd.Done
		a.Remaining = cmd.Remaining
		a.Decisions = cmd.Decisions
		a.Uncertain = cmd.Uncertain
		if e = tx.WorkflowWrites().AppendActivity(ctx, c.ProjectID, a); e != nil {
			return nil, e
		}
		if e = persistActivity(command, c, before, &i, []port.Activity{}, []port.Activity{a}); e != nil {
			return nil, e
		}
		return ActivityResult{Issue: i, Activity: a}, nil
	})
}
func persistActivity(command operation.CommandContext, c domain.Caller, before domain.Issue, after *domain.Issue, previous, next []port.Activity) error {
	tx := command.Tx
	ctx := tx.Context()
	deps, e := tx.Issues().Dependencies(ctx, c.ProjectID, before.ID)
	if e != nil {
		return e
	}
	after.Revision = before.Revision + 1
	after.UpdatedAt = command.Now
	if e = tx.IssueWrites().Save(ctx, c.ProjectID, *after, before.Revision); e != nil {
		return e
	}
	return issue.SaveEffect(command, c.ProjectID, &issue.State{Issue: before, Dependencies: deps, ActivityChanges: previous}, issue.State{Issue: *after, Dependencies: deps, ActivityChanges: next})
}
func (s *Service) DeleteComment(ctx context.Context, c domain.Caller, m port.Mutation, id, revision, comment string) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "activity.delete", func(command operation.CommandContext) (any, error) {
		f, e := observed(command, c, id, revision)
		if e != nil {
			return nil, e
		}
		tx := command.Tx
		ctx := tx.Context()
		activities, e := tx.Workflow().Activities(ctx, c.ProjectID, id, false)
		if e != nil {
			return nil, e
		}
		for _, a := range activities {
			if a.ID == comment && a.Kind == "comment" {
				before := a
				if e = tx.WorkflowWrites().DeleteComment(ctx, c.ProjectID, id, comment, command.Now); e != nil {
					return nil, e
				}
				a.DeletedAt = &command.Now
				a.UpdatedAt = command.Now
				i := f.Issue
				if e = persistActivity(command, c, f.Issue, &i, []port.Activity{before}, []port.Activity{a}); e != nil {
					return nil, e
				}
				return ActivityResult{Issue: i, Activity: a}, nil
			}
		}
		return nil, domain.ErrNotFound
	})
}
func (s *Service) ListActivity(ctx context.Context, c domain.Caller, id string) ([]port.Activity, error) {
	var result []port.Activity
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		ctx := read.Context()
		if e := access.Authorize(ctx, read, c, true, false); e != nil {
			return e
		}
		if _, e := read.Issues().Get(ctx, c.ProjectID, id, false); e != nil {
			return e
		}
		var e error
		result, e = read.Workflow().Activities(ctx, c.ProjectID, id, false)
		return e
	})
	return result, e
}
