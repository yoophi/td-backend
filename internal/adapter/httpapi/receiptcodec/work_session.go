package receiptcodec

import (
	"time"

	"github.com/yoophi/td-backend/internal/application/worksession"
	"github.com/yoophi/td-backend/internal/domain"
)

type WorkSessionDTO struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	SessionID string     `json:"session_id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	StartSHA  string     `json:"start_sha"`
	EndSHA    string     `json:"end_sha"`
}

func WorkSessionDTOFrom(w domain.WorkSession) WorkSessionDTO {
	return WorkSessionDTO{w.ID, w.Name, w.SessionID, w.StartedAt, w.EndedAt, w.StartSHA, w.EndSHA}
}
func WorkSessionIssueIDs(s worksession.State) []string {
	ids := []string{}
	for _, tag := range s.Tags {
		ids = append(ids, tag.IssueID)
	}
	return ids
}
