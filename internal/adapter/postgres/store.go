// Package postgres implements application storage ports in PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type Store struct {
	db      *sql.DB
	version string
}

var _ port.UnitOfWork = (*Store)(nil)
var _ port.ReadStore = (*Store)(nil)

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL connection: invalid configuration")
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: unavailable")
	}
	s := &Store{db: db}
	var versionNumber int
	if err := db.QueryRowContext(ctx, "SHOW server_version_num").Scan(&versionNumber); err != nil || versionNumber < 160000 {
		db.Close()
		return nil, fmt.Errorf("PostgreSQL 16 or newer required")
	}
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&s.version); err != nil {
		db.Close()
		return nil, fmt.Errorf("read PostgreSQL version: %w", classify(err))
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("PostgreSQL migration: %w", classify(err))
	}
	return s, nil
}

func (s *Store) Version() string { return s.version }
func (s *Store) Close() error    { return s.db.Close() }

type scope struct {
	tx       *boundTx
	ctx      context.Context
	active   atomic.Bool
	writable bool
}

func (s *scope) check() error {
	if !s.active.Load() {
		return domain.ErrScopeClosed
	}
	return s.ctx.Err()
}
func (s *scope) Context() context.Context          { return s.ctx }
func (s *scope) Projects() port.ProjectReader      { return projectRepo{s} }
func (s *scope) Issues() port.IssueReader          { return issueRepo{s} }
func (s *scope) ProjectWrites() port.ProjectWriter { return projectRepo{s} }
func (s *scope) IssueWrites() port.IssueWriter     { return issueRepo{s} }

type scopeContextKey struct{}

func (s *Store) Write(ctx context.Context, fn func(port.TxScope) error) error {
	if ctx.Value(scopeContextKey{}) != nil {
		return fmt.Errorf("nested write transaction is not allowed")
	}
	return s.transaction(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable}, true, func(sc *scope) error { return fn(sc) })
}

func (s *Store) Snapshot(ctx context.Context, fn func(port.ReadSnapshot) error) error {
	if ctx.Value(scopeContextKey{}) != nil {
		return fmt.Errorf("nested read transaction is not allowed")
	}
	return s.transaction(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, false, func(sc *scope) error { return fn(sc) })
}

func (s *Store) transaction(ctx context.Context, opts *sql.TxOptions, write bool, fn func(*scope) error) error {
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return classify(err)
	}
	sc := &scope{tx: &boundTx{Tx: tx}, ctx: context.WithValue(ctx, scopeContextKey{}, true), writable: write}
	sc.active.Store(true)
	defer func() { sc.active.Store(false); _ = tx.Rollback() }()
	if err := fn(sc); err != nil {
		return classify(err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return classify(tx.Commit())
}

// boundTx changes only bind-marker syntax. The queries and schema remain
// PostgreSQL-specific; this does not translate SQLite SQL expressions.
type boundTx struct{ *sql.Tx }

func (b *boundTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return b.Tx.ExecContext(ctx, bind(query), normalizeArgs(args)...)
}
func (b *boundTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return b.Tx.QueryContext(ctx, bind(query), normalizeArgs(args)...)
}
func (b *boundTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return b.Tx.QueryRowContext(ctx, bind(query), normalizeArgs(args)...)
}

func normalizeArgs(args []any) []any {
	converted := make([]any, len(args))
	copy(converted, args)
	for i, value := range args {
		if b, ok := value.(bool); ok {
			if b {
				converted[i] = 1
			} else {
				converted[i] = 0
			}
		}
	}
	return converted
}

func bind(query string) string {
	var out strings.Builder
	out.Grow(len(query) + 8)
	position := 1
	quoted := byte(0)
	for i := 0; i < len(query); i++ {
		c := query[i]
		if quoted != 0 {
			out.WriteByte(c)
			if c == quoted {
				if i+1 < len(query) && query[i+1] == quoted {
					i++
					out.WriteByte(query[i])
				} else {
					quoted = 0
				}
			}
			continue
		}
		if c == '\'' || c == '"' {
			quoted = c
			out.WriteByte(c)
		} else if c == '?' {
			fmt.Fprintf(&out, "$%d", position)
			position++
		} else {
			out.WriteByte(c)
		}
	}
	return out.String()
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "40001", "40P01", "55P03", "57014":
			return fmt.Errorf("%w: PostgreSQL transaction unavailable", domain.ErrUnavailable)
		case "23505", "23503", "23514", "23502":
			return fmt.Errorf("%w: PostgreSQL constraint violation", domain.ErrConflict)
		}
		if strings.HasPrefix(pg.Code, "08") {
			return fmt.Errorf("%w: PostgreSQL connection unavailable", domain.ErrUnavailable)
		}
	}
	return err
}
