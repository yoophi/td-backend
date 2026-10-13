// Package port owns application output interfaces; it has no SQL/HTTP dependency.
package port

import (
	"context"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

type UnitOfWork interface {
	Write(context.Context, func(TxScope) error) error
}
type ReadStore interface {
	Snapshot(context.Context, func(ReadSnapshot) error) error
}

// All repositories in one scope use the same database transaction. A scope may
// not escape its callback. Callbacks never perform network I/O and are not retried.
type ReadSnapshot interface {
	Context() context.Context
	Projects() ProjectReader
	Issues() IssueReader
	Access() AccessReader
	Sessions() SessionReader
	Operations() OperationReader
	Workflow() WorkflowReader
	Boards() BoardReader
	Notes() NoteReader
	Queries() QueryReader
}
type TxScope interface {
	ReadSnapshot
	ProjectWrites() ProjectWriter
	IssueWrites() IssueWriter
	AccessWrites() AccessWriter
	SessionWrites() SessionWriter
	BoardWrites() BoardWriter
	NoteWrites() NoteWriter
	OperationWrites() OperationWriter
	WorkflowWrites() WorkflowWriter
}
type ProjectReader interface {
	Get(context.Context, string) (*domain.Project, error)
	ListForActor(context.Context, string) ([]domain.Project, error)
}
type ProjectWriter interface {
	Create(context.Context, domain.Project) error
	AdvanceChange(context.Context, string) (int64, error)
}
type IssueReader interface {
	Get(context.Context, string, string, bool) (*domain.Issue, error)
	List(context.Context, string, bool) ([]domain.Issue, error)
	All(context.Context, string, bool) ([]domain.Issue, error)
	Dependencies(context.Context, string, string) ([]Dependency, error)
	Context(context.Context, string, string, bool) (IssueContext, error)
	Files(context.Context, string, string) ([]domain.IssueFile, error)
}
type IssueWriter interface {
	// expected=0 creates a record; otherwise atomically checks expected revision.
	Save(context.Context, string, domain.Issue, int64) error
	AddDependency(context.Context, string, Dependency) error
	RemoveDependency(context.Context, string, string, string) error
	ReplaceFiles(context.Context, string, string, []domain.IssueFile) error
	SupersedeReviews(context.Context, string, string, time.Time) error
	RecordCreation(context.Context, string, string, string, string, time.Time) error
}

type Dependency struct{ ID, IssueID, DependsOnID string }

type IssueContext struct {
	Logs                    []domain.Log
	Comments                []domain.Comment
	LatestHandoff           *domain.Handoff
	Children                []domain.Issue
	Dependencies, BlockedBy []Dependency
	Files                   []domain.IssueFile
	Reviews                 []domain.IssueReview
}
