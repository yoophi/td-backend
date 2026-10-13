package operation

import (
	"context"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type AuditService struct{ Reads port.ReadStore }

func auditInvalid(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}
func (s *AuditService) Get(ctx context.Context, c domain.Caller, key string) (port.Receipt, error) {
	var out port.Receipt
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		if !domain.ValidOperationKey(key) {
			return auditInvalid("operation_id", "UUIDv4 operation key required")
		}
		var e error
		out, e = r.Operations().AuditGet(r.Context(), c.ProjectID, key)
		return e
	})
	return out, e
}
func (s *AuditService) List(ctx context.Context, c domain.Caller, f port.AuditFilter) ([]port.Receipt, error) {
	out := []port.Receipt{}
	if f.Limit == 0 {
		f.Limit = 200
	}
	if f.Limit < 1 || f.Limit > 1000 || f.Offset < 0 {
		return out, auditInvalid("pagination", "limit must be 1..1000 and offset nonnegative")
	}
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		var e error
		out, e = r.Operations().List(r.Context(), c.ProjectID, f)
		return e
	})
	return out, e
}
