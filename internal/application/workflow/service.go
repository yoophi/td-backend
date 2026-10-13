package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type Service struct {
	Reads  port.ReadStore
	Runner operation.Runner
	ID     func(string) (string, error)
}
type TransitionCommand struct {
	TargetStatus       *domain.Status `json:"-"`
	ReviewedBy         string         `json:"reviewed_by"`
	SelfReview         bool           `json:"self_review"`
	RecordOnly         bool           `json:"record_only"`
	AdminReason        string         `json:"admin_reason"`
	SelfCloseException string         `json:"self_close_exception"`
	Reason             string         `json:"reason"`
	Force              bool           `json:"force"`
	Snapshot           *port.Snapshot `json:"snapshot"`
}
type HandoffCommand struct {
	Done         []string       `json:"done"`
	Remaining    []string       `json:"remaining"`
	Decisions    []string       `json:"decisions"`
	Uncertain    []string       `json:"uncertain"`
	Snapshot     *port.Snapshot `json:"snapshot"`
	SubmitReview *bool          `json:"submit_review"`
}
type TransitionResult struct {
	Issue                              domain.Issue
	DescendantStatusUpdates            []domain.Issue
	Noop                               bool
	ParentStatusUpdates, AutoUnblocked []domain.Issue
}
type HandoffResult struct {
	Issue           domain.Issue
	Handoff         port.Activity
	ReviewRequested bool
}

