package port

import (
	"context"

	"github.com/yoophi/td-backend/internal/domain"
)

type BoardReader interface {
	Get(context.Context, string, string, bool) (domain.Board, error)
	List(context.Context, string) ([]domain.Board, error)
	Positions(context.Context, string, string) ([]domain.BoardIssue, error)
}

type BoardWriter interface {
	Create(context.Context, string, domain.Board) error
	Save(context.Context, string, domain.Board, int64) error
	// ReplacePositions preserves caller-supplied sparse keys and timestamps.
	// The caller saves the owning board with its observed revision in this scope.
	ReplacePositions(context.Context, string, string, []domain.BoardIssue) error
}
