package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/domain"
)

func TestRecordReviewChangesRequestedAndDuplicateApproval(t *testing.T) {
	h, _, session, path := reviewFixture(t, "trusted")
	for _, body := range []string{`{"summary":"missing decision"}`, `{"decision":"approved","summary":" "}`, `{"decision":"approved","summary":"test","reviewed_by":"line\nbreak"}`} {
		status, _, data := issueRequest(t, h, "POST", path+"/reviews", session, key(t), `"r-3"`, body)
		if status != 400 {
			t.Fatal(body, status, string(data))
		}
	}
	op := key(t)
	body := `{"decision":"approved","summary":"Verified tests","self_review":true}`
	status, etag, first := issueRequest(t, h, "POST", path+"/reviews", session, op, `"r-3"`, body)
	if status != 201 || etag != `"r-4"` || issueFrom(t, first).Status != domain.StatusInReview {
		t.Fatal(status, etag, string(first))
	}
	var result struct {
		Data struct{ Review domain.IssueReview }
	}
	if e := json.Unmarshal(first, &result); e != nil {
		t.Fatal(e)
	}
	if result.Data.Review.ID == "" || !result.Data.Review.SelfReview || result.Data.Review.Decision != "approved" {
		t.Fatal(string(first))
	}
	status, replayTag, data := issueRequest(t, h, "POST", path+"/reviews", session, op, `"r-3"`, body)
	if status != 201 || replayTag != etag || string(data) != string(first) {
		t.Fatal("review replay changed")
	}
	for _, endpoint := range []string{"/reviews", "/approve"} {
		duplicate := body
		if endpoint == "/approve" {
			duplicate = `{"record_only":true,"reason":"Duplicate approval","self_review":true}`
		}
		status, _, data = issueRequest(t, h, "POST", path+endpoint, session, key(t), `"r-4"`, duplicate)
		if status != 409 {
			t.Fatal(endpoint, status, string(data))
		}
	}
	status, etag, data = issueRequest(t, h, "POST", path+"/reviews", session, key(t), `"r-4"`, `{"decision":"changes_requested","summary":"Found a regression","self_review":true}`)
	if status != 201 || etag != `"r-5"` {
		t.Fatal(status, etag, string(data))
	}
	updated := detailReviews(t, h, path, session)
	if updated.Status != domain.StatusInReview || updated.ReviewerSession != nil || len(*updated.Reviews) != 2 || (*updated.Reviews)[0].SupersededAt == nil || (*updated.Reviews)[1].Decision != "changes_requested" {
		t.Fatalf("review state %+v", updated)
	}
	status, _, data = issueRequest(t, h, "POST", path+"/close", session, key(t), `"r-5"`, `{"reason":"Try closing without approval"}`)
	if status != 403 {
		t.Fatal(status, string(data))
	}
}
func TestRecordReviewModes(t *testing.T) {
	for _, mode := range []string{"strict", "balanced"} {
		t.Run(mode, func(t *testing.T) {
			h, p, _, path := reviewFixture(t, mode)
			status, data := h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", `{"context_id":"reviewer"}`)
			if status != 201 {
				t.Fatal(status, string(data))
			}
			reviewer := sessionFrom(t, data).ID
			status, _, data = issueRequest(t, h, "POST", path+"/reviews", reviewer, key(t), `"r-3"`, `{"decision":"approved","summary":"Independent check"}`)
			if status != 409 {
				t.Fatal(status, string(data))
			}
		})
	}
}
func TestExplicitStatusUsesWorkflowAndReviewPolicy(t *testing.T) {
	h, p, implementer, path := reviewFixture(t, "delegated")
	status, _, data := issueRequest(t, h, "POST", path+"/status", implementer, key(t), `"r-3"`, `{"status":"closed","force":true}`)
	if status != 403 {
		t.Fatal("status bypassed policy", status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/status", implementer, key(t), `"r-3"`, `{"status":"blocked"}`)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/status", implementer, key(t), `"r-3"`, `{"status":"unknown"}`)
	if status != 400 {
		t.Fatal(status, string(data))
	}
	status, etag, data := issueRequest(t, h, "POST", path+"/status", implementer, key(t), `"r-3"`, `{"status":"in_review"}`)
	if status != 200 || etag != `"r-3"` || !strings.Contains(string(data), `"noop":true`) {
		t.Fatal(status, etag, string(data))
	}
	status, data = h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", `{"context_id":"reviewer"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	reviewer := sessionFrom(t, data).ID
	status, etag, data = issueRequest(t, h, "POST", path+"/status", reviewer, key(t), `"r-3"`, `{"status":"closed","reason":"Independently verified"}`)
	if status != 200 || etag != `"r-4"` || issueFrom(t, data).Status != domain.StatusClosed {
		t.Fatal(status, etag, string(data))
	}
	status, etag, data = issueRequest(t, h, "POST", path+"/status", implementer, key(t), `"r-4"`, `{"status":"open"}`)
	if status != 200 || etag != `"r-5"` || issueFrom(t, data).Status != domain.StatusOpen {
		t.Fatal(status, etag, string(data))
	}
}
