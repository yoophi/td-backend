package workflow

import (
	"context"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/reviewpolicy"
)

// Facts is loaded in the command's transaction. Callers must not cache it across
// requests: participation, content version and the latest handoff may change.
type Facts struct {
	Issue        domain.Issue
	Mode         reviewpolicy.Mode
	SessionID    string
	History      []domain.IssueSessionHistory
	ActiveReview *port.Review
	Handoff      *port.Activity
}

func LoadFacts(ctx context.Context, read port.ReadSnapshot, caller domain.Caller, id string) (Facts, error) {
	facts := Facts{SessionID: caller.SessionID}
	p, e := read.Projects().Get(ctx, caller.ProjectID)
	if e != nil {
		return facts, e
	}
	facts.Mode, e = reviewpolicy.ParseMode(p.ReviewPolicyMode)
	if e != nil {
		return facts, e
	}
	i, e := read.Issues().Get(ctx, caller.ProjectID, id, false)
	if e != nil {
		return facts, e
	}
	facts.Issue = *i
	facts.History, e = read.Workflow().History(ctx, caller.ProjectID, id)
	if e != nil {
		return facts, e
	}
	facts.ActiveReview, e = read.Workflow().ActiveReview(ctx, caller.ProjectID, id)
	if e != nil {
		return facts, e
	}
	facts.Handoff, e = read.Workflow().LatestHandoff(ctx, caller.ProjectID, id)
	return facts, e
}
func (f Facts) HasActiveApproval() bool {
	r := f.ActiveReview
	if r == nil || r.Record.SupersededAt != nil || r.BasisContentVersion != f.Issue.ContentVersion {
		return false
	}
	if r.Record.Decision != reviewpolicy.DecisionApproved && r.Record.Decision != reviewpolicy.DecisionApprovedByParentCascade {
		return false
	}
	if r.Record.Decision == reviewpolicy.DecisionApprovedByParentCascade {
		return true
	}
	handoff := ""
	if f.Handoff != nil {
		if f.Handoff.ContentVersion != f.Issue.ContentVersion {
			return false
		}
		handoff = f.Handoff.ID
	}
	return r.BasisHandoffID == handoff
}
func (f Facts) involvement() (any, implementation bool) {
	for _, h := range f.History {
		if h.SessionID == f.SessionID {
			any = true
			if h.Action == domain.ActionSessionStarted || h.Action == domain.ActionSessionUnstarted {
				implementation = true
			}
		}
	}
	return
}
func (f Facts) Reviewer(self bool, attribution string) reviewpolicy.ReviewerEligibility {
	any, implementation := f.involvement()
	return reviewpolicy.EvaluateReviewerEligibility(reviewpolicy.ReviewerEligibilityInput{
		Mode: f.Mode, Issue: &f.Issue, SessionID: f.SessionID, SessionIsImplementer: f.Issue.ImplementerSession == f.SessionID, SessionIsCreator: f.Issue.CreatorSession == f.SessionID,
		HasImplementationHistory: implementation, WasAnyInvolved: any, HasActiveApproval: f.HasActiveApproval(), SelfReviewAcknowledged: self, AttributedTo: attribution,
	})
}
func (f Facts) Closer(self bool, attribution string) reviewpolicy.CloseEligibility {
	any, implementation := f.involvement()
	decision := reviewpolicy.EvaluateCloseEligibility(reviewpolicy.CloseEligibilityInput{
		Mode: f.Mode, Issue: &f.Issue, SessionID: f.SessionID, SessionIsImplementer: f.Issue.ImplementerSession == f.SessionID, SessionIsCreator: f.Issue.CreatorSession == f.SessionID,
		SessionIsReviewerOfRecord: f.Issue.ReviewerSession == f.SessionID, SessionIsReviewRequester: f.Issue.ReviewRequestedBySession == f.SessionID,
		HasImplementationHistory: implementation, WasAnyInvolved: any, HasActiveApproval: f.HasActiveApproval(), SelfReviewAcknowledged: self, AttributedTo: attribution,
	})
	if f.Mode != reviewpolicy.ModeDelegated && decision.Allowed && decision.CreatorOpenBypass && f.HasImplementationHistory() {
		return reviewpolicy.CloseEligibility{RejectionMessage: "implementation history requires review"}
	}
	return decision
}

func (f Facts) HasImplementationHistory() bool {
	for _, h := range f.History {
		if h.Action == domain.ActionSessionStarted || h.Action == domain.ActionSessionUnstarted {
			return true
		}
	}
	return false
}
