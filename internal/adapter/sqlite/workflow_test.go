package sqlite

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/workflow"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/reviewpolicy"
)

func TestWorkflowFactsHistoryReviewBasisAndAtomicity(t *testing.T) {
	s, _ := openTest(t)
	c := seedOperation(t, s)
	var calls atomic.Int64
	if _, e := testRunner(s).Run(context.Background(), c, testMutation(), "issue.update", editIssue(c, &calls)); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	ctx := context.Background()
	e := s.Write(ctx, func(tx port.TxScope) error {
		ctx := tx.Context()
		if e := tx.WorkflowWrites().RecordHistory(ctx, c.ProjectID, domain.IssueSessionHistory{ID: "started", IssueID: "td-one", SessionID: c.SessionID, Action: domain.ActionSessionStarted, CreatedAt: now}, "operation-1"); e != nil {
			return e
		}
		if e := tx.WorkflowWrites().AppendActivity(ctx, c.ProjectID, port.Activity{ID: "handoff", IssueID: "td-one", SessionID: c.SessionID, OperationRowID: "operation-1", Kind: "handoff", ContentVersion: 1, Done: []string{"implemented"}, Snapshot: &port.Snapshot{CommitSHA: "abc", Branch: "main", DirtyFiles: 2}, CreatedAt: now}); e != nil {
			return e
		}
		return tx.WorkflowWrites().SaveReview(ctx, c.ProjectID, port.Review{Record: domain.IssueReview{ID: "review", IssueID: "td-one", ReviewerSession: c.SessionID, Decision: "approved", Summary: "tested", SelfReview: true, CreatedAt: now}, BasisContentVersion: 1, BasisHandoffID: "handoff", OperationRowID: "operation-1"})
	})
	if e != nil {
		t.Fatal(e)
	}
	e = s.Snapshot(ctx, func(read port.ReadSnapshot) error {
		f, e := workflow.LoadFacts(read.Context(), read, c, "td-one")
		if e != nil {
			return e
		}
		if !f.HasActiveApproval() || len(f.History) != 1 || f.Handoff == nil || f.Handoff.Snapshot == nil || f.Handoff.Snapshot.CommitSHA != "abc" {
			t.Fatalf("facts %+v", f)
		}
		if f.Reviewer(false, "").Allowed || !f.Reviewer(true, "").Allowed || !f.Reviewer(false, "reviewer").Allowed {
			t.Fatal("implementation history not applied")
		}
		f.Mode = reviewpolicy.ModeDelegated
		if f.Reviewer(true, "reviewer").Allowed {
			t.Fatal("delegated attribution bypass")
		}
		f.Issue.ContentVersion++
		if f.HasActiveApproval() {
			t.Fatal("content-stale approval accepted")
		}
		f.Issue.ContentVersion--
		f.Handoff.ContentVersion = 0
		if f.HasActiveApproval() {
			t.Fatal("obsolete handoff content accepted")
		}
		f.Handoff.ContentVersion = 1
		f.Handoff.ID = "new-handoff"
		if f.HasActiveApproval() {
			t.Fatal("handoff-stale approval accepted")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// The active-review unique index and deferred operation FK protect atomicity.
	for _, mode := range []string{"second approval", "missing receipt"} {
		e = s.Write(ctx, func(tx port.TxScope) error {
			ctx := tx.Context()
			if e := tx.WorkflowWrites().RecordHistory(ctx, c.ProjectID, domain.IssueSessionHistory{ID: "must-rollback", IssueID: "td-one", SessionID: c.SessionID, Action: domain.ActionSessionUnstarted, CreatedAt: now}, "operation-1"); e != nil {
				return e
			}
			if mode == "second approval" {
				return tx.WorkflowWrites().SaveReview(ctx, c.ProjectID, port.Review{Record: domain.IssueReview{ID: "second", IssueID: "td-one", ReviewerSession: c.SessionID, Decision: "approved", CreatedAt: now}, BasisContentVersion: 1, OperationRowID: "operation-1"})
			}
			return tx.WorkflowWrites().AppendActivity(ctx, c.ProjectID, port.Activity{ID: "orphan", IssueID: "td-one", SessionID: c.SessionID, OperationRowID: "missing", Kind: "log", Message: "must rollback", LogType: domain.LogTypeProgress, ContentVersion: 1, CreatedAt: now})
		})
		if !errors.Is(e, domain.ErrConflict) {
			t.Fatalf("%s: %v", mode, e)
		}
		e = s.Snapshot(ctx, func(read port.ReadSnapshot) error {
			history, e := read.Workflow().History(read.Context(), c.ProjectID, "td-one")
			if len(history) != 1 {
				t.Fatal("partial history committed")
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
	}
}
