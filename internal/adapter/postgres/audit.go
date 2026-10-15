package postgres

import (
	"context"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func (r operationRepo) AuditGet(ctx context.Context, project, key string) (port.Receipt, error) {
	if e := r.check(); e != nil {
		return port.Receipt{}, e
	}
	rows, e := r.tx.QueryContext(ctx, "SELECT "+receiptColumns+" FROM operations WHERE project_id=? AND operation_id=? LIMIT 2", project, key)
	if e != nil {
		return port.Receipt{}, classify(e)
	}
	defer rows.Close()
	if !rows.Next() {
		if e := rows.Err(); e != nil {
			return port.Receipt{}, e
		}
		return port.Receipt{}, domain.ErrNotFound
	}
	out, e := scanReceipt(rows)
	if e != nil {
		return out, e
	}
	if rows.Next() {
		return port.Receipt{}, domain.ErrConflict
	}
	return out, rows.Err()
}
func (r operationRepo) List(ctx context.Context, project string, f port.AuditFilter) ([]port.Receipt, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	q := "SELECT " + receiptColumns + " FROM operations WHERE project_id=?"
	args := []any{project}
	if f.SessionID != "" {
		q += " AND session_id=?"
		args = append(args, f.SessionID)
	}
	if f.IssueID != "" {
		q += " AND EXISTS (SELECT 1 FROM operation_effects e WHERE e.project_id=operations.project_id AND e.operation_row_id=operations.row_id AND e.entity_kind='issue' AND e.entity_id=?)"
		args = append(args, f.IssueID)
	}
	q += " ORDER BY created_at DESC,row_id DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)
	rows, e := r.tx.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	out := []port.Receipt{}
	for rows.Next() {
		o, e := scanReceipt(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (r operationRepo) Effects(ctx context.Context, project, rowID string) ([]port.Effect, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, `SELECT entity_kind,entity_id,after_revision,state_version,before_json,after_json FROM operation_effects WHERE project_id=? AND operation_row_id=? ORDER BY entity_kind,entity_id`, project, rowID)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	out := []port.Effect{}
	for rows.Next() {
		f := port.Effect{ProjectID: project, OperationRowID: rowID}
		if e = rows.Scan(&f.EntityKind, &f.EntityID, &f.AfterRevision, &f.StateVersion, &f.BeforeJSON, &f.AfterJSON); e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r operationRepo) RecordUndo(ctx context.Context, project, rowID, original, reason string) error {
	if e := r.check(); e != nil {
		return e
	}
	_, e := r.tx.ExecContext(ctx, `INSERT INTO operation_undos(project_id,operation_row_id,original_row_id,reason) VALUES(?,?,?,?)`, project, rowID, original, reason)
	return classify(e)
}
