package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type focusRepo struct{ *scope }

func (s *scope) Focus() port.FocusReader       { return focusRepo{s} }
func (s *scope) FocusWrites() port.FocusWriter { return focusRepo{s} }
func (r focusRepo) Get(ctx context.Context, project, session string) (domain.SessionFocus, error) {
	f := domain.SessionFocus{SessionID: session}
	if e := r.check(); e != nil {
		return f, e
	}
	var issue sql.NullString
	e := r.tx.QueryRowContext(ctx, `SELECT issue_id,revision FROM session_focus WHERE project_id=? AND session_id=?`, project, session).Scan(&issue, &f.Revision)
	if errors.Is(e, sql.ErrNoRows) {
		return f, nil
	}
	if e != nil {
		return f, classify(e)
	}
	if issue.Valid {
		f.IssueID = &issue.String
	}
	return f, nil
}
func (r focusRepo) Save(ctx context.Context, project string, f domain.SessionFocus, expected int64) error {
	if e := r.check(); e != nil {
		return e
	}
	if expected < 0 || f.Revision != expected+1 {
		return domain.ErrConflict
	}
	if expected == 0 {
		_, e := r.tx.ExecContext(ctx, `INSERT INTO session_focus(project_id,session_id,issue_id,revision) VALUES(?,?,?,1)`, project, f.SessionID, f.IssueID)
		return classify(e)
	}
	result, e := r.tx.ExecContext(ctx, `UPDATE session_focus SET issue_id=?,revision=? WHERE project_id=? AND session_id=? AND revision=?`, f.IssueID, f.Revision, project, f.SessionID, expected)
	if e != nil {
		return classify(e)
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return domain.ErrStale
	}
	return nil
}
