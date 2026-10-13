package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yoophi/td-backend/internal/adapter/sqlite"
	"github.com/yoophi/td-backend/internal/adapter/tokenauth"
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/operation"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestIssueDetailRelatedRecordsAndArchive(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Parent issue","description":"complete description"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	parent := issueFrom(t, data)
	status, _, data = issueRequest(t, h, "POST", base, session, key(t), "", fmt.Sprintf(`{"title":"Child issue","parent_id":%q,"depends_on":[%q]}`, parent.ID, parent.ID))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	child := issueFrom(t, data)
	status, etag, data := issueRequest(t, h, "GET", base+"/"+parent.ID+"?with=reviews", session, "", "", "")
	if status != 200 || etag != `"r-1"` {
		t.Fatal(status, etag, string(data))
	}
	var response struct {
		Data receiptcodec.IssueDetailDTO `json:"data"`
	}
	if e := json.Unmarshal(data, &response); e != nil {
		t.Fatal(e)
	}
	d := response.Data
	if len(d.Children) != 1 || d.Children[0].ID != child.ID || len(d.BlockedBy) != 1 || d.BlockedBy[0].IssueID != child.ID || d.BlockedBy[0].DependsOnID != parent.ID || d.Issue.Description != "complete description" {
		t.Fatalf("detail %+v", d)
	}
	if d.Logs == nil || d.Comments == nil || d.Dependencies == nil || d.Files == nil || d.Issue.Reviews == nil || *d.Issue.Reviews == nil || d.LatestHandoff != nil {
		t.Fatalf("incorrect empty values: %s", data)
	}
	for _, query := range []string{"?with=unknown", "?with_deleted=1", "?with_deleted=true&with_deleted=false", "?unknown=value"} {
		status, _, data = issueRequest(t, h, "GET", base+"/"+parent.ID+query, session, "", "", "")
		if status != 400 {
			t.Fatal(query, status, string(data))
		}
	}
	status, _, data = issueRequest(t, h, "DELETE", base+"/"+parent.ID, session, key(t), `"r-1"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", base+"/"+parent.ID, session, "", "", "")
	if status != 404 {
		t.Fatal(status, string(data))
	}
	status, etag, data = issueRequest(t, h, "GET", base+"/"+parent.ID+"?with_deleted=true", session, "", "", "")
	if status != 200 || etag != `"r-2"` {
		t.Fatal(status, etag, string(data))
	}
}

func TestIssueListPagesFiltersAndChangeToken(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	ids := []string{}
	for _, body := range []string{`{"title":"Alpha issue","description":"search text","acceptance":"criterion","priority":"P1","labels":["one","two"]}`, `{"title":"Beta issue","priority":"P2","labels":["one"]}`, `{"title":"Gamma issue","priority":"P1"}`} {
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", body)
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	type page struct {
		Issues               []receiptcodec.IssueDTO `json:"issues"`
		Total, Limit, Offset int
		HasMore              bool   `json:"has_more"`
		ChangeToken          string `json:"change_token"`
	}
	get := func(query string) page {
		t.Helper()
		status, _, data := issueRequest(t, h, "GET", base+query, session, "", "", "")
		if status != 200 {
			t.Fatal(status, string(data))
		}
		var out struct{ Data page }
		if e := json.Unmarshal(data, &out); e != nil {
			t.Fatal(e)
		}
		return out.Data
	}
	first := get("?limit=1&sort=title")
	second := get("?limit=1&offset=1&sort=title")
	if first.Total != 3 || first.Issues[0].ID != ids[0] || !first.HasMore || second.Issues[0].ID != ids[1] || second.ChangeToken != first.ChangeToken || first.Issues[0].Description != "search text" || first.Issues[0].Acceptance != "criterion" {
		t.Fatalf("pages %+v %+v", first, second)
	}
	filtered := get("?labels=one&labels=two&priority=P1")
	if filtered.Total != 1 || filtered.Issues[0].ID != ids[0] {
		t.Fatalf("filter %+v", filtered)
	}
	searched := get("?search=criterion&search_mode=text")
	if searched.Total != 1 {
		t.Fatalf("search %+v", searched)
	}
	empty := get("?offset=9223372036854775807")
	if empty.Issues == nil || len(empty.Issues) != 0 || empty.HasMore || empty.Total != 3 {
		t.Fatalf("empty page %+v", empty)
	}
	status, _, data := issueRequest(t, h, "DELETE", base+"/"+ids[2], session, key(t), `"r-1"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	after := get("")
	if after.Total != 2 || after.ChangeToken == first.ChangeToken {
		t.Fatalf("changed page %+v", after)
	}
	if get("?with_deleted=true").Total != 3 {
		t.Fatal("archive missing")
	}
	for _, query := range []string{"?limit=0", "?limit=1001", "?offset=-1", "?status=unknown", "?sort=invalid", "?order=invalid", "?include_closed=1", "?limit=1&limit=2", "?unknown=1"} {
		status, _, data = issueRequest(t, h, "GET", base+query, session, "", "", "")
		if status != 400 {
			t.Fatal(query, status, string(data))
		}
	}
}

