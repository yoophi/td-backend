package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type testReceiptEncoder struct{ fail bool }

func (e testReceiptEncoder) Encode(_ string, _ any) (port.StoredReply, error) {
	if e.fail {
		return port.StoredReply{}, errors.New("encoder failed")
	}
	return port.StoredReply{Status: 200, Headers: map[string]string{"ETag": `"r-2"`}, Body: []byte("original response\n")}, nil
}
func seedOperation(t *testing.T, s *Store) domain.Caller {
	t.Helper()
	c := domain.Caller{Actor: domain.Actor{ID: "a"}, ProjectID: "prj_0199dc38-1200-7000-8000-000000000001", SessionID: "s"}
	_, err := s.writer.Exec(`INSERT INTO actors(id,name,created_at) VALUES('a','actor','2026-10-13T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Write(context.Background(), func(tx port.TxScope) error {
		ctx := tx.Context()
		now := time.Now().UTC()
		if e := tx.ProjectWrites().Create(ctx, domain.Project{ID: c.ProjectID, Name: "test", ReviewPolicyMode: "trusted", TitleMinLength: 1, TitleMaxLength: 200, CreatedAt: now}); e != nil {
			return e
		}
		if e := tx.AccessWrites().AssignRole(ctx, c.ProjectID, "a", domain.RoleOwner, now); e != nil {
			return e
		}
		if e := tx.SessionWrites().Create(ctx, c.ProjectID, domain.Session{ID: "s", ActorID: "a", ContextID: "test", StartedAt: now, LastActivity: now}); e != nil {
			return e
		}
		return tx.IssueWrites().Save(ctx, c.ProjectID, domain.Issue{ID: "td-one", Title: "Original issue", Revision: 1, ContentVersion: 1, Status: domain.StatusOpen, Type: domain.TypeTask, Priority: domain.PriorityP2, CreatorSession: "s", CreatedAt: now, UpdatedAt: now}, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func testRunner(s *Store) operation.Runner {
	var next atomic.Int64
	return operation.Runner{Writes: s, Encoder: testReceiptEncoder{}, Now: time.Now, ID: func(string) (string, error) { return fmt.Sprintf("operation-%d", next.Add(1)), nil }}
}
func testMutation() port.Mutation {
	return port.Mutation{Key: "11111111-1111-4111-8111-111111111111", Fingerprint: []byte("observed revision one"), Method: "PATCH", Path: "/issue"}
}
func editIssue(c domain.Caller, calls *atomic.Int64) func(operation.CommandContext) (any, error) {
	return func(command operation.CommandContext) (any, error) {
		calls.Add(1)
		tx := command.Tx
		ctx := tx.Context()
		issue, e := tx.Issues().Get(ctx, c.ProjectID, "td-one", false)
		if e != nil {
			return nil, e
		}
		if issue.Revision != 1 {
			return nil, domain.ErrStale
		}
		issue.Revision = 2
		issue.Title = "Updated issue"
		issue.UpdatedAt = command.Now
		if e = tx.IssueWrites().Save(ctx, c.ProjectID, *issue, 1); e != nil {
			return nil, e
		}
		e = tx.OperationWrites().SaveEffect(ctx, port.Effect{ProjectID: c.ProjectID, OperationRowID: command.RowID, EntityKind: "issue", EntityID: issue.ID, AfterRevision: "r-2", StateVersion: 1, BeforeJSON: []byte(`{"revision":1}`), AfterJSON: []byte(`{"revision":2}`)})
		return issue, e
	}
}
func assertOperationState(t *testing.T, s *Store, c domain.Caller, revision, changes, receipts, effects int) {
	t.Helper()
	for _, check := range []struct {
		query string
		want  int
	}{
		{"SELECT revision FROM issues WHERE project_id=?", revision},
		{"SELECT change_seq FROM projects WHERE id=?", changes},
		{"SELECT count(*) FROM operations WHERE project_id=?", receipts},
		{"SELECT count(*) FROM operation_effects WHERE project_id=?", effects},
	} {
		var got int
		if e := s.writer.QueryRow(check.query, c.ProjectID).Scan(&got); e != nil || got != check.want {
			t.Fatalf("%s got %d want %d: %v", check.query, got, check.want, e)
		}
	}
}
func TestProjectCommandConcurrentReplayAndRestart(t *testing.T) {
	s, path := openTest(t)
	c := seedOperation(t, s)
	runner := testRunner(s)
	m := testMutation()
	var calls atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reply, e := runner.Run(context.Background(), c, m, "issue.update", editIssue(c, &calls))
			if e != nil || reply.Status != 200 || reply.Headers["ETag"] != `"r-2"` || string(reply.Body) != "original response\n" {
				t.Errorf("reply %#v: %v", reply, e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("effects ran %d times", calls.Load())
	}
	assertOperationState(t, s, c, 2, 1, 1, 1)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(context.Background(), path, Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	runner.Writes = reopened
	reply, e := runner.Run(context.Background(), c, m, "issue.update", editIssue(c, &calls))
	if e != nil || string(reply.Body) != "original response\n" || calls.Load() != 1 {
		t.Fatalf("restart replay %#v %v", reply, e)
	}
	m.Fingerprint = []byte("different observed revision")
	if _, e = runner.Run(context.Background(), c, m, "issue.update", editIssue(c, &calls)); !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	m = testMutation()
	m.Key = "22222222-2222-4222-8222-222222222222"
	if _, e = runner.Run(context.Background(), c, m, "issue.update", editIssue(c, &calls)); !errors.Is(e, domain.ErrStale) {
		t.Fatal(e)
	}
	if _, e = reopened.writer.Exec("DELETE FROM project_memberships WHERE project_id=?", c.ProjectID); e != nil {
		t.Fatal(e)
	}
	if _, e = runner.Run(context.Background(), c, testMutation(), "issue.update", editIssue(c, &calls)); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal(e)
	}
	assertOperationState(t, reopened, c, 2, 1, 1, 1)
}
func TestProjectCommandRollsBackEffectsAndCounter(t *testing.T) {
	for _, mode := range []string{"encoder", "receipt", "commit"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := openTest(t)
			c := seedOperation(t, s)
			runner := testRunner(s)
			var calls atomic.Int64
			apply := editIssue(c, &calls)
			switch mode {
			case "encoder":
				runner.Encoder = testReceiptEncoder{fail: true}
			case "receipt":
				if _, e := s.writer.Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON operations BEGIN SELECT RAISE(ABORT,'receipt rejected'); END`); e != nil {
					t.Fatal(e)
				}
			case "commit":
				base := apply
				apply = func(command operation.CommandContext) (any, error) {
					outcome, e := base(command)
					if e != nil {
						return nil, e
					}
					e = command.Tx.OperationWrites().SaveEffect(command.Tx.Context(), port.Effect{ProjectID: c.ProjectID, OperationRowID: "missing-receipt", EntityKind: "issue", EntityID: "td-two", AfterRevision: "r-1", StateVersion: 1, AfterJSON: []byte(`{}`)})
					return outcome, e
				}
			}
			reply, e := runner.Run(context.Background(), c, testMutation(), "issue.update", apply)
			if e == nil || reply.Status != 0 || len(reply.Body) != 0 {
				t.Fatalf("failed transaction exposed reply %#v %v", reply, e)
			}
			assertOperationState(t, s, c, 1, 0, 0, 0)
		})
	}
}
