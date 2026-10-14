package receiptcodec

import (
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

type LogDTO struct {
	ID            string         `json:"id"`
	IssueID       string         `json:"issue_id"`
	SessionID     string         `json:"session_id"`
	WorkSessionID string         `json:"work_session_id"`
	Message       string         `json:"message"`
	Type          domain.LogType `json:"type"`
	Timestamp     time.Time      `json:"timestamp"`
}

func WorkSessionLogDTOFrom(log domain.WorkSessionLog) LogDTO {
	return LogDTO{log.ID, "", log.SessionID, log.WorkSessionID, log.Message, log.Type, log.Timestamp}
}

type HandoffDTO struct {
	ID        string    `json:"id"`
	IssueID   string    `json:"issue_id"`
	SessionID string    `json:"session_id"`
	Done      []string  `json:"done"`
	Remaining []string  `json:"remaining"`
	Decisions []string  `json:"decisions"`
	Uncertain []string  `json:"uncertain"`
	Timestamp time.Time `json:"timestamp"`
}
type DependencyDTO struct {
	ID           string `json:"dep_id"`
	IssueID      string `json:"issue_id"`
	DependsOnID  string `json:"depends_on_id"`
	RelationType string `json:"relation_type"`
}
type IssueDetailDTO struct {
	Issue         IssueDTO           `json:"issue"`
	Logs          []LogDTO           `json:"logs"`
	Comments      []domain.Comment   `json:"comments"`
	LatestHandoff *HandoffDTO        `json:"latest_handoff"`
	Children      []IssueDTO         `json:"children"`
	Dependencies  []DependencyDTO    `json:"dependencies"`
	BlockedBy     []DependencyDTO    `json:"blocked_by"`
	Files         []domain.IssueFile `json:"files"`
}

func DetailDTOFrom(d issue.Detail, reviews bool) IssueDetailDTO {
	result := IssueDetailDTO{Issue: IssueDTOFrom(d.Issue), Logs: []LogDTO{}, Comments: append([]domain.Comment{}, d.Context.Comments...), Children: []IssueDTO{}, Dependencies: dependenciesDTO(d.Context.Dependencies), BlockedBy: dependenciesDTO(d.Context.BlockedBy), Files: append([]domain.IssueFile{}, d.Context.Files...)}
	if reviews {
		v := append([]domain.IssueReview{}, d.Context.Reviews...)
		result.Issue.Reviews = &v
	}
	for _, l := range d.Context.Logs {
		result.Logs = append(result.Logs, LogDTO{l.ID, l.IssueID, l.SessionID, l.WorkSessionID, l.Message, l.Type, l.Timestamp})
	}
	if h := d.Context.LatestHandoff; h != nil {
		result.LatestHandoff = &HandoffDTO{h.ID, h.IssueID, h.SessionID, append([]string{}, h.Done...), append([]string{}, h.Remaining...), append([]string{}, h.Decisions...), append([]string{}, h.Uncertain...), h.Timestamp}
	}
	for _, i := range d.Context.Children {
		result.Children = append(result.Children, IssueDTOFrom(i))
	}
	return result
}
func dependenciesDTO(deps []port.Dependency) []DependencyDTO {
	result := []DependencyDTO{}
	for _, d := range deps {
		result = append(result, DependencyDTO{d.ID, d.IssueID, d.DependsOnID, "depends_on"})
	}
	return result
}
