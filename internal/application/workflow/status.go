package workflow

import (
	"context"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type StatusCommand struct {
	Status domain.Status `json:"status"`
	Reason string        `json:"reason"`
	Force  bool          `json:"force"`
}

func (s *Service) SetStatus(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd StatusCommand) (port.StoredReply, error) {
	switch cmd.Status {
	case domain.StatusOpen, domain.StatusInProgress, domain.StatusBlocked, domain.StatusInReview, domain.StatusClosed:
	default:
		return port.StoredReply{}, invalid("status", "invalid status")
	}
	return s.Transition(ctx, c, m, id, revision, "status", TransitionCommand{TargetStatus: &cmd.Status, Reason: cmd.Reason, Force: cmd.Force})
}
func statusAction(from, to domain.Status) (string, error) {
	switch to {
	case domain.StatusClosed:
		if from == domain.StatusInReview {
			return "approve", nil
		}
		return "close", nil
	case domain.StatusInProgress:
		if from == domain.StatusOpen || from == domain.StatusBlocked {
			return "start", nil
		}
		if from == domain.StatusInReview {
			return "resume", nil
		}
	case domain.StatusInReview:
		if from == domain.StatusOpen || from == domain.StatusInProgress {
			return "review", nil
		}
	case domain.StatusBlocked:
		if from == domain.StatusOpen || from == domain.StatusInProgress {
			return "block", nil
		}
	case domain.StatusOpen:
		switch from {
		case domain.StatusClosed:
			return "reopen", nil
		case domain.StatusInReview:
			return "reject", nil
		case domain.StatusBlocked:
			return "unblock", nil
		case domain.StatusInProgress:
			return "unstart", nil
		}
	}
	return "", domain.ErrConflict
}