func TestIssueListStatusDefaultsAndProjectIsolation(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	for _, status := range []domain.Status{domain.StatusOpen, domain.StatusClosed, domain.StatusBlocked, domain.StatusInReview} {
		code, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Status fixture"}`)
		if code != 201 {
			t.Fatal(code, string(data))
		}
		id := issueFrom(t, data).ID
		e := h.store.Write(context.Background(), func(tx port.TxScope) error {
			i, e := tx.Issues().Get(tx.Context(), p, id, false)
			if e != nil {
				return e
			}
			i.Status = status
			i.Revision++
			return tx.IssueWrites().Save(tx.Context(), p, *i, 1)
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		query string
		total int
	}{{"", 3}, {"?include_closed=true", 4}, {"?status=closed", 1}, {"?status=blocked&status=in_review", 2}} {
		status, _, data := issueRequest(t, h, "GET", base+tc.query, session, "", "", "")
		if status != 200 {
			t.Fatal(status, string(data))
		}
		var result struct{ Data struct{ Total int } }
		if e := json.Unmarshal(data, &result); e != nil || result.Data.Total != tc.total {
			t.Fatal(tc, string(data), e)
		}
	}
	code, data := h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Other"}`)
	if code != 201 {
		t.Fatal(code, string(data))
	}
	other := projectFrom(t, data)
	code, data = h.request("POST", "/v1/projects/"+other.ID+"/sessions", testToken, key(t), "", `{"context_id":"other"}`)
	if code != 201 {
		t.Fatal(code, string(data))
	}
	otherSession := sessionFrom(t, data)
	code, _, data = issueRequest(t, h, "GET", "/v1/projects/"+other.ID+"/issues", otherSession.ID, "", "", "")
	if code != 200 || !strings.Contains(string(data), `"total":0`) {
		t.Fatal(code, string(data))
	}
	code, _, data = issueRequest(t, h, "GET", base, otherSession.ID, "", "", "")
	if code != 404 {
		t.Fatal(code, string(data))
	}
}

func TestLostIssueReplyReplaysAfterServerRestart(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Original before restart"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	id := issueFrom(t, data).ID
	op := key(t)
	path := base + "/" + id
	body := `{"title":"Updated before restart"}`
	request, e := http.NewRequest("PATCH", h.server.URL+path, strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+testToken)
	request.Header.Set("X-TD-Session", session)
	request.Header.Set("Idempotency-Key", op)
	request.Header.Set("If-Match", `"r-1"`)
	request.Header.Set("Content-Type", "application/json")
	response, e := h.server.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close() // discard the committed response, as after a transport loss
	var original port.StoredReply
	e = h.store.Snapshot(context.Background(), func(read port.ReadSnapshot) error {
		receipt, e := read.Operations().Get(read.Context(), "act-bootstrap", p, op)
		original = receipt.Reply
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	h.server.Close()
	if e = h.store.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := sqlite.Open(context.Background(), h.path, sqlite.Options{})
	if e != nil {
		t.Fatal(e)
	}
	h.store = reopened
	h.auth = tokenauth.New(reopened, reopened)
	h.projects.Reads = reopened
	h.projects.Writes = reopened
	svc := &issue.Service{Reads: reopened, ID: h.projects.ID, Runner: operation.Runner{Writes: reopened, Encoder: receiptcodec.Codec{}, ID: h.projects.ID, Now: time.Now}}
	h.server = httptest.NewServer(New(Options{Projects: h.projects, Auth: h.auth, Issues: svc}))
	status, etag, data := issueRequest(t, h, "PATCH", path, session, op, `"r-1"`, body)
	if status != original.Status || etag != original.Headers["ETag"] || string(data) != string(original.Body) {
		t.Fatalf("restart replay %d %s %s", status, etag, data)
	}
	status, etag, data = issueRequest(t, h, "GET", path, session, "", "", "")
	if status != 200 || etag != `"r-2"` {
		t.Fatal("duplicate effect", status, etag, string(data))
	}
}
