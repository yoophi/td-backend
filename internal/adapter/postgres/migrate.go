package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The lock is transaction-scoped, so concurrent server startups cannot
	// inspect and apply the same migration simultaneously.
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(214748, 23)"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at text NOT NULL)`); err != nil {
		return err
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	known := map[int]bool{}
	for _, file := range entries {
		name := file.Name()
		version, e := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if e != nil {
			return fmt.Errorf("invalid migration name %s", name)
		}
		known[version] = true
		body, e := migrationFiles.ReadFile("migrations/" + name)
		if e != nil {
			return e
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(body))
		var oldName, oldChecksum string
		e = tx.QueryRowContext(ctx, "SELECT name,checksum FROM schema_migrations WHERE version=$1", version).Scan(&oldName, &oldChecksum)
		if e == nil {
			if oldName != name || oldChecksum != checksum {
				return fmt.Errorf("migration %s checksum mismatch", name)
			}
			continue
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if _, e = tx.ExecContext(ctx, string(body)); e != nil {
			return fmt.Errorf("%s: %w", name, e)
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES($1,$2,$3,$4)", version, name, checksum, time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
			return e
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err = rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		if !known[v] {
			rows.Close()
			return fmt.Errorf("database migration %d is newer than this binary", v)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return tx.Commit()
}
