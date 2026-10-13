package tokenauth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

type Auth struct {
	reads  port.ReadStore
	writes port.UnitOfWork
}

func New(reads port.ReadStore, writes port.UnitOfWork) *Auth { return &Auth{reads, writes} }
func (a *Auth) Bootstrap(ctx context.Context, token string) error {
	if len(token) < 32 {
		return fmt.Errorf("TD_BACKEND_BOOTSTRAP_TOKEN must contain at least 32 characters")
	}
	digest := sha256.Sum256([]byte(token))
	return a.writes.Write(ctx, func(tx port.TxScope) error { return tx.AccessWrites().Bootstrap(ctx, digest[:], time.Now().UTC()) })
}
func (a *Auth) Resolve(ctx context.Context, token string) (domain.Actor, error) {
	if token == "" {
		return domain.Actor{}, domain.ErrUnauthorized
	}
	digest := sha256.Sum256([]byte(token))
	var actor domain.Actor
	err := a.reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		var e error
		actor, e = read.Access().ActorByToken(ctx, digest[:])
		return e
	})
	return actor, err
}
