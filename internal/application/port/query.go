package port

import (
	"context"
	"github.com/yoophi/td-backend/internal/domain/tdq"
)

type QueryReader interface {
	// Facts batches related project records in the caller's snapshot or writer
	// transaction; it must not open a new transaction for each issue.
	Facts(context.Context, string) (tdq.Facts, error)
}
