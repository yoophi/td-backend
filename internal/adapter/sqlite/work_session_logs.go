package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

const workSessionLogColumns = "id,work_session_id,session_id,revision,message,log_type,created_at,deleted_at"

func scanWorkSessionLog(row interface{ Scan(...any) error }) (domain.WorkSessionLog, error) {
	var log domain.WorkSessionLog
	var created string
	var removed sql.NullString
	e := row.Scan(&log.ID, &log.WorkSessionID, &log.SessionID, &log.Revision, &log.Message, &log.Type, &created, &removed)
	if errors.Is(e, sql.ErrNoRows) {
		return log, domain.ErrNotFound
	}
	if e != nil {
		return log, classify(e)
	}
	if log.Timestamp, e = time.Parse(time.RFC3339Nano, created); e != nil {
		return log, e
	}
	if removed.Valid {
		stamp, e := time.Parse(time.RFC3339Nano, removed.String)
		if e != nil {
			return log, e
		}
		log.DeletedAt = &stamp
	}
	return log, nil
}

func (r workSessionRepo) GetLogArchived(ctx context.Context, project, id string) (domain.WorkSessionLog, error) {
	if e := r.check(); e != nil {
		return domain.WorkSessionLog{}, e
	}
	return scanWorkSessionLog(r.tx.QueryRowContext(ctx, "SELECT "+workSessionLogColumns+" FROM work_session_logs WHERE project_id=? AND id=?", project, id))
}

func (r workSessionRepo) Logs(ctx context.Context, project, workSession string) ([]domain.WorkSessionLog, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, "SELECT "+workSessionLogColumns+" FROM work_session_logs WHERE project_id=? AND work_session_id=? AND deleted_at IS NULL ORDER BY created_at,id", project, workSession)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	out := []domain.WorkSessionLog{}
	for rows.Next() {
		log, e := scanWorkSessionLog(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

func (r workSessionRepo) SaveLog(ctx context.Context, project string, log domain.WorkSessionLog, expected int64, operationRowID string) error {
	if e := r.check(); e != nil {
		return e
	}
	if expected < 0 || log.Revision != expected+1 {
		return domain.ErrConflict
	}
	if expected == 0 {
		_, e := r.tx.ExecContext(ctx, `INSERT INTO work_session_logs(project_id,id,work_session_id,session_id,operation_row_id,revision,message,log_type,created_at,deleted_at) VALUES(?,?,?,?,?,1,?,?,?,?)`, project, log.ID, log.WorkSessionID, log.SessionID, operationRowID, log.Message, log.Type, log.Timestamp.UTC().Format(time.RFC3339Nano), boardTime(log.DeletedAt))
		return classify(e)
	}
	result, e := r.tx.ExecContext(ctx, `UPDATE work_session_logs SET revision=?,deleted_at=? WHERE project_id=? AND id=? AND work_session_id=? AND session_id=? AND revision=?`, log.Revision, boardTime(log.DeletedAt), project, log.ID, log.WorkSessionID, log.SessionID, expected)
	if e != nil {
		return classify(e)
	}
	count, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if count != 1 {
		return domain.ErrStale
	}
	return nil
}
