// Package project implements project/session use cases through application ports.
package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	Writes  port.UnitOfWork
	Reads   port.ReadStore
	Encoder port.ReceiptEncoder
	ID      func(string) (string, error)
	Now     func() time.Time
}

var capabilities = []string{"projects", "sessions", "idempotent_init", "issues", "workflow", "relations", "activity", "boards", "notes", "work_sessions", "focus", "query", "monitor", "stats", "labels", "presence", "operations", "undo", "changes", "events"}

func Capabilities() []string { return append([]string{}, capabilities...) }

type CreateCommand struct {
	Name             string `json:"name"`
	Repository       string `json:"repository"`
	ReviewPolicyMode string `json:"review_policy_mode"`
	TitleMinLength   int    `json:"title_min_length"`
	TitleMaxLength   int    `json:"title_max_length"`
}
type SessionCommand struct {
	Name              string `json:"name"`
	Branch            string `json:"branch"`
	AgentType         string `json:"agent_type"`
	ContextID         string `json:"context_id"`
	PreviousSessionID string `json:"previous_session_id"`
}
type ProjectResult struct {
	Project domain.Project `json:"project"`
}
type SessionResult struct {
	Session domain.Session `json:"session"`
}

func validation(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}
func ValidateMutation(m port.Mutation) error {
	if !domain.ValidOperationKey(m.Key) {
		return validation("Idempotency-Key", "expected lowercase canonical UUIDv4")
	}
	if len(m.Fingerprint) == 0 {
		return validation("request", "request fingerprint required")
	}
	return nil
}

