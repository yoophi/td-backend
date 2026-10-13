package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"reflect"
	"strings"
	"time"
)

// Column mappings come exclusively from the compiled domain struct, never input.
type issueField struct {
	index                     int
	key, column               string
	nullableString, boolValue bool
}

var issueFields, issueProjection = buildIssueMapping()

func buildIssueMapping() ([]issueField, string) {
	t := reflect.TypeOf(domain.Issue{})
	fields := []issueField{}
	projection := []string{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key := strings.Split(f.Tag.Get("json"), ",")[0]
		if key == "labels" {
			continue
		}
		column := key
		if key == "revision_number" {
			column = "revision"
		}
		nullableString := key == "parent_id" || key == "implementer_session" || key == "reviewer_session" || key == "review_requested_by_session" || key == "closed_by_session"
		expr := "i." + column
		if nullableString {
			expr = "coalesce(" + expr + ",'')"
		}
		if f.Type.Kind() == reflect.Bool {
			expr = "json(CASE " + expr + " WHEN 1 THEN 'true' ELSE 'false' END)"
		}
		projection = append(projection, "'"+key+"',"+expr)
		fields = append(fields, issueField{i, key, column, nullableString, f.Type.Kind() == reflect.Bool})
	}
	projection = append(projection, `'labels',json((SELECT json_group_array(label) FROM (SELECT label FROM issue_labels WHERE project_id=i.project_id AND issue_id=i.id ORDER BY label)))`)
	return fields, "json_object(" + strings.Join(projection, ",") + ")"
}

type issueRepo struct{ *scope }

func (r issueRepo) Get(ctx context.Context, project, id string, deleted bool) (*domain.Issue, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	q := "SELECT " + issueProjection + " FROM issues i WHERE project_id=? AND id=?"
	if !deleted {
		q += " AND deleted_at IS NULL"
	}
	var data []byte
	err := r.tx.QueryRowContext(ctx, q, project, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, classify(err)
	}
	var issue domain.Issue
	err = json.Unmarshal(data, &issue)
	return &issue, err
}
func (r issueRepo) List(ctx context.Context, project string, all bool) ([]domain.Issue, error) {
	return r.list(ctx, project, all, false)
}
func (r issueRepo) All(ctx context.Context, project string, deleted bool) ([]domain.Issue, error) {
	return r.list(ctx, project, true, deleted)
}
func (r issueRepo) list(ctx context.Context, project string, all, deleted bool) ([]domain.Issue, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	q := "SELECT " + issueProjection + " FROM issues i WHERE project_id=?"
	if !deleted {
		q += " AND deleted_at IS NULL"
	}
	if !all {
		q += " AND status <> 'closed'"
	}
	q += " ORDER BY priority,id"
	rows, err := r.tx.QueryContext(ctx, q, project)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	result := []domain.Issue{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var issue domain.Issue
		if err = json.Unmarshal(data, &issue); err != nil {
			return nil, err
		}
		result = append(result, issue)
	}
	return result, rows.Err()
}
func sqlValue(v reflect.Value, field issueField) any {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if t, ok := v.Interface().(time.Time); ok {
		return t.UTC().Format(time.RFC3339Nano)
	}
	if field.nullableString && v.String() == "" {
		return nil
	}
	return v.Interface()
}
func (r issueRepo) Save(ctx context.Context, project string, issue domain.Issue, expected int64) error {
	if err := r.check(); err != nil {
		return err
	}
	v := reflect.ValueOf(issue)
	columns := []string{}
	values := []any{}
	for _, field := range issueFields {
		columns = append(columns, field.column)
		values = append(values, sqlValue(v.Field(field.index), field))
	}
	if expected == 0 {
		if issue.Revision != 1 {
			return domain.ErrStale
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)+1), ",")
		args := append([]any{project}, values...)
		if _, err := r.tx.ExecContext(ctx, "INSERT INTO issues(project_id,"+strings.Join(columns, ",")+") VALUES("+placeholders+")", args...); err != nil {
			return classify(err)
		}
	} else {
		if issue.Revision != expected+1 {
			return domain.ErrStale
		}
		sets := make([]string, len(columns))
		for i, c := range columns {
			sets[i] = c + "=?"
		}
		values = append(values, project, issue.ID, expected)
		result, err := r.tx.ExecContext(ctx, "UPDATE issues SET "+strings.Join(sets, ",")+" WHERE project_id=? AND id=? AND revision=?", values...)
		if err != nil {
			return classify(err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrStale
		}
	}
	if _, err := r.tx.ExecContext(ctx, "DELETE FROM issue_labels WHERE project_id=? AND issue_id=?", project, issue.ID); err != nil {
		return classify(err)
	}
	for _, label := range issue.Labels {
		if _, err := r.tx.ExecContext(ctx, "INSERT OR IGNORE INTO issue_labels(project_id,issue_id,label) VALUES(?,?,?)", project, issue.ID, label); err != nil {
			return classify(err)
		}
	}
	return nil
}

