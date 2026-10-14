// Package worksession owns shared multi-issue work sessions. Active selection
// and local worktree paths remain client context.
package worksession

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type Service struct {
	Reads  port.ReadStore
	Runner operation.Runner
	ID     func(string) (string, error)
}
type StartCommand struct {
	Name     string `json:"name"`
	StartSHA string `json:"start_sha"`
}
type TagCommand struct {
	IssueIDs *[]string `json:"issue_ids"`
}
type EndCommand struct {
	EndSHA string `json:"end_sha"`
}

// State version 1 preserves internal revision and original tag timestamps for undo.
type State struct {
	WorkSession domain.WorkSession        `json:"work_session"`
	Tags        []domain.WorkSessionIssue `json:"tags"`
}
type Result struct{ State State }

func invalid(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}
func (s *Service) List(ctx context.Context, c domain.Caller) ([]domain.WorkSession, error) {
	var out []domain.WorkSession
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		var e error
		out, e = r.WorkSessions().List(r.Context(), c.ProjectID)
		return e
	})
	return out, e
}
func load(r port.ReadSnapshot, project, id string) (State, error) {
	w, e := r.WorkSessions().Get(r.Context(), project, id)
	if e != nil {
		return State{}, e
	}
	tags, e := r.WorkSessions().Tags(r.Context(), project, id)
	return State{w, tags}, e
}
func (s *Service) Get(ctx context.Context, c domain.Caller, id string) (State, error) {
	var out State
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		var e error
		out, e = load(r, c.ProjectID, id)
		return e
	})
	return out, e
}
func (s *Service) ListActivity(ctx context.Context, c domain.Caller, id string) ([]port.Activity, error) {
	var out []port.Activity
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		if _, e := r.WorkSessions().Get(r.Context(), c.ProjectID, id); e != nil {
			return e
		}
		var e error
		out, e = r.Workflow().WorkSessionActivities(r.Context(), c.ProjectID, id)
		return e
	})
	return out, e
}
func (s *Service) Start(ctx context.Context, c domain.Caller, m port.Mutation, cmd StartCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "work_session.start", func(command operation.CommandContext) (any, error) {
		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			return nil, invalid("name", "nonblank work session name required")
		}
		id, e := s.ID("work_session")
		if e != nil {
			return nil, e
		}
		state := State{WorkSession: domain.WorkSession{ID: id, Name: name, SessionID: c.SessionID, Revision: 1, StartedAt: command.Now, StartSHA: cmd.StartSHA}, Tags: []domain.WorkSessionIssue{}}
		if e = command.Tx.WorkSessionWrites().Save(command.Tx.Context(), c.ProjectID, state.WorkSession, 0); e != nil {
			return nil, e
		}
		if e = saveEffect(command, c.ProjectID, nil, state); e != nil {
			return nil, e
		}
		return Result{state}, nil
	})
}
func (s *Service) Tag(ctx context.Context, c domain.Caller, m port.Mutation, id string, cmd TagCommand) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, "work_session.tag", func(command operation.CommandContext, state *State) error {
		if cmd.IssueIDs == nil {
			return invalid("issue_ids", "issue_ids array required; empty clears tags")
		}
		ids := append([]string{}, (*cmd.IssueIDs)...)
		slices.Sort(ids)
		old := map[string]domain.WorkSessionIssue{}
		for _, tag := range state.Tags {
			old[tag.IssueID] = tag
		}
		tags := []domain.WorkSessionIssue{}
		for i, id := range ids {
			if id == "" || (i > 0 && ids[i-1] == id) {
				return invalid("issue_ids", "nonempty unique issue IDs required")
			}
			if _, e := command.Tx.Issues().Get(command.Tx.Context(), c.ProjectID, id, false); e != nil {
				return e
			}
			tag, ok := old[id]
			if !ok {
				tag = domain.WorkSessionIssue{WorkSessionID: state.WorkSession.ID, IssueID: id, TaggedAt: command.Now}
			}
			tags = append(tags, tag)
		}
		state.Tags = tags
		return command.Tx.WorkSessionWrites().ReplaceTags(command.Tx.Context(), c.ProjectID, state.WorkSession.ID, tags)
	})
}
func (s *Service) End(ctx context.Context, c domain.Caller, m port.Mutation, id string, cmd EndCommand) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, "work_session.end", func(command operation.CommandContext, state *State) error {
		state.WorkSession.EndedAt = &command.Now
		state.WorkSession.EndSHA = cmd.EndSHA
		return nil
	})
}
func (s *Service) mutate(ctx context.Context, c domain.Caller, m port.Mutation, id, kind string, edit func(operation.CommandContext, *State) error) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, kind, func(command operation.CommandContext) (any, error) {
		before, e := load(command.Tx, c.ProjectID, id)
		if e != nil {
			return nil, e
		}
		if before.WorkSession.SessionID != c.SessionID {
			return nil, domain.ErrForbidden
		}
		if before.WorkSession.EndedAt != nil {
			return nil, domain.ErrConflict
		}
		after := State{WorkSession: before.WorkSession, Tags: append([]domain.WorkSessionIssue{}, before.Tags...)}
		if e = edit(command, &after); e != nil {
			return nil, e
		}
		after.WorkSession.Revision++
		if e = command.Tx.WorkSessionWrites().Save(command.Tx.Context(), c.ProjectID, after.WorkSession, before.WorkSession.Revision); e != nil {
			return nil, e
		}
		if e = saveEffect(command, c.ProjectID, &before, after); e != nil {
			return nil, e
		}
		return Result{after}, nil
	})
}
func saveEffect(command operation.CommandContext, project string, before *State, after State) error {
	var previous []byte
	var e error
	if before != nil {
		if previous, e = json.Marshal(before); e != nil {
			return e
		}
	}
	next, e := json.Marshal(after)
	if e != nil {
		return e
	}
	return command.Tx.OperationWrites().SaveEffect(command.Tx.Context(), port.Effect{ProjectID: project, OperationRowID: command.RowID, EntityKind: "work_session", EntityID: after.WorkSession.ID, AfterRevision: domain.Revision(after.WorkSession.Revision), StateVersion: 1, BeforeJSON: previous, AfterJSON: next})
}
