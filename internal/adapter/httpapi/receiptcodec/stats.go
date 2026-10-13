package receiptcodec

import "github.com/yoophi/td-backend/internal/application/monitor"

type StatsDTO struct {
	Total             int            `json:"total"`
	ByStatus          map[string]int `json:"by_status"`
	ByType            map[string]int `json:"by_type"`
	ByPriority        map[string]int `json:"by_priority"`
	OldestOpen        *IssueDTO      `json:"oldest_open"`
	NewestTask        *IssueDTO      `json:"newest_task"`
	LastClosed        *IssueDTO      `json:"last_closed"`
	CreatedToday      int            `json:"created_today"`
	CreatedThisWeek   int            `json:"created_this_week"`
	TotalPoints       int            `json:"total_points"`
	AvgPointsPerTask  float64        `json:"avg_points_per_task"`
	CompletionRate    float64        `json:"completion_rate"`
	TotalLogs         int            `json:"total_logs"`
	TotalHandoffs     int            `json:"total_handoffs"`
	MostActiveSession string         `json:"most_active_session"`
}

func StatsDTOFrom(s monitor.Stats) StatsDTO {
	out := StatsDTO{Total: s.Total, ByStatus: s.ByStatus, ByType: s.ByType, ByPriority: s.ByPriority, CreatedToday: s.CreatedToday, CreatedThisWeek: s.CreatedThisWeek, TotalPoints: s.TotalPoints, AvgPointsPerTask: s.AvgPointsPerTask, CompletionRate: s.CompletionRate, TotalLogs: s.TotalLogs, TotalHandoffs: s.TotalHandoffs, MostActiveSession: s.MostActiveSession}
	if s.OldestOpen != nil {
		i := IssueDTOFrom(*s.OldestOpen)
		out.OldestOpen = &i
	}
	if s.NewestTask != nil {
		i := IssueDTOFrom(*s.NewestTask)
		out.NewestTask = &i
	}
	if s.LastClosed != nil {
		i := IssueDTOFrom(*s.LastClosed)
		out.LastClosed = &i
	}
	return out
}
