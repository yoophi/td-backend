// Package monitor assembles the session-aware projection in one read snapshot.
package monitor

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/board"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/workflow"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/reviewpolicy"
	"github.com/yoophi/td-backend/internal/domain/tdq"
)

type Service struct {
	Reads port.ReadStore
	Now   func() time.Time
}
type Options struct {
	IncludeClosed                     bool
	Search, SearchMode, Sort, BoardID string
}
type Card struct {
	Issue            domain.Issue
	Transitions      []string
	Category         string
	Review           *port.Review
	HasApproval      bool
	Dependencies     []string
	OpenDependencies int
}
type Result struct {
	Titles                 map[string]string
	Categories             map[string][]Card
	Focused                *Card
	InProgress             []Card
	Activity               []port.Activity
	RecentHandoffs         []port.Activity
	ActiveSessions         []string
	SessionID, ChangeToken string
	Timestamp              time.Time
}

func invalid(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}
func (s *Service) Get(ctx context.Context, c domain.Caller, opts Options) (Result, error) {
	out := Result{Titles: map[string]string{}, Categories: map[string][]Card{}, InProgress: []Card{}, RecentHandoffs: []port.Activity{}, ActiveSessions: []string{}, SessionID: c.SessionID}
	for _, name := range []string{"reviewable", "needs_rework", "in_progress", "ready", "pending_review", "ready_to_close", "pending_other", "blocked", "closed"} {
		out.Categories[name] = []Card{}
	}
	if opts.SearchMode == "" {
		opts.SearchMode = "auto"
	}
	if !slices.Contains([]string{"auto", "text", "tdq"}, opts.SearchMode) {
		return out, invalid("search_mode", "expected auto, text or tdq")
	}
	e := s.Reads.Snapshot(ctx, func(r port.ReadSnapshot) error {
		if e := access.Authorize(r.Context(), r, c, true, false); e != nil {
			return e
		}
		p, e := r.Projects().Get(r.Context(), c.ProjectID)
		if e != nil {
			return e
		}
		mode, e := reviewpolicy.ParseMode(p.ReviewPolicyMode)
		if e != nil {
			return e
		}
		role, e := r.Access().Role(r.Context(), c.ProjectID, c.Actor.ID)
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		if s.Now != nil {
			now = s.Now().UTC()
		}
		out.Timestamp = now
		out.ChangeToken = fmt.Sprintf("%s:%d", p.ID, p.ChangeSeq)
		facts, e := r.Queries().Facts(r.Context(), c.ProjectID, false)
		if e != nil {
			return e
		}
		policy, e := r.Workflow().Facts(r.Context(), c.ProjectID)
		if e != nil {
			return e
		}
		query := ""
		text := ""
		if opts.Search != "" {
			if opts.SearchMode == "text" {
				text = opts.Search
			} else if _, e := tdq.ParseIssues(opts.Search); e != nil {
				if opts.SearchMode == "tdq" {
					return invalid("search", e.Error())
				}
				text = opts.Search
			} else {
				query = opts.Search
			}
		}
		parsed, e := tdq.ParseIssues(query)
		if e != nil {
			return invalid("search", e.Error())
		}
		if opts.Sort != "" {
			field := strings.TrimPrefix(opts.Sort, "-")
			for _, ch := range field {
				if !(ch >= 'a' && ch <= 'z') && ch != '_' {
					return invalid("sort", "expected a TDQ sort field with optional '-' prefix")
				}
			}
			override, e := tdq.ParseIssues("sort:" + opts.Sort)
			if e != nil {
				return invalid("sort", e.Error())
			}
			parsed.Sort = override.Sort
		}
		selected, e := tdq.SelectQuery(parsed, &facts, tdq.EvalContext{CurrentSession: c.SessionID, Now: now}, tdq.FilterOptions{IncludeClosed: opts.IncludeClosed})
		if e != nil {
			return invalid("search", e.Error())
		}
		if opts.BoardID != "" {
			b, e := r.Boards().Get(r.Context(), c.ProjectID, opts.BoardID, false)
			if e != nil {
				return e
			}
			candidates, e := tdq.Filter(b.Query, &facts, tdq.EvalContext{CurrentSession: c.SessionID, Now: now}, opts.IncludeClosed)
			if e != nil {
				return invalid("board_id", e.Error())
			}
			allowed := map[string]bool{}
			for _, i := range selected {
				allowed[i.ID] = true
			}
			positions, e := r.Boards().Positions(r.Context(), c.ProjectID, b.ID)
			if e != nil {
				return e
			}
			selected = []domain.Issue{}
			for _, v := range board.ApplyPositions(b.ID, candidates, positions) {
				if allowed[v.Issue.ID] {
					selected = append(selected, v.Issue)
				}
			}
		}
		cards := map[string]Card{}
		for id, i := range facts.Issues {
			f := workflow.Facts{Issue: i, SessionID: c.SessionID, Mode: mode, History: policy.History[id], ActiveReview: policy.ActiveReviews[id], Handoff: policy.LatestHandoffs[id]}
			deps := append([]string{}, facts.Dependencies[id]...)
			open := 0
			for _, id := range deps {
				target, ok := facts.Issues[id]
				if !ok || target.Status != domain.StatusClosed {
					open++
				}
			}
			card := Card{Issue: i, Review: f.ActiveReview, HasApproval: f.HasActiveApproval(), Dependencies: deps, OpenDependencies: open, Category: category(f, facts.Rework[id], open > 0), Transitions: transitions(f, role != domain.RoleReader)}
			cards[id] = card
			out.Titles[id] = i.Title
		}
		for _, i := range selected {
			card := cards[i.ID]
			if text != "" && !strings.Contains(strings.ToLower(i.ID+"\n"+i.Title+"\n"+i.Description+"\n"+i.Acceptance), strings.ToLower(text)) {
				continue
			}
			out.Categories[card.Category] = append(out.Categories[card.Category], card)
		}
		all, e := tdq.Filter("", &facts, tdq.EvalContext{CurrentSession: c.SessionID, Now: now}, true)
		if e != nil {
			return e
		}
		for _, i := range all {
			if i.Status == domain.StatusInProgress {
				out.InProgress = append(out.InProgress, cards[i.ID])
			}
		}
		focus, e := r.Focus().Get(r.Context(), c.ProjectID, c.SessionID)
		if e != nil {
			return e
		}
		if focus.IssueID != nil {
			if card, ok := cards[*focus.IssueID]; ok {
				out.Focused = &card
			}
		}
		out.Activity, e = r.Workflow().Feed(r.Context(), c.ProjectID, 50)
		if e != nil {
			return e
		}
		out.RecentHandoffs, e = r.Workflow().RecentHandoffs(r.Context(), c.ProjectID, now.Add(-24*time.Hour))
		if e != nil {
			return e
		}
		sessions, e := r.Sessions().List(r.Context(), c.ProjectID)
		if e != nil {
			return e
		}
		for _, session := range sessions {
			if session.ID == c.SessionID || !session.LastActivity.Before(now.Add(-5*time.Minute)) {
				out.ActiveSessions = append(out.ActiveSessions, session.ID)
			}
		}
		slices.Sort(out.ActiveSessions)
		return nil
	})
	return out, e
}
func category(f workflow.Facts, rework, blocked bool) string {
	switch f.Issue.Status {
	case domain.StatusOpen:
		if blocked {
			return "blocked"
		}
		return "ready"
	case domain.StatusInProgress:
		if rework {
			return "needs_rework"
		}
		return "in_progress"
	case domain.StatusBlocked:
		return "blocked"
	case domain.StatusClosed:
		return "closed"
	case domain.StatusInReview:
		if f.Mode == reviewpolicy.ModeTrusted || f.Mode == reviewpolicy.ModeDelegated {
			if f.HasActiveApproval() {
				if f.Closer(false, "").Allowed {
					return "ready_to_close"
				}
				return "pending_other"
			}
			if f.Reviewer(false, "").Allowed {
				return "reviewable"
			}
			involved := f.Issue.ImplementerSession == f.SessionID
			for _, h := range f.History {
				if h.SessionID == f.SessionID && (h.Action == domain.ActionSessionStarted || h.Action == domain.ActionSessionUnstarted) {
					involved = true
				}
			}
			if involved {
				if f.Mode == reviewpolicy.ModeTrusted {
					return "reviewable"
				}
				return "pending_review"
			}
			return "pending_other"
		}
		if f.Issue.ImplementerSession == f.SessionID {
			return "pending_review"
		}
		return "reviewable"
	}
	return "blocked"
}

