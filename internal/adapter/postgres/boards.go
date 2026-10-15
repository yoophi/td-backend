package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type boardRepo struct{ *scope }

func (s *scope) Boards() port.BoardReader { return boardRepo{s} }

const boardColumns = "id,name,query,is_builtin,view_mode,revision,last_viewed_at,created_at,updated_at,deleted_at"

func scanBoard(row interface{ Scan(...any) error }) (domain.Board, error) {
	var b domain.Board
	var created, updated string
	var viewed, deleted sql.NullString
	e := row.Scan(&b.ID, &b.Name, &b.Query, &b.IsBuiltin, &b.ViewMode, &b.Revision, &viewed, &created, &updated, &deleted)
	if errors.Is(e, sql.ErrNoRows) {
		return b, domain.ErrNotFound
	}
	if e != nil {
		return b, classify(e)
	}
	if b.CreatedAt, e = time.Parse(time.RFC3339Nano, created); e != nil {
		return b, e
	}
	if b.UpdatedAt, e = time.Parse(time.RFC3339Nano, updated); e != nil {
		return b, e
	}
	for _, pair := range []struct {
		raw    sql.NullString
		target **time.Time
	}{{viewed, &b.LastViewedAt}, {deleted, &b.DeletedAt}} {
		if pair.raw.Valid {
			v, err := time.Parse(time.RFC3339Nano, pair.raw.String)
			if err != nil {
				return b, err
			}
			*pair.target = &v
		}
	}
	return b, nil
}

func (r boardRepo) Get(ctx context.Context, project, id string, deleted bool) (domain.Board, error) {
	if e := r.check(); e != nil {
		return domain.Board{}, e
	}
	q := "SELECT " + boardColumns + " FROM boards WHERE project_id=? AND id=?"
	if !deleted {
		q += " AND deleted_at IS NULL"
	}
	return scanBoard(r.tx.QueryRowContext(ctx, q, project, id))
}

func (r boardRepo) List(ctx context.Context, project string) ([]domain.Board, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, "SELECT "+boardColumns+" FROM boards WHERE project_id=? AND deleted_at IS NULL ORDER BY is_builtin DESC,name,id", project)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	result := []domain.Board{}
	for rows.Next() {
		b, e := scanBoard(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

func boardTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (r boardRepo) Create(ctx context.Context, project string, b domain.Board) error {
	if e := r.check(); e != nil {
		return e
	}
	_, e := r.tx.ExecContext(ctx, `INSERT INTO boards(project_id,id,name,query,is_builtin,view_mode,revision,last_viewed_at,created_at,updated_at,deleted_at) VALUES(?,?,?,?,?,?,1,?,?,?,?)`, project, b.ID, b.Name, b.Query, b.IsBuiltin, b.ViewMode, boardTime(b.LastViewedAt), b.CreatedAt.UTC().Format(time.RFC3339Nano), b.UpdatedAt.UTC().Format(time.RFC3339Nano), boardTime(b.DeletedAt))
	return classify(e)
}

func (r boardRepo) Save(ctx context.Context, project string, b domain.Board, expected int64) error {
	if e := r.check(); e != nil {
		return e
	}
	if expected < 1 || b.Revision != expected+1 {
		return domain.ErrConflict
	}
	result, e := r.tx.ExecContext(ctx, `UPDATE boards SET name=?,query=?,is_builtin=?,view_mode=?,revision=?,last_viewed_at=?,updated_at=?,deleted_at=? WHERE project_id=? AND id=? AND revision=?`, b.Name, b.Query, b.IsBuiltin, b.ViewMode, b.Revision, boardTime(b.LastViewedAt), b.UpdatedAt.UTC().Format(time.RFC3339Nano), boardTime(b.DeletedAt), project, b.ID, expected)
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

func (r boardRepo) Positions(ctx context.Context, project, id string) ([]domain.BoardIssue, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, `SELECT issue_id,position,added_at FROM board_positions WHERE project_id=? AND board_id=? ORDER BY position,issue_id`, project, id)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	result := []domain.BoardIssue{}
	for rows.Next() {
		b := domain.BoardIssue{BoardID: id}
		var added string
		if e = rows.Scan(&b.IssueID, &b.Position, &added); e != nil {
			return nil, e
		}
		if b.AddedAt, e = time.Parse(time.RFC3339Nano, added); e != nil {
			return nil, e
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

func (r boardRepo) ReplacePositions(ctx context.Context, project, id string, positions []domain.BoardIssue) error {
	if e := r.check(); e != nil {
		return e
	}
	if _, e := r.Get(ctx, project, id, false); e != nil {
		return e
	}
	for _, p := range positions {
		if p.BoardID != id {
			return domain.ErrConflict
		}
	}
	// Delete then insert inside the writer transaction avoids transient UNIQUE
	// collisions when two existing cards swap sparse sort keys.
	if _, e := r.tx.ExecContext(ctx, `DELETE FROM board_positions WHERE project_id=? AND board_id=?`, project, id); e != nil {
		return classify(e)
	}
	for _, p := range positions {
		_, e := r.tx.ExecContext(ctx, `INSERT INTO board_positions(project_id,board_id,issue_id,position,added_at) VALUES(?,?,?,?,?)`, project, id, p.IssueID, p.Position, p.AddedAt.UTC().Format(time.RFC3339Nano))
		if e != nil {
			return classify(e)
		}
	}
	return nil
}
