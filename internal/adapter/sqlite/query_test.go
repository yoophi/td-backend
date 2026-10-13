package sqlite

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func TestQueryFactsStayInSnapshotAndEnforceProjectScope(t *testing.T) {
	s, _ := openTest(t)
	c := seedOperation(t, s)
	ctx := context.Background()
	var calls atomic.Int64
	if _, e := testRunner(s).Run(ctx, c, testMutation(), "issue.update", editIssue(c, &calls)); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	var escaped port.QueryReader
	e := s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		before, e := read.Issues().Get(read.Context(), c.ProjectID, "td-one", false)
		if e != nil {
			return e
		}
		escaped = read.Queries()
		// This separate writer commits while the reader remains on its first
		// WAL snapshot. Issue and related activity must both remain unchanged.
		e = s.Write(ctx, func(tx port.TxScope) error {
			after := *before
			after.Revision++
			after.Title = "Concurrent query update"
			if e := tx.IssueWrites().Save(tx.Context(), c.ProjectID, after, before.Revision); e != nil {
				return e
			}
			return tx.WorkflowWrites().AppendActivity(tx.Context(), c.ProjectID, port.Activity{ID: "new-log", IssueID: "td-one", SessionID: c.SessionID, OperationRowID: "operation-1", Kind: "log", LogType: domain.LogTypeProgress, Message: "Concurrent activity", ContentVersion: 1, CreatedAt: now})
		})
		if e != nil {
			return e
		}
		facts, e := read.Queries().Facts(read.Context(), c.ProjectID)
		if e != nil {
			return e
		}
		if facts.Issues["td-one"].Revision != 2 || len(facts.Logs["td-one"]) != 0 {
			t.Fatal("mixed snapshot", facts)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = escaped.Facts(ctx, c.ProjectID); !errors.Is(e, domain.ErrScopeClosed) {
		t.Fatal(e)
	}
	e = s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		facts, e := read.Queries().Facts(read.Context(), c.ProjectID)
		if e != nil {
			return e
		}
		if facts.Issues["td-one"].Revision != 3 || len(facts.Logs["td-one"]) != 1 || facts.Logs["td-one"][0].Message != "Concurrent activity" {
			t.Fatal("missing new snapshot", facts)
		}
		other, e := read.Queries().Facts(read.Context(), "another-project")
		if e != nil {
			return e
		}
		if len(other.Issues) != 0 || len(other.Logs) != 0 || len(other.Dependencies) != 0 {
			t.Fatal("project leak", other)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// Archived issues' activity must never leak back into a query snapshot.
	e = s.Write(ctx, func(tx port.TxScope) error {
		i, e := tx.Issues().Get(tx.Context(), c.ProjectID, "td-one", false)
		if e != nil {
			return e
		}
		i.DeletedAt = &now
		i.Revision++
		return tx.IssueWrites().Save(tx.Context(), c.ProjectID, *i, 3)
	})
	if e != nil {
		t.Fatal(e)
	}
	e = s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		facts, e := read.Queries().Facts(read.Context(), c.ProjectID)
		if e != nil {
			return e
		}
		if len(facts.Issues) != 0 || len(facts.Logs) != 0 {
			t.Fatal("archived facts", facts)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
