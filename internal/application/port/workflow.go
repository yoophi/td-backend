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

// WorkflowFacts batches policy inputs for all live issues in one project.
type WorkflowFacts struct {
	History        map[string][]domain.IssueSessionHistory
	ActiveReviews  map[string]*Review
	LatestHandoffs map[string]*Activity
}
type ActivityCounts struct {
	Logs, Handoffs    int
	MostActiveSession string
}
type WorkflowReader interface {
	Counts(context.Context, string) (ActivityCounts, error)
	Facts(context.Context, string) (WorkflowFacts, error)
	Feed(context.Context, string, int) ([]Activity, error)
	RecentHandoffs(context.Context, string, time.Time) ([]Activity, error)
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
