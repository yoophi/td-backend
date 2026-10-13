// Package operation implements the transaction boundary for project commands.
package operation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type Runner struct {
	Writes  port.UnitOfWork
	Encoder port.ReceiptEncoder
	ID      func(string) (string, error)
	Now     func() time.Time
}

// CommandContext supplies one timestamp and operation ID to all aggregate
// effects. A callback must use Tx.Context() and may not start another transaction.
type CommandContext struct {
	Tx    port.TxScope
	RowID string
	Now   time.Time
}

// Run authorizes even retries, but invokes apply only for an unseen key. The
// callback performs observed-revision checks after the receipt lookup, so a lost
// response can be replayed after later edits without repeating its effects.
func (r Runner) Run(ctx context.Context, c domain.Caller, m port.Mutation, kind string, apply func(CommandContext) (any, error)) (port.StoredReply, error) {
	if !domain.ValidOperationKey(m.Key) || len(m.Fingerprint) == 0 {
		return port.StoredReply{}, &domain.ValidationError{Fields: []domain.FieldError{{Field: "Idempotency-Key", Rule: "invalid", Message: "UUIDv4 key and request fingerprint required"}}}
	}
	var reply port.StoredReply
	err := r.Writes.Write(ctx, func(tx port.TxScope) error {
		ctx := tx.Context()
		if e := access.Authorize(ctx, tx, c, true, true); e != nil {
			return e
		}
		previous, e := tx.Operations().Get(ctx, c.Actor.ID, c.ProjectID, m.Key)
		if e == nil {
			if !bytes.Equal(previous.Fingerprint, m.Fingerprint) {
				return domain.ErrConflict
			}
			reply = previous.Reply
			return nil
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		row, e := r.ID("operation")
		if e != nil {
			return e
		}
		if row == "" {
			return fmt.Errorf("empty operation row ID")
		}
		now := r.Now().UTC()
		outcome, e := apply(CommandContext{Tx: tx, RowID: row, Now: now})
		if e != nil {
			return e
		}
		if _, e = tx.ProjectWrites().AdvanceChange(ctx, c.ProjectID); e != nil {
			return e
		}
		reply, e = r.Encoder.Encode(kind, outcome)
		if e != nil {
			return e
		}
		return tx.OperationWrites().Save(ctx, port.Receipt{RowID: row, ProjectID: c.ProjectID, ActorID: c.Actor.ID, SessionID: c.SessionID, Scope: c.ProjectID, Key: m.Key, Kind: kind, Method: m.Method, Path: m.Path, Fingerprint: m.Fingerprint, RepresentationVersion: 1, Reply: reply, CreatedAt: now})
	})
	if err != nil {
		return port.StoredReply{}, err
	}
	return reply, nil
}
