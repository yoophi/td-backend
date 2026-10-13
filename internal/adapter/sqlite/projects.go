package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/yoophi/td-backend/internal/domain"
	"time"
)

type projectRepo struct{ *scope }

func (r projectRepo) Get(ctx context.Context, id string) (*domain.Project, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	p := &domain.Project{}
	var created string
	err := r.tx.QueryRowContext(ctx, `SELECT id,name,repository,review_policy_mode,title_min_length,title_max_length,change_seq,created_at FROM projects WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.Repository, &p.ReviewPolicyMode, &p.TitleMinLength, &p.TitleMaxLength, &p.ChangeSeq, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, classify(err)
	}
	p.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return p, err
}
func (r projectRepo) Create(ctx context.Context, p domain.Project) error {
	if err := r.check(); err != nil {
		return err
	}
	_, err := r.tx.ExecContext(ctx, `INSERT INTO projects(id,name,repository,review_policy_mode,title_min_length,title_max_length,change_seq,created_at) VALUES(?,?,?,?,?,?,?,?)`, p.ID, p.Name, p.Repository, p.ReviewPolicyMode, p.TitleMinLength, p.TitleMaxLength, p.ChangeSeq, p.CreatedAt.UTC().Format(time.RFC3339Nano))
	return classify(err)
}
func (r projectRepo) AdvanceChange(ctx context.Context, id string) (int64, error) {
	if err := r.check(); err != nil {
		return 0, err
	}
	var value int64
	err := r.tx.QueryRowContext(ctx, "UPDATE projects SET change_seq=change_seq+1 WHERE id=? RETURNING change_seq", id).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return value, classify(err)
}
