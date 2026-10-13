package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func TestBoardPositionsAtomicSwapIsolationAndRevision(t *testing.T) {
	s, _ := openTest(t)
	seed(t, s)
	ctx := context.Background()
	now := time.Now().UTC()
	b := domain.Board{ID: "board", Name: "Work", Query: "status != closed", ViewMode: "backlog", Revision: 1, CreatedAt: now, UpdatedAt: now}
	initial := []domain.BoardIssue{{BoardID: b.ID, IssueID: "p-issue", Position: 1024, AddedAt: now}, {BoardID: b.ID, IssueID: "second", Position: 2048, AddedAt: now}}
	if e := s.Write(ctx, func(tx port.TxScope) error {
		issue, e := tx.Issues().Get(tx.Context(), "p", "p-issue", false)
		if e != nil {
			return e
		}
		issue.ID = "second"
		if e = tx.IssueWrites().Save(tx.Context(), "p", *issue, 0); e != nil {
			return e
		}
		if e = tx.BoardWrites().Create(tx.Context(), "p", b); e != nil {
			return e
		}
		return tx.BoardWrites().ReplacePositions(tx.Context(), "p", b.ID, initial)
	}); e != nil {
		t.Fatal(e)
	}
	read := func(wantRevision int64, want []domain.BoardIssue) {
		t.Helper()
		if e := s.Snapshot(ctx, func(read port.ReadSnapshot) error {
			got, e := read.Boards().Get(read.Context(), "p", b.ID, false)
			if e != nil {
				return e
			}
			if got.Revision != wantRevision || got.Name != b.Name || !got.CreatedAt.Equal(now) {
				t.Fatalf("board %+v", got)
			}
			positions, e := read.Boards().Positions(read.Context(), "p", b.ID)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(positions, want) {
				t.Fatalf("positions %+v want %+v", positions, want)
			}
			if _, e = read.Boards().Get(read.Context(), "q", b.ID, false); !errors.Is(e, domain.ErrNotFound) {
				t.Fatalf("project isolation: %v", e)
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	read(1, initial)
	swapped := []domain.BoardIssue{initial[1], initial[0]}
	swapped[0].Position, swapped[1].Position = 1024, 2048
	if e := s.Write(ctx, func(tx port.TxScope) error {
		b.Revision = 2
		if e := tx.BoardWrites().Save(tx.Context(), "p", b, 1); e != nil {
			return e
		}
		return tx.BoardWrites().ReplacePositions(tx.Context(), "p", b.ID, swapped)
	}); e != nil {
		t.Fatal(e)
	}
	read(2, swapped)
	for _, invalid := range [][]domain.BoardIssue{
		{{BoardID: b.ID, IssueID: "p-issue", Position: 1024, AddedAt: now}, {BoardID: b.ID, IssueID: "second", Position: 1024, AddedAt: now}},
		{{BoardID: b.ID, IssueID: "p-issue", Position: 1024, AddedAt: now}, {BoardID: b.ID, IssueID: "q-issue", Position: 2048, AddedAt: now}},
	} {
		e := s.Write(ctx, func(tx port.TxScope) error {
			b.Revision = 3
			if e := tx.BoardWrites().Save(tx.Context(), "p", b, 2); e != nil {
				return e
			}
			return tx.BoardWrites().ReplacePositions(tx.Context(), "p", b.ID, invalid)
		})
		if !errors.Is(e, domain.ErrConflict) {
			t.Fatalf("expected position failure: %v", e)
		}
		read(2, swapped)
	}
	e := s.Write(ctx, func(tx port.TxScope) error {
		b.Revision = 2
		return tx.BoardWrites().Save(tx.Context(), "p", b, 1)
	})
	if !errors.Is(e, domain.ErrStale) {
		t.Fatal("stale update", e)
	}
	read(2, swapped)
	if e = s.Write(ctx, func(tx port.TxScope) error {
		b.Revision, b.LastViewedAt = 3, &now
		if e := tx.BoardWrites().Save(tx.Context(), "p", b, 2); e != nil {
			return e
		}
		return tx.BoardWrites().ReplacePositions(tx.Context(), "p", b.ID, nil)
	}); e != nil {
		t.Fatal(e)
	}
	read(3, []domain.BoardIssue{})
}

func TestBoardArchiveNameReuseAndScope(t *testing.T) {
	s, _ := openTest(t)
	seed(t, s)
	ctx, now := context.Background(), time.Now().UTC()
	b := domain.Board{ID: "one", Name: "Same", Query: "", ViewMode: "swimlanes", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if e := s.Write(ctx, func(tx port.TxScope) error { return tx.BoardWrites().Create(tx.Context(), "p", b) }); e != nil {
		t.Fatal(e)
	}
	duplicate := b
	duplicate.ID = "two"
	if e := s.Write(ctx, func(tx port.TxScope) error { return tx.BoardWrites().Create(tx.Context(), "p", duplicate) }); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("duplicate live name", e)
	}
	if e := s.Write(ctx, func(tx port.TxScope) error {
		b.DeletedAt, b.Revision = &now, 2
		if e := tx.BoardWrites().Save(tx.Context(), "p", b, 1); e != nil {
			return e
		}
		return tx.BoardWrites().Create(tx.Context(), "p", duplicate)
	}); e != nil {
		t.Fatal(e)
	}
	var escaped port.BoardReader
	if e := s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		escaped = read.Boards()
		if _, e := escaped.Get(read.Context(), "p", b.ID, false); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("archive visible", e)
		}
		archived, e := escaped.Get(read.Context(), "p", b.ID, true)
		if e != nil {
			return e
		}
		if archived.DeletedAt == nil || !archived.DeletedAt.Equal(now) || archived.Revision != 2 {
			t.Fatalf("archive %+v", archived)
		}
		live, e := escaped.List(read.Context(), "p")
		if e != nil {
			return e
		}
		if len(live) != 1 || live[0].ID != duplicate.ID {
			t.Fatal(live)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := escaped.List(ctx, "p"); !errors.Is(e, domain.ErrScopeClosed) {
		t.Fatal("escaped scope", e)
	}
}
