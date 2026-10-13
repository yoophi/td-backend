package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func TestUndoMigrationPreservesVersionOneWorkSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	body, e := migrationFiles.ReadFile("migrations/0001_initial.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(body)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,checksum TEXT NOT NULL,applied_at TEXT NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO schema_migrations VALUES(1,'0001_initial.sql',?,'2026-10-13T00:00:00Z')`, fmt.Sprintf("%x", sha256.Sum256(body))); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO actors(id,name,created_at) VALUES('a','actor','2026-10-13T00:00:00Z'); INSERT INTO projects(id,name,created_at) VALUES('p','project','2026-10-13T00:00:00Z'); INSERT INTO sessions(project_id,id,actor_id,context_id,started_at,last_activity) VALUES('p','s','a','context','2026-10-13T00:00:00Z','2026-10-13T00:00:00Z'); INSERT INTO work_sessions(project_id,id,session_id,name,started_at,start_sha) VALUES('p','ws-old','s','Existing work','2026-10-13T00:00:00Z','original');`); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := Open(context.Background(), path, Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var version int
	if e = s.writer.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); e != nil || version != 2 {
		t.Fatal(version, e)
	}
	now := time.Now().UTC()
	if e = s.Write(context.Background(), func(tx port.TxScope) error {
		w, e := tx.WorkSessions().Get(tx.Context(), "p", "ws-old")
		if e != nil {
			return e
		}
		if w.Revision != 1 || w.Name != "Existing work" || w.StartSHA != "original" || w.DeletedAt != nil {
			t.Fatal(w)
		}
		w.Revision = 2
		w.DeletedAt = &now
		return tx.WorkSessionWrites().Save(tx.Context(), "p", w, 1)
	}); e != nil {
		t.Fatal(e)
	}
	if e = s.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		if _, e := r.WorkSessions().Get(r.Context(), "p", "ws-old"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		w, e := r.WorkSessions().GetArchived(r.Context(), "p", "ws-old")
		if e != nil {
			return e
		}
		if w.DeletedAt == nil || !w.DeletedAt.Equal(now) || w.Revision != 2 || w.StartSHA != "original" {
			t.Fatal(w)
		}
		if _, e := r.Workflow().WorkSessionAccess(r.Context(), "p", "ws-old"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("archived activity attachment", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
