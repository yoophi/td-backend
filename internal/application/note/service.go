// Package note owns shared note lifecycle and revisioned changes.
package note

import (
	"context"
	"encoding/json"
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
type CreateCommand struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Pinned  bool   `json:"pinned"`
}
type PatchCommand struct {
	Title    *string `json:"title"`
	Content  *string `json:"content"`
	Pinned   *bool   `json:"pinned"`
	Archived *bool   `json:"archived"`
}
type Result struct{ Note domain.Note }

// State version 1 preserves the complete note, including deletion, for undo.
type State struct {
	Note domain.Note `json:"note"`
}

func invalid(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}
func validate(n *domain.Note) error {
	n.Title = strings.TrimSpace(n.Title)
	if n.Title == "" {
		return invalid("title", "nonblank note title required")
	}
	return nil
}
func (s *Service) List(ctx context.Context, c domain.Caller) ([]domain.Note, error) {
	return s.list(ctx, c, false)
}
func (s *Service) ListIncludingDeleted(ctx context.Context, c domain.Caller) ([]domain.Note, error) {
	return s.list(ctx, c, true)
}
func (s *Service) list(ctx context.Context, c domain.Caller, includeDeleted bool) ([]domain.Note, error) {
	var result []domain.Note
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		if e := access.Authorize(read.Context(), read, c, true, false); e != nil {
			return e
		}
		var e error
		if includeDeleted {
			result, e = read.Notes().ListIncludingDeleted(read.Context(), c.ProjectID)
		} else {
			result, e = read.Notes().List(read.Context(), c.ProjectID)
		}
		return e
	})
	return result, e
}
func (s *Service) Get(ctx context.Context, c domain.Caller, id string) (domain.Note, error) {
	return s.get(ctx, c, id, false)
}
func (s *Service) GetIncludingDeleted(ctx context.Context, c domain.Caller, id string) (domain.Note, error) {
	return s.get(ctx, c, id, true)
}
func (s *Service) get(ctx context.Context, c domain.Caller, id string, includeDeleted bool) (domain.Note, error) {
	var result domain.Note
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		if e := access.Authorize(read.Context(), read, c, true, false); e != nil {
			return e
		}
		var e error
		result, e = read.Notes().Get(read.Context(), c.ProjectID, id, includeDeleted)
		return e
	})
	return result, e
}
func (s *Service) Create(ctx context.Context, c domain.Caller, m port.Mutation, cmd CreateCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "note.create", func(command operation.CommandContext) (any, error) {
		n := domain.Note{Title: cmd.Title, Content: cmd.Content, Pinned: cmd.Pinned, Revision: 1, CreatedAt: command.Now, UpdatedAt: command.Now}
		if e := validate(&n); e != nil {
			return nil, e
		}
		var e error
		if n.ID, e = s.ID("note"); e != nil {
			return nil, e
		}
		if e = command.Tx.NoteWrites().Save(command.Tx.Context(), c.ProjectID, n, 0); e != nil {
			return nil, e
		}
		if e = saveEffect(command, c.ProjectID, nil, State{n}); e != nil {
			return nil, e
		}
		return Result{n}, nil
	})
}
func (s *Service) Patch(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd PatchCommand) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, revision, "note.update", false, func(command operation.CommandContext, n *domain.Note) error {
		if cmd.Title == nil && cmd.Content == nil && cmd.Pinned == nil && cmd.Archived == nil {
			return invalid("body", "at least one field required")
		}
		if cmd.Title != nil {
			n.Title = *cmd.Title
		}
		if cmd.Content != nil {
			n.Content = *cmd.Content
		}
		if cmd.Pinned != nil {
			n.Pinned = *cmd.Pinned
		}
		if cmd.Archived != nil {
			n.Archived = *cmd.Archived
		}
		return validate(n)
	})
}
func (s *Service) Delete(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, revision, "note.delete", false, func(command operation.CommandContext, n *domain.Note) error { n.DeletedAt = &command.Now; return nil })
}
func (s *Service) Restore(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, revision, "note.restore", true, func(_ operation.CommandContext, n *domain.Note) error {
		if n.DeletedAt == nil {
			return domain.ErrConflict
		}
		n.DeletedAt = nil
		return nil
	})
}
func (s *Service) mutate(ctx context.Context, c domain.Caller, m port.Mutation, id, revision, kind string, includeDeleted bool, edit func(operation.CommandContext, *domain.Note) error) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, kind, func(command operation.CommandContext) (any, error) {
		expected, e := domain.Observe(revision)
		if e != nil {
			return nil, e
		}
		n, e := command.Tx.Notes().Get(command.Tx.Context(), c.ProjectID, id, includeDeleted)
		if e != nil {
			return nil, e
		}
		if n.Revision != expected {
			return nil, domain.ErrStale
		}
		before := State{n}
		if e = edit(command, &n); e != nil {
			return nil, e
		}
		n.Revision++
		n.UpdatedAt = command.Now
		if e = command.Tx.NoteWrites().Save(command.Tx.Context(), c.ProjectID, n, expected); e != nil {
			return nil, e
		}
		if e = saveEffect(command, c.ProjectID, &before, State{n}); e != nil {
			return nil, e
		}
		return Result{n}, nil
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
	return command.Tx.OperationWrites().SaveEffect(command.Tx.Context(), port.Effect{ProjectID: project, OperationRowID: command.RowID, EntityKind: "note", EntityID: after.Note.ID, AfterRevision: domain.Revision(after.Note.Revision), StateVersion: 1, BeforeJSON: previous, AfterJSON: next})
}
