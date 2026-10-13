package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func issueFrom(t *testing.T, data []byte) receiptcodec.IssueDTO {
	t.Helper()
	var body struct {
		Data struct {
			Issue receiptcodec.IssueDTO `json:"issue"`
		} `json:"data"`
	}
	if e := json.Unmarshal(data, &body); e != nil || body.Data.Issue.ID == "" {
		t.Fatalf("issue response %s: %v", data, e)
	}
	return body.Data.Issue
}
func issueSetup(t *testing.T) (*harness, string, string) {
	h := newHarness(t)
	status, data := h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Issues","title_min_length":3}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	p := projectFrom(t, data)
	status, data = h.request("POST", "/v1/projects/"+p.ID+"/sessions", testToken, key(t), "", `{"context_id":"issue-test"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	session := sessionFrom(t, data)
	return h, p.ID, session.ID
}
func issueRequest(t *testing.T, h *harness, method, path, session, op, revision, body string) (int, string, []byte) {
	t.Helper()
	r, e := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("X-TD-Session", session)
	r.Header.Set("Idempotency-Key", op)
	r.Header.Set("If-Match", revision)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	response, e := h.server.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	data, e := io.ReadAll(response.Body)
	if e != nil {
		t.Fatal(e)
	}
	return response.StatusCode, response.Header.Get("ETag"), data
}
func TestIssueMutationsObservedReplayClearDeleteRestore(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	createKey := key(t)
	status, etag, data := issueRequest(t, h, "POST", base, session, createKey, "", `{"title":"한국어 제목","description":"full description","acceptance":"full acceptance","labels":["b","a","b"],"points":0,"due_date":"2028-02-29","minor":true}`)
	if status != 201 || etag != `"r-1"` {
		t.Fatal(status, etag, string(data))
	}
	created := issueFrom(t, data)
	path := base + "/" + created.ID
	if created.Description != "full description" || created.Acceptance != "full acceptance" || created.ParentID != nil || created.CreatorSession == nil || *created.CreatorSession != session || created.Status != domain.StatusOpen || len(created.Labels) != 2 || created.Labels[0] != "a" {
		t.Fatalf("incomplete issue %+v", created)
	}
	status, replayedETag, replayed := issueRequest(t, h, "POST", base, session, createKey, "", `{ "minor":true,"due_date":"2028-02-29","points":0,"labels":["b","a","b"],"acceptance":"full acceptance","description":"full description","title":"한국어 제목"}`)
	if status != 201 || replayedETag != etag || string(replayed) != string(data) {
		t.Fatal("create replay differs")
	}
	patchKey := key(t)
	patch := `{"description":"","acceptance":"","labels":[],"minor":false,"due_date":"","defer_until":"2026-12-31"}`
	status, _, data = issueRequest(t, h, "PATCH", path, session, key(t), "", patch)
	if status != 428 {
		t.Fatal(status, string(data))
	}
	status, etag, data = issueRequest(t, h, "PATCH", path, session, patchKey, `"r-1"`, patch)
	if status != 200 || etag != `"r-2"` {
		t.Fatal(status, etag, string(data))
	}
	updated := issueFrom(t, data)
	if updated.Description != "" || updated.Acceptance != "" || updated.Labels == nil || len(updated.Labels) != 0 || updated.Minor || updated.DueDate != nil || updated.DeferUntil == nil {
		t.Fatalf("clear failed %+v", updated)
	}
	status, replayedETag, replayed = issueRequest(t, h, "PATCH", path, session, patchKey, `"r-1"`, patch)
	if status != 200 || replayedETag != etag || string(replayed) != string(data) {
		t.Fatal("patch replay differs")
	}
	status, _, data = issueRequest(t, h, "PATCH", path, session, key(t), `"r-1"`, patch)
	if status != 412 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "PATCH", path, session, patchKey, `"r-2"`, patch)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	status, etag, data = issueRequest(t, h, "DELETE", path, session, key(t), `"r-2"`, "")
	if status != 200 || etag != `"r-3"` || !strings.Contains(string(data), `"deleted":true`) {
		t.Fatal(status, etag, string(data))
	}
	if e := h.store.Snapshot(context.Background(), func(read port.ReadSnapshot) error {
		_, e := read.Issues().Get(read.Context(), p, created.ID, false)
		if e != domain.ErrNotFound {
			t.Fatalf("deleted visible: %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	status, etag, data = issueRequest(t, h, "POST", path+"/restore", session, key(t), `"r-3"`, "")
	if status != 200 || etag != `"r-4"` {
		t.Fatal(status, etag, string(data))
	}
	if issueFrom(t, data).DeletedAt != nil {
		t.Fatal("not restored")
	}
}
func TestIssueValidationAndAtomicRelations(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	for _, body := range []string{`{"Title":"case sensitive title"}`, `{"title":"valid","type":""}`, `{"title":"valid","priority":""}`, `{}`, `{"title":"ab"}`, `{"title":"valid","status":"closed"}`, `{"title":"valid","points":4}`, `{"title":"valid","labels":[null]}`, `{"title":"valid","due_date":"2025-02-29"}`, `{"title":"valid","type":"bad"}`, `{"title":"valid","priority":"bad"}`, `{"title":null}`, `{"title":"valid","id":"forged"}`} {
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", body)
		if status != 400 {
			t.Fatalf("%s: %d %s", body, status, data)
		}
	}
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Dependency target"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	target := issueFrom(t, data)
	status, _, data = issueRequest(t, h, "POST", base, session, key(t), "", fmt.Sprintf(`{"title":"Dependent issue","parent_id":%q,"depends_on":[%q,%q]}`, target.ID, target.ID, target.ID))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	dependent := issueFrom(t, data)
	if e := h.store.Snapshot(context.Background(), func(read port.ReadSnapshot) error {
		deps, e := read.Issues().Dependencies(read.Context(), p, dependent.ID)
		if e != nil {
			return e
		}
		if len(deps) != 1 || deps[0].DependsOnID != target.ID {
			t.Fatalf("dependencies %+v", deps)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	status, _, data = issueRequest(t, h, "PATCH", base+"/"+target.ID, session, key(t), `"r-1"`, fmt.Sprintf(`{"parent_id":%q}`, dependent.ID))
	if status != 400 {
		t.Fatal(status, string(data))
	}
	for _, body := range []string{`{}`, `{"labels":null}`, `{"status":"closed"}`, `{"title":"valid","creator_session":"forged"}`} {
		status, _, data = issueRequest(t, h, "PATCH", base+"/"+target.ID, session, key(t), `"r-1"`, body)
		if status != 400 {
			t.Fatal(body, status, string(data))
		}
	}
	status, _, data = issueRequest(t, h, "POST", base, session, key(t), "", fmt.Sprintf(`{"title":"Must roll back","depends_on":[%q,"missing"]}`, target.ID))
	if status != 404 {
		t.Fatal(status, string(data))
	}
	if e := h.store.Snapshot(context.Background(), func(read port.ReadSnapshot) error {
		list, e := read.Issues().List(read.Context(), p, true)
		if len(list) != 2 {
			t.Fatalf("failed command leaked issue: %d", len(list))
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
