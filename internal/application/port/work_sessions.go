package port

import (
	"context"

	"github.com/yoophi/td-backend/internal/domain"
)

type WorkSessionReader interface {
	GetArchived(context.Context, string, string) (domain.WorkSession, error)
	Get(context.Context, string, string) (domain.WorkSession, error)
	List(context.Context, string) ([]domain.WorkSession, error)
	Tags(context.Context, string, string) ([]domain.WorkSessionIssue, error)
	Logs(context.Context, string, string) ([]domain.WorkSessionLog, error)
	GetLogArchived(context.Context, string, string) (domain.WorkSessionLog, error)
}
type WorkSessionWriter interface {
	Save(context.Context, string, domain.WorkSession, int64) error
	ReplaceTags(context.Context, string, string, []domain.WorkSessionIssue) error
	SaveLog(context.Context, string, domain.WorkSessionLog, int64, string) error
}
