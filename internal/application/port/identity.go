package port

import (
	"context"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

type Authenticator interface {
	Resolve(context.Context, string) (domain.Actor, error)
}
type AccessReader interface {
	ActorByToken(context.Context, []byte) (domain.Actor, error)
	Role(context.Context, string, string) (domain.Role, error)
}
type AccessWriter interface {
	Bootstrap(context.Context, []byte, time.Time) error
	AssignRole(context.Context, string, string, domain.Role, time.Time) error
}
type SessionReader interface {
	Get(context.Context, string, string) (domain.Session, error)
	List(context.Context, string) ([]domain.Session, error)
}
type SessionWriter interface {
	Touch(context.Context, string, string, time.Time) error
	Create(context.Context, string, domain.Session) error
}
