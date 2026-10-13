package issue

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/tdq"
)

type ListOptions struct {
	Limit, Offset                                   int
	IncludeClosed, WithDeleted                      bool
	Statuses, Types, Priorities, Labels             []string
	Epic, ParentID, Search, SearchMode, Sort, Order string
}
type ListResult struct {
	Issues               []domain.Issue
	Total, Limit, Offset int
	HasMore              bool
	ChangeToken          string
}

func (s *Service) List(ctx context.Context, c domain.Caller, q ListOptions) (ListResult, error) {
	result := ListResult{Issues: []domain.Issue{}}
	explicitSort := q.Sort != "" || q.Order != ""
	if q.Limit == 0 {
		q.Limit = 200
	}
	if q.Sort == "" {
		q.Sort = "priority"
	}
	if q.Order == "" {
		q.Order = "asc"
	}
	if q.SearchMode == "" {
		q.SearchMode = "auto"
	}
	if q.Limit < 1 || q.Limit > 1000 || q.Offset < 0 {
		return result, invalid("pagination", "limit must be 1..1000 and offset nonnegative")
	}
	if !slices.Contains([]string{"priority", "created", "updated", "id", "title", "status", "type", "points"}, q.Sort) {
		return result, invalid("sort", "invalid sort field")
	}
	if q.Order != "asc" && q.Order != "desc" {
		return result, invalid("order", "expected asc or desc")
	}
	if q.SearchMode != "text" && q.SearchMode != "auto" && q.SearchMode != "tdq" {
		return result, invalid("search_mode", "expected auto, text or tdq")
	}
	for _, group := range []struct {
		field           string
		values, allowed []string
	}{
		{"status", q.Statuses, []string{"open", "in_progress", "blocked", "in_review", "closed"}},
		{"type", q.Types, []string{"bug", "feature", "task", "epic", "chore"}},
		{"priority", q.Priorities, []string{"P0", "P1", "P2", "P3", "P4"}},
	} {
		for _, v := range group.values {
			if !slices.Contains(group.allowed, v) {
				return result, invalid(group.field, "invalid filter value")
			}
		}
	}
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		ctx := read.Context()
		if e := access.Authorize(ctx, read, c, true, false); e != nil {
			return e
		}
		p, e := read.Projects().Get(ctx, c.ProjectID)
		if e != nil {
			return e
		}
		useTDQ, keepTDQSort := false, false
		if q.Search != "" && q.SearchMode != "text" {
			parsed, parseErr := tdq.ParseIssues(q.Search)
			if parseErr != nil && q.SearchMode == "tdq" {
				return invalid("search", parseErr.Error())
			}
			if parseErr == nil {
				useTDQ = true
				keepTDQSort = parsed.Sort != nil && !explicitSort
			}
		}
		var all, candidates []domain.Issue
		if useTDQ {
			facts, err := read.Queries().Facts(ctx, c.ProjectID, q.WithDeleted)
			if err != nil {
				return err
			}
			for _, i := range facts.Issues {
				all = append(all, i)
			}
			now := time.Now()
			if s.Runner.Now != nil {
				now = s.Runner.Now()
			}
			candidates, err = tdq.Select(q.Search, &facts, tdq.EvalContext{CurrentSession: c.SessionID, Now: now}, tdq.FilterOptions{IncludeClosed: true, WithDeleted: q.WithDeleted})
			if err != nil {
				return invalid("search", err.Error())
			}
		} else {
			all, e = read.Issues().All(ctx, c.ProjectID, q.WithDeleted)
			if e != nil {
				return e
			}
			candidates = all
		}
		parents := map[string]string{}
		for _, i := range all {
			parents[i.ID] = i.ParentID
		}
		descendant := func(i domain.Issue) bool {
			seen := map[string]bool{}
			for parent := i.ParentID; parent != "" && !seen[parent]; parent = parents[parent] {
				if parent == q.Epic {
					return true
				}
				seen[parent] = true
			}
			return false
		}
		for _, i := range candidates {
			if len(q.Statuses) > 0 {
				if !slices.Contains(q.Statuses, string(i.Status)) {
					continue
				}
			} else if !q.IncludeClosed && i.Status == domain.StatusClosed {
				continue
			}
			if len(q.Types) > 0 && !slices.Contains(q.Types, string(i.Type)) {
				continue
			}
			if len(q.Priorities) > 0 && !slices.Contains(q.Priorities, string(i.Priority)) {
				continue
			}
			if q.ParentID != "" && i.ParentID != q.ParentID {
				continue
			}
			if q.Epic != "" && !descendant(i) {
				continue
			}
			labelsMatch := true
			for _, label := range q.Labels {
				if !slices.Contains(i.Labels, label) {
					labelsMatch = false
					break
				}
			}
			if !labelsMatch {
				continue
			}
			if !useTDQ && q.Search != "" && !strings.Contains(strings.ToLower(i.ID+"\n"+i.Title+"\n"+i.Description+"\n"+i.Acceptance), strings.ToLower(q.Search)) {
				continue
			}
			result.Issues = append(result.Issues, i)
		}
		if !keepTDQSort {
			slices.SortFunc(result.Issues, func(a, b domain.Issue) int {
				var n int
				switch q.Sort {
				case "priority":
					n = cmp.Compare(a.Priority, b.Priority)
				case "created":
					n = a.CreatedAt.Compare(b.CreatedAt)
				case "updated":
					n = a.UpdatedAt.Compare(b.UpdatedAt)
				case "id":
					n = cmp.Compare(a.ID, b.ID)
				case "title":
					n = cmp.Compare(a.Title, b.Title)
				case "status":
					n = cmp.Compare(a.Status, b.Status)
				case "type":
					n = cmp.Compare(a.Type, b.Type)
				case "points":
					n = cmp.Compare(a.Points, b.Points)
				}
				if q.Order == "desc" {
					n = -n
				}
				if n == 0 {
					return cmp.Compare(a.ID, b.ID)
				}
				return n
			})
		}
		result.Total = len(result.Issues)
		result.Limit = q.Limit
		result.Offset = q.Offset
		start := min(q.Offset, result.Total)
		end := start + min(q.Limit, result.Total-start)
		result.Issues = result.Issues[start:end]
		result.HasMore = end < result.Total
		result.ChangeToken = fmt.Sprintf("%s:%d", p.ID, p.ChangeSeq)
		return nil
	})
	return result, e
}
