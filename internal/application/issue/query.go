package issue

import (
	"context"
	"time"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/tdq"
)

// Query evaluates the expression against one project snapshot, including closed
// issues when the expression selects them. Soft-deleted issues are excluded.
func (s *Service) Query(ctx context.Context, c domain.Caller, query string) ([]string, error) {
	ids := []string{}
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		if _, e := tdq.ParseIssues(query); e != nil {
			return invalid("q", e.Error())
		}
		facts, e := r.Queries().Facts(r.Context(), c.ProjectID, false)
		if e != nil {
			return e
		}
		now := time.Now()
		if s.Runner.Now != nil {
			now = s.Runner.Now()
		}
		issues, e := tdq.Filter(query, &facts, tdq.EvalContext{CurrentSession: c.SessionID, Now: now}, true)
		if e != nil {
			return invalid("q", e.Error())
		}
		for _, i := range issues {
			ids = append(ids, i.ID)
		}
		return nil
	})
	return ids, e
}
