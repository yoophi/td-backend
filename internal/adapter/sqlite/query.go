package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/tdq"
)

type queryRepo struct{ *scope }

func (s *scope) Queries() port.QueryReader { return queryRepo{s} }

func (r queryRepo) rows(ctx context.Context, query, project string, consume func(*sql.Rows) error) error {
	rows, e := r.tx.QueryContext(ctx, query, project)
	if e != nil {
		return classify(e)
	}
	defer rows.Close()
	for rows.Next() {
		if e = consume(rows); e != nil {
			return e
		}
	}
	return rows.Err()
}

// Facts uses five queries regardless of project issue count. All relations are
// project scoped and evaluated from the same existing transaction as Issues.
func (r queryRepo) Facts(ctx context.Context, project string, withDeleted bool) (tdq.Facts, error) {
	f := tdq.Facts{Issues: map[string]domain.Issue{}, Logs: map[string][]domain.Log{}, Comments: map[string][]domain.Comment{}, Handoffs: map[string]*domain.Handoff{}, Files: map[string][]domain.IssueFile{}, Dependencies: map[string][]string{}, Rework: map[string]bool{}}
	if e := r.check(); e != nil {
		return f, e
	}
	issues, e := r.Issues().All(ctx, project, withDeleted)
	if e != nil {
		return f, e
	}
	for _, i := range issues {
		f.Issues[i.ID] = i
	}
	live := " AND i.deleted_at IS NULL"
	if withDeleted {
		live = ""
	}
	e = r.rows(ctx, `SELECT a.id,a.issue_id,a.session_id,coalesce(a.work_session_id,''),a.kind,a.message,coalesce(a.log_type,''),a.payload_version,a.payload_json,a.created_at FROM activities a JOIN issues i ON i.project_id=a.project_id AND i.id=a.issue_id WHERE a.project_id=? AND a.deleted_at IS NULL`+live+` ORDER BY a.created_at,a.id`, project, func(rows *sql.Rows) error {
		var id, issue, session, ws, kind, message, logType, stamp string
		var payload []byte
		var version int
		if e := rows.Scan(&id, &issue, &session, &ws, &kind, &message, &logType, &version, &payload, &stamp); e != nil {
			return e
		}
		if version != 1 {
			return fmt.Errorf("unsupported query activity version %d", version)
		}
		now, e := time.Parse(time.RFC3339Nano, stamp)
		if e != nil {
			return e
		}
		switch kind {
		case "log":
			f.Logs[issue] = append(f.Logs[issue], domain.Log{ID: id, IssueID: issue, SessionID: session, WorkSessionID: ws, Message: message, Type: domain.LogType(logType), Timestamp: now})
		case "comment":
			f.Comments[issue] = append(f.Comments[issue], domain.Comment{ID: id, IssueID: issue, SessionID: session, Text: message, CreatedAt: now})
		case "handoff":
			var h domain.Handoff
			if e = json.Unmarshal(payload, &h); e != nil {
				return e
			}
			h.ID, h.IssueID, h.SessionID, h.Timestamp = id, issue, session, now
			f.Handoffs[issue] = &h
		}
		return nil
	})
	if e != nil {
		return f, e
	}
	e = r.rows(ctx, `SELECT d.issue_id,d.depends_on_id FROM issue_dependencies d JOIN issues i ON i.project_id=d.project_id AND i.id=d.issue_id WHERE d.project_id=?`+live+` ORDER BY d.issue_id,d.depends_on_id`, project, func(rows *sql.Rows) error {
		var id, target string
		if e := rows.Scan(&id, &target); e != nil {
			return e
		}
		f.Dependencies[id] = append(f.Dependencies[id], target)
		return nil
	})
	if e != nil {
		return f, e
	}
	e = r.rows(ctx, `SELECT f.id,f.issue_id,f.file_path,f.role,f.linked_sha,f.linked_at FROM issue_files f JOIN issues i ON i.project_id=f.project_id AND i.id=f.issue_id WHERE f.project_id=?`+live+` ORDER BY f.issue_id,f.file_path,f.role,f.id`, project, func(rows *sql.Rows) error {
		var file domain.IssueFile
		var stamp string
		if e := rows.Scan(&file.ID, &file.IssueID, &file.FilePath, &file.Role, &file.LinkedSHA, &stamp); e != nil {
			return e
		}
		var e error
		file.LinkedAt, e = time.Parse(time.RFC3339Nano, stamp)
		if e != nil {
			return e
		}
		f.Files[file.IssueID] = append(f.Files[file.IssueID], file)
		return nil
	})
	if e != nil {
		return f, e
	}
	// Effects carry state transitions for both explicit review and handoff's
	// combined submit-review transaction. Text logs are never parsed as events.
	e = r.rows(ctx, `SELECT e.entity_id,e.state_version,coalesce(json_extract(e.before_json,'$.issue.status'),''),json_extract(e.after_json,'$.issue.status') FROM operation_effects e JOIN operations o ON o.project_id=e.project_id AND o.row_id=e.operation_row_id JOIN issues i ON i.project_id=e.project_id AND i.id=e.entity_id WHERE e.project_id=? AND e.entity_kind='issue'`+live+` AND i.status IN ('open','in_progress') AND o.command_kind IN ('workflow.transition','workflow.handoff') AND NOT EXISTS (SELECT 1 FROM operation_undos u WHERE u.project_id=o.project_id AND u.original_row_id=o.row_id) ORDER BY o.created_at,o.row_id`, project, func(rows *sql.Rows) error {
		var id, before, after string
		var version int
		if e := rows.Scan(&id, &version, &before, &after); e != nil {
			return e
		}
		if version < 1 || version > 3 {
			return fmt.Errorf("unsupported query issue effect version %d", version)
		}
		if before == "in_review" && after == "open" {
			f.Rework[id] = true
		}
		if before != "in_review" && after == "in_review" {
			delete(f.Rework, id)
		}
		return nil
	})
	return f, e
}
