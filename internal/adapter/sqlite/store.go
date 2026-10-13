// Package sqlite implements application ports using one persistent SQLite file.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	driver "modernc.org/sqlite"
)

type Options struct {
	Readers     int
	BusyTimeout time.Duration
}
type Store struct {
	writer, reader *sql.DB
	version        string
}

var _ port.UnitOfWork = (*Store)(nil)
var _ port.ReadStore = (*Store)(nil)

func Open(ctx context.Context, filename string, options Options) (*Store, error) {
	if filename == "" || filename == ":memory:" {
		return nil, fmt.Errorf("persistent SQLite filename required")
	}
	abs, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	// Explicit restrictive creation; an existing DB retains its owner permissions.
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if options.Readers == 0 {
		options.Readers = 4
	}
	if options.BusyTimeout == 0 {
		options.BusyTimeout = 5 * time.Second
	}
	if options.Readers < 1 || options.BusyTimeout < time.Millisecond {
		return nil, fmt.Errorf("invalid SQLite pool options")
	}
	s := &Store{}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	writerDSN := dsn(abs, options.BusyTimeout, false)
	s.writer, err = sql.Open("sqlite", writerDSN)
	if err != nil {
		return nil, err
	}
	s.writer.SetMaxOpenConns(1)
	s.writer.SetMaxIdleConns(1)
	if err = s.writer.PingContext(ctx); err != nil {
		return nil, err
	}
	if err = s.writer.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&s.version); err != nil {
		return nil, err
	}
	if !supportedVersion(s.version) {
		return nil, fmt.Errorf("SQLite %s lacks required WAL fixes (3.51.3+ required)", s.version)
	}
	var mode string
	if err = s.writer.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return nil, err
	}
	if mode != "wal" {
		return nil, fmt.Errorf("WAL unavailable: %s", mode)
	}
	if err = s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("migration: %w", err)
	}
	s.reader, err = sql.Open("sqlite", dsn(abs, options.BusyTimeout, true))
	if err != nil {
		return nil, err
	}
	s.reader.SetMaxOpenConns(options.Readers)
	s.reader.SetMaxIdleConns(options.Readers)
	if err = s.reader.PingContext(ctx); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}
func dsn(filename string, busy time.Duration, reader bool) string {
	u := url.URL{Scheme: "file", Path: filename}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout("+strconv.FormatInt(busy.Milliseconds(), 10)+")")
	q.Add("_pragma", "synchronous(FULL)")
	if reader {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
		q.Set("_txlock", "deferred")
	} else {
		q.Set("_txlock", "immediate")
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func supportedVersion(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return false
	}
	nums := make([]int, 3)
	for i := range nums {
		n, e := strconv.Atoi(parts[i])
		if e != nil {
			return false
		}
		nums[i] = n
	}
	return nums[0] > 3 || nums[0] == 3 && (nums[1] > 51 || nums[1] == 51 && nums[2] >= 3)
}
func (s *Store) Version() string { return s.version }
func (s *Store) Close() error {
	var errs []error
	if s.reader != nil {
		errs = append(errs, s.reader.Close())
	}
	if s.writer != nil {
		errs = append(errs, s.writer.Close())
	}
	return errors.Join(errs...)
}

type scope struct {
	tx       *sql.Tx
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
	return s.transaction(ctx, s.writer, true, func(sc *scope) error { return fn(sc) })
}
func (s *Store) Snapshot(ctx context.Context, fn func(port.ReadSnapshot) error) error {
	if ctx.Value(scopeContextKey{}) != nil {
		return fmt.Errorf("nested read transaction is not allowed")
	}
	return s.transaction(ctx, s.reader, false, func(sc *scope) error { return fn(sc) })
}
func (s *Store) transaction(ctx context.Context, db *sql.DB, write bool, fn func(*scope) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return classify(err)
	}
	sc := &scope{tx: tx, ctx: context.WithValue(ctx, scopeContextKey{}, true), writable: write}
	sc.active.Store(true)
	defer func() { sc.active.Store(false); _ = tx.Rollback() }()
	if err = fn(sc); err != nil {
		return classify(err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return classify(tx.Commit())
}
func classify(err error) error {
	if err == nil {
		return nil
	}
	var e *driver.Error
	if errors.As(err, &e) {
		switch e.Code() & 255 {
		case 5, 6:
			return fmt.Errorf("%w: SQLite contention", domain.ErrUnavailable)
		case 19:
			return fmt.Errorf("%w: %v", domain.ErrConflict, err)
		}
	}
	return err
}
