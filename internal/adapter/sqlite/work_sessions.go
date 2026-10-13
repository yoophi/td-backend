package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type workSessionRepo struct{ *scope }

func (s *scope) WorkSessions() port.WorkSessionReader      { return workSessionRepo{s} }
func (s *scope) WorkSessionWrites() port.WorkSessionWriter { return workSessionRepo{s} }

const workSessionColumns = "id,session_id,name,revision,started_at,ended_at,start_sha,end_sha"

func scanWorkSession(row interface{ Scan(...any) error }) (domain.WorkSession, error) {
	var w domain.WorkSession
	var start string
	var end sql.NullString
	e := row.Scan(&w.ID, &w.SessionID, &w.Name, &w.Revision, &start, &end, &w.StartSHA, &w.EndSHA)
	if errors.Is(e, sql.ErrNoRows) {
		return w, domain.ErrNotFound
	}
	if e != nil {
		return w, classify(e)
	}
	if w.StartedAt, e = time.Parse(time.RFC3339Nano, start); e != nil {
		return w, e
	}
	if end.Valid {
		v, e := time.Parse(time.RFC3339Nano, end.String)
		if e != nil {
			return w, e
		}
		w.EndedAt = &v
	}
	return w, nil
}
func (r workSessionRepo) Get(ctx context.Context, project, id string) (domain.WorkSession, error) {
	if e := r.check(); e != nil {
		return domain.WorkSession{}, e
	}
	return scanWorkSession(r.tx.QueryRowContext(ctx, "SELECT "+workSessionColumns+" FROM work_sessions WHERE project_id=? AND id=?", project, id))
}
func (r workSessionRepo) List(ctx context.Context, project string) ([]domain.WorkSession, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, "SELECT "+workSessionColumns+" FROM work_sessions WHERE project_id=? ORDER BY started_at DESC,id", project)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	out := []domain.WorkSession{}
	for rows.Next() {
		w, e := scanWorkSession(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (r workSessionRepo) Tags(ctx context.Context, project, id string) ([]domain.WorkSessionIssue, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, `SELECT issue_id,tagged_at FROM work_session_issues WHERE project_id=? AND work_session_id=? ORDER BY tagged_at,issue_id`, project, id)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	out := []domain.WorkSessionIssue{}
	for rows.Next() {
		w := domain.WorkSessionIssue{WorkSessionID: id}
		var stamp string
		if e = rows.Scan(&w.IssueID, &stamp); e != nil {
			return nil, e
		}
		if w.TaggedAt, e = time.Parse(time.RFC3339Nano, stamp); e != nil {
			return nil, e
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (r workSessionRepo) Save(ctx context.Context, project string, w domain.WorkSession, expected int64) error {
	if e := r.check(); e != nil {
		return e
	}
	if expected < 0 || w.Revision != expected+1 {
		return domain.ErrConflict
	}
	if expected == 0 {
		_, e := r.tx.ExecContext(ctx, `INSERT INTO work_sessions(project_id,id,session_id,name,revision,started_at,ended_at,start_sha,end_sha) VALUES(?,?,?,?,1,?,?,?,?)`, project, w.ID, w.SessionID, w.Name, w.StartedAt.UTC().Format(time.RFC3339Nano), boardTime(w.EndedAt), w.StartSHA, w.EndSHA)
		return classify(e)
	}
	result, e := r.tx.ExecContext(ctx, `UPDATE work_sessions SET name=?,revision=?,ended_at=?,end_sha=? WHERE project_id=? AND id=? AND session_id=? AND revision=?`, w.Name, w.Revision, boardTime(w.EndedAt), w.EndSHA, project, w.ID, w.SessionID, expected)
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
func (r workSessionRepo) ReplaceTags(ctx context.Context, project, id string, tags []domain.WorkSessionIssue) error {
	if e := r.check(); e != nil {
		return e
	}
	if _, e := r.Get(ctx, project, id); e != nil {
		return e
	}
	for _, tag := range tags {
		if tag.WorkSessionID != id {
			return domain.ErrConflict
		}
	}
	if _, e := r.tx.ExecContext(ctx, `DELETE FROM work_session_issues WHERE project_id=? AND work_session_id=?`, project, id); e != nil {
		return classify(e)
	}
	for _, tag := range tags {
		if _, e := r.tx.ExecContext(ctx, `INSERT INTO work_session_issues(project_id,work_session_id,issue_id,tagged_at) VALUES(?,?,?,?)`, project, id, tag.IssueID, tag.TaggedAt.UTC().Format(time.RFC3339Nano)); e != nil {
			return classify(e)
		}
	}
	return nil
}
