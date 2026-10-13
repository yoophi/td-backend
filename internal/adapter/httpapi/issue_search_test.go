package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/tdq"
)

type searchPage struct {
	Issues      []receiptcodec.IssueDTO `json:"issues"`
	Total       int                     `json:"total"`
	ChangeToken string                  `json:"change_token"`
	HasMore     bool                    `json:"has_more"`
}

func TestIssueSearchTDQTextAutoAndDeletedFacts(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	ids := []string{}
	for _, body := range []string{`{"title":"Alpha snapshot issue","description":"retain full detail","acceptance":"accept this criterion","priority":"P1","type":"bug","labels":["api"],"minor":true}`, `{"title":"Beta snapshot issue","priority":"P2","type":"bug","labels":["api"],"minor":true}`, `{"title":"Gamma snapshot issue","priority":"P1","type":"task","minor":true}`} {
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", body)
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	get := func(search, mode, extra string) searchPage {
		t.Helper()
		status, _, data := issueRequest(t, h, "GET", base+"?search="+url.QueryEscape(search)+"&search_mode="+mode+extra, session, "", "", "")
		if status != 200 {
			t.Fatal(status, string(data))
		}
		var out struct{ Data searchPage }
		if e := json.Unmarshal(data, &out); e != nil {
			t.Fatal(e)
		}
		return out.Data
	}
	first := get("type = bug sort:title", "tdq", "&limit=1")
	second := get("type = bug sort:title", "auto", "&limit=1&offset=1")
	if first.Total != 2 || !first.HasMore || first.Issues[0].ID != ids[0] || second.Issues[0].ID != ids[1] || first.ChangeToken != second.ChangeToken || first.Issues[0].Description != "retain full detail" || first.Issues[0].Acceptance != "accept this criterion" {
		t.Fatal(first, second)
	}
	if got := get("type = bug", "tdq", "&priority=P1&labels=api&type=bug"); got.Total != 1 || got.Issues[0].ID != ids[0] {
		t.Fatal(got)
	}
	if got := get("sort:title", "tdq", "&sort=title&order=desc"); got.Total != 3 || got.Issues[0].ID != ids[2] {
		t.Fatal(got)
	}
	if got := get("sort:-title", "tdq", ""); got.Total != 3 || got.Issues[0].ID != ids[2] {
		t.Fatal(got)
	}
	if got := get("snapshot issue", "auto", ""); got.Total != 3 {
		t.Fatal("text fallback", got)
	}
	if got := get("type = bug", "text", ""); got.Total != 0 {
		t.Fatal("forced text", got)
	}
	if got := get("", "tdq", ""); got.Total != 3 {
		t.Fatal("empty search", got)
	}
	for _, q := range []string{"?search_mode=unknown", "?search_mode=tdq&search=" + url.QueryEscape("type =")} {
		status, _, data := issueRequest(t, h, "GET", base+q, session, "", "", "")
		if status != 400 {
			t.Fatal(status, string(data))
		}
	}
	status, _, data := issueRequest(t, h, "POST", base+"/"+ids[0]+"/close", session, key(t), `"r-1"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if got := get("type = bug", "tdq", ""); got.Total != 1 {
		t.Fatal("default closed exclusion", got)
	}
	if got := get("type = bug", "tdq", "&status=closed"); got.Total != 1 || got.Issues[0].ID != ids[0] {
		t.Fatal("explicit closed", got)
	}
	if got := get("type = bug", "tdq", "&include_closed=true"); got.Total != 2 {
		t.Fatal(got)
	}
	status, _, data = issueRequest(t, h, "POST", base+"/"+ids[1]+"/comments", session, key(t), `"r-1"`, `{"text":"preserved evidence"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "DELETE", base+"/"+ids[1], session, key(t), `"r-2"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if got := get(`comment.text ~ "preserved"`, "tdq", ""); got.Total != 0 {
		t.Fatal(got)
	}
	if got := get(`comment.text ~ "preserved"`, "tdq", "&with_deleted=true"); got.Total != 1 || got.Issues[0].ID != ids[1] || got.Issues[0].DeletedAt == nil {
		t.Fatal("deleted facts", got)
	}
}

// Force a separate WAL writer commit after the list has read its project token
// and before its batch facts read. The list must still observe the old records.
type queryHookStore struct {
	port.ReadStore
	hook func()
}
type queryHookSnapshot struct {
	port.ReadSnapshot
	hook func()
}
type queryHookReader struct {
	port.QueryReader
	hook func()
}

func (s queryHookStore) Snapshot(ctx context.Context, fn func(port.ReadSnapshot) error) error {
	return s.ReadStore.Snapshot(ctx, func(r port.ReadSnapshot) error { return fn(queryHookSnapshot{r, s.hook}) })
}
func (s queryHookSnapshot) Queries() port.QueryReader {
	return queryHookReader{s.ReadSnapshot.Queries(), s.hook}
}
func (r queryHookReader) Facts(ctx context.Context, p string, deleted bool) (tdq.Facts, error) {
	r.hook()
	return r.QueryReader.Facts(ctx, p, deleted)
}
func TestIssueTDQListKeepsRecordsTotalAndTokenSnapshot(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Alpha snapshot consistency"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	id := issueFrom(t, data).ID
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	var seq int64
	if e = inspect.QueryRow(`SELECT change_seq FROM projects WHERE id=?`, p).Scan(&seq); e != nil {
		t.Fatal(e)
	}
	called := false
	svc := issue.Service{Reads: queryHookStore{h.store, func() {
		if called {
			t.Fatal("facts hook called twice")
		}
		called = true
		tx, e := inspect.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if _, e = tx.Exec(`UPDATE issues SET title='Beta snapshot consistency',revision=revision+1 WHERE project_id=? AND id=?`, p, id); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(`UPDATE projects SET change_seq=change_seq+1 WHERE id=?`, p); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}}}
	c := domain.Caller{ProjectID: p, SessionID: session, Actor: domain.Actor{ID: "act-bootstrap"}}
	opts := issue.ListOptions{Search: `title ~ "Alpha"`, SearchMode: "tdq", Limit: 1}
	got, e := svc.List(context.Background(), c, opts)
	if e != nil {
		t.Fatal(e)
	}
	if !called || got.Total != 1 || len(got.Issues) != 1 || got.Issues[0].Title != "Alpha snapshot consistency" || got.ChangeToken != fmt.Sprintf("%s:%d", p, seq) {
		t.Fatal(got)
	}
	svc.Reads = h.store
	got, e = svc.List(context.Background(), c, opts)
	if e != nil {
		t.Fatal(e)
	}
	if got.Total != 0 || len(got.Issues) != 0 || got.ChangeToken != fmt.Sprintf("%s:%d", p, seq+1) {
		t.Fatal("fresh snapshot", got)
	}
}
