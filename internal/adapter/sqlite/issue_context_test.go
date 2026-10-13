package sqlite

import (
	"context"
	"github.com/yoophi/td-backend/internal/application/port"
	"sync/atomic"
	"testing"
)

func TestIssueContextReadsPersistedActivitiesFilesAndReviews(t *testing.T) {
	s, _ := openTest(t)
	c := seedOperation(t, s)
	runner := testRunner(s)
	var calls atomic.Int64
	if _, e := runner.Run(context.Background(), c, testMutation(), "issue.update", editIssue(c, &calls)); e != nil {
		t.Fatal(e)
	}
	for _, record := range []struct{ id, kind, message, logType, payload string }{
		{"log-one", "log", "progress message", "progress", `{}`},
		{"comment-one", "comment", "comment text", "", `{}`},
		{"handoff-one", "handoff", "", "", `{"done":["done"],"remaining":["next"]}`},
		{"handoff-two", "handoff", "", "", `{"done":["latest"],"uncertain":[]}`},
	} {
		_, e := s.writer.Exec(`INSERT INTO activities(project_id,id,issue_id,session_id,operation_row_id,kind,message,log_type,content_version,payload_json,created_at,updated_at) VALUES(?,?,'td-one','s','operation-1',?,?,NULLIF(?,''),1,?,'2026-10-13T00:00:00Z','2026-10-13T00:00:00Z')`, c.ProjectID, record.id, record.kind, record.message, record.logType, record.payload)
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.writer.Exec(`INSERT INTO issue_files(project_id,id,issue_id,file_path,role,linked_at) VALUES(?,'file-one','td-one','src/main.go','implementation','2026-10-13T00:00:00Z')`, c.ProjectID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.writer.Exec(`INSERT INTO issue_reviews(project_id,id,issue_id,reviewer_session,decision,summary,self_review,basis_content_version,operation_row_id,created_at) VALUES(?,'review-one','td-one','s','approved','reviewed',1,1,'operation-1','2026-10-13T00:00:00Z')`, c.ProjectID); e != nil {
		t.Fatal(e)
	}
	if e := s.Snapshot(context.Background(), func(read port.ReadSnapshot) error {
		got, e := read.Issues().Context(read.Context(), c.ProjectID, "td-one", true)
		if e != nil {
			return e
		}
		if len(got.Logs) != 1 || got.Logs[0].Message != "progress message" || len(got.Comments) != 1 || got.Comments[0].Text != "comment text" || got.LatestHandoff == nil || got.LatestHandoff.ID != "handoff-two" || got.LatestHandoff.Done[0] != "latest" || len(got.Files) != 1 || got.Files[0].FilePath != "src/main.go" || len(got.Reviews) != 1 || !got.Reviews[0].SelfReview {
			t.Fatalf("context %+v", got)
		}
		other, e := read.Issues().Context(read.Context(), "other-project", "td-one", true)
		if e != nil {
			return e
		}
		if len(other.Logs)+len(other.Comments)+len(other.Files)+len(other.Reviews) != 0 || other.LatestHandoff != nil {
			t.Fatal("cross-project leak")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
