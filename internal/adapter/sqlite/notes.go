package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type noteRepo struct{ *scope }

func (s *scope) Notes() port.NoteReader      { return noteRepo{s} }
func (s *scope) NoteWrites() port.NoteWriter { return noteRepo{s} }

const noteColumns = "id,title,content,pinned,archived,revision,created_at,updated_at,deleted_at"

func scanNote(row interface{ Scan(...any) error }) (domain.Note, error) {
	var n domain.Note
	var created, updated string
	var deleted sql.NullString
	e := row.Scan(&n.ID, &n.Title, &n.Content, &n.Pinned, &n.Archived, &n.Revision, &created, &updated, &deleted)
	if errors.Is(e, sql.ErrNoRows) {
		return n, domain.ErrNotFound
	}
	if e != nil {
		return n, classify(e)
	}
	if n.CreatedAt, e = time.Parse(time.RFC3339Nano, created); e != nil {
		return n, e
	}
	if n.UpdatedAt, e = time.Parse(time.RFC3339Nano, updated); e != nil {
		return n, e
	}
	if deleted.Valid {
		t, err := time.Parse(time.RFC3339Nano, deleted.String)
		if err != nil {
			return n, err
		}
		n.DeletedAt = &t
	}
	return n, nil
}
func (r noteRepo) Get(ctx context.Context, project, id string, deleted bool) (domain.Note, error) {
	if e := r.check(); e != nil {
		return domain.Note{}, e
	}
	q := "SELECT " + noteColumns + " FROM notes WHERE project_id=? AND id=?"
	if !deleted {
		q += " AND deleted_at IS NULL"
	}
	return scanNote(r.tx.QueryRowContext(ctx, q, project, id))
}
func (r noteRepo) List(ctx context.Context, project string) ([]domain.Note, error) {
	return r.list(ctx, project, false)
}
func (r noteRepo) ListIncludingDeleted(ctx context.Context, project string) ([]domain.Note, error) {
	return r.list(ctx, project, true)
}
func (r noteRepo) list(ctx context.Context, project string, includeDeleted bool) ([]domain.Note, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	query := "SELECT " + noteColumns + " FROM notes WHERE project_id=?"
	if !includeDeleted {
		query += " AND deleted_at IS NULL"
	}
	query += " ORDER BY pinned DESC,updated_at DESC,id"
	rows, e := r.tx.QueryContext(ctx, query, project)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	result := []domain.Note{}
	for rows.Next() {
		n, e := scanNote(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, n)
	}
	return result, rows.Err()
}
func (r noteRepo) Save(ctx context.Context, project string, n domain.Note, expected int64) error {
	if e := r.check(); e != nil {
		return e
	}
	if expected < 0 || n.Revision != expected+1 {
		return domain.ErrConflict
	}
	if expected == 0 {
		_, e := r.tx.ExecContext(ctx, `INSERT INTO notes(project_id,id,title,content,pinned,archived,revision,created_at,updated_at,deleted_at) VALUES(?,?,?,?,?,?,1,?,?,?)`, project, n.ID, n.Title, n.Content, n.Pinned, n.Archived, n.CreatedAt.UTC().Format(time.RFC3339Nano), n.UpdatedAt.UTC().Format(time.RFC3339Nano), boardTime(n.DeletedAt))
		return classify(e)
	}
	result, e := r.tx.ExecContext(ctx, `UPDATE notes SET title=?,content=?,pinned=?,archived=?,revision=?,updated_at=?,deleted_at=? WHERE project_id=? AND id=? AND revision=?`, n.Title, n.Content, n.Pinned, n.Archived, n.Revision, n.UpdatedAt.UTC().Format(time.RFC3339Nano), boardTime(n.DeletedAt), project, n.ID, expected)
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
