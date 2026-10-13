package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

func (r workflowRepo) Activities(ctx context.Context, project, issue string, deleted bool) ([]port.Activity, error) {
	return r.activityList(ctx, project, issue, deleted, 0, "", time.Time{})
}
func (r workflowRepo) Feed(ctx context.Context, project string, limit int) ([]port.Activity, error) {
	if limit < 1 || limit > 1000 {
		return nil, domain.ErrConflict
	}
	return r.activityList(ctx, project, "", false, limit, "", time.Time{})
}
func (r workflowRepo) RecentHandoffs(ctx context.Context, project string, since time.Time) ([]port.Activity, error) {
	return r.activityList(ctx, project, "", false, 0, "handoff", since)
}
func (r workflowRepo) activityList(ctx context.Context, project, issue string, deleted bool, limit int, kind string, since time.Time) ([]port.Activity, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	q := `SELECT a.id,a.issue_id,a.session_id,coalesce(a.work_session_id,''),a.operation_row_id,o.operation_id,actor.name,a.kind,a.message,coalesce(a.log_type,''),a.content_version,a.payload_version,a.payload_json,a.created_at,a.updated_at,coalesce(a.deleted_at,'') FROM activities a JOIN operations o ON a.operation_row_id=o.row_id JOIN actors actor ON o.actor_id=actor.id WHERE a.project_id=?`
	args := []any{project}
	if issue != "" {
		q += " AND a.issue_id=?"
		args = append(args, issue)
	} else {
		q += " AND EXISTS (SELECT 1 FROM issues i WHERE i.project_id=a.project_id AND i.id=a.issue_id AND i.deleted_at IS NULL)"
	}
	if kind != "" {
		q += " AND a.kind=?"
		args = append(args, kind)
	}
	if !since.IsZero() {
		q += " AND a.created_at>=?"
		args = append(args, since.UTC().Format(time.RFC3339Nano))
	}
	if !deleted {
		q += " AND a.deleted_at IS NULL"
	}
	if kind != "" {
		q += " ORDER BY a.created_at DESC,a.id DESC"
	} else if limit > 0 {
		q += " ORDER BY a.created_at DESC,a.id DESC LIMIT ?"
		args = append(args, limit)
	} else {
		q += " ORDER BY a.created_at,a.id"
	}
	rows, e := r.tx.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	result := []port.Activity{}
	for rows.Next() {
		var a port.Activity
		var version int
		var payload []byte
		var created, updated, removed string
		a.IssueID = issue
		if e = rows.Scan(&a.ID, &a.IssueID, &a.SessionID, &a.WorkSessionID, &a.OperationRowID, &a.OperationID, &a.Author, &a.Kind, &a.Message, &a.LogType, &a.ContentVersion, &version, &payload, &created, &updated, &removed); e != nil {
			return nil, e
		}
		if version != 1 {
			return nil, fmt.Errorf("unsupported activity payload version %d", version)
		}
		var decoded activityPayload
		if e = json.Unmarshal(payload, &decoded); e != nil {
			return nil, e
		}
		a.Done = decoded.Done
		a.Remaining = decoded.Remaining
		a.Decisions = decoded.Decisions
		a.Uncertain = decoded.Uncertain
		a.Snapshot = decoded.Snapshot
		a.SnapshotEvent = decoded.SnapshotEvent
		a.CreatedAt, e = time.Parse(time.RFC3339Nano, created)
		if e != nil {
			return nil, e
		}
		a.UpdatedAt, e = time.Parse(time.RFC3339Nano, updated)
		if e != nil {
			return nil, e
		}
		if removed != "" {
			stamp, e := time.Parse(time.RFC3339Nano, removed)
			if e != nil {
				return nil, e
			}
			a.DeletedAt = &stamp
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
func (r workflowRepo) DeleteComment(ctx context.Context, project, issue, id string, now time.Time) error {
	if e := r.check(); e != nil {
		return e
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	result, e := r.tx.ExecContext(ctx, `UPDATE activities SET deleted_at=?,updated_at=? WHERE project_id=? AND issue_id=? AND id=? AND kind='comment' AND deleted_at IS NULL`, stamp, stamp, project, issue, id)
	if e != nil {
		return classify(e)
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return domain.ErrNotFound
	}
	return nil
}
func (r workflowRepo) WorkSessionAccess(ctx context.Context, project, id string) (port.WorkSessionAccess, error) {
	var access port.WorkSessionAccess
	if e := r.check(); e != nil {
		return access, e
	}
	e := r.tx.QueryRowContext(ctx, `SELECT session_id,ended_at IS NOT NULL FROM work_sessions WHERE project_id=? AND id=? AND deleted_at IS NULL`, project, id).Scan(&access.SessionID, &access.Ended)
	if errors.Is(e, sql.ErrNoRows) {
		return access, domain.ErrNotFound
	}
	return access, classify(e)
}

// SetActivityDeleted restores or hides an activity without deleting its audit data.
func (r workflowRepo) SetActivityDeleted(ctx context.Context, project, issue, id string, deleted *time.Time, now time.Time) error {
	if e := r.check(); e != nil {
		return e
	}
	result, e := r.tx.ExecContext(ctx, `UPDATE activities SET deleted_at=?,updated_at=? WHERE project_id=? AND issue_id=? AND id=?`, boardTime(deleted), now.UTC().Format(time.RFC3339Nano), project, issue, id)
	if e != nil {
		return classify(e)
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return domain.ErrNotFound
	}
	return nil
}
