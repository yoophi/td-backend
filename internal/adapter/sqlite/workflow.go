package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type workflowRepo struct{ *scope }

func (s *scope) Workflow() port.WorkflowReader       { return workflowRepo{s} }
func (s *scope) WorkflowWrites() port.WorkflowWriter { return workflowRepo{s} }
func (r workflowRepo) History(ctx context.Context, project, issue string) ([]domain.IssueSessionHistory, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, `SELECT id,session_id,action,created_at FROM issue_session_history WHERE project_id=? AND issue_id=? ORDER BY created_at,id`, project, issue)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	result := []domain.IssueSessionHistory{}
	for rows.Next() {
		var h domain.IssueSessionHistory
		var stamp string
		h.IssueID = issue
		if e = rows.Scan(&h.ID, &h.SessionID, &h.Action, &stamp); e != nil {
			return nil, e
		}
		h.CreatedAt, e = time.Parse(time.RFC3339Nano, stamp)
		if e != nil {
			return nil, e
		}
		result = append(result, h)
	}
	return result, rows.Err()
}
func (r workflowRepo) RecordHistory(ctx context.Context, project string, h domain.IssueSessionHistory, operation string) error {
	if e := r.check(); e != nil {
		return e
	}
	_, e := r.tx.ExecContext(ctx, `INSERT INTO issue_session_history(project_id,id,issue_id,session_id,action,operation_row_id,created_at) VALUES(?,?,?,?,?,?,?)`, project, h.ID, h.IssueID, h.SessionID, h.Action, operation, h.CreatedAt.UTC().Format(time.RFC3339Nano))
	return classify(e)
}
func (r workflowRepo) ActiveReview(ctx context.Context, project, issue string) (*port.Review, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	var result port.Review
	v := &result.Record
	v.IssueID = issue
	var stamp string
	e := r.tx.QueryRowContext(ctx, `SELECT id,reviewer_session,coalesce(requested_by_session,''),decision,summary,self_review,reviewed_by,basis_content_version,coalesce(basis_handoff_id,''),operation_row_id,created_at FROM issue_reviews WHERE project_id=? AND issue_id=? AND superseded_at IS NULL`, project, issue).Scan(&v.ID, &v.ReviewerSession, &v.RequestedBySession, &v.Decision, &v.Summary, &v.SelfReview, &v.ReviewedBy, &result.BasisContentVersion, &result.BasisHandoffID, &result.OperationRowID, &stamp)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, classify(e)
	}
	v.CreatedAt, e = time.Parse(time.RFC3339Nano, stamp)
	return &result, e
}
func (r workflowRepo) SaveReview(ctx context.Context, project string, review port.Review) error {
	if e := r.check(); e != nil {
		return e
	}
	v := review.Record
	var superseded any
	if v.SupersededAt != nil {
		superseded = v.SupersededAt.UTC().Format(time.RFC3339Nano)
	}
	_, e := r.tx.ExecContext(ctx, `INSERT INTO issue_reviews(project_id,id,issue_id,reviewer_session,requested_by_session,decision,summary,self_review,reviewed_by,basis_content_version,basis_handoff_id,operation_row_id,created_at,superseded_at) VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?,?,NULLIF(?,''),?,?,?)`, project, v.ID, v.IssueID, v.ReviewerSession, v.RequestedBySession, v.Decision, v.Summary, v.SelfReview, v.ReviewedBy, review.BasisContentVersion, review.BasisHandoffID, review.OperationRowID, v.CreatedAt.UTC().Format(time.RFC3339Nano), superseded)
	return classify(e)
}

type activityPayload struct {
	Done      []string       `json:"done"`
	Remaining []string       `json:"remaining"`
	Decisions []string       `json:"decisions"`
	Uncertain []string       `json:"uncertain"`
	Snapshot  *port.Snapshot `json:"snapshot,omitempty"`
}

func (r workflowRepo) AppendActivity(ctx context.Context, project string, a port.Activity) error {
	if e := r.check(); e != nil {
		return e
	}
	payload, e := json.Marshal(activityPayload{a.Done, a.Remaining, a.Decisions, a.Uncertain, a.Snapshot})
	if e != nil {
		return e
	}
	stamp := a.CreatedAt.UTC().Format(time.RFC3339Nano)
	_, e = r.tx.ExecContext(ctx, `INSERT INTO activities(project_id,id,issue_id,session_id,work_session_id,operation_row_id,kind,message,log_type,content_version,payload_version,payload_json,created_at,updated_at) VALUES(?,?,?,?,NULLIF(?,''),?,?,?,NULLIF(?,''),?,1,?,?,?)`, project, a.ID, a.IssueID, a.SessionID, a.WorkSessionID, a.OperationRowID, a.Kind, a.Message, a.LogType, a.ContentVersion, string(payload), stamp, stamp)
	return classify(e)
}
func (r workflowRepo) LatestHandoff(ctx context.Context, project, issue string) (*port.Activity, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	a := port.Activity{IssueID: issue, Kind: "handoff"}
	var payload []byte
	var stamp string
	var version int
	e := r.tx.QueryRowContext(ctx, `SELECT id,session_id,coalesce(work_session_id,''),operation_row_id,content_version,payload_version,payload_json,created_at FROM activities WHERE project_id=? AND issue_id=? AND kind='handoff' AND deleted_at IS NULL ORDER BY created_at DESC,id DESC LIMIT 1`, project, issue).Scan(&a.ID, &a.SessionID, &a.WorkSessionID, &a.OperationRowID, &a.ContentVersion, &version, &payload, &stamp)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, classify(e)
	}
	if version != 1 {
		return nil, fmt.Errorf("unsupported handoff payload version %d", version)
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
	a.CreatedAt, e = time.Parse(time.RFC3339Nano, stamp)
	return &a, e
}
