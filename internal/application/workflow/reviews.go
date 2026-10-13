package workflow

import (
	"context"
	"strings"

	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/reviewpolicy"
)

type ReviewCommand struct {
	Decision   string `json:"decision"`
	Summary    string `json:"summary"`
	ReviewedBy string `json:"reviewed_by"`
	SelfReview bool   `json:"self_review"`
}
type ReviewResult struct {
	Issue  domain.Issue
	Review domain.IssueReview
}

func (s *Service) RecordReview(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd ReviewCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "workflow.review", func(command operation.CommandContext) (any, error) {
		cmd.Summary = strings.TrimSpace(cmd.Summary)
		if cmd.Summary == "" {
			return nil, invalid("summary", "review summary required")
		}
		if cmd.Decision != reviewpolicy.DecisionApproved && cmd.Decision != reviewpolicy.DecisionChangesRequested {
			return nil, invalid("decision", "expected approved or changes_requested")
		}
		options := TransitionCommand{Reason: cmd.Summary, ReviewedBy: cmd.ReviewedBy, SelfReview: cmd.SelfReview, RecordOnly: true}
		if e := validateTransition(&options, "approve"); e != nil {
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
		if f.Mode != reviewpolicy.ModeTrusted && f.Mode != reviewpolicy.ModeDelegated {
			return nil, domain.ErrConflict
		}
		if i.Minor {
			return nil, invalid("review", "minor issues do not need a recorded review")
		}
		if i.Status != domain.StatusInReview {
			return nil, domain.ErrConflict
		}
		decision := f.Reviewer(options.SelfReview, options.ReviewedBy)
		if !decision.Allowed {
			return nil, domain.ErrForbidden
		}
		if cmd.Decision == reviewpolicy.DecisionApproved && f.HasActiveApproval() {
			return nil, domain.ErrConflict
		}
		if f.Handoff == nil || f.Handoff.ContentVersion != i.ContentVersion {
			return nil, invalid("handoff", "current handoff required before review")
		}
		if cmd.Decision == reviewpolicy.DecisionApproved {
			if e = s.recordApproval(command, c, &i, f.Handoff.ID, cmd.Summary, decision); e != nil {
				return nil, e
			}
		} else {
			if e = tx.IssueWrites().SupersedeReviews(ctx, c.ProjectID, id, command.Now); e != nil {
				return nil, e
			}
			reviewID, e := s.ID("review")
			if e != nil {
				return nil, e
			}
			review := port.Review{Record: domain.IssueReview{ID: reviewID, IssueID: id, ReviewerSession: c.SessionID, Decision: cmd.Decision, Summary: cmd.Summary, RequestedBySession: i.ReviewRequestedBySession, SelfReview: decision.SelfReview, ReviewedBy: decision.AttributedTo, CreatedAt: command.Now}, BasisContentVersion: i.ContentVersion, BasisHandoffID: f.Handoff.ID, OperationRowID: command.RowID}
			if e = tx.WorkflowWrites().SaveReview(ctx, c.ProjectID, review); e != nil {
				return nil, e
			}
			if e = s.history(command, c, i, domain.ActionSessionReviewChangesRequested); e != nil {
				return nil, e
			}
			i.ReviewerSession = ""
			i.ReviewedAt = nil
		}
		activity, e := s.activity(command, c, i, "log", "Review recorded ("+cmd.Decision+"): "+cmd.Summary, nil)
		if e != nil {
			return nil, e
		}
		if decision.SelfReview {
			activity.LogType = domain.LogTypeSecurity
		}
		if e = tx.WorkflowWrites().AppendActivity(ctx, c.ProjectID, activity); e != nil {
			return nil, e
		}
		if e = persist(command, c, before, &i); e != nil {
			return nil, e
		}
		saved, e := tx.Workflow().ActiveReview(ctx, c.ProjectID, id)
		if e != nil {
			return nil, e
		}
		if saved == nil {
			return nil, domain.ErrConflict
		}
		return ReviewResult{Issue: i, Review: saved.Record}, nil
	})
}
