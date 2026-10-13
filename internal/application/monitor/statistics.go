package monitor

import (
	"context"
	"slices"
	"time"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type Stats struct {
	Total, CreatedToday, CreatedThisWeek, TotalPoints, TotalLogs, TotalHandoffs int
	ByStatus, ByType, ByPriority                                                map[string]int
	OldestOpen, NewestTask, LastClosed                                          *domain.Issue
	AvgPointsPerTask, CompletionRate                                            float64
	MostActiveSession                                                           string
}

func (s *Service) Stats(ctx context.Context, c domain.Caller) (Stats, error) {
	out := Stats{ByStatus: map[string]int{}, ByType: map[string]int{}, ByPriority: map[string]int{}}
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		all, e := r.Issues().All(r.Context(), c.ProjectID, false)
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		if s.Now != nil {
			now = s.Now().UTC()
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		tomorrow := today.AddDate(0, 0, 1)
		week := now.AddDate(0, 0, -7)
		for _, i := range all {
			out.Total++
			out.TotalPoints += i.Points
			out.ByStatus[string(i.Status)]++
			out.ByType[string(i.Type)]++
			out.ByPriority[string(i.Priority)]++
			if !i.CreatedAt.Before(today) && i.CreatedAt.Before(tomorrow) {
				out.CreatedToday++
			}
			if !i.CreatedAt.Before(week) {
				out.CreatedThisWeek++
			}
			if i.Status == domain.StatusOpen && (out.OldestOpen == nil || earlier(i.CreatedAt, i.ID, out.OldestOpen.CreatedAt, out.OldestOpen.ID)) {
				v := i
				out.OldestOpen = &v
			}
			if out.NewestTask == nil || later(i.CreatedAt, i.ID, out.NewestTask.CreatedAt, out.NewestTask.ID) {
				v := i
				out.NewestTask = &v
			}
			if i.Status == domain.StatusClosed && i.ClosedAt != nil && (out.LastClosed == nil || later(*i.ClosedAt, i.ID, *out.LastClosed.ClosedAt, out.LastClosed.ID)) {
				v := i
				out.LastClosed = &v
			}
		}
		if out.Total > 0 {
			out.AvgPointsPerTask = float64(out.TotalPoints) / float64(out.Total)
			out.CompletionRate = float64(out.ByStatus[string(domain.StatusClosed)]) / float64(out.Total)
		}
		counts, e := r.Workflow().Counts(r.Context(), c.ProjectID)
		if e != nil {
			return e
		}
		out.TotalLogs = counts.Logs
		out.TotalHandoffs = counts.Handoffs
		out.MostActiveSession = counts.MostActiveSession
		return nil
	})
	return out, e
}

// Equal timestamps use ascending ID for each highlighted issue.
func earlier(a time.Time, aID string, b time.Time, bID string) bool {
	return a.Before(b) || (a.Equal(b) && aID < bID)
}
func later(a time.Time, aID string, b time.Time, bID string) bool {
	return a.After(b) || (a.Equal(b) && aID < bID)
}
func (s *Service) Labels(ctx context.Context, c domain.Caller) ([]string, error) {
	labels := []string{}
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		all, e := r.Issues().All(r.Context(), c.ProjectID, false)
		if e != nil {
			return e
		}
		distinct := map[string]bool{}
		for _, i := range all {
			for _, label := range i.Labels {
				distinct[label] = true
			}
		}
		for label := range distinct {
			labels = append(labels, label)
		}
		slices.Sort(labels)
		return nil
	})
	return labels, e
}
