package sqlite

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func TestPatchReviewFreshnessMatchesTDPolicy(t *testing.T) {
	title, description, acceptance, sprint, date := "Changed title", "changed description", "changed acceptance", "new sprint", "2027-01-01"
	labels := []string{"new"}
	points := 5
	minor := true
	priority := domain.PriorityP1
	kind := domain.TypeBug
	for _, tc := range []struct {
		name       string
		cmd        issue.PatchCommand
		invalidate bool
	}{
		{"title", issue.PatchCommand{Title: &title}, true},
		{"description", issue.PatchCommand{Description: &description}, true},
		{"priority", issue.PatchCommand{Priority: &priority}, true},
		{"type", issue.PatchCommand{Type: &kind}, true},
		{"minor", issue.PatchCommand{Minor: &minor}, true},
		{"labels", issue.PatchCommand{Labels: &labels}, false},
		{"points", issue.PatchCommand{Points: &points}, false},
		{"due date", issue.PatchCommand{DueDate: &date}, false},
		{"defer date", issue.PatchCommand{DeferUntil: &date}, false},
		{"sprint", issue.PatchCommand{Sprint: &sprint}, false},
		{"acceptance upstream parity", issue.PatchCommand{Acceptance: &acceptance}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTest(t)
			c := seedOperation(t, s)
			var calls atomic.Int64
			if _, e := testRunner(s).Run(context.Background(), c, testMutation(), "issue.update", editIssue(c, &calls)); e != nil {
				t.Fatal(e)
			}
			if _, e := s.writer.Exec(`UPDATE issues SET reviewer_session='s',reviewed_at='2026-10-13T00:00:00Z' WHERE project_id=?`, c.ProjectID); e != nil {
				t.Fatal(e)
			}
			if _, e := s.writer.Exec(`INSERT INTO issue_reviews(project_id,id,issue_id,reviewer_session,decision,basis_content_version,operation_row_id,created_at) VALUES(?,'review','td-one','s','approved',1,'operation-1','2026-10-13T00:00:00Z')`, c.ProjectID); e != nil {
				t.Fatal(e)
			}
			id := func(string) (string, error) { return "new-operation", nil }
			service := issue.Service{Reads: s, ID: id, Runner: operation.Runner{Writes: s, Encoder: receiptcodec.Codec{}, ID: id, Now: time.Now}}
			mutation := testMutation()
			mutation.Key = "22222222-2222-4222-8222-222222222222"
			if _, e := service.Patch(context.Background(), c, mutation, "td-one", `"r-2"`, tc.cmd); e != nil {
				t.Fatal(e)
			}
			if e := s.Snapshot(context.Background(), func(read port.ReadSnapshot) error {
				i, e := read.Issues().Get(read.Context(), c.ProjectID, "td-one", false)
				if e != nil {
					return e
				}
				related, e := read.Issues().Context(read.Context(), c.ProjectID, "td-one", true)
				if e != nil {
					return e
				}
				expectedVersion := int64(1)
				if tc.invalidate {
					expectedVersion = 2
				}
				if i.Revision != 3 || i.ContentVersion != expectedVersion || len(related.Reviews) != 1 || (related.Reviews[0].SupersededAt != nil) != tc.invalidate {
					t.Fatalf("issue %+v reviews %+v", i, related.Reviews)
				}
				if tc.invalidate && (i.ReviewerSession != "" || i.ReviewedAt != nil) {
					t.Fatal("stale review attribution retained")
				}
				if !tc.invalidate && (i.ReviewerSession != "s" || i.ReviewedAt == nil) {
					t.Fatal("metadata edit lost approval")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
