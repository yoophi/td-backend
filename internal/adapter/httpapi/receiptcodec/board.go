package receiptcodec

import (
	"github.com/yoophi/td-backend/internal/application/board"
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

type BoardCardDTO struct {
	BoardID     string   `json:"board_id"`
	Position    int      `json:"position"`
	HasPosition bool     `json:"has_position"`
	Category    string   `json:"category"`
	Issue       IssueDTO `json:"issue"`
}
type BoardDetailDTO struct {
	Board  BoardDTO       `json:"board"`
	Issues []BoardCardDTO `json:"issues"`
}

func BoardDetailDTOFrom(d board.Detail) BoardDetailDTO {
	out := BoardDetailDTO{Board: BoardDTOFrom(d.Board), Issues: []BoardCardDTO{}}
	for _, v := range d.Issues {
		out.Issues = append(out.Issues, BoardCardDTO{v.BoardID, v.Position, v.HasPosition, v.Category, IssueDTOFrom(v.Issue)})
	}
	return out
}

func BoardDTOFrom(b domain.Board) BoardDTO {
	return BoardDTO{b.ID, b.Name, b.Query, b.IsBuiltin, b.ViewMode, domain.Revision(b.Revision), b.LastViewedAt, b.CreatedAt, b.UpdatedAt}
}
