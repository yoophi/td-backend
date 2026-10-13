package workflow

import (
	"errors"
	"fmt"

	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/reviewpolicy"
)

func (s *Service) cascadeClose(command operation.CommandContext, c domain.Caller, closed domain.Issue, additionalClosed ...domain.Issue) ([]domain.Issue, []domain.Issue, error) {
	parents, unblocked := []domain.Issue{}, []domain.Issue{}
	tx := command.Tx
	ctx := tx.Context()
	closedIDs := []string{closed.ID}
	for _, i := range additionalClosed {
		closedIDs = append(closedIDs, i.ID)
	}
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

// cascadeApproval records the named parent-approval exemption for descendants.
// It traverses through blocked/closed nodes, but only closes open, in-progress
// and in-review descendants, matching td monitor's approval behavior.
func (s *Service) cascadeApproval(command operation.CommandContext, c domain.Caller, parent domain.Issue) ([]domain.Issue, error) {
	tx := command.Tx
	ctx := tx.Context()
	all, e := tx.Issues().All(ctx, c.ProjectID, false)
	if e != nil {
		return nil, e
	}
	children := map[string][]domain.Issue{}
	for _, i := range all {
		children[i.ParentID] = append(children[i.ParentID], i)
	}
	queue := append([]domain.Issue{}, children[parent.ID]...)
	seen := map[string]bool{parent.ID: true}
	result := []domain.Issue{}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		if seen[i.ID] {
			return nil, domain.ErrConflict
		}
		seen[i.ID] = true
		queue = append(queue, children[i.ID]...)
		if i.Status != domain.StatusOpen && i.Status != domain.StatusInProgress && i.Status != domain.StatusInReview {
			continue
		}
		before := i
		i.Status = domain.StatusClosed
		i.ClosedAt = &command.Now
		i.ReviewedAt = &command.Now
		i.ReviewerSession = c.SessionID
		i.ClosedBySession = c.SessionID
		if i.ImplementerSession == "" {
			i.ImplementerSession = c.SessionID
		}
		if e = tx.IssueWrites().SupersedeReviews(ctx, c.ProjectID, i.ID, command.Now); e != nil {
			return nil, e
		}
		handoff, e := tx.Workflow().LatestHandoff(ctx, c.ProjectID, i.ID)
		if e != nil {
			return nil, e
		}
		handoffID := ""
		if handoff != nil {
			handoffID = handoff.ID
		}
		id, e := s.ID("review")
		if e != nil {
			return nil, e
		}
		summary := "Cascaded approval from " + parent.ID
		review := port.Review{Record: domain.IssueReview{ID: id, IssueID: i.ID, ReviewerSession: c.SessionID, Decision: reviewpolicy.DecisionApprovedByParentCascade, Summary: summary, RequestedBySession: i.ReviewRequestedBySession, CreatedAt: command.Now}, BasisContentVersion: i.ContentVersion, BasisHandoffID: handoffID, OperationRowID: command.RowID}
		if e = tx.WorkflowWrites().SaveReview(ctx, c.ProjectID, review); e != nil {
			return nil, e
		}
		if e = s.history(command, c, i, domain.ActionSessionReviewApproved); e != nil {
			return nil, e
		}
		if e = s.history(command, c, i, domain.ActionSessionClosed); e != nil {
			return nil, e
		}
		if e = s.cascadeLog(command, c, i, summary); e != nil {
			return nil, e
		}
		if e = persist(command, c, before, &i); e != nil {
			return nil, e
		}
		result = append(result, i)
	}
	return result, nil
}
