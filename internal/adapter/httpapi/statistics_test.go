package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/monitor"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func statsFrom(t *testing.T, h *harness, p, session string) receiptcodec.StatsDTO {
	t.Helper()
	status, _, data := issueRequest(t, h, "GET", "/v1/projects/"+p+"/stats", session, "", "", "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	var out struct{ Data receiptcodec.StatsDTO }
	if e := json.Unmarshal(data, &out); e != nil {
		t.Fatal(e)
	}
	return out.Data
}
func TestStatsLabelsEmptyCountsArchiveAndHighlights(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	empty := statsFrom(t, h, p, session)
	if empty.Total != 0 || empty.TotalLogs != 0 || empty.TotalHandoffs != 0 || empty.AvgPointsPerTask != 0 || empty.OldestOpen != nil || empty.NewestTask != nil || empty.LastClosed != nil || empty.ByStatus == nil {
		t.Fatal(empty)
	}
	ids := []string{}
	for _, body := range []string{`{"title":"Alpha statistics task","points":3,"type":"task","labels":["api","shared"],"description":"full statistics context","minor":true}`, `{"title":"Beta statistics bug","points":5,"type":"bug","labels":["shared","zeta"],"minor":true}`, `{"title":"Gamma archived task","points":2,"labels":["archived-only"],"minor":true}`} {
		status, _, data := issueRequest(t, h, "POST", root+"/issues", session, key(t), "", body)
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	for _, id := range ids[:2] {
		status, _, data := issueRequest(t, h, "POST", root+"/issues/"+id+"/handoffs", session, key(t), `"r-1"`, `{"done":["snapshot metrics"],"submit_review":false}`)
		if status != 201 {
			t.Fatal(status, string(data))
		}
	}
	status, _, data := issueRequest(t, h, "POST", root+"/issues/"+ids[1]+"/close", session, key(t), `"r-2"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "DELETE", root+"/issues/"+ids[2], session, key(t), `"r-1"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for n, id := range ids {
		stamp := now.AddDate(0, 0, -8)
		if n == 1 {
			stamp = now.Add(-time.Hour)
		}
		if _, e = inspect.Exec(`UPDATE issues SET created_at=? WHERE project_id=? AND id=?`, stamp.Format(time.RFC3339Nano), p, id); e != nil {
			t.Fatal(e)
		}
	}
	svc := monitor.Service{Reads: h.store, Now: func() time.Time { return now }}
	c := domain.Caller{ProjectID: p, SessionID: session, Actor: domain.Actor{ID: "act-bootstrap"}}
	got, e := svc.Stats(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	if got.Total != 2 || got.TotalPoints != 8 || got.AvgPointsPerTask != 4 || got.CompletionRate != 0.5 || got.ByStatus["open"] != 1 || got.ByStatus["closed"] != 1 || got.ByType["task"] != 1 || got.ByType["bug"] != 1 || got.CreatedToday != 1 || got.CreatedThisWeek != 1 || got.TotalLogs != 1 || got.TotalHandoffs != 2 || got.MostActiveSession != session {
		t.Fatal(got)
	}
	if got.OldestOpen == nil || got.OldestOpen.ID != ids[0] || got.OldestOpen.Description != "full statistics context" || got.NewestTask == nil || got.NewestTask.ID != ids[1] || got.LastClosed == nil || got.LastClosed.ID != ids[1] {
		t.Fatal(got)
	}
	status, _, data = issueRequest(t, h, "GET", root+"/labels", session, "", "", "")
	var labels struct {
		Data struct {
			Labels    []string `json:"labels"`
			Workflows []any    `json:"workflows"`
			Default   string   `json:"default_workflow"`
		}
	}
	if e = json.Unmarshal(data, &labels); e != nil {
		t.Fatal(e)
	}
	if status != 200 || !reflect.DeepEqual(labels.Data.Labels, []string{"api", "shared", "zeta"}) || labels.Data.Workflows == nil || len(labels.Data.Workflows) != 0 || labels.Data.Default != "standard" {
		t.Fatal(status, string(data))
	}
	stats := statsFrom(t, h, p, session)
	if stats.Total != got.Total || stats.TotalHandoffs != got.TotalHandoffs || stats.OldestOpen == nil || stats.OldestOpen.Description != "full statistics context" {
		t.Fatal(stats)
	}
	for _, path := range []string{"/stats?unknown=1", "/labels?unknown=1"} {
		status, _, data = issueRequest(t, h, "GET", root+path, session, "", "", "")
		if status != 400 {
			t.Fatal(status, string(data))
		}
	}
	status, data = h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Isolated stats project"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	other := projectFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects/"+other+"/sessions", testToken, key(t), "", `{"context_id":"other-stats"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	otherSession := sessionFrom(t, data).ID
	if stats := statsFrom(t, h, other, otherSession); stats.Total != 0 || stats.TotalHandoffs != 0 || stats.TotalLogs != 0 {
		t.Fatal("project leak", stats)
	}
	status, _, data = issueRequest(t, h, "GET", root+"/stats", otherSession, "", "", "")
	if status != 404 {
		t.Fatal(status, string(data))
	}
}

// Change both records and activity totals between the two stats reads. They
// must stay on the snapshot established by the Issue read.
type countHookStore struct {
	port.ReadStore
	hook func()
}
type countHookSnapshot struct {
	port.ReadSnapshot
	hook func()
}
type countHookWorkflow struct {
	port.WorkflowReader
	hook func()
}

func (s countHookStore) Snapshot(ctx context.Context, fn func(port.ReadSnapshot) error) error {
	return s.ReadStore.Snapshot(ctx, func(r port.ReadSnapshot) error { return fn(countHookSnapshot{r, s.hook}) })
}
func (s countHookSnapshot) Workflow() port.WorkflowReader {
	return countHookWorkflow{s.ReadSnapshot.Workflow(), s.hook}
}
func (r countHookWorkflow) Counts(ctx context.Context, p string) (port.ActivityCounts, error) {
	r.hook()
	return r.WorkflowReader.Counts(ctx, p)
}
func TestStatsMaintainIssueAndActivitySnapshot(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	status, _, data := issueRequest(t, h, "POST", root+"/issues", session, key(t), "", `{"title":"Snapshot statistics consistency"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	id := issueFrom(t, data).ID
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	svc := monitor.Service{Reads: countHookStore{h.store, func() {
		tx, e := inspect.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if _, e = tx.Exec(`UPDATE issues SET deleted_at=updated_at WHERE project_id=? AND id=?`, p, id); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(`INSERT INTO activities(project_id,id,issue_id,session_id,operation_row_id,kind,message,log_type,content_version,payload_version,payload_json,created_at,updated_at) SELECT ?,?,?,?,row_id,'log','new stats activity','progress',1,1,'{}',created_at,created_at FROM operations WHERE project_id=? AND command_kind='issue.create' LIMIT 1`, p, "stats-log", id, session, p); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}}}
	c := domain.Caller{ProjectID: p, SessionID: session, Actor: domain.Actor{ID: "act-bootstrap"}}
	stats, e := svc.Stats(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	if stats.Total != 1 || stats.TotalLogs != 0 {
		t.Fatal("mixed snapshot", fmt.Sprintf("%+v", stats))
	}
	svc.Reads = h.store
	stats, e = svc.Stats(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	if stats.Total != 0 || stats.TotalLogs != 1 {
		t.Fatal("preserved archived logs", stats)
	}
}

func TestCompletedReadCapabilitiesAreAdvertised(t *testing.T) {
	h := newHarness(t)
	status, data := h.request("GET", "/v1/capabilities", testToken, "", "", "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	var out struct {
		Data struct {
			Capabilities []string `json:"capabilities"`
		}
	}
	if e := json.Unmarshal(data, &out); e != nil {
		t.Fatal(e)
	}
	caps := map[string]bool{}
	for _, c := range out.Data.Capabilities {
		caps[c] = true
	}
	for _, name := range []string{"issues", "workflow", "boards", "notes", "work_sessions", "query", "monitor", "stats", "labels", "focus", "presence", "operations", "undo"} {
		if !caps[name] {
			t.Fatal("missing capability", name)
		}
	}
	for _, name := range []string{"events", "sse", "changes"} {
		if caps[name] {
			t.Fatal("unfinished capability", name)
		}
	}
}
