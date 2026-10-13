// Package board owns shared board definitions and observed board mutations.
package board

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/tdq"
)

type Service struct {
	Reads  port.ReadStore
	Runner operation.Runner
	ID     func(string) (string, error)
}
type CreateCommand struct {
	Name     string  `json:"name"`
	Query    *string `json:"query"`
	ViewMode *string `json:"view_mode"`
}
type PatchCommand struct {
	Name     *string `json:"name"`
	Query    *string `json:"query"`
	ViewMode *string `json:"view_mode"`
}
type PositionCommand struct {
	IssueID  string `json:"issue_id"`
	Position int    `json:"position"`
}
type Result struct{ Board domain.Board }

// State version 1 includes definition and all explicit positions for undo.
type State struct {
	Board     domain.Board        `json:"board"`
	Positions []domain.BoardIssue `json:"positions"`
}

func invalid(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}
func validate(b *domain.Board) error {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return invalid("name", "nonblank board name required")
	}
	if b.ViewMode != "swimlanes" && b.ViewMode != "backlog" {
		return invalid("view_mode", "expected swimlanes or backlog")
	}
	_, e := tdq.ParseIssues(b.Query)
	if e != nil {
		return invalid("query", e.Error())
	}
	return nil
}

func (s *Service) List(ctx context.Context, c domain.Caller) ([]domain.Board, error) {
	var result []domain.Board
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		if e := access.Authorize(read.Context(), read, c, true, false); e != nil {
			return e
		}
		var e error
		result, e = read.Boards().List(read.Context(), c.ProjectID)
		return e
	})
	return result, e
}

func (s *Service) Create(ctx context.Context, c domain.Caller, m port.Mutation, cmd CreateCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "board.create", func(command operation.CommandContext) (any, error) {
		if cmd.Query == nil {
			return nil, invalid("query", "query is required (empty selects all issues)")
		}
		b := domain.Board{Name: cmd.Name, Query: *cmd.Query, ViewMode: "swimlanes", Revision: 1, CreatedAt: command.Now, UpdatedAt: command.Now}
		if cmd.ViewMode != nil {
			b.ViewMode = *cmd.ViewMode
		}
		if e := validate(&b); e != nil {
			return nil, e
		}
		var e error
		if b.ID, e = s.ID("board"); e != nil {
			return nil, e
		}
		if e = command.Tx.BoardWrites().Create(command.Tx.Context(), c.ProjectID, b); e != nil {
			return nil, e
		}
		if e = saveEffect(command, c.ProjectID, nil, State{Board: b, Positions: []domain.BoardIssue{}}); e != nil {
			return nil, e
		}
		return Result{Board: b}, nil
	})
}

func (s *Service) Patch(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd PatchCommand) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, revision, "board.update", func(command operation.CommandContext, state *State) error {
		if cmd.Name == nil && cmd.Query == nil && cmd.ViewMode == nil {
			return invalid("body", "at least one field required")
		}
		b := &state.Board
		if b.IsBuiltin && (cmd.Name != nil || cmd.Query != nil) {
			return domain.ErrForbidden
		}
		if cmd.Name != nil {
			b.Name = *cmd.Name
		}
		if cmd.Query != nil {
			b.Query = *cmd.Query
		}
		if cmd.ViewMode != nil {
			b.ViewMode = *cmd.ViewMode
		}
		return validate(b)
	})
}

func (s *Service) Delete(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, revision, "board.delete", func(command operation.CommandContext, state *State) error {
		if state.Board.IsBuiltin {
			return domain.ErrForbidden
		}
		state.Board.DeletedAt = &command.Now
		return nil
	})
}
func (s *Service) Visit(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, revision, "board.visit", func(command operation.CommandContext, state *State) error {
		state.Board.LastViewedAt = &command.Now
		return nil
	})
}
func (s *Service) Position(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd PositionCommand) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, revision, "board.position", func(command operation.CommandContext, state *State) error {
		if cmd.IssueID == "" {
			return invalid("issue_id", "issue ID required")
		}
		if cmd.Position < 1 {
			return invalid("position", "expected a 1-based insertion slot")
		}
		if _, e := command.Tx.Issues().Get(command.Tx.Context(), c.ProjectID, cmd.IssueID, false); e != nil {
			return e
		}
		positions, e := InsertSlot(state.Positions, id, cmd.IssueID, cmd.Position, command.Now)
		if e != nil {
			return e
		}
		state.Positions = positions
		return command.Tx.BoardWrites().ReplacePositions(command.Tx.Context(), c.ProjectID, id, positions)
	})
}
func (s *Service) Unpin(ctx context.Context, c domain.Caller, m port.Mutation, id, revision, issueID string) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, revision, "board.unpin", func(command operation.CommandContext, state *State) error {
		if _, e := command.Tx.Issues().Get(command.Tx.Context(), c.ProjectID, issueID, false); e != nil {
			return e
		}
		positions := []domain.BoardIssue{}
		for _, p := range state.Positions {
			if p.IssueID != issueID {
				positions = append(positions, p)
			}
		}
		state.Positions = positions
		return command.Tx.BoardWrites().ReplacePositions(command.Tx.Context(), c.ProjectID, id, positions)
	})
}

func (s *Service) mutate(ctx context.Context, c domain.Caller, m port.Mutation, id, revision, kind string, edit func(operation.CommandContext, *State) error) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, kind, func(command operation.CommandContext) (any, error) {
		expected, e := domain.Observe(revision)
		if e != nil {
			return nil, e
		}
		tx := command.Tx
		b, e := tx.Boards().Get(tx.Context(), c.ProjectID, id, false)
		if e != nil {
			return nil, e
		}
		if b.Revision != expected {
			return nil, domain.ErrStale
		}
		positions, e := tx.Boards().Positions(tx.Context(), c.ProjectID, id)
		if e != nil {
			return nil, e
		}
		before := State{Board: b, Positions: positions}
		after := State{Board: b, Positions: append([]domain.BoardIssue{}, positions...)}
		if e = edit(command, &after); e != nil {
			return nil, e
		}
		after.Board.Revision++
		after.Board.UpdatedAt = command.Now
		if e = tx.BoardWrites().Save(tx.Context(), c.ProjectID, after.Board, expected); e != nil {
			return nil, e
		}
		if e = saveEffect(command, c.ProjectID, &before, after); e != nil {
			return nil, e
		}
		return Result{Board: after.Board}, nil
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
	return command.Tx.OperationWrites().SaveEffect(command.Tx.Context(), port.Effect{ProjectID: project, OperationRowID: command.RowID, EntityKind: "board", EntityID: after.Board.ID, AfterRevision: domain.Revision(after.Board.Revision), StateVersion: 1, BeforeJSON: previous, AfterJSON: next})
}
