package port

import (
	"context"

	"github.com/yoophi/td-backend/internal/domain"
)

type NoteReader interface {
	Get(context.Context, string, string, bool) (domain.Note, error)
	// List includes archived notes and excludes deleted notes.
	List(context.Context, string) ([]domain.Note, error)
	ListIncludingDeleted(context.Context, string) ([]domain.Note, error)
}
type NoteWriter interface {
	// expected=0 creates a record; otherwise checks the observed revision.
	Save(context.Context, string, domain.Note, int64) error
}
