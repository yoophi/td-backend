package worksession

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type LogCommand struct {
	Message string         `json:"message"`
	Type    domain.LogType `json:"type"`
}

type LogResult struct{ Log domain.WorkSessionLog }

func (s *Service) ListLogs(ctx context.Context, c domain.Caller, id string) ([]domain.WorkSessionLog, error) {
	var out []domain.WorkSessionLog
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		if _, e := r.WorkSessions().Get(r.Context(), c.ProjectID, id); e != nil {
			return e
		}
		var e error
		out, e = r.WorkSessions().Logs(r.Context(), c.ProjectID, id)
		return e
	})
	return out, e
}

func (s *Service) Log(ctx context.Context, c domain.Caller, m port.Mutation, id string, cmd LogCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "work_session.log", func(command operation.CommandContext) (any, error) {
		message := strings.TrimSpace(cmd.Message)
		if message == "" {
			return nil, invalid("message", "nonblank work session log message required")
		}
		if !slices.Contains([]domain.LogType{domain.LogTypeProgress, domain.LogTypeSecurity, domain.LogTypeBlocker, domain.LogTypeDecision, domain.LogTypeHypothesis, domain.LogTypeTried, domain.LogTypeResult, domain.LogTypeOrchestration}, cmd.Type) {
			return nil, invalid("type", "valid log type required")
		}
		ws, e := command.Tx.WorkSessions().Get(command.Tx.Context(), c.ProjectID, id)
		if e != nil {
			return nil, e
		}
		if ws.SessionID != c.SessionID {
			return nil, domain.ErrForbidden
		}
		if ws.EndedAt != nil {
			return nil, domain.ErrConflict
		}
		logID, e := s.ID("work_session_log")
		if e != nil {
			return nil, e
		}
		log := domain.WorkSessionLog{ID: logID, WorkSessionID: id, SessionID: c.SessionID, Revision: 1, Message: message, Type: cmd.Type, Timestamp: command.Now}
		if e = command.Tx.WorkSessionWrites().SaveLog(command.Tx.Context(), c.ProjectID, log, 0, command.RowID); e != nil {
			return nil, e
		}
		after, e := json.Marshal(log)
		if e != nil {
			return nil, e
		}
		if e = command.Tx.OperationWrites().SaveEffect(command.Tx.Context(), port.Effect{ProjectID: c.ProjectID, OperationRowID: command.RowID, EntityKind: "work_session_log", EntityID: log.ID, AfterRevision: domain.Revision(log.Revision), StateVersion: 1, AfterJSON: after}); e != nil {
			return nil, e
		}
		return LogResult{log}, nil
	})
}