func invalid(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}
func validateSnapshot(s *port.Snapshot) error {
	if s != nil && s.DirtyFiles < 0 {
		return invalid("snapshot.dirty_files", "must be nonnegative")
	}
	return nil
}
func observed(command operation.CommandContext, c domain.Caller, id, revision string) (Facts, error) {
	expected, e := domain.Observe(revision)
	if e != nil {
		return Facts{}, e
	}
	facts, e := LoadFacts(command.Tx.Context(), command.Tx, c, id)
	if e != nil {
		return facts, e
	}
	if facts.Issue.Revision != expected {
		return facts, domain.ErrStale
	}
	return facts, nil
}
func persist(command operation.CommandContext, c domain.Caller, before domain.Issue, after *domain.Issue) error {
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
	return issue.SaveEffect(command, c.ProjectID, &issue.State{Issue: before, Dependencies: deps}, issue.State{Issue: *after, Dependencies: deps})
}
func invalidate(command operation.CommandContext, c domain.Caller, i *domain.Issue) error {
	i.ContentVersion++
	i.ReviewerSession = ""
	i.ReviewedAt = nil
	return command.Tx.IssueWrites().SupersedeReviews(command.Tx.Context(), c.ProjectID, i.ID, command.Now)
}
func (s *Service) history(command operation.CommandContext, c domain.Caller, i domain.Issue, action domain.IssueSessionAction) error {
	return command.Tx.WorkflowWrites().RecordHistory(command.Tx.Context(), c.ProjectID, domain.IssueSessionHistory{ID: command.RowID + ":" + string(action) + ":" + i.ID, IssueID: i.ID, SessionID: c.SessionID, Action: action, CreatedAt: command.Now}, command.RowID)
}
func (s *Service) activity(command operation.CommandContext, c domain.Caller, i domain.Issue, kind, message string, snapshot *port.Snapshot) (port.Activity, error) {
	id, e := s.ID("activity")
	if e != nil {
		return port.Activity{}, e
	}
	a := port.Activity{ID: id, IssueID: i.ID, SessionID: c.SessionID, OperationRowID: command.RowID, Kind: kind, Message: message, SnapshotEvent: kind, Author: c.Actor.Name, ContentVersion: i.ContentVersion, CreatedAt: command.Now, UpdatedAt: command.Now, Snapshot: snapshot}
	if kind == "log" {
		a.LogType = domain.LogTypeProgress
	}
	return a, nil
}
func (s *Service) Transition(ctx context.Context, c domain.Caller, m port.Mutation, id, revision, action string, cmd TransitionCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "workflow.transition", func(command operation.CommandContext) (any, error) {
		if e := validateTransition(&cmd, action); e != nil {
			return nil, e
		}
		f, e := observed(command, c, id, revision)
		if e != nil {
			return nil, e
		}
		i := f.Issue
		before := i
		result := TransitionResult{ParentStatusUpdates: []domain.Issue{}, AutoUnblocked: []domain.Issue{}}
		var history domain.IssueSessionAction
		if cmd.TargetStatus != nil {
			if *cmd.TargetStatus == i.Status {
				result.Issue = i
				result.Noop = true
				return result, nil
			}
			action, e = statusAction(i.Status, *cmd.TargetStatus)
			if e != nil {
				return nil, e
			}
		}
		switch action {
		case "resume":
			if i.Status != domain.StatusInReview {
				return nil, domain.ErrConflict
			}
			if e = invalidate(command, c, &i); e != nil {
				return nil, e
			}
			i.Status = domain.StatusInProgress
			i.ImplementerSession = c.SessionID
			i.ReviewRequestedBySession = ""
			history = domain.ActionSessionStarted

		case "approve", "close":
			if e = s.finish(command, c, f, &i, action, cmd); e != nil {
				return nil, e
			}
		case "reject":
			if i.Status != domain.StatusInReview {
				return nil, domain.ErrConflict
			}
			if e = invalidate(command, c, &i); e != nil {
				return nil, e
			}
			i.Status = domain.StatusOpen
			i.ImplementerSession = ""
			i.ReviewRequestedBySession = ""
			i.ClosedAt = nil
			i.ClosedBySession = ""
		case "start":
			if i.Status == domain.StatusInProgress && i.ImplementerSession == c.SessionID {
				result.Issue = i
				result.Noop = true
				return result, nil
			}
			if i.Status != domain.StatusOpen && !(i.Status == domain.StatusBlocked && cmd.Force) {
				return nil, domain.ErrConflict
			}
			i.Status = domain.StatusInProgress
			i.ImplementerSession = c.SessionID
			history = domain.ActionSessionStarted
		case "unstart":
			if i.Status != domain.StatusInProgress {
				return nil, domain.ErrConflict
			}
			if i.ImplementerSession != c.SessionID {
				return nil, domain.ErrForbidden
			}
			i.Status = domain.StatusOpen
			i.ImplementerSession = ""
			history = domain.ActionSessionUnstarted
		case "block":
			if i.Status != domain.StatusOpen && i.Status != domain.StatusInProgress {
				return nil, domain.ErrConflict
			}
			i.Status = domain.StatusBlocked
		case "unblock":
			if i.Status != domain.StatusBlocked {
				return nil, domain.ErrConflict
			}
			i.Status = domain.StatusOpen
		case "reopen":
			if i.Status != domain.StatusClosed {
				return nil, domain.ErrConflict
			}
			i.Status = domain.StatusOpen
			i.ClosedAt = nil
			i.ClosedBySession = ""
			i.ReviewRequestedBySession = ""
			if e = invalidate(command, c, &i); e != nil {
				return nil, e
			}
		case "review":
			if i.Status != domain.StatusOpen && i.Status != domain.StatusInProgress {
				return nil, domain.ErrConflict
			}
			if f.Handoff == nil || f.Handoff.ContentVersion != i.ContentVersion {
				return nil, invalid("handoff", "current handoff required before review")
			}
			i.Status = domain.StatusInReview
			i.ReviewRequestedBySession = c.SessionID
			if i.ImplementerSession == "" {
				i.ImplementerSession = c.SessionID
			}
		default:
			return nil, invalid("action", "unsupported workflow action")
		}
		if history != "" {
			if e = s.history(command, c, i, history); e != nil {
				return nil, e
			}
		}
		message := strings.TrimSpace(cmd.Reason)
		if message == "" {
			message = fmt.Sprintf("Workflow: %s", action)
		}
		a, e := s.activity(command, c, i, "log", message, cmd.Snapshot)
		if e != nil {
			return nil, e
		}
		a.SnapshotEvent = action
		if action == "block" {
			a.LogType = domain.LogTypeBlocker
		}
		if (action == "approve" || action == "close") && cmd.SelfReview {
			a.LogType = domain.LogTypeSecurity
		}
		if e = command.Tx.WorkflowWrites().AppendActivity(command.Tx.Context(), c.ProjectID, a); e != nil {
			return nil, e
		}
		if e = persist(command, c, before, &i); e != nil {
			return nil, e
		}
		result.Issue = i
		if action == "review" {
			result.ParentStatusUpdates, e = s.cascadeReview(command, c, i)
			if e != nil {
				return nil, e
			}
		}
		if i.Status == domain.StatusClosed && before.Status != domain.StatusClosed {
			if action == "approve" || (action == "close" && f.HasActiveApproval() && cmd.AdminReason == "" && cmd.SelfCloseException == "") {
				result.DescendantStatusUpdates, e = s.cascadeApproval(command, c, i)
				if e != nil {
					return nil, e
				}
			}
			result.ParentStatusUpdates, result.AutoUnblocked, e = s.cascadeClose(command, c, i, result.DescendantStatusUpdates...)
			if e != nil {
				return nil, e
			}
		}
		return result, nil
	})
}
func (s *Service) Handoff(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd HandoffCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "workflow.handoff", func(command operation.CommandContext) (any, error) {
		if e := validateSnapshot(cmd.Snapshot); e != nil {
			return nil, e
		}
		f, e := observed(command, c, id, revision)
		if e != nil {
			return nil, e
		}
		i := f.Issue
		before := i
		if i.Status == domain.StatusClosed {
			return nil, domain.ErrConflict
		}
		if e = invalidate(command, c, &i); e != nil {
			return nil, e
		}
		a, e := s.activity(command, c, i, "handoff", "", cmd.Snapshot)
		if e != nil {
			return nil, e
		}
		a.Done = cmd.Done
		a.Remaining = cmd.Remaining
		a.Decisions = cmd.Decisions
		a.Uncertain = cmd.Uncertain
		if e = command.Tx.WorkflowWrites().AppendActivity(command.Tx.Context(), c.ProjectID, a); e != nil {
			return nil, e
		}
		submit := cmd.SubmitReview == nil || *cmd.SubmitReview
		requested := submit && i.Status == domain.StatusInProgress
		if requested {
			i.Status = domain.StatusInReview
			i.ReviewRequestedBySession = c.SessionID
		}
		if e = persist(command, c, before, &i); e != nil {
			return nil, e
		}
		if requested {
			if _, e = s.cascadeReview(command, c, i); e != nil {
				return nil, e
			}
		}
		return HandoffResult{Issue: i, Handoff: a, ReviewRequested: requested}, nil
	})
}
func (s *Service) cascadeReview(command operation.CommandContext, c domain.Caller, child domain.Issue) ([]domain.Issue, error) {
	changed := []domain.Issue{}
	seen := map[string]bool{child.ID: true}
	tx := command.Tx
	ctx := tx.Context()
	for parentID := child.ParentID; parentID != ""; {
		if seen[parentID] {
			return nil, domain.ErrConflict
		}
		seen[parentID] = true
		parent, e := tx.Issues().Get(ctx, c.ProjectID, parentID, false)
		if errors.Is(e, domain.ErrNotFound) {
			break
		}
		if e != nil {
			return nil, e
		}
		if parent.Type != domain.TypeEpic || parent.Status == domain.StatusInReview || parent.Status == domain.StatusClosed {
			break
		}
		related, e := tx.Issues().Context(ctx, c.ProjectID, parent.ID, false)
		if e != nil {
			return nil, e
		}
		ready := len(related.Children) > 0
		for _, sibling := range related.Children {
			if sibling.Status != domain.StatusInReview && sibling.Status != domain.StatusClosed {
				ready = false
				break
			}
		}
		if !ready {
			break
		}
		before := *parent
		parent.Status = domain.StatusInReview
		parent.ReviewRequestedBySession = c.SessionID
		if e = s.cascadeLog(command, c, *parent, "Auto-submitted for review: all children ready"); e != nil {
			return nil, e
		}
		if e = persist(command, c, before, parent); e != nil {
			return nil, e
		}
		changed = append(changed, *parent)
		parentID = parent.ParentID
	}
	return changed, nil
}
