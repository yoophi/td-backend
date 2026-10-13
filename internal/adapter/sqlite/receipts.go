package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

type operationRepo struct{ *scope }

func (s *scope) Operations() port.OperationReader      { return operationRepo{s} }
func (s *scope) OperationWrites() port.OperationWriter { return operationRepo{s} }
func (r operationRepo) Get(ctx context.Context, actor, scope, key string) (port.Receipt, error) {
	var o port.Receipt
	if e := r.check(); e != nil {
		return o, e
	}
	var stamp string
	var headers []byte
	e := r.tx.QueryRowContext(ctx, `SELECT row_id,coalesce(project_id,''),actor_id,coalesce(session_id,''),scope_key,operation_id,fingerprint,command_kind,request_method,request_path,representation_version,response_status,response_headers,response_body,created_at FROM operations WHERE actor_id=? AND scope_key=? AND operation_id=?`, actor, scope, key).Scan(&o.RowID, &o.ProjectID, &o.ActorID, &o.SessionID, &o.Scope, &o.Key, &o.Fingerprint, &o.Kind, &o.Method, &o.Path, &o.RepresentationVersion, &o.Reply.Status, &headers, &o.Reply.Body, &stamp)
	if errors.Is(e, sql.ErrNoRows) {
		return o, domain.ErrNotFound
	}
	if e != nil {
		return o, classify(e)
	}
	if e = json.Unmarshal(headers, &o.Reply.Headers); e != nil {
		return o, e
	}
	o.CreatedAt, e = time.Parse(time.RFC3339Nano, stamp)
	return o, e
}
func (r operationRepo) Save(ctx context.Context, o port.Receipt) error {
	if e := r.check(); e != nil {
		return e
	}
	headers, e := json.Marshal(o.Reply.Headers)
	if e != nil {
		return e
	}
	_, e = r.tx.ExecContext(ctx, `INSERT INTO operations(row_id,project_id,actor_id,session_id,scope_key,operation_id,fingerprint,command_kind,request_method,request_path,representation_version,response_status,response_headers,response_body,created_at) VALUES(?,NULLIF(?,''),?,NULLIF(?,''),?,?,?,?,?,?,?,?,?,?,?)`, o.RowID, o.ProjectID, o.ActorID, o.SessionID, o.Scope, o.Key, o.Fingerprint, o.Kind, o.Method, o.Path, o.RepresentationVersion, o.Reply.Status, string(headers), o.Reply.Body, o.CreatedAt.UTC().Format(time.RFC3339Nano))
	return classify(e)
}
