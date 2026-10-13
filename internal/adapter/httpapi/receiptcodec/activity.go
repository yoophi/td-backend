package receiptcodec

import (
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

type ActivityDTO struct {
	ID            string              `json:"id"`
	IssueID       string              `json:"issue_id"`
	Kind          string              `json:"kind"`
	OperationID   string              `json:"operation_id"`
	SessionID     string              `json:"session_id"`
	WorkSessionID string              `json:"work_session_id"`
	Message       string              `json:"message"`
	LogType       domain.LogType      `json:"log_type,omitempty"`
	Done          []string            `json:"done"`
	Remaining     []string            `json:"remaining"`
	Decisions     []string            `json:"decisions"`
	Uncertain     []string            `json:"uncertain"`
	Snapshot      *domain.GitSnapshot `json:"snapshot"`
	Author        string              `json:"author"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	Native        bool                `json:"native"`
	Edited        bool                `json:"edited"`
}

func ActivityDTOFrom(a port.Activity) ActivityDTO {
	out := ActivityDTO{ID: a.ID, IssueID: a.IssueID, Kind: a.Kind, OperationID: a.OperationID, SessionID: a.SessionID, WorkSessionID: a.WorkSessionID, Message: a.Message, LogType: a.LogType, Done: append([]string{}, a.Done...), Remaining: append([]string{}, a.Remaining...), Decisions: append([]string{}, a.Decisions...), Uncertain: append([]string{}, a.Uncertain...), Author: a.Author, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, Native: true, Edited: !a.UpdatedAt.Equal(a.CreatedAt)}
	if a.Snapshot != nil {
		event := a.SnapshotEvent
		if event == "" {
			event = a.Kind
		}
		out.Snapshot = &domain.GitSnapshot{ID: a.ID + "-git", IssueID: a.IssueID, Event: event, CommitSHA: a.Snapshot.CommitSHA, Branch: a.Snapshot.Branch, DirtyFiles: a.Snapshot.DirtyFiles, Timestamp: a.CreatedAt}
	}
	return out
}
