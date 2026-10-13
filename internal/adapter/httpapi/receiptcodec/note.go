package receiptcodec

import (
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

type NoteDTO struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Pinned    bool       `json:"pinned"`
	Archived  bool       `json:"archived"`
	DeletedAt *time.Time `json:"deleted_at"`
	Revision  string     `json:"revision"`
}

func NoteDTOFrom(n domain.Note) NoteDTO {
	return NoteDTO{n.ID, n.Title, n.Content, n.CreatedAt, n.UpdatedAt, n.Pinned, n.Archived, n.DeletedAt, domain.Revision(n.Revision)}
}
