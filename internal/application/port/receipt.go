package port

import (
	"context"
	"time"
)

type StoredReply struct {
	Status  int
	Headers map[string]string
	Body    []byte
}
type ReceiptEncoder interface {
	Encode(string, any) (StoredReply, error)
}
type Mutation struct {
	Key          string
	Fingerprint  []byte
	Method, Path string
}
type Receipt struct {
	RowID, ProjectID, ActorID, SessionID, Scope, Key, Kind, Method, Path string
	Fingerprint                                                          []byte
	RepresentationVersion                                                int
	Reply                                                                StoredReply
	CreatedAt                                                            time.Time
}
type AuditFilter struct {
	SessionID, IssueID string
	Limit, Offset      int
}
type OperationReader interface {
	AuditGet(context.Context, string, string) (Receipt, error)
	List(context.Context, string, AuditFilter) ([]Receipt, error)
	Effects(context.Context, string, string) ([]Effect, error)
	Get(context.Context, string, string, string) (Receipt, error)
}
type OperationWriter interface {
	RecordUndo(context.Context, string, string, string, string) error
	Save(context.Context, Receipt) error
	SaveEffect(context.Context, Effect) error
}

// Effect is a versioned aggregate snapshot for conditional undo. BeforeJSON is
// nil for creation; AfterJSON is always present, including soft-deleted state.
type Effect struct {
	ProjectID, OperationRowID, EntityKind, EntityID, AfterRevision string
	StateVersion                                                   int
	BeforeJSON, AfterJSON                                          []byte
}
