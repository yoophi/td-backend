package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/monitor"
	"github.com/yoophi/td-backend/internal/domain"
)

func getMonitor(t *testing.T, h *harness, p, session, query string) receiptcodec.MonitorResultDTO {
	t.Helper()
	status, _, data := issueRequest(t, h, "GET", "/v1/projects/"+p+"/monitor"+query, session, "", "", "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	var out struct{ Data receiptcodec.MonitorResultDTO }
	if e := json.Unmarshal(data, &out); e != nil {
		t.Fatal(e)
	}
	return out.Data
}
func bucket(t *testing.T, m receiptcodec.MonitorResultDTO, name string) []receiptcodec.IssueDTO {
	t.Helper()
	raw, e := json.Marshal(m.Monitor.TaskList[name])
	if e != nil {
		t.Fatal(e)
	}
	var cards []receiptcodec.IssueDTO
	if e = json.Unmarshal(raw, &cards); e != nil {
		t.Fatal(e)
	}
	if cards == nil {
		t.Fatalf("null bucket %s", name)
	}
	return cards
}
func hasTransition(i receiptcodec.IssueDTO, action string) bool {
	return i.AvailableTransitions != nil && slices.Contains(*i.AvailableTransitions, action)
}
func TestMonitorReviewOptionsIncludePastImplementation(t *testing.T) {
	h, p, first := issueSetup(t)
	status, data := h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", `{"context_id":"later-implementer"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	later := sessionFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", `{"context_id":"independent-reviewer"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	reviewer := sessionFrom(t, data).ID
	base := "/v1/projects/" + p + "/issues"
	status, _, data = issueRequest(t, h, "POST", base, first, key(t), "", `{"title":"Historical implementation review"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	path := base + "/" + issueFrom(t, data).ID
	for _, step := range []struct{ action, session, revision, body string }{
		{"start", first, "r-1", `{}`},
		{"unstart", first, "r-2", `{}`},
		{"start", later, "r-3", `{}`},
		{"handoffs", later, "r-4", `{"done":["implemented"]}`},
	} {
		status, _, data = issueRequest(t, h, "POST", path+"/"+step.action, step.session, key(t), `"`+step.revision+`"`, step.body)
		if status != 200 && status != 201 {
			t.Fatal(step.action, status, string(data))
		}
	}
	past := bucket(t, getMonitor(t, h, p, first, ""), "reviewable")
	independent := bucket(t, getMonitor(t, h, p, reviewer, ""), "reviewable")
	if len(past) != 1 || past[0].ImplementerSession == nil || *past[0].ImplementerSession != later || past[0].ReviewOptions == nil || !past[0].ReviewOptions.SelfReviewPrompt || !past[0].ReviewOptions.RecordReviewAvailable {
		t.Fatalf("past implementation was not reflected: %+v", past)
	}
	if len(independent) != 1 || independent[0].ReviewOptions == nil || independent[0].ReviewOptions.SelfReviewPrompt || !independent[0].ReviewOptions.RecordReviewAvailable {
		t.Fatalf("independent reviewer got historical prompt: %+v", independent)
	}
}
func TestMonitorPoliciesApprovalFreshnessAndTransitions(t *testing.T) {
	for _, mode := range []string{"trusted", "delegated", "strict", "balanced"} {
		t.Run(mode, func(t *testing.T) {
			h, p, session, path := reviewFixture(t, mode)
			status, data := h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", `{"context_id":"monitor-reviewer"}`)
			if status != 201 {
				t.Fatal(status, string(data))
			}
			reviewer := sessionFrom(t, data).ID
			name := "pending_review"
			if mode == "trusted" {
				name = "reviewable"
			}
			mine := bucket(t, getMonitor(t, h, p, session, ""), name)
			if len(mine) != 1 {
				t.Fatal(name, mine)
			}
			if hasTransition(mine[0], "approve") != (mode == "trusted") {
				t.Fatal(mode, mine[0])
			}
			if mine[0].ReviewOptions == nil || mine[0].ReviewOptions.SelfReviewPrompt != (mode == "trusted") || mine[0].ReviewOptions.RecordReviewAvailable != (mode == "trusted") {
				t.Fatalf("implementation history review options differ from policy: %s %+v", mode, mine[0].ReviewOptions)
			}
			theirs := bucket(t, getMonitor(t, h, p, reviewer, ""), "reviewable")
			if len(theirs) != 1 || !hasTransition(theirs[0], "approve") {
				t.Fatal(theirs)
			}
			if theirs[0].ReviewOptions == nil || theirs[0].ReviewOptions.SelfReviewPrompt || theirs[0].ReviewOptions.RecordReviewAvailable != (mode == "trusted" || mode == "delegated") {
				t.Fatalf("independent reviewer options differ from policy: %s %+v", mode, theirs[0].ReviewOptions)
			}
			if mode == "trusted" || mode == "delegated" {
				status, _, data = issueRequest(t, h, "POST", path+"/approve", reviewer, key(t), `"r-3"`, `{"record_only":true,"reason":"Verified monitor parity"}`)
				if status != 200 {
					t.Fatal(status, string(data))
				}
				ready := bucket(t, getMonitor(t, h, p, session, ""), "ready_to_close")
				if len(ready) != 1 || ready[0].ActiveReview == nil || !hasTransition(ready[0], "close") {
					t.Fatal(ready)
				}
				// Advertised close accepts its ordinary required reason.
				status, _, data = issueRequest(t, h, "POST", path+"/close", session, key(t), `"r-4"`, `{"reason":"Close independently reviewed implementation"}`)
				if status != 200 {
					t.Fatal(status, string(data))
				}
			} else {
				status, _, data = issueRequest(t, h, "POST", path+"/approve", reviewer, key(t), `"r-3"`, `{}`)
				if status != 200 {
					t.Fatal(status, string(data))
				}
			}
			closed := bucket(t, getMonitor(t, h, p, session, "?include_closed=true"), "closed")
			if len(closed) != 1 || !hasTransition(closed[0], "reopen") {
				t.Fatal(closed)
			}
			if got := bucket(t, getMonitor(t, h, p, session, ""), "closed"); len(got) != 0 {
				t.Fatal("closed default", got)
			}
		})
	}
}
func TestMonitorFocusDependenciesFeedSearchAndReaderRole(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	ids := []string{}
	for _, title := range []string{"Alpha monitor task", "Beta monitor task"} {
		status, _, data := issueRequest(t, h, "POST", root+"/issues", session, key(t), "", fmt.Sprintf(`{"title":%q,"description":"full monitor context","minor":true}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	status, _, data := issueRequest(t, h, "POST", root+"/issues/"+ids[0]+"/dependencies", session, key(t), `"r-1"`, fmt.Sprintf(`{"depends_on":%q}`, ids[1]))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", root+"/issues/"+ids[1]+"/start", session, key(t), `"r-1"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "PUT", root+"/focus", session, key(t), "", fmt.Sprintf(`{"issue_id":%q}`, ids[1]))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", root+"/issues/"+ids[1]+"/handoffs", session, key(t), `"r-2"`, `{"done":["first milestone"],"submit_review":false}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", root+"/issues/"+ids[1]+"/handoffs", session, key(t), `"r-3"`, `{"done":["second milestone"],"submit_review":false}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	m := getMonitor(t, h, p, session, "")
	if m.SessionID != session || m.ChangeToken == "" || m.Monitor.FocusedIssue == nil || m.Monitor.FocusedIssue.ID != ids[1] || m.Monitor.FocusedIssue.Description != "full monitor context" || len(m.Monitor.InProgress) != 1 || len(m.Monitor.RecentHandoffs) != 2 || !slices.Contains(m.Monitor.ActiveSessions, session) {
		t.Fatal(m)
	}
	if cards := bucket(t, m, "blocked"); len(cards) != 1 || cards[0].ID != ids[0] {
		t.Fatal(cards)
	}
	if cards := bucket(t, m, "in_progress"); len(cards) != 1 || !hasTransition(cards[0], "review") || !hasTransition(cards[0], "unstart") {
		t.Fatal(cards)
	}
	searched := getMonitor(t, h, p, session, "?search_mode=tdq&search="+url.QueryEscape(`title ~ "Alpha"`))
	if len(bucket(t, searched, "in_progress")) != 0 || len(bucket(t, searched, "blocked")) != 1 || searched.Monitor.FocusedIssue == nil || len(searched.Monitor.InProgress) != 1 {
		t.Fatal(searched)
	}
	for _, q := range []string{"?include_closed=1", "?sort=invalid", "?sort=" + url.QueryEscape("title status = open"), "?search_mode=invalid", "?search_mode=tdq&search=" + url.QueryEscape("status ="), "?unknown=1", "?sort=id&sort=title"} {
		status, _, data = issueRequest(t, h, "GET", root+"/monitor"+q, session, "", "", "")
		if status != 400 {
			t.Fatal(q, status, string(data))
		}
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	// Create enough persistent logs to test the feed cap without hiding handoffs.
	for n := 0; n < 55; n++ {
		if _, e = inspect.Exec(`INSERT INTO activities(project_id,id,issue_id,session_id,operation_row_id,kind,message,log_type,content_version,payload_version,payload_json,created_at,updated_at) SELECT project_id,?,issue_id,session_id,operation_row_id,'log','feed evidence','progress',content_version,1,'{}',created_at,updated_at FROM activities WHERE project_id=? AND issue_id=? LIMIT 1`, fmt.Sprintf("feed-%03d", n), p, ids[1]); e != nil {
			t.Fatal(e)
		}
	}
	m = getMonitor(t, h, p, session, "")
	if len(m.Monitor.Activity) != 50 || len(m.Monitor.RecentHandoffs) != 2 {
		t.Fatal("feed bounds", m.Monitor.Activity, m.Monitor.RecentHandoffs)
	}
	for _, a := range m.Monitor.Activity {
		if a.IssueID == "" || a.IssueTitle == "" {
			t.Fatal("activity attribution", a)
		}
	}
	if _, e = inspect.Exec(`UPDATE project_memberships SET role='reader' WHERE project_id=? AND actor_id='act-bootstrap'`, p); e != nil {
		t.Fatal(e)
	}
	m = getMonitor(t, h, p, session, "")
	for _, name := range []string{"blocked", "in_progress"} {
		for _, card := range bucket(t, m, name) {
			if card.AvailableTransitions == nil || len(*card.AvailableTransitions) != 0 {
				t.Fatal("reader transitions", card)
			}
		}
	}
}
func TestMonitorSnapshotIncludesOldFocusFeedAndToken(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	status, _, data := issueRequest(t, h, "POST", root+"/issues", session, key(t), "", `{"title":"Alpha monitor snapshot"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	id := issueFrom(t, data).ID
	status, _, data = issueRequest(t, h, "PUT", root+"/focus", session, key(t), "", fmt.Sprintf(`{"issue_id":%q}`, id))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	var seq int64
	if e = inspect.QueryRow(`SELECT change_seq FROM projects WHERE id=?`, p).Scan(&seq); e != nil {
		t.Fatal(e)
	}
	svc := monitor.Service{Reads: queryHookStore{h.store, func() {
		tx, e := inspect.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		for _, q := range []string{`UPDATE issues SET title='Beta monitor snapshot',revision=revision+1 WHERE project_id=?`, `UPDATE session_focus SET issue_id=NULL,revision=revision+1 WHERE project_id=?`, `UPDATE projects SET change_seq=change_seq+1 WHERE id=?`} {
			if _, e = tx.Exec(q, p); e != nil {
				t.Fatal(e)
			}
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}}}
	out, e := svc.Get(context.Background(), domain.Caller{ProjectID: p, SessionID: session, Actor: domain.Actor{ID: "act-bootstrap"}}, monitor.Options{})
	if e != nil {
		t.Fatal(e)
	}
	if out.Focused == nil || out.Focused.Issue.Title != "Alpha monitor snapshot" || out.ChangeToken != fmt.Sprintf("%s:%d", p, seq) || len(out.Categories["ready"]) != 1 {
		t.Fatal(out)
	}
	fresh := getMonitor(t, h, p, session, "")
	if fresh.Monitor.FocusedIssue != nil || !strings.HasSuffix(fresh.ChangeToken, fmt.Sprintf(":%d", seq+1)) {
		t.Fatal(fresh)
	}
}

func TestMonitorSessionPresenceHeartbeatReplayAndRollback(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	status, data := h.request("POST", root+"/sessions", testToken, key(t), "", `{"context_id":"presence-observer"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	observer := sessionFrom(t, data).ID
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`UPDATE sessions SET last_activity='2000-01-01T00:00:00Z' WHERE project_id=?`, p); e != nil {
		t.Fatal(e)
	}
	m := getMonitor(t, h, p, observer, "")
	if slices.Contains(m.Monitor.ActiveSessions, session) || !slices.Contains(m.Monitor.ActiveSessions, observer) {
		t.Fatal("expired presence", m)
	}
	path := root + "/sessions/" + session + "/heartbeat"
	op := key(t)
	status, _, first := issueRequest(t, h, "POST", path, session, op, "", "")
	if status != 200 {
		t.Fatal(status, string(first))
	}
	m = getMonitor(t, h, p, observer, "")
	if !slices.Contains(m.Monitor.ActiveSessions, session) {
		t.Fatal("heartbeat missing", m)
	}
	token := m.ChangeToken
	status, _, data = issueRequest(t, h, "POST", path, session, op, "", "")
	if status != 200 || string(data) != string(first) || getMonitor(t, h, p, observer, "").ChangeToken != token {
		t.Fatal("heartbeat replay", status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path, observer, key(t), "", "")
	if status != 403 {
		t.Fatal("other owner heartbeat", status, string(data))
	}
	if _, e = inspect.Exec(`UPDATE sessions SET last_activity='2000-01-01T00:00:00Z' WHERE project_id=? AND id=?`, p, session); e != nil {
		t.Fatal(e)
	}
	if _, e = inspect.Exec(`CREATE TRIGGER fail_presence BEFORE UPDATE OF last_activity ON sessions BEGIN SELECT RAISE(ABORT,'presence failed'); END`); e != nil {
		t.Fatal(e)
	}
	createKey := key(t)
	body := `{"title":"Implement atomic presence update"}`
	status, _, data = issueRequest(t, h, "POST", root+"/issues", session, createKey, "", body)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	m = getMonitor(t, h, p, observer, "")
	if m.ChangeToken != token || len(bucket(t, m, "ready")) != 0 || slices.Contains(m.Monitor.ActiveSessions, session) {
		t.Fatal("write rollback", m)
	}
	if _, e = inspect.Exec(`DROP TRIGGER fail_presence`); e != nil {
		t.Fatal(e)
	}
	status, _, data = issueRequest(t, h, "POST", root+"/issues", session, createKey, "", body)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	m = getMonitor(t, h, p, observer, "")
	if m.ChangeToken == token || len(bucket(t, m, "ready")) != 1 || !slices.Contains(m.Monitor.ActiveSessions, session) {
		t.Fatal("write presence", m)
	}
}

func TestMonitorReworkReviewResetAndBoardOrder(t *testing.T) {
	h, p, session, path := reviewFixture(t, "trusted")
	status, _, data := issueRequest(t, h, "POST", path+"/reject", session, key(t), `"r-3"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/start", session, key(t), `"r-4"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if cards := bucket(t, getMonitor(t, h, p, session, ""), "needs_rework"); len(cards) != 1 {
		t.Fatal(cards)
	}
	status, _, data = issueRequest(t, h, "POST", path+"/handoffs", session, key(t), `"r-5"`, `{"done":["fixed regression"]}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	if cards := bucket(t, getMonitor(t, h, p, session, ""), "needs_rework"); len(cards) != 0 {
		t.Fatal("later review clears rework", cards)
	}
	root := "/v1/projects/" + p
	ids := []string{}
	for _, title := range []string{"Alpha board monitor card", "Beta board monitor card"} {
		status, _, data := issueRequest(t, h, "POST", root+"/issues", session, key(t), "", fmt.Sprintf(`{"title":%q,"minor":true}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	status, _, data = issueRequest(t, h, "POST", root+"/boards", session, key(t), "", `{"name":"Monitor ordering","query":"title ~ board sort:title"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	b := boardFrom(t, data)
	status, _, data = issueRequest(t, h, "POST", root+"/boards/"+b.ID+"/move", session, key(t), `"r-1"`, fmt.Sprintf(`{"issue_id":%q,"before_id":%q}`, ids[1], ids[0]))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	m := getMonitor(t, h, p, session, "?board_id="+b.ID)
	cards := bucket(t, m, "ready")
	if len(cards) != 2 || cards[0].ID != ids[1] || cards[1].ID != ids[0] || len(bucket(t, m, "reviewable")) != 0 {
		t.Fatal(cards, m)
	}
	cards = bucket(t, getMonitor(t, h, p, session, "?board_id="+b.ID+"&search=Alpha&search_mode=text"), "ready")
	if len(cards) != 1 || cards[0].ID != ids[0] {
		t.Fatal(cards)
	}
	status, _, data = issueRequest(t, h, "GET", root+"/monitor?board_id=missing", session, "", "", "")
	if status != 404 {
		t.Fatal(status, string(data))
	}
}