func (r issueRepo) Dependencies(ctx context.Context, project, issue string) ([]port.Dependency, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, `SELECT id,issue_id,depends_on_id FROM issue_dependencies WHERE project_id=? AND issue_id=? ORDER BY depends_on_id`, project, issue)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	result := []port.Dependency{}
	for rows.Next() {
		var d port.Dependency
		if e = rows.Scan(&d.ID, &d.IssueID, &d.DependsOnID); e != nil {
			return nil, e
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
func (r issueRepo) AddDependency(ctx context.Context, project string, d port.Dependency) error {
	if e := r.check(); e != nil {
		return e
	}
	_, e := r.tx.ExecContext(ctx, `INSERT INTO issue_dependencies(project_id,id,issue_id,depends_on_id) VALUES(?,?,?,?)`, project, d.ID, d.IssueID, d.DependsOnID)
	return classify(e)
}
func (r issueRepo) SupersedeReviews(ctx context.Context, project, issue string, now time.Time) error {
	if e := r.check(); e != nil {
		return e
	}
	_, e := r.tx.ExecContext(ctx, `UPDATE issue_reviews SET superseded_at=? WHERE project_id=? AND issue_id=? AND superseded_at IS NULL`, now.UTC().Format(time.RFC3339Nano), project, issue)
	return classify(e)
}
func (r issueRepo) RecordCreation(ctx context.Context, project, issue, session, operation string, now time.Time) error {
	if e := r.check(); e != nil {
		return e
	}
	_, e := r.tx.ExecContext(ctx, `INSERT INTO issue_session_history(project_id,id,issue_id,session_id,action,operation_row_id,created_at) VALUES(?,?,?,?,'created',?,?)`, project, operation+":created:"+issue, issue, session, operation, now.UTC().Format(time.RFC3339Nano))
	return classify(e)
}

func (r issueRepo) RemoveDependency(ctx context.Context, project, issue, id string) error {
	if e := r.check(); e != nil {
		return e
	}
	result, e := r.tx.ExecContext(ctx, `DELETE FROM issue_dependencies WHERE project_id=? AND issue_id=? AND id=?`, project, issue, id)
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
func (r issueRepo) ReplaceFiles(ctx context.Context, project, issue string, files []domain.IssueFile) error {
	if e := r.check(); e != nil {
		return e
	}
	if _, e := r.tx.ExecContext(ctx, `DELETE FROM issue_files WHERE project_id=? AND issue_id=?`, project, issue); e != nil {
		return classify(e)
	}
	for _, file := range files {
		if file.IssueID != issue {
			return domain.ErrConflict
		}
		if _, e := r.tx.ExecContext(ctx, `INSERT INTO issue_files(project_id,id,issue_id,file_path,role,linked_sha,linked_at) VALUES(?,?,?,?,?,?,?)`, project, file.ID, issue, file.FilePath, file.Role, file.LinkedSHA, file.LinkedAt.UTC().Format(time.RFC3339Nano)); e != nil {
			return classify(e)
		}
	}
	return nil
}
