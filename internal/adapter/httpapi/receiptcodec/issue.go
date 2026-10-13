package receiptcodec

import (
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

type IssueDTO struct {
	Revision                 string          `json:"revision"`
	ID                       string          `json:"id"`
	Title                    string          `json:"title"`
	Description              string          `json:"description"`
	Status                   domain.Status   `json:"status"`
	Type                     domain.Type     `json:"type"`
	Priority                 domain.Priority `json:"priority"`
	Points                   int             `json:"points"`
	Labels                   []string        `json:"labels"`
	ParentID                 *string         `json:"parent_id"`
	Acceptance               string          `json:"acceptance"`
	Sprint                   string          `json:"sprint"`
	ImplementerSession       *string         `json:"implementer_session"`
	CreatorSession           *string         `json:"creator_session"`
	ReviewerSession          *string         `json:"reviewer_session"`
	ReviewRequestedBySession *string         `json:"review_requested_by_session"`
	ClosedBySession          *string         `json:"closed_by_session"`
	CreatedAt                time.Time       `json:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at"`
	ReviewedAt               *time.Time      `json:"reviewed_at"`
	ClosedAt                 *time.Time      `json:"closed_at"`
	DeletedAt                *time.Time      `json:"deleted_at"`
	Minor                    bool            `json:"minor"`
	CreatedBranch            *string         `json:"created_branch"`
	DeferUntil               *string         `json:"defer_until"`
	DueDate                  *string         `json:"due_date"`
	DeferCount               int             `json:"defer_count"`
}

func IssueDTOFrom(i domain.Issue) IssueDTO {
	labels := append([]string{}, i.Labels...)
	return IssueDTO{Revision: domain.Revision(i.Revision),
		ID:                       i.ID,
		Title:                    i.Title,
		Description:              i.Description,
		Status:                   i.Status,
		Type:                     i.Type,
		Priority:                 i.Priority,
		Points:                   i.Points,
		Labels:                   labels,
		ParentID:                 nullable(i.ParentID),
		Acceptance:               i.Acceptance,
		Sprint:                   i.Sprint,
		ImplementerSession:       nullable(i.ImplementerSession),
		CreatorSession:           nullable(i.CreatorSession),
		ReviewerSession:          nullable(i.ReviewerSession),
		ReviewRequestedBySession: nullable(i.ReviewRequestedBySession),
		ClosedBySession:          nullable(i.ClosedBySession),
		CreatedAt:                i.CreatedAt,
		UpdatedAt:                i.UpdatedAt,
		ReviewedAt:               i.ReviewedAt,
		ClosedAt:                 i.ClosedAt,
		DeletedAt:                i.DeletedAt,
		Minor:                    i.Minor,
		CreatedBranch:            nullable(i.CreatedBranch),
		DeferUntil:               i.DeferUntil,
		DueDate:                  i.DueDate,
		DeferCount:               i.DeferCount,
	}
}
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
