package port

import (
	"context"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

// Review keeps storage-independent review basis alongside its public audit data.
type Review struct {
	Record              domain.IssueReview
	BasisContentVersion int64
	BasisHandoffID      string
	OperationRowID      string
}
type Activity struct {
	OperationID, Author, SnapshotEvent                    string
	UpdatedAt                                             time.Time
	DeletedAt                                             *time.Time
	ID, IssueID, SessionID, WorkSessionID, OperationRowID string
	Kind, Message                                         string
	LogType                                               domain.LogType
	ContentVersion                                        int64
	Done, Remaining, Decisions, Uncertain                 []string
	Snapshot                                              *Snapshot
	CreatedAt                                             time.Time
}
type Snapshot struct {
	CommitSHA  string `json:"commit_sha"`
	Branch     string `json:"branch"`
	DirtyFiles int    `json:"dirty_files"`
}
type WorkflowReader interface {
	History(context.Context, string, string) ([]domain.IssueSessionHistory, error)
	ActiveReview(context.Context, string, string) (*Review, error)
	LatestHandoff(context.Context, string, string) (*Activity, error)
	Activities(context.Context, string, string, bool) ([]Activity, error)
	WorkSessionAccess(context.Context, string, string) (WorkSessionAccess, error)
}
type WorkflowWriter interface {
	RecordHistory(context.Context, string, domain.IssueSessionHistory, string) error
	SaveReview(context.Context, string, Review) error
	AppendActivity(context.Context, string, Activity) error
	DeleteComment(context.Context, string, string, string, time.Time) error
}

type WorkSessionAccess struct {
	SessionID string
	Ended     bool
}
