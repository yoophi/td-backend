package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func (r issueRepo) Context(ctx context.Context, project, id string, reviews bool) (port.IssueContext, error) {
	result := port.IssueContext{Logs: []domain.Log{}, Comments: []domain.Comment{}, Children: []domain.Issue{}, Dependencies: []port.Dependency{}, BlockedBy: []port.Dependency{}, Files: []domain.IssueFile{}, Reviews: []domain.IssueReview{}}
	if e := r.check(); e != nil {
		return result, e
	}
	if e := r.readActivities(ctx, project, id, &result); e != nil {
		return result, e
	}
	// All queries use this repository's transaction; no separate pool read can
	// observe a different revision between issue and related records.
	all, e := r.List(ctx, project, true)
	if e != nil {
		return result, e
	}
	for _, i := range all {
		if i.ParentID == id {
			result.Children = append(result.Children, i)
		}
	}
	if result.Dependencies, e = r.Dependencies(ctx, project, id); e != nil {
		return result, e
	}
	rows, e := r.tx.QueryContext(ctx, `SELECT id,issue_id,depends_on_id FROM issue_dependencies WHERE project_id=? AND depends_on_id=? ORDER BY issue_id`, project, id)
	if e != nil {
		return result, classify(e)
	}
	for rows.Next() {
		var d port.Dependency
		if e = rows.Scan(&d.ID, &d.IssueID, &d.DependsOnID); e != nil {
			rows.Close()
			return result, e
		}
		result.BlockedBy = append(result.BlockedBy, d)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return result, e
	}
	result.Files, e = r.Files(ctx, project, id)
	if e != nil {
		return result, e
	}
	if reviews {
		e = r.readReviews(ctx, project, id, &result)
	}
	return result, e
}
func (r issueRepo) readActivities(ctx context.Context, project, id string, result *port.IssueContext) error {
	rows, e := r.tx.QueryContext(ctx, `SELECT id,session_id,coalesce(work_session_id,''),kind,message,coalesce(log_type,''),payload_version,payload_json,created_at FROM activities WHERE project_id=? AND issue_id=? AND deleted_at IS NULL ORDER BY created_at,id`, project, id)
	if e != nil {
		return classify(e)
	}
	defer rows.Close()
	for rows.Next() {
		var activityID, session, ws, kind, message, logType, payload, stamp string
		var version int
		if e = rows.Scan(&activityID, &session, &ws, &kind, &message, &logType, &version, &payload, &stamp); e != nil {
			return e
		}
		now, e := time.Parse(time.RFC3339Nano, stamp)
		if e != nil {
			return e
		}
		if version != 1 {
			return fmt.Errorf("unsupported activity payload version %d", version)
		}
		switch kind {
		case "log":
			result.Logs = append(result.Logs, domain.Log{ID: activityID, IssueID: id, SessionID: session, WorkSessionID: ws, Message: message, Type: domain.LogType(logType), Timestamp: now})
		case "comment":
			result.Comments = append(result.Comments, domain.Comment{ID: activityID, IssueID: id, SessionID: session, Text: message, CreatedAt: now})
		case "handoff":
			var handoff domain.Handoff
			if e = json.Unmarshal([]byte(payload), &handoff); e != nil {
				return e
			}
			handoff.ID = activityID
			handoff.IssueID = id
			handoff.SessionID = session
			handoff.Timestamp = now
			result.LatestHandoff = &handoff
		}
	}
	return rows.Err()
}
func (r issueRepo) readReviews(ctx context.Context, project, id string, result *port.IssueContext) error {
	rows, e := r.tx.QueryContext(ctx, `SELECT id,reviewer_session,coalesce(requested_by_session,''),decision,summary,self_review,reviewed_by,created_at,coalesce(superseded_at,'') FROM issue_reviews WHERE project_id=? AND issue_id=? ORDER BY created_at,id`, project, id)
	if e != nil {
		return classify(e)
	}
	defer rows.Close()
	for rows.Next() {
		var review domain.IssueReview
		var stamp, superseded string
		review.IssueID = id
		if e = rows.Scan(&review.ID, &review.ReviewerSession, &review.RequestedBySession, &review.Decision, &review.Summary, &review.SelfReview, &review.ReviewedBy, &stamp, &superseded); e != nil {
			return e
		}
		review.CreatedAt, e = time.Parse(time.RFC3339Nano, stamp)
		if e != nil {
			return e
		}
		if superseded != "" {
			v, e := time.Parse(time.RFC3339Nano, superseded)
			if e != nil {
				return e
			}
			review.SupersededAt = &v
		}
		result.Reviews = append(result.Reviews, review)
	}
	return rows.Err()
}

func (r issueRepo) Files(ctx context.Context, project, id string) ([]domain.IssueFile, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, `SELECT id,issue_id,file_path,role,linked_sha,linked_at FROM issue_files WHERE project_id=? AND issue_id=? ORDER BY file_path,role,id`, project, id)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	result := []domain.IssueFile{}
	for rows.Next() {
		var f domain.IssueFile
		var stamp string
		if e = rows.Scan(&f.ID, &f.IssueID, &f.FilePath, &f.Role, &f.LinkedSHA, &stamp); e != nil {
			return nil, e
		}
		f.LinkedAt, e = time.Parse(time.RFC3339Nano, stamp)
		if e != nil {
			return nil, e
		}
		result = append(result, f)
	}
	return result, rows.Err()
}
