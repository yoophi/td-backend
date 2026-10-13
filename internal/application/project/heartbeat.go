package project

import (
	"context"
	"time"

	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type HeartbeatResult struct {
	SessionID    string    `json:"session_id"`
	LastActivity time.Time `json:"last_activity"`
}

func (s *Service) Heartbeat(ctx context.Context, c domain.Caller, m port.Mutation, id string) (port.StoredReply, error) {
	runner := operation.Runner{Writes: s.Writes, Encoder: s.Encoder, ID: s.ID, Now: s.Now}
	return runner.Run(ctx, c, m, "session.heartbeat", func(command operation.CommandContext) (any, error) {
		if id != c.SessionID {
			return nil, domain.ErrForbidden
		}
		return HeartbeatResult{c.SessionID, command.Now}, nil
	})
}
