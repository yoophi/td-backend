package port

import (
	"context"
	"github.com/yoophi/td-backend/internal/domain/tdq"
)

type QueryReader interface {
	// Facts batches related project records in the caller's snapshot or writer
	// transaction; it must not open a new transaction for each issue.
	// The bool includes soft-deleted issues and their facts for archive queries.
	Facts(context.Context, string, bool) (tdq.Facts, error)
}
