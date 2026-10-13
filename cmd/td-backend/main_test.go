package main

import (
	"context"
	"database/sql"
	"github.com/yoophi/td-backend/internal/adapter/sqlite"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationFailureDoesNotListen(t *testing.T) {
	t.Setenv("TD_BACKEND_BOOTSTRAP_TOKEN", "0123456789abcdef0123456789abcdef")
	path := filepath.Join(t.TempDir(), "bad.db")
	store, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE schema_migrations SET checksum='corrupted'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	if err = run(context.Background(), []string{"--db", path, "--addr", address}); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatal("migration failure accepted")
	}
	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal("failed startup bound listener", err)
	}
	listener.Close()
}

func TestInvalidCredentialFailsBeforeStorageOrListen(t *testing.T) {
	t.Setenv("TD_BACKEND_BOOTSTRAP_TOKEN", "")
	path := filepath.Join(t.TempDir(), "should-not-exist.db")
	if err := run(context.Background(), []string{"--db", path, "--addr", "127.0.0.1:0"}); err == nil {
		t.Fatal("missing bootstrap credential accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid credential initialized storage", err)
	}
}
