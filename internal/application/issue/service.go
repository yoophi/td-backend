// Package issue implements observed issue mutations through application ports.
package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yoophi/td-backend/internal/application/access"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/reviewpolicy"
)

type Service struct {
	Runner operation.Runner
	Reads  port.ReadStore
	ID     func(string) (string, error)
}
type Result struct{ Issue domain.Issue }

// State is the versioned issue aggregate payload persisted for conditional undo.
type State struct {
	ActivityChanges []port.Activity    `json:"activity_changes,omitempty"`
	Files           []domain.IssueFile `json:"files"`
	Issue           domain.Issue       `json:"issue"`
	Dependencies    []port.Dependency  `json:"dependencies"`
}

func invalid(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}
func datePointer(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func validate(i *domain.Issue, p *domain.Project) error {
	i.Title = strings.TrimSpace(i.Title)
	n := utf8.RuneCountInString(i.Title)
	if n < p.TitleMinLength || n > p.TitleMaxLength {
		return invalid("title", fmt.Sprintf("title must contain %d..%d characters", p.TitleMinLength, p.TitleMaxLength))
	}
	if !slices.Contains([]domain.Type{domain.TypeBug, domain.TypeFeature, domain.TypeTask, domain.TypeEpic, domain.TypeChore}, i.Type) {
		return invalid("type", "invalid issue type")
	}
	if !slices.Contains([]domain.Priority{domain.PriorityP0, domain.PriorityP1, domain.PriorityP2, domain.PriorityP3, domain.PriorityP4}, i.Priority) {
		return invalid("priority", "invalid priority")
	}
	if i.Points != 0 && !domain.IsValidPoints(i.Points) {
		return invalid("points", "invalid estimate")
	}
	for field, value := range map[string]*string{"due_date": i.DueDate, "defer_until": i.DeferUntil} {
		if value != nil {
			parsed, e := time.Parse("2006-01-02", *value)
			if e != nil || parsed.Format("2006-01-02") != *value {
				return invalid(field, "expected YYYY-MM-DD or empty string")
			}
		}
	}
	labels := make([]string, 0, len(i.Labels))
	for _, label := range i.Labels {
		label = strings.TrimSpace(label)
		if label == "" {
			return invalid("labels", "empty label is not allowed")
		}
		labels = append(labels, label)
	}
	slices.Sort(labels)
	i.Labels = slices.Compact(labels)
	return nil
}
func validateParent(ctx context.Context, read port.ReadSnapshot, project string, i domain.Issue) error {
	seen := map[string]bool{i.ID: true}
	for parent := i.ParentID; parent != ""; {
		if seen[parent] {
			return invalid("parent_id", "parent cycle")
		}
		seen[parent] = true
		next, e := read.Issues().Get(ctx, project, parent, false)
		if e != nil {
			return e
		}
		parent = next.ParentID
	}
	return nil
}
func (s *Service) Create(ctx context.Context, c domain.Caller, m port.Mutation, cmd CreateCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "issue.create", func(command operation.CommandContext) (any, error) {
		tx := command.Tx
		ctx := tx.Context()
		p, e := tx.Projects().Get(ctx, c.ProjectID)
		if e != nil {
			return nil, e
		}
		kind, priority := domain.TypeTask, domain.PriorityP2
		if cmd.Type != nil {
			kind = *cmd.Type
		}
		if cmd.Priority != nil {
			priority = *cmd.Priority
		}
		id, e := s.ID("issue")
		if e != nil {
			return nil, e
		}
		i := domain.Issue{ID: id, Title: cmd.Title, Description: cmd.Description, Acceptance: cmd.Acceptance, Type: kind, Priority: priority, Points: cmd.Points, Labels: cmd.Labels, ParentID: cmd.ParentID, Sprint: cmd.Sprint, Minor: cmd.Minor, DueDate: datePointer(cmd.DueDate), DeferUntil: datePointer(cmd.DeferUntil), CreatedBranch: cmd.CreatedBranch, CreatorSession: c.SessionID, Status: domain.StatusOpen, Revision: 1, ContentVersion: 1, CreatedAt: command.Now, UpdatedAt: command.Now}
		if e = validate(&i, p); e != nil {
			return nil, e
		}
		if e = validateParent(ctx, tx, c.ProjectID, i); e != nil {
			return nil, e
		}
		dependencies := []port.Dependency{}
		seen := map[string]bool{}
		for _, target := range cmd.DependsOn {
			if strings.TrimSpace(target) == "" {
				return nil, invalid("depends_on", "nonempty issue ID required")
			}
			if seen[target] {
				continue
			}
			seen[target] = true
			if target == i.ID {
				return nil, invalid("depends_on", "self dependency")
			}
			if _, e = tx.Issues().Get(ctx, c.ProjectID, target, false); e != nil {
				return nil, e
			}
			depID, e := s.ID("dependency")
			if e != nil {
				return nil, e
			}
			dependencies = append(dependencies, port.Dependency{ID: depID, IssueID: i.ID, DependsOnID: target})
		}
		if e = tx.IssueWrites().Save(ctx, c.ProjectID, i, 0); e != nil {
			return nil, e
		}
		for _, d := range dependencies {
			if e = tx.IssueWrites().AddDependency(ctx, c.ProjectID, d); e != nil {
				return nil, e
			}
		}
		if e = tx.IssueWrites().RecordCreation(ctx, c.ProjectID, i.ID, c.SessionID, command.RowID, command.Now); e != nil {
			return nil, e
		}
		if e = SaveEffect(command, c.ProjectID, nil, State{Issue: i, Dependencies: dependencies}); e != nil {
			return nil, e
		}
		blocked := map[string]bool{}
		for _, observation := range cmd.Blocks {
			if strings.TrimSpace(observation.IssueID) == "" || blocked[observation.IssueID] {
				return nil, invalid("blocks", "nonempty unique Issue IDs required")
			}
			blocked[observation.IssueID] = true
			revision := `"` + observation.Revision + `"`
			if _, e = domain.Observe(revision); e != nil {
				return nil, invalid("blocks.revision", "expected r-N observed revision")
			}
			before, e := relationState(command, c, observation.IssueID, revision)
			if e != nil {
				return nil, e
			}
			if e = validateDependencyEdge(command, c, observation.IssueID, i.ID); e != nil {
				return nil, e
			}
			depID, e := s.ID("dependency")
			if e != nil {
				return nil, e
			}
			edge := port.Dependency{ID: depID, IssueID: observation.IssueID, DependsOnID: i.ID}
			if e = tx.IssueWrites().AddDependency(ctx, c.ProjectID, edge); e != nil {
				return nil, e
			}
			after := before
			after.Dependencies = append(append([]port.Dependency{}, before.Dependencies...), edge)
			if e = saveRelation(command, c, before, &after); e != nil {
				return nil, e
			}
		}
		return Result{Issue: i}, nil
	})
}
func SaveEffect(command operation.CommandContext, project string, before *State, after State) error {
	if after.Files == nil || (before != nil && before.Files == nil) {
		files, e := command.Tx.Issues().Files(command.Tx.Context(), project, after.Issue.ID)
		if e != nil {
			return e
		}
		if after.Files == nil {
			after.Files = files
		}
		if before != nil && before.Files == nil {
			before.Files = files
		}
	}

	var previous []byte
	var e error
	if before != nil {
		previous, e = json.Marshal(before)
		if e != nil {
			return e
		}
	}
	next, e := json.Marshal(after)
	if e != nil {
		return e
	}
	version := 2
	if after.ActivityChanges != nil || (before != nil && before.ActivityChanges != nil) {
		version = 3
	}
	return command.Tx.OperationWrites().SaveEffect(command.Tx.Context(), port.Effect{ProjectID: project, OperationRowID: command.RowID, EntityKind: "issue", EntityID: after.Issue.ID, AfterRevision: domain.Revision(after.Issue.Revision), StateVersion: version, BeforeJSON: previous, AfterJSON: next})
}
func (s *Service) Patch(ctx context.Context, c domain.Caller, m port.Mutation, id, observed string, cmd PatchCommand) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, observed, "issue.update", false, func(command operation.CommandContext, i *domain.Issue) error {
		if cmd.Empty() {
			return invalid("body", "at least one field required")
		}
		before := *i
		cmd.Apply(i)
		p, e := command.Tx.Projects().Get(command.Tx.Context(), c.ProjectID)
		if e != nil {
			return e
		}
		if e = validate(i, p); e != nil {
			return e
		}
		if e = validateParent(command.Tx.Context(), command.Tx, c.ProjectID, *i); e != nil {
			return e
		}
		// Use the same substantive-field policy as td; metadata edits preserve approval.
		if reviewpolicy.IsReviewInvalidatingMutation(reviewpolicy.IssueMutation{
			DescriptionChanged: before.Description != i.Description,
			TitleChanged:       before.Title != i.Title,
			TypeChanged:        before.Type != i.Type,
			PriorityChanged:    before.Priority != i.Priority,
			MinorChanged:       before.Minor != i.Minor,
			ParentIDChanged:    before.ParentID != i.ParentID,
		}) {
			i.ContentVersion++
			if e = command.Tx.IssueWrites().SupersedeReviews(command.Tx.Context(), c.ProjectID, id, command.Now); e != nil {
				return e
			}
			i.ReviewerSession = ""
			i.ReviewedAt = nil
		}
		return nil
	})
}
func (s *Service) Delete(ctx context.Context, c domain.Caller, m port.Mutation, id, observed string) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, observed, "issue.delete", false, func(command operation.CommandContext, i *domain.Issue) error { i.DeletedAt = &command.Now; return nil })
}
func (s *Service) Restore(ctx context.Context, c domain.Caller, m port.Mutation, id, observed string) (port.StoredReply, error) {
	return s.mutate(ctx, c, m, id, observed, "issue.restore", true, func(command operation.CommandContext, i *domain.Issue) error {
		if i.DeletedAt == nil {
			return domain.ErrConflict
		}
		i.DeletedAt = nil
		return nil
	})
}
func (s *Service) mutate(ctx context.Context, c domain.Caller, m port.Mutation, id, observed, kind string, deleted bool, edit func(operation.CommandContext, *domain.Issue) error) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, kind, func(command operation.CommandContext) (any, error) {
		expected, e := domain.Observe(observed)
		if e != nil {
			return nil, e
		}
		tx := command.Tx
		ctx := tx.Context()
		i, e := tx.Issues().Get(ctx, c.ProjectID, id, deleted)
		if e != nil {
			return nil, e
		}
		if i.Revision != expected {
			return nil, domain.ErrStale
		}
		deps, e := tx.Issues().Dependencies(ctx, c.ProjectID, id)
		if e != nil {
			return nil, e
		}
		before := State{Issue: *i, Dependencies: deps}
		if e = edit(command, i); e != nil {
			return nil, e
		}
		i.Revision++
		i.UpdatedAt = command.Now
		if e = tx.IssueWrites().Save(ctx, c.ProjectID, *i, expected); e != nil {
			return nil, e
		}
		if e = SaveEffect(command, c.ProjectID, &before, State{Issue: *i, Dependencies: deps}); e != nil {
			return nil, e
		}
		return Result{Issue: *i}, nil
	})
}
func (s *Service) Get(ctx context.Context, c domain.Caller, id string, deleted bool) (domain.Issue, error) {
	var i domain.Issue
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		ctx := read.Context()
		if e := access.Authorize(ctx, read, c, true, false); e != nil {
			return e
		}
		got, e := read.Issues().Get(ctx, c.ProjectID, id, deleted)
		if e == nil {
			i = *got
		}
		return e
	})
	return i, e
}

type Detail struct {
	Issue   domain.Issue
	Context port.IssueContext
}

func (s *Service) Detail(ctx context.Context, c domain.Caller, id string, deleted, reviews bool) (Detail, error) {
	var result Detail
	e := s.Reads.Snapshot(ctx, func(read port.ReadSnapshot) error {
		ctx := read.Context()
		if e := access.Authorize(ctx, read, c, true, false); e != nil {
			return e
		}
		i, e := read.Issues().Get(ctx, c.ProjectID, id, deleted)
		if e != nil {
			return e
		}
		result.Issue = *i
		result.Context, e = read.Issues().Context(ctx, c.ProjectID, id, reviews)
		return e
	})
	return result, e
}
