package undo

import (
	"github.com/yoophi/td-backend/internal/application/board"
	"github.com/yoophi/td-backend/internal/application/focus"
	"github.com/yoophi/td-backend/internal/application/note"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/worksession"
	"github.com/yoophi/td-backend/internal/domain"
)

func prepare(tx port.TxScope, c domain.Caller, effect port.Effect, key, reason string) (plan, error) {
	if effect.EntityKind == "issue" {
		return prepareIssue(tx, c, effect, key, reason)
	}
	if effect.StateVersion != 1 {
		return nil, domain.ErrConflict
	}
	switch effect.EntityKind {
	case "note":
		current, e := tx.Notes().Get(tx.Context(), c.ProjectID, effect.EntityID, true)
		if e != nil {
			return nil, e
		}
		if e = observed(effect, current.Revision); e != nil {
			return nil, e
		}
		before := note.State{Note: current}
		target := before
		if effect.BeforeJSON != nil {
			target = note.State{}
			if e = decode(effect.BeforeJSON, &target); e != nil {
				return nil, e
			}
			if target.Note.ID != current.ID {
				return nil, domain.ErrConflict
			}
		}
		return func(command operation.CommandContext) (*domain.Issue, error) {
			if effect.BeforeJSON == nil {
				target.Note.DeletedAt = &command.Now
			}
			target.Note.Revision = current.Revision + 1
			target.Note.UpdatedAt = command.Now
			if e := command.Tx.NoteWrites().Save(command.Tx.Context(), c.ProjectID, target.Note, current.Revision); e != nil {
				return nil, e
			}
			return nil, inverse(command, c, effect, before, target, target.Note.Revision)
		}, nil
	case "board":
		current, e := tx.Boards().Get(tx.Context(), c.ProjectID, effect.EntityID, true)
		if e != nil {
			return nil, e
		}
		if e = observed(effect, current.Revision); e != nil {
			return nil, e
		}
		positions, e := tx.Boards().Positions(tx.Context(), c.ProjectID, current.ID)
		if e != nil {
			return nil, e
		}
		before := board.State{Board: current, Positions: positions}
		target := before
		if effect.BeforeJSON != nil {
			target = board.State{}
			if e = decode(effect.BeforeJSON, &target); e != nil {
				return nil, e
			}
			if target.Board.ID != current.ID || target.Board.IsBuiltin != current.IsBuiltin {
				return nil, domain.ErrConflict
			}
		}
		return func(command operation.CommandContext) (*domain.Issue, error) {
			if effect.BeforeJSON == nil {
				target.Board.DeletedAt = &command.Now
			}
			target.Board.Revision = current.Revision + 1
			target.Board.UpdatedAt = command.Now
			if e := command.Tx.BoardWrites().Save(command.Tx.Context(), c.ProjectID, target.Board, current.Revision); e != nil {
				return nil, e
			}
			// Creation inverses archive their board; no position can exist without a
			// later revision. Other inverses restore its live definition before positions.
			if target.Board.DeletedAt == nil {
				if e := command.Tx.BoardWrites().ReplacePositions(command.Tx.Context(), c.ProjectID, current.ID, target.Positions); e != nil {
					return nil, e
				}
			}
			return nil, inverse(command, c, effect, before, target, target.Board.Revision)
		}, nil
	case "focus":
		if effect.EntityID != c.SessionID {
			return nil, domain.ErrForbidden
		}
		current, e := tx.Focus().Get(tx.Context(), c.ProjectID, effect.EntityID)
		if e != nil {
			return nil, e
		}
		if e = observed(effect, current.Revision); e != nil {
			return nil, e
		}
		before := focus.State{Focus: current}
		target := before
		if effect.BeforeJSON != nil {
			target = focus.State{}
			if e = decode(effect.BeforeJSON, &target); e != nil {
				return nil, e
			}
			if target.Focus.SessionID != current.SessionID {
				return nil, domain.ErrConflict
			}
		} else {
			target.Focus.IssueID = nil
		}
		return func(command operation.CommandContext) (*domain.Issue, error) {
			target.Focus.Revision = current.Revision + 1
			if e := command.Tx.FocusWrites().Save(command.Tx.Context(), c.ProjectID, target.Focus, current.Revision); e != nil {
				return nil, e
			}
			return nil, inverse(command, c, effect, before, target, target.Focus.Revision)
		}, nil
	case "work_session_log":
		current, e := tx.WorkSessions().GetLogArchived(tx.Context(), c.ProjectID, effect.EntityID)
		if e != nil {
			return nil, e
		}
		if current.SessionID != c.SessionID {
			return nil, domain.ErrForbidden
		}
		if e = observed(effect, current.Revision); e != nil {
			return nil, e
		}
		before := current
		return func(command operation.CommandContext) (*domain.Issue, error) {
			current.DeletedAt = &command.Now
			current.Revision++
			if e := command.Tx.WorkSessionWrites().SaveLog(command.Tx.Context(), c.ProjectID, current, before.Revision, command.RowID); e != nil {
				return nil, e
			}
			return nil, inverse(command, c, effect, before, current, current.Revision)
		}, nil
	case "work_session":
		current, e := tx.WorkSessions().GetArchived(tx.Context(), c.ProjectID, effect.EntityID)
		if e != nil {
			return nil, e
		}
		if current.SessionID != c.SessionID {
			return nil, domain.ErrForbidden
		}
		if e = observed(effect, current.Revision); e != nil {
			return nil, e
		}
		tags, e := tx.WorkSessions().Tags(tx.Context(), c.ProjectID, current.ID)
		if e != nil {
			return nil, e
		}
		before := worksession.State{WorkSession: current, Tags: tags}
		target := before
		if effect.BeforeJSON != nil {
			target = worksession.State{}
			if e = decode(effect.BeforeJSON, &target); e != nil {
				return nil, e
			}
			if target.WorkSession.ID != current.ID || target.WorkSession.SessionID != current.SessionID {
				return nil, domain.ErrConflict
			}
		}
		return func(command operation.CommandContext) (*domain.Issue, error) {
			if effect.BeforeJSON == nil {
				target.WorkSession.DeletedAt = &command.Now
			}
			target.WorkSession.Revision = current.Revision + 1
			if e := command.Tx.WorkSessionWrites().Save(command.Tx.Context(), c.ProjectID, target.WorkSession, current.Revision); e != nil {
				return nil, e
			}
			if target.WorkSession.DeletedAt == nil {
				if e := command.Tx.WorkSessionWrites().ReplaceTags(command.Tx.Context(), c.ProjectID, current.ID, target.Tags); e != nil {
					return nil, e
				}
			}
			return nil, inverse(command, c, effect, before, target, target.WorkSession.Revision)
		}, nil
	default:
		return nil, domain.ErrConflict
	}
}
