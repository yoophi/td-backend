package board

import (
	"context"
	"time"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/tdq"
)

type Detail struct {
	Board  domain.Board
	Issues []domain.BoardIssueView
}
type MoveCommand struct {
	IssueID       string `json:"issue_id"`
	BeforeID      string `json:"before_id"`
	IncludeClosed bool   `json:"include_closed"`
}

func candidates(read port.ReadSnapshot, project, session, query string, closed bool, now time.Time) ([]domain.Issue, error) {
	facts, e := read.Queries().Facts(read.Context(), project, false)
	if e != nil {
		return nil, e
	}
	issues, e := tdq.Filter(query, &facts, tdq.EvalContext{CurrentSession: session, Now: now}, closed)
	if e != nil {
		return nil, invalid("query", e.Error())
	}
	return issues, nil
}

func (s *Service) Detail(ctx context.Context, c domain.Caller, id string, includeClosed bool) (Detail, error) {
	var result Detail
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		if e := access.Authorize(read.Context(), read, c, true, false); e != nil {
			return e
		}
		b, e := read.Boards().Get(read.Context(), c.ProjectID, id, false)
		if e != nil {
			return e
		}
		now := time.Now()
		if s.Runner.Now != nil {
			now = s.Runner.Now()
		}
		issues, e := candidates(read, c.ProjectID, c.SessionID, b.Query, includeClosed, now)
		if e != nil {
			return e
		}
		positions, e := read.Boards().Positions(read.Context(), c.ProjectID, id)
		if e != nil {
			return e
		}
		result = Detail{Board: b, Issues: ApplyPositions(id, issues, positions)}
		return nil
	})
	return result, e
}

func (s *Service) Move(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd MoveCommand) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, revision, "board.move", func(command operation.CommandContext, state *State) error {
		if cmd.IssueID == "" {
			return invalid("issue_id", "issue ID required")
		}
		issues, e := candidates(command.Tx, c.ProjectID, c.SessionID, state.Board.Query, cmd.IncludeClosed, command.Now)
		if e != nil {
			return e
		}
		positions, e := MoveBefore(state.Positions, id, issues, cmd.IssueID, cmd.BeforeID, command.Now)
		if e != nil {
			return e
		}
		state.Positions = positions
		return command.Tx.BoardWrites().ReplacePositions(command.Tx.Context(), c.ProjectID, id, positions)
	})
}
