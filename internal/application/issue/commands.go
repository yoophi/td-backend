package issue

import "github.com/yoophi/td-backend/internal/domain"

type CreateCommand struct {
	Title         string           `json:"title"`
	Description   string           `json:"description"`
	Acceptance    string           `json:"acceptance"`
	Type          *domain.Type     `json:"type"`
	Priority      *domain.Priority `json:"priority"`
	Points        int              `json:"points"`
	Labels        []string         `json:"labels"`
	ParentID      string           `json:"parent_id"`
	Sprint        string           `json:"sprint"`
	Minor         bool             `json:"minor"`
	DueDate       string           `json:"due_date"`
	DeferUntil    string           `json:"defer_until"`
	CreatedBranch string           `json:"created_branch"`
	DependsOn     []string         `json:"depends_on"`
}
type PatchCommand struct {
	Title       *string          `json:"title"`
	Description *string          `json:"description"`
	Acceptance  *string          `json:"acceptance"`
	Type        *domain.Type     `json:"type"`
	Priority    *domain.Priority `json:"priority"`
	Points      *int             `json:"points"`
	Labels      *[]string        `json:"labels"`
	ParentID    *string          `json:"parent_id"`
	Sprint      *string          `json:"sprint"`
	Minor       *bool            `json:"minor"`
	DueDate     *string          `json:"due_date"`
	DeferUntil  *string          `json:"defer_until"`
}

func (cmd PatchCommand) Empty() bool {
	return cmd.Title == nil && cmd.Description == nil && cmd.Acceptance == nil && cmd.Type == nil && cmd.Priority == nil && cmd.Points == nil && cmd.Labels == nil && cmd.ParentID == nil && cmd.Sprint == nil && cmd.Minor == nil && cmd.DueDate == nil && cmd.DeferUntil == nil
}
func (cmd PatchCommand) Apply(i *domain.Issue) {
	if cmd.Title != nil {
		i.Title = *cmd.Title
	}
	if cmd.Description != nil {
		i.Description = *cmd.Description
	}
	if cmd.Acceptance != nil {
		i.Acceptance = *cmd.Acceptance
	}
	if cmd.Type != nil {
		i.Type = *cmd.Type
	}
	if cmd.Priority != nil {
		i.Priority = *cmd.Priority
	}
	if cmd.Points != nil {
		i.Points = *cmd.Points
	}
	if cmd.Labels != nil {
		i.Labels = *cmd.Labels
	}
	if cmd.ParentID != nil {
		i.ParentID = *cmd.ParentID
	}
	if cmd.Sprint != nil {
		i.Sprint = *cmd.Sprint
	}
	if cmd.Minor != nil {
		i.Minor = *cmd.Minor
	}
	if cmd.DueDate != nil {
		i.DueDate = datePointer(*cmd.DueDate)
	}
	if cmd.DeferUntil != nil {
		i.DeferUntil = datePointer(*cmd.DeferUntil)
	}
}
