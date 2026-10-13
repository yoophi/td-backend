package workflow

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/reviewpolicy"
)

func validateTransition(cmd *TransitionCommand, action string) error {
	if e := validateSnapshot(cmd.Snapshot); e != nil {
		return e
	}
	raw := cmd.ReviewedBy
	cmd.ReviewedBy = strings.TrimSpace(raw)
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	cmd.AdminReason = strings.TrimSpace(cmd.AdminReason)
	if (raw != "" && cmd.ReviewedBy == "") || utf8.RuneCountInString(cmd.ReviewedBy) > reviewpolicy.MaxReviewedByLen {
		return invalid("reviewed_by", "expected 1..120 nonblank characters")
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || (unicode.IsControl(r) && r != '\t') }) {
		return invalid("reviewed_by", "control characters and newlines are not allowed")
	}
	if cmd.SelfReview && cmd.ReviewedBy != "" {
		return invalid("self_review", "mutually exclusive with reviewed_by")
	}
	if cmd.SelfReview && cmd.Reason == "" {
		return invalid("reason", "self review requires a reason")
	}
	if cmd.SelfCloseException != "" {
		return invalid("self_close_exception", "unsupported self-close exception")
	}
	if cmd.RecordOnly && action != "approve" {
		return invalid("record_only", "only approve supports record_only")
	}
	if (cmd.SelfReview || cmd.ReviewedBy != "") && action != "approve" && action != "close" {
		return invalid("reviewed_by", "review attribution only applies to approval or close")
	}
	if cmd.AdminReason != "" && action != "close" {
		return invalid("admin_reason", "only close supports administrative reason")
	}
	return nil
}
func (s *Service) finish(command operation.CommandContext, c domain.Caller, f Facts, i *domain.Issue, action string, cmd TransitionCommand) error {
	if i.Status == domain.StatusClosed {
		return domain.ErrConflict
	}
	if action == "approve" && i.Status != domain.StatusInReview {
		return domain.ErrConflict
	}
	if cmd.RecordOnly && (f.Mode != reviewpolicy.ModeTrusted && f.Mode != reviewpolicy.ModeDelegated) {
		return domain.ErrConflict
	}
	if cmd.RecordOnly && i.Minor {
		return invalid("record_only", "minor issues do not need a recorded review")
	}
	if cmd.RecordOnly && f.HasActiveApproval() {
		return domain.ErrConflict
	}
	if cmd.RecordOnly && cmd.Reason == "" {
		return invalid("reason", "record-only approval requires a reason")
	}
	tx := command.Tx
	ctx := tx.Context()
	// Administrative close is an explicit owner action, distinct from force.
	if cmd.AdminReason != "" {
		role, e := tx.Access().Role(ctx, c.ProjectID, c.Actor.ID)
		if e != nil {
			return e
		}
		if role != domain.RoleOwner {
			return domain.ErrForbidden
		}
		if e = invalidate(command, c, i); e != nil {
			return e
		}
		a, e := s.activity(command, c, *i, "log", "Administrative close: "+cmd.AdminReason, nil)
		if e != nil {
			return e
		}
		a.LogType = domain.LogTypeSecurity
		if e = tx.WorkflowWrites().AppendActivity(ctx, c.ProjectID, a); e != nil {
			return e
		}
	} else if i.Status == domain.StatusInReview {
		recorded := f.HasActiveApproval() && (f.Mode == reviewpolicy.ModeTrusted || f.Mode == reviewpolicy.ModeDelegated)
		if recorded && !cmd.RecordOnly {
			if f.ActiveReview.Record.ReviewerSession != c.SessionID && cmd.Reason == "" {
				return invalid("reason", "closing another session's approval requires a reason")
			}
			i.ReviewerSession = f.ActiveReview.Record.ReviewerSession
			i.ReviewedAt = &f.ActiveReview.Record.CreatedAt
		} else {
			if action == "close" && !i.Minor {
				return domain.ErrForbidden
			}
			if f.Handoff == nil || f.Handoff.ContentVersion != i.ContentVersion {
				return invalid("handoff", "current handoff required before approval")
			}
			decision := f.Reviewer(cmd.SelfReview, cmd.ReviewedBy)
			if !decision.Allowed {
				return domain.ErrForbidden
			}
			if decision.RequiresReason && cmd.Reason == "" {
				return invalid("reason", "approval requires a reason")
			}
			if !cmd.RecordOnly {
				closeDecision := f.Closer(cmd.SelfReview, cmd.ReviewedBy)
				if !closeDecision.Allowed {
					return domain.ErrForbidden
				}
				if closeDecision.RequiresReason && cmd.Reason == "" {
					return invalid("reason", "close requires a reason")
				}
			}
			if e := s.recordApproval(command, c, i, f.Handoff.ID, cmd.Reason, decision); e != nil {
				return e
			}
		}
	} else {
		decision := f.Closer(cmd.SelfReview, cmd.ReviewedBy)
		if !decision.Allowed {
			return domain.ErrForbidden
		}
		if decision.RequiresReason && cmd.Reason == "" {
			return invalid("reason", "close requires a reason")
		}
		// Implementation outside review must not bypass the recorded-review flow.
		if !i.Minor && !decision.CreatorOpenBypass {
			return domain.ErrForbidden
		}
	}
	if cmd.RecordOnly {
		return nil
	}
	i.Status = domain.StatusClosed
	i.ClosedBySession = c.SessionID
	i.ClosedAt = &command.Now
	return s.history(command, c, *i, domain.ActionSessionClosed)
}
func (s *Service) recordApproval(command operation.CommandContext, c domain.Caller, i *domain.Issue, handoff, reason string, decision reviewpolicy.ReviewerEligibility) error {
	tx := command.Tx
	ctx := tx.Context()
	if e := tx.IssueWrites().SupersedeReviews(ctx, c.ProjectID, i.ID, command.Now); e != nil {
		return e
	}
	id, e := s.ID("review")
	if e != nil {
		return e
	}
	review := port.Review{Record: domain.IssueReview{ID: id, IssueID: i.ID, ReviewerSession: c.SessionID, Decision: reviewpolicy.DecisionApproved, Summary: reason, RequestedBySession: i.ReviewRequestedBySession, SelfReview: decision.SelfReview, ReviewedBy: decision.AttributedTo, CreatedAt: command.Now}, BasisContentVersion: i.ContentVersion, BasisHandoffID: handoff, OperationRowID: command.RowID}
	if e = tx.WorkflowWrites().SaveReview(ctx, c.ProjectID, review); e != nil {
		return e
	}
	if e = s.history(command, c, *i, domain.ActionSessionReviewApproved); e != nil {
		return e
	}
	i.ReviewerSession = c.SessionID
	i.ReviewedAt = &command.Now
	return nil
}
