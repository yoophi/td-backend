// Package changes reads committed project counters and coalesces invalidations.
package changes

import (
	"context"
	"fmt"
	"time"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type Service struct {
	Reads port.ReadStore
	Now   func() time.Time
}
type Snapshot struct {
	Token     string
	Timestamp time.Time
}

func (s *Service) Current(ctx context.Context, c domain.Caller) (Snapshot, error) {
	var out Snapshot
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		var e error
		out, e = s.read(r, c.ProjectID)
		return e
	})
	return out, e
}
func (s *Service) committed(ctx context.Context, project string) (Snapshot, error) {
	var out Snapshot
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error { var e error; out, e = s.read(r, project); return e })
	return out, e
}
func (s *Service) read(r port.ReadSnapshot, project string) (Snapshot, error) {
	p, e := r.Projects().Get(r.Context(), project)
	if e != nil {
		return Snapshot{}, e
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	return Snapshot{fmt.Sprintf("%s:%d", p.ID, p.ChangeSeq), now}, nil
}
