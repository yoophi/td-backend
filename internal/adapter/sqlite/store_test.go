package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
func seed(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	_, err := s.writer.ExecContext(ctx, `INSERT INTO actors(id,name,created_at) VALUES('a','actor','2026-10-13T00:00:00Z');
 INSERT INTO projects(id,name,created_at) VALUES('p','one','2026-10-13T00:00:00Z'),('q','two','2026-10-13T00:00:00Z');
 INSERT INTO sessions(project_id,id,actor_id,context_id,started_at,last_activity) VALUES('p','s','a','context','2026-10-13T00:00:00Z','2026-10-13T00:00:00Z'),('q','s','a','context','2026-10-13T00:00:00Z','2026-10-13T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"p", "q"} {
		err = s.Write(ctx, func(tx port.TxScope) error {
			return tx.IssueWrites().Save(ctx, project, domain.Issue{ID: project + "-issue", Revision: 1, ContentVersion: 1, Title: "Issue storage test", Status: domain.StatusOpen, Type: domain.TypeTask, Priority: domain.PriorityP2, CreatorSession: "s", Labels: []string{"two", "one"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, 0)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestMigrationAndEveryConnectionSettings(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	t.Log("embedded SQLite", s.Version())
	var count int
	if err := s.writer.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&count); err != nil || count != 23 {
		t.Fatalf("tables %d: %v", count, err)
	}
	s.writer.SetConnMaxLifetime(time.Nanosecond)
	for _, pool := range []*struct {
		reader bool
		n      int
	}{{false, 1}, {true, 4}} {
		db := s.writer
		if pool.reader {
			db = s.reader
		}
		conns := []interface{ Close() error }{}
		for i := 0; i < pool.n; i++ {
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, conn)
			for name, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000, "synchronous": 2, "query_only": boolInt(pool.reader)} {
				var v int
				if err = conn.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v); err != nil || v != want {
					t.Fatalf("%s=%d want %d: %v", name, v, want, err)
				}
			}
			var mode string
			if err = conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
				t.Fatal(mode, err)
			}
		}
		for _, c := range conns {
			c.Close()
		}
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func TestRollbackScopeLifetimeAndProjectIsolation(t *testing.T) {
	s, _ := openTest(t)
	seed(t, s)
	ctx := context.Background()
	sentinel := errors.New("receipt encoder failed")
	var escaped port.IssueReader
	err := s.Write(ctx, func(tx port.TxScope) error {
		escaped = tx.Issues()
		issue, e := escaped.Get(ctx, "p", "p-issue", false)
		if e != nil {
			return e
		}
		issue.Title = "This must be rolled back"
		issue.Revision++
		if e = tx.IssueWrites().Save(ctx, "p", *issue, 1); e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err = escaped.Get(ctx, "p", "p-issue", false); !errors.Is(err, domain.ErrScopeClosed) {
		t.Fatal("scope escaped", err)
	}
	if err = s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		issue, e := read.Issues().Get(ctx, "p", "p-issue", false)
		if e != nil {
			return e
		}
		if issue.Revision != 1 || issue.Title != "Issue storage test" || len(issue.Labels) != 2 {
			t.Fatalf("rollback/round trip: %+v", issue)
		}
		_, e = read.Issues().Get(ctx, "q", "p-issue", false)
		if !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("project leaked", e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = s.Write(ctx, func(tx port.TxScope) error {
		issue, e := tx.Issues().Get(ctx, "p", "p-issue", false)
		if e != nil {
			return e
		}
		issue.ParentID = "q-issue"
		issue.Revision++
		return tx.IssueWrites().Save(ctx, "p", *issue, 1)
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("cross project FK", err)
	}
	err = s.Write(ctx, func(tx port.TxScope) error { return s.Write(tx.Context(), func(port.TxScope) error { return nil }) })
	if err == nil {
		t.Fatal("nested UoW accepted")
	}
}
func TestWALSnapshotAndConcurrentConditionalWrite(t *testing.T) {
	s, _ := openTest(t)
	seed(t, s)
	ctx := context.Background()
	err := s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		first, e := read.Issues().Get(ctx, "p", "p-issue", false)
		if e != nil {
			return e
		}
		e = s.Write(ctx, func(tx port.TxScope) error {
			updated := *first
			updated.Revision++
			updated.Title = "Changed during read snapshot"
			return tx.IssueWrites().Save(ctx, "p", updated, first.Revision)
		})
		if e != nil {
			return e
		}
		same, e := read.Issues().Get(ctx, "p", "p-issue", false)
		if e != nil {
			return e
		}
		if same.Revision != first.Revision {
			t.Fatal("snapshot drift")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var base domain.Issue
	err = s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		i, e := read.Issues().Get(ctx, "p", "p-issue", false)
		if e == nil {
			base = *i
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.Write(ctx, func(tx port.TxScope) error {
				i := base
				i.Revision++
				return tx.IssueWrites().Save(ctx, "p", i, base.Revision)
			})
		}()
	}
	wg.Wait()
	close(results)
	success, stale := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, domain.ErrStale) {
			stale++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatal(success, stale)
	}
}
func TestMigrationMismatchAndUnknownVersionRejectReopen(t *testing.T) {
	for _, update := range []string{"UPDATE schema_migrations SET checksum='changed'", "INSERT INTO schema_migrations VALUES(999,'unknown','checksum','now')"} {
		t.Run(update, func(t *testing.T) {
			s, path := openTest(t)
			if _, err := s.writer.Exec(update); err != nil {
				t.Fatal(err)
			}
			s.Close()
			other, err := Open(context.Background(), path, Options{})
			if err == nil {
				other.Close()
				t.Fatal("unsafe migration accepted")
			}
		})
	}
}
func TestForeignKeyFailureAtCommitRollsBackMutation(t *testing.T) {
	s, _ := openTest(t)
	seed(t, s)
	ctx := context.Background()
	err := s.Write(ctx, func(tx port.TxScope) error {
		issue, e := tx.Issues().Get(ctx, "p", "p-issue", false)
		if e != nil {
			return e
		}
		issue.Revision++
		if e = tx.IssueWrites().Save(ctx, "p", *issue, 1); e != nil {
			return e
		}
		_, e = tx.(*scope).tx.ExecContext(ctx, `INSERT INTO activities(project_id,id,issue_id,session_id,operation_row_id,kind,message,log_type,content_version,created_at,updated_at) VALUES('p','log','p-issue','s','missing','log','message','progress',1,'now','now')`)
		return e
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	err = s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		i, e := read.Issues().Get(ctx, "p", "p-issue", false)
		if e != nil {
			return e
		}
		if i.Revision != 1 {
			t.Fatal("commit failure left effects")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestWriteLockDeadlineAndReadOnlySnapshot(t *testing.T) {
	s, _ := openTest(t)
	seed(t, s)
	ctx := context.Background()
	err := s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		_, e := read.(*scope).tx.Exec("UPDATE projects SET name='bad'")
		if e == nil {
			t.Fatal("read pool wrote")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Writer pool waiting respects caller cancellation without replaying callbacks.
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	timeout, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	called := false
	err = s.Write(timeout, func(port.TxScope) error { called = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatal(err, called)
	}
}
