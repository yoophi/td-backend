package receiptcodec

import (
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

type BoardDTO struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Query        string     `json:"query"`
	IsBuiltin    bool       `json:"is_builtin"`
	ViewMode     string     `json:"view_mode"`
	Revision     string     `json:"revision"`
	LastViewedAt *time.Time `json:"last_viewed_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func BoardDTOFrom(b domain.Board) BoardDTO {
	return BoardDTO{b.ID, b.Name, b.Query, b.IsBuiltin, b.ViewMode, domain.Revision(b.Revision), b.LastViewedAt, b.CreatedAt, b.UpdatedAt}
}
