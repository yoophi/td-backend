package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

func (s *scope) Access() port.AccessReader         { return accessRepo{s} }
func (s *scope) AccessWrites() port.AccessWriter   { return accessRepo{s} }
func (s *scope) Sessions() port.SessionReader      { return sessionRepo{s} }
func (s *scope) SessionWrites() port.SessionWriter { return sessionRepo{s} }
func (s *scope) BoardWrites() port.BoardWriter     { return boardRepo{s} }

type accessRepo struct{ *scope }

func (r accessRepo) ActorByToken(ctx context.Context, digest []byte) (domain.Actor, error) {
	var a domain.Actor
	if e := r.check(); e != nil {
		return a, e
	}
	e := r.tx.QueryRowContext(ctx, `SELECT a.id,a.name,a.can_create_project FROM actors a JOIN access_tokens t ON a.id=t.actor_id WHERE t.token_digest=? AND t.revoked_at IS NULL`, digest).Scan(&a.ID, &a.Name, &a.CanCreateProject)
	if errors.Is(e, sql.ErrNoRows) {
		return a, domain.ErrUnauthorized
	}
	return a, classify(e)
}
func (r accessRepo) Role(ctx context.Context, project, actor string) (domain.Role, error) {
	if e := r.check(); e != nil {
		return "", e
	}
	var role domain.Role
	e := r.tx.QueryRowContext(ctx, "SELECT role FROM project_memberships WHERE project_id=? AND actor_id=?", project, actor).Scan(&role)
	if errors.Is(e, sql.ErrNoRows) {
		var exists int
		if e = r.tx.QueryRowContext(ctx, "SELECT 1 FROM projects WHERE id=?", project).Scan(&exists); errors.Is(e, sql.ErrNoRows) {
			return "", domain.ErrNotFound
		}
		if e != nil {
			return "", classify(e)
		}
		return "", domain.ErrForbidden
	}
	return role, classify(e)
}
func (r accessRepo) Bootstrap(ctx context.Context, digest []byte, now time.Time) error {
	if e := r.check(); e != nil {
		return e
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, e := r.tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(id,name,can_create_project,created_at) VALUES('act-bootstrap','Bootstrap owner',1,?)`, stamp); e != nil {
		return classify(e)
	}
	// A deliberate bootstrap credential change revokes the previous bootstrap token.
	if _, e := r.tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at=? WHERE actor_id='act-bootstrap' AND token_digest<>? AND revoked_at IS NULL`, stamp, digest); e != nil {
		return classify(e)
	}
	_, e := r.tx.ExecContext(ctx, `INSERT INTO access_tokens(id,actor_id,token_digest,created_at) VALUES('bootstrap-token','act-bootstrap',?,?) ON CONFLICT(id) DO UPDATE SET token_digest=excluded.token_digest,revoked_at=NULL`, digest, stamp)
	return classify(e)
}
func (r accessRepo) AssignRole(ctx context.Context, project, actor string, role domain.Role, now time.Time) error {
	if e := r.check(); e != nil {
		return e
	}
	_, e := r.tx.ExecContext(ctx, "INSERT INTO project_memberships(project_id,actor_id,role,created_at) VALUES(?,?,?,?)", project, actor, role, now.UTC().Format(time.RFC3339Nano))
	return classify(e)
}

type sessionRepo struct{ *scope }

func scanSession(row interface{ Scan(...any) error }) (domain.Session, error) {
	var s domain.Session
	var started, last string
	e := row.Scan(&s.ID, &s.ActorID, &s.Name, &s.Branch, &s.AgentType, &s.ContextID, &s.PreviousSessionID, &started, &last)
	if errors.Is(e, sql.ErrNoRows) {
		return s, domain.ErrNotFound
	}
	if e != nil {
		return s, classify(e)
	}
	s.StartedAt, e = time.Parse(time.RFC3339Nano, started)
	if e != nil {
		return s, e
	}
	s.LastActivity, e = time.Parse(time.RFC3339Nano, last)
	return s, e
}

const sessionColumns = "id,actor_id,name,branch,agent_type,context_id,previous_session_id,started_at,last_activity"

func (r sessionRepo) Get(ctx context.Context, project, id string) (domain.Session, error) {
	if e := r.check(); e != nil {
		return domain.Session{}, e
	}
	return scanSession(r.tx.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE project_id=? AND id=?", project, id))
}
func (r sessionRepo) List(ctx context.Context, project string) ([]domain.Session, error) {
	if e := r.check(); e != nil {
		return nil, e
	}
	rows, e := r.tx.QueryContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE project_id=? ORDER BY started_at,id", project)
	if e != nil {
		return nil, classify(e)
	}
	defer rows.Close()
	result := []domain.Session{}
	for rows.Next() {
		s, e := scanSession(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, s)
	}
	return result, rows.Err()
}
func (r sessionRepo) Create(ctx context.Context, project string, s domain.Session) error {
	if e := r.check(); e != nil {
		return e
	}
	_, e := r.tx.ExecContext(ctx, "INSERT INTO sessions(project_id,"+sessionColumns+") VALUES(?,?,?,?,?,?,?,?,?,?)", project, s.ID, s.ActorID, s.Name, s.Branch, s.AgentType, s.ContextID, s.PreviousSessionID, s.StartedAt.UTC().Format(time.RFC3339Nano), s.LastActivity.UTC().Format(time.RFC3339Nano))
	return classify(e)
}

func (r sessionRepo) Touch(ctx context.Context, project, id string, now time.Time) error {
	if e := r.check(); e != nil {
		return e
	}
	result, e := r.tx.ExecContext(ctx, `UPDATE sessions SET last_activity=? WHERE project_id=? AND id=?`, now.UTC().Format(time.RFC3339Nano), project, id)
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