// Authorize retains the project service entry point for existing callers.
func Authorize(ctx context.Context, read port.ReadSnapshot, c domain.Caller, session, write bool) error {
	return access.Authorize(ctx, read, c, session, write)
}
func replay(ctx context.Context, tx port.TxScope, actor, scope string, m port.Mutation) (port.StoredReply, bool, error) {
	receipt, e := tx.Operations().Get(ctx, actor, scope, m.Key)
	if errors.Is(e, domain.ErrNotFound) {
		return port.StoredReply{}, false, nil
	}
	if e != nil {
		return port.StoredReply{}, false, e
	}
	if !bytes.Equal(receipt.Fingerprint, m.Fingerprint) {
		return port.StoredReply{}, false, fmt.Errorf("%w: Idempotency-Key already belongs to a different request", domain.ErrConflict)
	}
	return receipt.Reply, true, nil
}
func (s *Service) storeReply(ctx context.Context, tx port.TxScope, c domain.Caller, scope, kind string, m port.Mutation, outcome any) (port.StoredReply, error) {
	reply, e := s.Encoder.Encode(kind, outcome)
	if e != nil {
		return port.StoredReply{}, e
	}
	row, e := s.ID("operation")
	if e != nil {
		return port.StoredReply{}, e
	}
	e = tx.OperationWrites().Save(ctx, port.Receipt{RowID: row, ProjectID: c.ProjectID, ActorID: c.Actor.ID, SessionID: c.SessionID, Scope: scope, Key: m.Key, Fingerprint: m.Fingerprint, Kind: kind, Method: m.Method, Path: m.Path, RepresentationVersion: 1, Reply: reply, CreatedAt: s.Now().UTC()})
	return reply, e
}
func (s *Service) Create(ctx context.Context, actor domain.Actor, m port.Mutation, cmd CreateCommand) (port.StoredReply, error) {
	if e := ValidateMutation(m); e != nil {
		return port.StoredReply{}, e
	}
	cmd.Name = strings.TrimSpace(cmd.Name)
	if cmd.Name == "" || utf8.RuneCountInString(cmd.Name) > 200 {
		return port.StoredReply{}, validation("name", "name must contain 1..200 characters")
	}
	if cmd.ReviewPolicyMode == "" {
		cmd.ReviewPolicyMode = "trusted"
	}
	switch cmd.ReviewPolicyMode {
	case "trusted", "delegated", "strict", "balanced":
	default:
		return port.StoredReply{}, validation("review_policy_mode", "invalid review policy")
	}
	if cmd.TitleMinLength == 0 {
		cmd.TitleMinLength = 15
	}
	if cmd.TitleMaxLength == 0 {
		cmd.TitleMaxLength = 200
	}
	if cmd.TitleMinLength < 1 || cmd.TitleMaxLength < cmd.TitleMinLength {
		return port.StoredReply{}, validation("title_max_length", "invalid title length limits")
	}
	var reply port.StoredReply
	err := s.Writes.Write(ctx, func(tx port.TxScope) error {
		if actor.ID == "" {
			return domain.ErrUnauthorized
		}
		if !actor.CanCreateProject {
			return domain.ErrForbidden
		}
		var found bool
		var e error
		if reply, found, e = replay(ctx, tx, actor.ID, "@server", m); found || e != nil {
			return e
		}
		now := s.Now().UTC()
		p := domain.Project{Name: cmd.Name, Repository: cmd.Repository, ReviewPolicyMode: cmd.ReviewPolicyMode, TitleMinLength: cmd.TitleMinLength, TitleMaxLength: cmd.TitleMaxLength, CreatedAt: now, ChangeSeq: 1, Capabilities: Capabilities()}
		for attempt := 0; attempt < 3; attempt++ {
			p.ID, e = s.ID("project")
			if e != nil {
				return e
			}
			if !domain.ValidProjectID(p.ID) {
				return fmt.Errorf("ID generator returned invalid Project ID")
			}
			e = tx.ProjectWrites().Create(ctx, p)
			if e == nil {
				break
			}
			if !errors.Is(e, domain.ErrConflict) {
				return e
			}
		}
		if e != nil {
			return e
		}
		if e = tx.AccessWrites().AssignRole(ctx, p.ID, actor.ID, domain.RoleOwner, now); e != nil {
			return e
		}
		boardID, e := s.ID("board")
		if e != nil {
			return e
		}
		if e = tx.BoardWrites().Create(ctx, p.ID, domain.Board{ID: boardID, Name: "All issues", Query: "", IsBuiltin: true, ViewMode: "swimlanes", CreatedAt: now, UpdatedAt: now}); e != nil {
			return e
		}
		reply, e = s.storeReply(ctx, tx, domain.Caller{Actor: actor, ProjectID: p.ID}, "@server", "project.create", m, ProjectResult{p})
		return e
	})
	return reply, err
}
func (s *Service) Get(ctx context.Context, c domain.Caller) (domain.Project, error) {
	var p domain.Project
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		if e := Authorize(ctx, read, c, true, false); e != nil {
			return e
		}
		value, e := read.Projects().Get(ctx, c.ProjectID)
		if e == nil {
			p = *value
			p.Capabilities = Capabilities()
		}
		return e
	})
	return p, e
}
func (s *Service) List(ctx context.Context, actor domain.Actor) ([]domain.Project, error) {
	if actor.ID == "" {
		return nil, domain.ErrUnauthorized
	}
	var list []domain.Project
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		var e error
		list, e = read.Projects().ListForActor(ctx, actor.ID)
		for i := range list {
			list[i].Capabilities = Capabilities()
		}
		return e
	})
	return list, e
}
func (s *Service) RegisterSession(ctx context.Context, c domain.Caller, m port.Mutation, cmd SessionCommand) (port.StoredReply, error) {
	if e := ValidateMutation(m); e != nil {
		return port.StoredReply{}, e
	}
	if strings.TrimSpace(cmd.ContextID) == "" {
		return port.StoredReply{}, validation("context_id", "context_id required")
	}
	var reply port.StoredReply
	e := s.Writes.Write(ctx, func(tx port.TxScope) error {
		if e := Authorize(ctx, tx, c, false, false); e != nil {
			return e
		}
		var found bool
		var e error
		if reply, found, e = replay(ctx, tx, c.Actor.ID, c.ProjectID, m); found || e != nil {
			return e
		}
		if cmd.PreviousSessionID != "" {
			previous, e := tx.Sessions().Get(ctx, c.ProjectID, cmd.PreviousSessionID)
			if e != nil {
				return e
			}
			if previous.ActorID != c.Actor.ID {
				return domain.ErrForbidden
			}
		}
		id, e := s.ID("session")
		if e != nil {
			return e
		}
		now := s.Now().UTC()
		session := domain.Session{ID: id, Name: cmd.Name, Branch: cmd.Branch, AgentType: cmd.AgentType, ContextID: cmd.ContextID, ActorID: c.Actor.ID, StartedAt: now, LastActivity: now}
		if cmd.PreviousSessionID != "" {
			session.PreviousSessionID = &cmd.PreviousSessionID
		}
		if e = tx.SessionWrites().Create(ctx, c.ProjectID, session); e != nil {
			return e
		}
		if _, e = tx.ProjectWrites().AdvanceChange(ctx, c.ProjectID); e != nil {
			return e
		}
		// Bootstrap session creation has no pre-existing session in the request fingerprint.
		reply, e = s.storeReply(ctx, tx, c, c.ProjectID, "session.create", m, SessionResult{session})
		return e
	})
	return reply, e
}
func (s *Service) ListSessions(ctx context.Context, c domain.Caller) ([]domain.Session, error) {
	var list []domain.Session
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		if e := Authorize(ctx, read, c, true, false); e != nil {
			return e
		}
		var e error
		list, e = read.Sessions().List(ctx, c.ProjectID)
		return e
	})
	return list, e
}
