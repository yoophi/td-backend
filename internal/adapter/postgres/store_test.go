package postgres_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/adapter/identity"
	"github.com/yoophi/td-backend/internal/adapter/postgres"
	"github.com/yoophi/td-backend/internal/adapter/tokenauth"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/project"
)

func testingDatabaseURL(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TD_BACKEND_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TD_BACKEND_TEST_DATABASE_URL for PostgreSQL integration tests")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "td_test_" + hex.EncodeToString(random[:])
	db, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE SCHEMA " + schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func TestProjectCreateAndReceiptReplay(t *testing.T) {
	dsn := testingDatabaseURL(t)
	ctx := context.Background()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	auth := tokenauth.New(store, store)
	const token = "0123456789abcdef0123456789abcdef"
	if err := auth.Bootstrap(ctx, token); err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Resolve(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	ids := identity.New()
	key, err := ids.UUID4()
	if err != nil {
		t.Fatal(err)
	}
	svc := project.Service{Writes: store, Reads: store, Encoder: receiptcodec.Codec{}, ID: ids.ID, Now: time.Now}
	mutation := port.Mutation{Key: key, Fingerprint: []byte("pg-project-create"), Method: "POST", Path: "/v1/projects"}
	first, err := svc.Create(ctx, actor, mutation, project.CreateCommand{Name: "PostgreSQL integration project"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(ctx, actor, mutation, project.CreateCommand{Name: "PostgreSQL integration project"})
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Body) != string(second.Body) || first.Status != second.Status {
		t.Fatal("project creation receipt replay differs")
	}
}

func TestMigrationRejectsTamperingAndUnknownVersion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"checksum", "UPDATE schema_migrations SET checksum='changed' WHERE version=1"},
		{"future", "INSERT INTO schema_migrations(version,name,checksum,applied_at) VALUES(999,'future','future','2026-01-01T00:00:00Z')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := testingDatabaseURL(t)
			store, err := postgres.Open(context.Background(), dsn)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.query); err != nil {
				t.Fatal(err)
			}
			_ = db.Close()
			store, err = postgres.Open(context.Background(), dsn)
			if err == nil {
				_ = store.Close()
				t.Fatal("migration tampering was accepted")
			}
		})
	}
}
