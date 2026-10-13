package issue

import (
	"context"
	"strings"

	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type DependencyCommand struct {
	DependsOn string `json:"depends_on"`
}
type FileInput struct {
	FilePath  string          `json:"file_path"`
	Role      domain.FileRole `json:"role"`
	LinkedSHA string          `json:"linked_sha"`
}
type FilesCommand struct {
	Files *[]FileInput `json:"files"`
}
type DependencyResult struct {
	Dependency port.Dependency
	Issue      domain.Issue
}

func relationState(command operation.CommandContext, c domain.Caller, id, revision string) (State, error) {
	expected, e := domain.Observe(revision)
	if e != nil {
		return State{}, e
	}
	tx := command.Tx
	ctx := tx.Context()
	i, e := tx.Issues().Get(ctx, c.ProjectID, id, false)
	if e != nil {
		return State{}, e
	}
	if i.Revision != expected {
		return State{}, domain.ErrStale
	}
	deps, e := tx.Issues().Dependencies(ctx, c.ProjectID, id)
	if e != nil {
		return State{}, e
	}
	files, e := tx.Issues().Files(ctx, c.ProjectID, id)
	if e != nil {
		return State{}, e
	}
	return State{Issue: *i, Dependencies: deps, Files: files}, nil
}
func saveRelation(command operation.CommandContext, c domain.Caller, before State, after *State) error {
	tx := command.Tx
	ctx := tx.Context()
	after.Issue.Revision++
	after.Issue.ContentVersion++
	after.Issue.UpdatedAt = command.Now
	after.Issue.ReviewerSession = ""
	after.Issue.ReviewedAt = nil
	if e := tx.IssueWrites().SupersedeReviews(ctx, c.ProjectID, after.Issue.ID, command.Now); e != nil {
		return e
	}
	if e := tx.IssueWrites().Save(ctx, c.ProjectID, after.Issue, before.Issue.Revision); e != nil {
		return e
	}
	return SaveEffect(command, c.ProjectID, &before, *after)
}
func (s *Service) AddDependency(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd DependencyCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "relation.add", func(command operation.CommandContext) (any, error) {
		if strings.TrimSpace(cmd.DependsOn) == "" {
			return nil, invalid("depends_on", "nonempty issue ID required")
		}
		before, e := relationState(command, c, id, revision)
		if e != nil {
			return nil, e
		}
		tx := command.Tx
		ctx := tx.Context()
		if _, e = tx.Issues().Get(ctx, c.ProjectID, cmd.DependsOn, false); e != nil {
			return nil, e
		}
		stack := []string{cmd.DependsOn}
		seen := map[string]bool{}
		for len(stack) > 0 {
			if e = ctx.Err(); e != nil {
				return nil, e
			}
			target := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if target == id {
				return nil, invalid("depends_on", "dependency cycle")
			}
			if seen[target] {
				continue
			}
			seen[target] = true
			edges, e := tx.Issues().Dependencies(ctx, c.ProjectID, target)
			if e != nil {
				return nil, e
			}
			for _, edge := range edges {
				stack = append(stack, edge.DependsOnID)
			}
		}
		depID, e := s.ID("dependency")
		if e != nil {
			return nil, e
		}
		d := port.Dependency{ID: depID, IssueID: id, DependsOnID: cmd.DependsOn}
		if e = tx.IssueWrites().AddDependency(ctx, c.ProjectID, d); e != nil {
			return nil, e
		}
		after := before
		after.Dependencies = append(append([]port.Dependency{}, before.Dependencies...), d)
		if e = saveRelation(command, c, before, &after); e != nil {
			return nil, e
		}
		return DependencyResult{Dependency: d, Issue: after.Issue}, nil
	})
}
func (s *Service) RemoveDependency(ctx context.Context, c domain.Caller, m port.Mutation, id, revision, dependency string) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "relation.remove", func(command operation.CommandContext) (any, error) {
		before, e := relationState(command, c, id, revision)
		if e != nil {
			return nil, e
		}
		tx := command.Tx
		if e = tx.IssueWrites().RemoveDependency(tx.Context(), c.ProjectID, id, dependency); e != nil {
			return nil, e
		}
		after := before
		after.Dependencies = []port.Dependency{}
		for _, d := range before.Dependencies {
			if d.ID != dependency {
				after.Dependencies = append(after.Dependencies, d)
			}
		}
		if e = saveRelation(command, c, before, &after); e != nil {
			return nil, e
		}
		return Result{Issue: after.Issue}, nil
	})
}
func (s *Service) ReplaceFiles(ctx context.Context, c domain.Caller, m port.Mutation, id, revision string, cmd FilesCommand) (port.StoredReply, error) {
	return s.Runner.Run(ctx, c, m, "relation.files", func(command operation.CommandContext) (any, error) {
		if cmd.Files == nil {
			return nil, invalid("files", "files array required")
		}
		before, e := relationState(command, c, id, revision)
		if e != nil {
			return nil, e
		}
		after := before
		after.Files = []domain.IssueFile{}
		seen := map[string]bool{}
		for _, input := range *cmd.Files {
			if strings.TrimSpace(input.FilePath) == "" {
				return nil, invalid("file_path", "nonempty path required")
			}
			switch input.Role {
			case domain.FileRoleImplementation, domain.FileRoleTest, domain.FileRoleReference, domain.FileRoleConfig:
			default:
				return nil, invalid("role", "invalid file role")
			}
			pair := input.FilePath + "\x00" + string(input.Role)
			if seen[pair] {
				return nil, invalid("files", "duplicate path and role")
			}
			seen[pair] = true
			fileID, e := s.ID("file")
			if e != nil {
				return nil, e
			}
			after.Files = append(after.Files, domain.IssueFile{ID: fileID, IssueID: id, FilePath: input.FilePath, Role: input.Role, LinkedSHA: input.LinkedSHA, LinkedAt: command.Now})
		}
		if e = command.Tx.IssueWrites().ReplaceFiles(command.Tx.Context(), c.ProjectID, id, after.Files); e != nil {
			return nil, e
		}
		if e = saveRelation(command, c, before, &after); e != nil {
			return nil, e
		}
		return Result{Issue: after.Issue}, nil
	})
}