// Transitions describe actions available with their ordinary required input
// (e.g. approval reason/self-review acknowledgement), without admin overrides.
func transitions(f workflow.Facts, writable bool) []string {
	out := []string{}
	if !writable {
		return out
	}
	i := f.Issue
	switch i.Status {
	case domain.StatusClosed:
		return []string{"reopen"}
	case domain.StatusBlocked:
		out = append(out, "unblock")
	case domain.StatusOpen:
		out = append(out, "start", "block")
	case domain.StatusInProgress:
		out = append(out, "block")
		if i.ImplementerSession == f.SessionID {
			out = append(out, "unstart")
		}
	case domain.StatusInReview:
		out = append(out, "reject")
		if f.HasActiveApproval() && (f.Mode == reviewpolicy.ModeTrusted || f.Mode == reviewpolicy.ModeDelegated) {
			out = append(out, "approve", "close")
		} else if f.Handoff != nil && f.Handoff.ContentVersion == i.ContentVersion {
			if f.Reviewer(false, "").Allowed || (f.Mode == reviewpolicy.ModeTrusted && f.Reviewer(true, "").Allowed) {
				out = append(out, "approve")
				if i.Minor {
					out = append(out, "close")
				}
			}
		}
		return out
	}
	if (i.Status == domain.StatusOpen || i.Status == domain.StatusInProgress) && f.Handoff != nil && f.Handoff.ContentVersion == i.ContentVersion {
		out = append(out, "review")
	}
	if f.Closer(false, "").Allowed && !(f.Mode == reviewpolicy.ModeDelegated && !i.Minor && f.HasImplementationHistory()) {
		out = append(out, "close")
	}
	return out
}
