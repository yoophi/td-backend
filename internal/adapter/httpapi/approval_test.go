package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/domain"
)

func reviewFixture(t *testing.T, mode string) (*harness, string, string, string) {
	t.Helper()
	h := newHarness(t)
	status, data := h.request("POST", "/v1/projects", testToken, key(t), "", fmt.Sprintf(`{"name":"Approval fixture","review_policy_mode":%q}`, mode))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	p := projectFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", `{"context_id":"implementer"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	session := sessionFrom(t, data).ID
	base := "/v1/projects/" + p + "/issues"
	status, _, data = issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Approval integration fixture"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	path := base + "/" + issueFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", path+"/start", session, key(t), `"r-1"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/handoffs", session, key(t), `"r-2"`, `{"done":["implemented"]}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	return h, p, session, path
}
func detailReviews(t *testing.T, h *harness, path, session string) receiptcodec.IssueDTO {
	t.Helper()
	status, _, data := issueRequest(t, h, "GET", path+"?with=reviews", session, "", "", "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	return issueFrom(t, data)
}
func TestTrustedApprovalAcknowledgementAndReopen(t *testing.T) {
	h, _, session, path := reviewFixture(t, "trusted")
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{}`, 403}, {`{"force":true}`, 403}, {`{"self_review":true}`, 400}, {`{"reviewed_by":" "}`, 400}, {`{"self_review":true,"reviewed_by":"peer","reason":"test"}`, 400}, {fmt.Sprintf(`{"reviewed_by":%q}`, strings.Repeat("a", 121)), 400},
	} {
		status, _, data := issueRequest(t, h, "POST", path+"/approve", session, key(t), `"r-3"`, tc.body)
		if status != tc.status {
			t.Fatal(tc, status, string(data))
		}
	}
	op := key(t)
	body := `{"self_review":true,"reason":"Verified integration tests"}`
	status, etag, first := issueRequest(t, h, "POST", path+"/approve", session, op, `"r-3"`, body)
	if status != 200 || etag != `"r-4"` || issueFrom(t, first).Status != domain.StatusClosed {
		t.Fatal(status, etag, string(first))
	}
	status, replayTag, data := issueRequest(t, h, "POST", path+"/approve", session, op, `"r-3"`, body)
	if status != 200 || etag != replayTag || string(data) != string(first) {
		t.Fatal("approval replay changed")
	}
	issue := detailReviews(t, h, path, session)
	if issue.Reviews == nil || len(*issue.Reviews) != 1 || !(*issue.Reviews)[0].SelfReview || issue.ReviewerSession == nil || *issue.ReviewerSession != session || issue.ClosedBySession == nil || *issue.ClosedBySession != session {
		t.Fatalf("approval %+v", issue)
	}
	status, etag, data = issueRequest(t, h, "POST", path+"/reopen", session, key(t), `"r-4"`, `{}`)
	if status != 200 || etag != `"r-5"` {
		t.Fatal(status, etag, string(data))
	}
	issue = detailReviews(t, h, path, session)
	if issue.Status != domain.StatusOpen || issue.ClosedAt != nil || issue.ReviewedAt != nil || (*issue.Reviews)[0].SupersededAt == nil {
		t.Fatalf("reopen %+v", issue)
	}
}
func TestDelegatedRecordOnlyAndCloseByImplementer(t *testing.T) {
	h, p, implementer, path := reviewFixture(t, "delegated")
	for _, body := range []string{`{"self_review":true,"reason":"tests"}`, `{"reviewed_by":"peer"}`, `{"force":true}`} {
		status, _, data := issueRequest(t, h, "POST", path+"/approve", implementer, key(t), `"r-3"`, body)
		if status != 403 {
			t.Fatal(status, string(data))
		}
	}
	status, data := h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", `{"context_id":"independent-reviewer"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	reviewer := sessionFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", path+"/approve", reviewer, key(t), `"r-3"`, `{"record_only":true}`)
	if status != 400 {
		t.Fatal(status, string(data))
	}
	status, etag, data := issueRequest(t, h, "POST", path+"/approve", reviewer, key(t), `"r-3"`, `{"record_only":true,"reason":"Independently verified"}`)
	if status != 200 || etag != `"r-4"` || issueFrom(t, data).Status != domain.StatusInReview {
		t.Fatal(status, etag, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/close", implementer, key(t), `"r-4"`, `{}`)
	if status != 400 {
		t.Fatal(status, string(data))
	}
	status, etag, data = issueRequest(t, h, "POST", path+"/close", implementer, key(t), `"r-4"`, `{"reason":"Closing with recorded approval"}`)
	if status != 200 || etag != `"r-5"` {
		t.Fatal(status, etag, string(data))
	}
	issue := detailReviews(t, h, path, implementer)
	if issue.Status != domain.StatusClosed || issue.ReviewerSession == nil || *issue.ReviewerSession != reviewer || issue.ClosedBySession == nil || *issue.ClosedBySession != implementer || len(*issue.Reviews) != 1 || (*issue.Reviews)[0].SelfReview {
		t.Fatalf("close attribution %+v", issue)
	}
}
func TestTrustedAttributedReviewAndRejectInvalidation(t *testing.T) {
	h, _, session, path := reviewFixture(t, "trusted")
	status, _, data := issueRequest(t, h, "POST", path+"/approve", session, key(t), `"r-3"`, `{"record_only":true,"reason":"Peer reviewed","reviewed_by":"Review agent"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	issue := detailReviews(t, h, path, session)
	if !(*issue.Reviews)[0].SelfReview || (*issue.Reviews)[0].ReviewedBy != "Review agent" {
		t.Fatal("attribution lost")
	}
	status, etag, data := issueRequest(t, h, "POST", path+"/reject", session, key(t), `"r-4"`, `{"reason":"Additional changes required"}`)
	if status != 200 || etag != `"r-5"` {
		t.Fatal(status, etag, string(data))
	}
	issue = detailReviews(t, h, path, session)
	if issue.Status != domain.StatusOpen || issue.ImplementerSession != nil || issue.ReviewerSession != nil || (*issue.Reviews)[0].SupersededAt == nil {
		t.Fatalf("rejection %+v", issue)
	}
	// Audit JSON retains the recorded review even after rejection.
	encoded, e := json.Marshal(issue)
	if e != nil || !strings.Contains(string(encoded), "Review agent") {
		t.Fatal(e, string(encoded))
	}
}

func TestApprovalReceiptFailureRollsBackReviewAndClose(t *testing.T) {
	h, _, session, path := reviewFixture(t, "trusted")
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`CREATE TRIGGER reject_approval_receipt BEFORE INSERT ON operations BEGIN SELECT RAISE(ABORT,'receipt failed'); END`); e != nil {
		t.Fatal(e)
	}
	op := key(t)
	body := `{"self_review":true,"reason":"Verified"}`
	status, _, data := issueRequest(t, h, "POST", path+"/approve", session, op, `"r-3"`, body)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	issue := detailReviews(t, h, path, session)
	if issue.Revision != "r-3" || issue.Status != domain.StatusInReview || issue.ClosedAt != nil || issue.ReviewerSession != nil || len(*issue.Reviews) != 0 {
		t.Fatalf("partial approval %+v", issue)
	}
	var count int
	if e = inspect.QueryRow(`SELECT count(*) FROM issue_session_history WHERE action IN ('review_approved','closed')`).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial history", count, e)
	}
	if _, e = inspect.Exec(`DROP TRIGGER reject_approval_receipt`); e != nil {
		t.Fatal(e)
	}
	status, _, data = issueRequest(t, h, "POST", path+"/approve", session, op, `"r-3"`, body)
	if status != 200 {
		t.Fatal(status, string(data))
	}
}
