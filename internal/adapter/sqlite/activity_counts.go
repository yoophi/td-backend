package sqlite

import (
	"context"
	"database/sql"

	"github.com/yoophi/td-backend/internal/application/port"
)

// Counts includes preserved activity on archived issues, as td's statistics do.
// Deleted activity is excluded. Empty projects return zero counts.
func (r workflowRepo) Counts(ctx context.Context, project string) (port.ActivityCounts, error) {
	out := port.ActivityCounts{}
	if e := r.check(); e != nil {
		return out, e
	}
	best := 0
	e := queryRepo{r.scope}.rows(ctx, `SELECT session_id,SUM(CASE WHEN kind='log' THEN 1 ELSE 0 END),SUM(CASE WHEN kind='handoff' THEN 1 ELSE 0 END) FROM activities WHERE project_id=? AND deleted_at IS NULL AND kind IN ('log','handoff') GROUP BY session_id ORDER BY session_id`, project, func(row *sql.Rows) error {
		var session string
		var logs, handoffs int
		if e := row.Scan(&session, &logs, &handoffs); e != nil {
			return e
		}
		out.Logs += logs
		out.Handoffs += handoffs
		if logs > best {
			best = logs
			out.MostActiveSession = session
		}
		return nil
	})
	return out, e
}
