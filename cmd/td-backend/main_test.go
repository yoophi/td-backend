package main

import (
	"context"
	"database/sql"
	"github.com/yoophi/td-backend/internal/adapter/sqlite"
	"net"
	"path/filepath"
	"testing"
)

func TestMigrationFailureDoesNotListen(t *testing.T) {
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
	if err = run(context.Background(), []string{"--db", path, "--addr", address}); err == nil {
		t.Fatal("migration failure accepted")
	}
	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal("failed startup bound listener", err)
	}
	listener.Close()
}
