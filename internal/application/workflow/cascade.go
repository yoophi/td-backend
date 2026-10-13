package workflow

import (
	"errors"
	"fmt"

	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/domain"
)

func (s *Service) cascadeClose(command operation.CommandContext, c domain.Caller, closed domain.Issue) ([]domain.Issue, []domain.Issue, error) {
	parents, unblocked := []domain.Issue{}, []domain.Issue{}
	tx := command.Tx
	ctx := tx.Context()
	closedIDs := []string{closed.ID}
	seen := map[string]bool{closed.ID: true}
	for id := closed.ParentID; id != ""; {
		if seen[id] {
			return nil, nil, domain.ErrConflict
		}
		seen[id] = true
		parent, e := tx.Issues().Get(ctx, c.ProjectID, id, false)
		if errors.Is(e, domain.ErrNotFound) {
			break
		}
		if e != nil {
			return nil, nil, e
		}
		if parent.Type != domain.TypeEpic || parent.Status == domain.StatusClosed {
			break
		}
		related, e := tx.Issues().Context(ctx, c.ProjectID, id, false)
		if e != nil {
			return nil, nil, e
		}
		ready := len(related.Children) > 0
		for _, child := range related.Children {
			if child.Status != domain.StatusClosed {
				ready = false
				break
			}
		}
		if !ready {
			break
		}
		before := *parent
		parent.Status = domain.StatusClosed
		parent.ClosedAt = &command.Now
		parent.ClosedBySession = c.SessionID
		if e = s.history(command, c, *parent, domain.ActionSessionClosed); e != nil {
			return nil, nil, e
		}
		if e = s.cascadeLog(command, c, *parent, "Auto-closed: all children closed"); e != nil {
			return nil, nil, e
		}
		if e = persist(command, c, before, parent); e != nil {
			return nil, nil, e
		}
		parents = append(parents, *parent)
		closedIDs = append(closedIDs, parent.ID)
		id = parent.ParentID
	}
	for _, id := range closedIDs {
		related, e := tx.Issues().Context(ctx, c.ProjectID, id, false)
		if e != nil {
			return nil, nil, e
		}
		for _, edge := range related.BlockedBy {
			dependent, e := tx.Issues().Get(ctx, c.ProjectID, edge.IssueID, false)
			if errors.Is(e, domain.ErrNotFound) {
				continue
			}
			if e != nil {
				return nil, nil, e
			}
			if dependent.Status != domain.StatusBlocked {
				continue
			}
			deps, e := tx.Issues().Dependencies(ctx, c.ProjectID, dependent.ID)
			if e != nil {
				return nil, nil, e
			}
			ready := len(deps) > 0
			for _, dep := range deps {
				target, e := tx.Issues().Get(ctx, c.ProjectID, dep.DependsOnID, false)
				if errors.Is(e, domain.ErrNotFound) {
					ready = false
					break
				}
				if e != nil {
					return nil, nil, e
				}
				if target.Status != domain.StatusClosed {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			before := *dependent
			dependent.Status = domain.StatusOpen
			dependent.ImplementerSession = ""
			if e = s.cascadeLog(command, c, *dependent, fmt.Sprintf("Auto-unblocked after %s closed", id)); e != nil {
				return nil, nil, e
			}
			if e = persist(command, c, before, dependent); e != nil {
				return nil, nil, e
			}
			unblocked = append(unblocked, *dependent)
		}
	}
	return parents, unblocked, nil
}
func (s *Service) cascadeLog(command operation.CommandContext, c domain.Caller, i domain.Issue, message string) error {
	a, e := s.activity(command, c, i, "log", message, nil)
	if e != nil {
		return e
	}
	return command.Tx.WorkflowWrites().AppendActivity(command.Tx.Context(), c.ProjectID, a)
}
