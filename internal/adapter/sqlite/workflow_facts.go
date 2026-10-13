package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

// Facts uses three batch queries in the caller's existing snapshot. Policy
// freshness remains an application decision; stale basis data is not hidden.
func (r workflowRepo) Facts(ctx context.Context, project string) (port.WorkflowFacts, error) {
	out := port.WorkflowFacts{History: map[string][]domain.IssueSessionHistory{}, ActiveReviews: map[string]*port.Review{}, LatestHandoffs: map[string]*port.Activity{}}
	if e := r.check(); e != nil {
		return out, e
	}
	rows := queryRepo{r.scope}.rows
	e := rows(ctx, `SELECT h.id,h.issue_id,h.session_id,h.action,h.created_at FROM issue_session_history h JOIN issues i ON i.project_id=h.project_id AND i.id=h.issue_id WHERE h.project_id=? AND i.deleted_at IS NULL ORDER BY h.created_at,h.id`, project, func(row *sql.Rows) error {
		var h domain.IssueSessionHistory
		var stamp string
		if e := row.Scan(&h.ID, &h.IssueID, &h.SessionID, &h.Action, &stamp); e != nil {
			return e
		}
		var e error
		if h.CreatedAt, e = time.Parse(time.RFC3339Nano, stamp); e != nil {
			return e
		}
		out.History[h.IssueID] = append(out.History[h.IssueID], h)
		return nil
	})
	if e != nil {
		return out, e
	}
	e = rows(ctx, `SELECT r.id,r.issue_id,r.reviewer_session,coalesce(r.requested_by_session,''),r.decision,r.summary,r.self_review,r.reviewed_by,r.basis_content_version,coalesce(r.basis_handoff_id,''),r.operation_row_id,r.created_at FROM issue_reviews r JOIN issues i ON i.project_id=r.project_id AND i.id=r.issue_id WHERE r.project_id=? AND r.superseded_at IS NULL AND i.deleted_at IS NULL ORDER BY r.issue_id`, project, func(row *sql.Rows) error {
		var review port.Review
		v := &review.Record
		var stamp string
		if e := row.Scan(&v.ID, &v.IssueID, &v.ReviewerSession, &v.RequestedBySession, &v.Decision, &v.Summary, &v.SelfReview, &v.ReviewedBy, &review.BasisContentVersion, &review.BasisHandoffID, &review.OperationRowID, &stamp); e != nil {
			return e
		}
		var e error
		if v.CreatedAt, e = time.Parse(time.RFC3339Nano, stamp); e != nil {
			return e
		}
		out.ActiveReviews[v.IssueID] = &review
		return nil
	})
	if e != nil {
		return out, e
	}
	e = rows(ctx, `SELECT id,issue_id,session_id,coalesce(work_session_id,''),operation_row_id,content_version,payload_version,payload_json,created_at FROM (SELECT a.*,row_number() OVER (PARTITION BY a.issue_id ORDER BY a.created_at DESC,a.id DESC) AS newest FROM activities a JOIN issues i ON i.project_id=a.project_id AND i.id=a.issue_id WHERE a.project_id=? AND a.kind='handoff' AND a.deleted_at IS NULL AND i.deleted_at IS NULL) WHERE newest=1 ORDER BY issue_id`, project, func(row *sql.Rows) error {
		a := port.Activity{Kind: "handoff"}
		var stamp string
		var version int
		var payload []byte
		if e := row.Scan(&a.ID, &a.IssueID, &a.SessionID, &a.WorkSessionID, &a.OperationRowID, &a.ContentVersion, &version, &payload, &stamp); e != nil {
			return e
		}
		if version != 1 {
			return fmt.Errorf("unsupported handoff payload version %d", version)
		}
		var decoded activityPayload
		if e := json.Unmarshal(payload, &decoded); e != nil {
			return e
		}
		a.Done, a.Remaining, a.Decisions, a.Uncertain = decoded.Done, decoded.Remaining, decoded.Decisions, decoded.Uncertain
		a.Snapshot, a.SnapshotEvent = decoded.Snapshot, decoded.SnapshotEvent
		var e error
		if a.CreatedAt, e = time.Parse(time.RFC3339Nano, stamp); e != nil {
			return e
		}
		out.LatestHandoffs[a.IssueID] = &a
		return nil
	})
	return out, e
}
