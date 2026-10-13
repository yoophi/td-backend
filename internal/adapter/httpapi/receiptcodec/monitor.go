package receiptcodec

import (
	"time"

	"github.com/yoophi/td-backend/internal/application/monitor"
)

type MonitorActivityDTO struct {
	Timestamp    time.Time `json:"timestamp"`
	SessionID    string    `json:"session_id"`
	Type         string    `json:"type"`
	IssueID      string    `json:"issue_id"`
	IssueTitle   string    `json:"issue_title"`
	Message      string    `json:"message"`
	LogType      string    `json:"log_type"`
	Action       string    `json:"action"`
	EntityID     string    `json:"entity_id"`
	EntityType   string    `json:"entity_type"`
	PreviousData string    `json:"previous_data"`
	NewData      string    `json:"new_data"`
}
type RecentHandoffDTO struct {
	IssueID   string    `json:"issue_id"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
}
type MonitorDTO struct {
	ObservationMode  string               `json:"observation_mode"`
	FullReconciledAt string               `json:"full_reconciled_at"`
	FocusedIssue     *IssueDTO            `json:"focused_issue"`
	InProgress       []IssueDTO           `json:"in_progress"`
	Activity         []MonitorActivityDTO `json:"activity"`
	TaskList         map[string]any       `json:"task_list"`
	RecentHandoffs   []RecentHandoffDTO   `json:"recent_handoffs"`
	ActiveSessions   []string             `json:"active_sessions"`
	Timestamp        time.Time            `json:"timestamp"`
}
type MonitorResultDTO struct {
	Monitor     MonitorDTO `json:"monitor"`
	SessionID   string     `json:"session_id"`
	ChangeToken string     `json:"change_token"`
}

func monitorIssue(c monitor.Card) IssueDTO {
	i := IssueDTOFrom(c.Issue)
	actions := append([]string{}, c.Transitions...)
	i.AvailableTransitions = &actions
	if c.HasApproval && c.Review != nil {
		r := c.Review.Record
		i.ActiveReview = &r
	}
	return i
}
func MonitorDTOFrom(r monitor.Result) MonitorResultDTO {
	m := MonitorDTO{ObservationMode: "snapshot", FullReconciledAt: r.Timestamp.Format(time.RFC3339Nano), InProgress: []IssueDTO{}, Activity: []MonitorActivityDTO{}, TaskList: map[string]any{"review_verification_required": false}, RecentHandoffs: []RecentHandoffDTO{}, ActiveSessions: append([]string{}, r.ActiveSessions...), Timestamp: r.Timestamp}
	if r.Focused != nil {
		i := monitorIssue(*r.Focused)
		m.FocusedIssue = &i
	}
	for _, card := range r.InProgress {
		m.InProgress = append(m.InProgress, monitorIssue(card))
	}
	for category, cards := range r.Categories {
		out := []IssueDTO{}
		for _, card := range cards {
			out = append(out, monitorIssue(card))
		}
		m.TaskList[category] = out
	}
	for _, a := range r.Activity {
		kind := a.Kind
		if kind != "log" && kind != "comment" {
			kind = "action"
		}
		m.Activity = append(m.Activity, MonitorActivityDTO{Timestamp: a.CreatedAt, SessionID: a.SessionID, Type: kind, IssueID: a.IssueID, IssueTitle: r.Titles[a.IssueID], Message: a.Message, LogType: string(a.LogType), Action: a.SnapshotEvent, EntityID: a.IssueID, EntityType: "issue"})
	}
	for _, h := range r.RecentHandoffs {
		m.RecentHandoffs = append(m.RecentHandoffs, RecentHandoffDTO{h.IssueID, h.SessionID, h.CreatedAt})
	}
	return MonitorResultDTO{m, r.SessionID, r.ChangeToken}
}
