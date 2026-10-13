package port

import (
	"context"

	"github.com/yoophi/td-backend/internal/domain"
)

type FocusReader interface {
	Get(context.Context, string, string) (domain.SessionFocus, error)
}
type FocusWriter interface {
	Save(context.Context, string, domain.SessionFocus, int64) error
}
