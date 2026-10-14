package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/worksession"
	"github.com/yoophi/td-backend/internal/domain"
)

func workSessionFrom(t *testing.T, data []byte) receiptcodec.WorkSessionDTO {
	t.Helper()
	var response struct {
		Data struct {
			WorkSession receiptcodec.WorkSessionDTO `json:"work_session"`
		} `json:"data"`
	}
	if e := json.Unmarshal(data, &response); e != nil || response.Data.WorkSession.ID == "" {
		t.Fatalf("work session response %s: %v", data, e)
	}
	return response.Data.WorkSession
}

func TestWorkSessionActivityRetainsUntaggedAndDeletedIssueHistory(t *testing.T) {
	h, project, session := issueSetup(t)
	root := "/v1/projects/" + project
	base := root + "/work-sessions"
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"name":"History"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	ws := workSessionFrom(t, data)
	issues := []receiptcodec.IssueDTO{}
	for _, title := range []string{"First work item", "Second work item"} {
		status, _, data = issueRequest(t, h, "POST", root+"/issues", session, key(t), "", fmt.Sprintf(`{"title":%q}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		issues = append(issues, issueFrom(t, data))
	}
	status, _, data = issueRequest(t, h, "PUT", base+"/"+ws.ID+"/issues", session, key(t), "", fmt.Sprintf(`{"issue_ids":[%q]}`, issues[0].ID))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	for _, issue := range issues {
		status, _, data = issueRequest(t, h, "POST", root+"/issues/"+issue.ID+"/logs", session, key(t), `"r-1"`, fmt.Sprintf(`{"message":"Work on %s","type":"progress","work_session_id":%q}`, issue.ID, ws.ID))
		if status != 201 {
			t.Fatal(status, string(data))
		}
	}
	status, _, data = issueRequest(t, h, "PUT", base+"/"+ws.ID+"/issues", session, key(t), "", `{"issue_ids":[]}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "DELETE", root+"/issues/"+issues[1].ID, session, key(t), `"r-2"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", base+"/"+ws.ID+"/activity", session, "", "", "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	var response struct {
		Data struct {
			Activity []receiptcodec.ActivityDTO `json:"activity"`
		} `json:"data"`
	}
	if e := json.Unmarshal(data, &response); e != nil || len(response.Data.Activity) != 2 {
		t.Fatalf("work session history: %s: %v", data, e)
	}
	for index, activity := range response.Data.Activity {
		if activity.IssueID != issues[index].ID || activity.WorkSessionID != ws.ID || activity.Kind != "log" {
			t.Fatalf("unexpected activity: %+v", activity)
		}
	}
	status, _, data = issueRequest(t, h, "GET", base+"/"+ws.ID+"/activity?unexpected=1", session, "", "", "")
	if status != 400 {
		t.Fatal(status, string(data))
	}
}

func TestWorkSessionLogWithoutTaggedIssueReplayAndUndo(t *testing.T) {
	h, project, session := issueSetup(t)
	root := "/v1/projects/" + project
	base := root + "/work-sessions"
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"name":"Untargeted progress"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	ws := workSessionFrom(t, data)
	path := base + "/" + ws.ID + "/logs"
	status, _, data = issueRequest(t, h, "GET", path, session, "", "", "")
	if status != 200 || !strings.Contains(string(data), `"logs":[]`) {
		t.Fatal(status, string(data))
	}
	status, otherData := h.request("POST", root+"/sessions", testToken, key(t), "", `{"context_id":"other-log-writer"}`)
	if status != 201 {
		t.Fatal(status, string(otherData))
	}
	other := sessionFrom(t, otherData).ID
	for _, tc := range []struct {
		caller, body string
		want         int
	}{{other, `{"message":"Foreign","type":"progress"}`, 403}, {session, `{"message":" ","type":"progress"}`, 400}, {session, `{"message":"Invalid","type":"other"}`, 400}} {
		status, _, data = issueRequest(t, h, "POST", path, tc.caller, key(t), "", tc.body)
		if status != tc.want {
			t.Fatal(status, string(data))
		}
	}
	op := key(t)
	body := `{"message":" No issues tagged yet ","type":"decision"}`
	status, _, first := issueRequest(t, h, "POST", path, session, op, "", body)
	if status != 201 {
		t.Fatal(status, string(first))
	}
	var result struct {
		Data struct {
			Log receiptcodec.LogDTO `json:"log"`
		} `json:"data"`
	}
	if e := json.Unmarshal(first, &result); e != nil || result.Data.Log.IssueID != "" || result.Data.Log.WorkSessionID != ws.ID || result.Data.Log.SessionID != session || result.Data.Log.Message != "No issues tagged yet" || result.Data.Log.Type != domain.LogTypeDecision {
		t.Fatal(string(first), e)
	}
	if stats := statsFrom(t, h, project, session); stats.TotalLogs != 1 || stats.MostActiveSession != session {
		t.Fatal("work-session log missing from statistics", stats)
	}
	status, _, data = issueRequest(t, h, "POST", path, session, op, "", body)
	if status != 201 || string(data) != string(first) {
		t.Fatal("log replay changed", status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", path, other, "", "", "")
	if status != 200 || !strings.Contains(string(data), result.Data.Log.ID) {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", root+"/operations/"+op+"/undo", session, key(t), "", `{"reason":"Correct work-session note"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", path, session, "", "", "")
	if status != 200 || !strings.Contains(string(data), `"logs":[]`) {
		t.Fatal(status, string(data))
	}
	if stats := statsFrom(t, h, project, session); stats.TotalLogs != 0 {
		t.Fatal("undone work-session log remains in statistics", stats)
	}
}
func TestWorkSessionOwnerTagsEndAndAtomicity(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	base := root + "/work-sessions"
	status, data := h.request("POST", root+"/sessions", testToken, key(t), "", `{"context_id":"observer"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	observer := sessionFrom(t, data).ID
	ids := []string{}
	for _, title := range []string{"Implement database snapshot reader", "Verify observed mutation replay"} {
		status, _, data := issueRequest(t, h, "POST", root+"/issues", session, key(t), "", fmt.Sprintf(`{"title":%q}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	for _, body := range []string{`{}`, `{"name":" "}`, `{"name":"Work","session_id":"forged"}`, `{"name":"Work","worktree_root":"/tmp"}`} {
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", body)
		if status != 400 {
			t.Fatal(status, string(data))
		}
	}
	op := key(t)
	body := `{"name":" Work ","start_sha":"abc"}`
	status, _, first := issueRequest(t, h, "POST", base, session, op, "", body)
	if status != 201 {
		t.Fatal(status, string(first))
	}
	ws := workSessionFrom(t, first)
	if ws.Name != "Work" || ws.SessionID != session || ws.StartSHA != "abc" || ws.EndedAt != nil {
		t.Fatal(ws)
	}
	path := base + "/" + ws.ID
	status, _, data = issueRequest(t, h, "POST", base, session, op, "", body)
	if status != 201 || string(data) != string(first) {
		t.Fatal("start replay", status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	counter := func() int64 {
		t.Helper()
		var n int64
		if e := inspect.QueryRow(`SELECT change_seq FROM projects WHERE id=?`, p).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	initial := counter()
	read := func(revision int64, count int) worksession.State {
		t.Helper()
		var state worksession.State
		if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
			var e error
			state.WorkSession, e = r.WorkSessions().Get(r.Context(), p, ws.ID)
			if e != nil {
				return e
			}
			state.Tags, e = r.WorkSessions().Tags(r.Context(), p, ws.ID)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if state.WorkSession.Revision != revision || len(state.Tags) != count {
			t.Fatalf("state %+v", state)
		}
		return state
	}
	read(1, 0)
	for _, tc := range []struct{ method, suffix, body string }{{"PUT", "/issues", `{"issue_ids":[]}`}, {"POST", "/end", `{}`}} {
		status, _, data = issueRequest(t, h, tc.method, path+tc.suffix, observer, key(t), "", tc.body)
		if status != 403 {
			t.Fatal(status, string(data))
		}
	}
	for _, body := range []string{`{}`, `{"issue_ids":null}`, `{"issue_ids":[""]}`, fmt.Sprintf(`{"issue_ids":[%q,%q]}`, ids[0], ids[0])} {
		status, _, data = issueRequest(t, h, "PUT", path+"/issues", session, key(t), "", body)
		if status != 400 {
			t.Fatal(status, string(data))
		}
	}
	tagBody := fmt.Sprintf(`{"issue_ids":[%q,%q]}`, ids[0], ids[1])
	tagKey := key(t)
	status, _, first = issueRequest(t, h, "PUT", path+"/issues", session, tagKey, "", tagBody)
	if status != 200 {
		t.Fatal(status, string(first))
	}
	tagged := read(2, 2)
	status, _, data = issueRequest(t, h, "PUT", path+"/issues", session, tagKey, "", tagBody)
	if status != 200 || string(data) != string(first) || counter() != initial+1 {
		t.Fatal("tag replay", status, string(data))
	}
	// Shared read is allowed for another owning project Session.
	status, _, data = issueRequest(t, h, "GET", path, observer, "", "", "")
	if status != 200 || workSessionFrom(t, data).ID != ws.ID {
		t.Fatal(status, string(data))
	}
	var detail struct {
		Data struct {
			IDs []string `json:"issue_ids"`
		} `json:"data"`
	}
	if e = json.Unmarshal(data, &detail); e != nil || len(detail.Data.IDs) != 2 {
		t.Fatal(e, string(data))
	}
	if _, e = inspect.Exec(`CREATE TRIGGER fail_ws_receipt BEFORE INSERT ON operations WHEN NEW.command_kind='work_session.tag' BEGIN SELECT RAISE(ABORT,'receipt failed'); END`); e != nil {
		t.Fatal(e)
	}
	clearKey := key(t)
	status, _, data = issueRequest(t, h, "PUT", path+"/issues", session, clearKey, "", `{"issue_ids":[]}`)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	read(2, 2)
	if counter() != initial+1 {
		t.Fatal("rollback counter")
	}
	if _, e = inspect.Exec(`DROP TRIGGER fail_ws_receipt`); e != nil {
		t.Fatal(e)
	}
	status, _, data = issueRequest(t, h, "PUT", path+"/issues", session, clearKey, "", `{"issue_ids":[]}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	read(3, 0)
	// Replacing identical tags preserves tagged_at while still producing a new revision.
	status, _, data = issueRequest(t, h, "PUT", path+"/issues", session, key(t), "", tagBody)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	again := read(4, 2)
	status, _, data = issueRequest(t, h, "PUT", path+"/issues", session, key(t), "", tagBody)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	same := read(5, 2)
	for i := range same.Tags {
		if !same.Tags[i].TaggedAt.Equal(again.Tags[i].TaggedAt) {
			t.Fatal("tag timestamp changed")
		}
	}
	var beforeJSON, afterJSON []byte
	var version int
	if e = inspect.QueryRow(`SELECT e.state_version,e.before_json,e.after_json FROM operation_effects e JOIN operations o ON o.row_id=e.operation_row_id WHERE o.operation_id=?`, clearKey).Scan(&version, &beforeJSON, &afterJSON); e != nil {
		t.Fatal(e)
	}
	var before, after worksession.State
	if e = json.Unmarshal(beforeJSON, &before); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(afterJSON, &after); e != nil {
		t.Fatal(e)
	}
	if version != 1 || before.WorkSession.Revision != 2 || after.WorkSession.Revision != 3 || len(before.Tags) != 2 || len(after.Tags) != 0 || !before.Tags[0].TaggedAt.Equal(tagged.Tags[0].TaggedAt) {
		t.Fatal("undo state", string(beforeJSON), string(afterJSON))
	}
	endKey := key(t)
	status, _, first = issueRequest(t, h, "POST", path+"/end", session, endKey, "", `{"end_sha":"def"}`)
	if status != 200 {
		t.Fatal(status, string(first))
	}
	ended := workSessionFrom(t, first)
	if ended.EndedAt == nil || ended.EndSHA != "def" {
		t.Fatal(ended)
	}
	read(6, 2)
	status, _, data = issueRequest(t, h, "POST", path+"/end", session, endKey, "", `{"end_sha":"def"}`)
	if status != 200 || string(data) != string(first) {
		t.Fatal("end replay", status, string(data))
	}
	for _, tc := range []struct{ method, suffix, body string }{{"PUT", "/issues", `{"issue_ids":[]}`}, {"POST", "/end", `{}`}} {
		status, _, data = issueRequest(t, h, tc.method, path+tc.suffix, session, key(t), "", tc.body)
		if status != 409 {
			t.Fatal(status, string(data))
		}
	}
	// Retrying an earlier operation returns its stored reply even after ending.
	status, _, data = issueRequest(t, h, "PUT", path+"/issues", session, tagKey, "", tagBody)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	read(6, 2)
	if counter() != initial+5 {
		t.Fatal("change counter", counter(), initial)
	}
	var escaped port.WorkSessionReader
	if e = h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		escaped = r.WorkSessions()
		all, e := escaped.List(r.Context(), p)
		if e != nil {
			return e
		}
		if len(all) != 1 || all[0].EndedAt == nil {
			t.Fatal(all)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = escaped.List(context.Background(), p); !errors.Is(e, domain.ErrScopeClosed) {
		t.Fatal("escaped", e)
	}
}
func TestWorkSessionForeignProjectTagRollback(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	status, _, data := issueRequest(t, h, "POST", root+"/work-sessions", session, key(t), "", `{"name":"Work"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	ws := workSessionFrom(t, data)
	status, _, data = issueRequest(t, h, "POST", root+"/issues", session, key(t), "", `{"title":"Implement local session tagging"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	local := issueFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Other"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	other := projectFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects/"+other+"/sessions", testToken, key(t), "", `{"context_id":"other"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	otherSession := sessionFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", "/v1/projects/"+other+"/issues", otherSession, key(t), "", `{"title":"Implement foreign project tagging"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	foreign := issueFrom(t, data).ID
	path := root + "/work-sessions/" + ws.ID
	status, _, data = issueRequest(t, h, "PUT", path+"/issues", session, key(t), "", fmt.Sprintf(`{"issue_ids":[%q,%q]}`, local, foreign))
	if status != 404 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", "/v1/projects/"+other+"/work-sessions/"+ws.ID, otherSession, "", "", "")
	if status != 404 {
		t.Fatal(status, string(data))
	}
	if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		w, e := r.WorkSessions().Get(r.Context(), p, ws.ID)
		if e != nil {
			return e
		}
		tags, e := r.WorkSessions().Tags(r.Context(), p, ws.ID)
		if e != nil {
			return e
		}
		if w.Revision != 1 || len(tags) != 0 {
			t.Fatal(w, tags)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
