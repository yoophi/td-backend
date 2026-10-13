// Package access centralizes project membership and session ownership checks.
package access

import (
	"context"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func invalid(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}

// Authorize is called inside a transaction, including before replaying a receipt.
func Authorize(ctx context.Context, read port.ReadSnapshot, c domain.Caller, session bool, write bool) error {
	if c.Actor.ID == "" {
		return domain.ErrUnauthorized
	}
	if !domain.ValidProjectID(c.ProjectID) {
		return invalid("project_id", "expected prj_ followed by UUIDv7")
	}
	role, e := read.Access().Role(ctx, c.ProjectID, c.Actor.ID)
	if e != nil {
		return e
	}
	if write && !role.CanWrite() {
		return domain.ErrForbidden
	}
	if session {
		if c.SessionID == "" {
			return invalid("X-TD-Session", "registered project session required")
		}
		s, e := read.Sessions().Get(ctx, c.ProjectID, c.SessionID)
		if e != nil {
			return e
		}
		if s.ActorID != c.Actor.ID {
			return domain.ErrForbidden
		}
	}
	return nil
}
