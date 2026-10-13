package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/yoophi/td-backend/internal/application/focus"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func TestFocusSessionScopeRevisionAndClear(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	path := root + "/focus"
	status, data := h.request("POST", root+"/sessions", testToken, key(t), "", `{"context_id":"other-focus"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	otherSession := sessionFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", root+"/issues", session, key(t), "", `{"title":"Implement durable focus storage"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	issue := issueFrom(t, data).ID
	get := func(s string, want *string) {
		t.Helper()
		status, _, data := issueRequest(t, h, "GET", path, s, "", "", "")
		var response struct {
			Data map[string]*string `json:"data"`
		}
		if e := json.Unmarshal(data, &response); e != nil {
			t.Fatal(e)
		}
		got, ok := response.Data["focused_issue_id"]
		if status != 200 || !ok || (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Fatal(status, string(data))
		}
	}
	get(session, nil)
	get(otherSession, nil)
	for _, body := range []string{`{}`, `null`, `{"issue_id":""}`, `{"issue_id":1}`, `{"issue_id":false}`, `{"issue_id":[]}`, `{"issue_id":{"issue_id":null}}`, `{"issue_id":[null]}`, `{"issue_id":null,"unknown":null}`, `{"issue_id":null,"issue_id":null}`, `{"Issue_ID":null}`, `{"issue_id":null} {}`} {
		status, _, data := issueRequest(t, h, "PUT", path, session, key(t), "", body)
		if status != 400 {
			t.Fatal(body, status, string(data))
		}
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
	op := key(t)
	body := fmt.Sprintf(`{"issue_id":%q}`, issue)
	status, _, first := issueRequest(t, h, "PUT", path, session, op, "", body)
	if status != 200 {
		t.Fatal(status, string(first))
	}
	get(session, &issue)
	get(otherSession, nil)
	status, _, data = issueRequest(t, h, "PUT", path, session, op, "", body)
	if status != 200 || string(data) != string(first) || counter() != initial+1 {
		t.Fatal("replay", status, string(data))
	}
	// A matching key in another Session has a different request fingerprint.
	status, _, data = issueRequest(t, h, "PUT", path, otherSession, op, "", body)
	if status != 409 {
		t.Fatal("other session replay", status, string(data))
	}
	get(otherSession, nil)
	if _, e = inspect.Exec(`CREATE TRIGGER fail_focus_receipt BEFORE INSERT ON operations WHEN NEW.command_kind='focus.set' BEGIN SELECT RAISE(ABORT,'receipt failed'); END`); e != nil {
		t.Fatal(e)
	}
	clearKey := key(t)
	status, _, data = issueRequest(t, h, "PUT", path, session, clearKey, "", `{"issue_id":null}`)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	get(session, &issue)
	if counter() != initial+1 {
		t.Fatal("failed counter")
	}
	if _, e = inspect.Exec(`DROP TRIGGER fail_focus_receipt`); e != nil {
		t.Fatal(e)
	}
	status, _, first = issueRequest(t, h, "PUT", path, session, clearKey, "", `{"issue_id":null}`)
	if status != 200 {
		t.Fatal(status, string(first))
	}
	get(session, nil)
	status, _, data = issueRequest(t, h, "PUT", path, session, clearKey, "", `{"issue_id":null}`)
	if status != 200 || string(data) != string(first) || counter() != initial+2 {
		t.Fatal("clear replay", status, string(data))
	}
	// A retry of the original set must not overwrite a subsequent clear.
	status, _, data = issueRequest(t, h, "PUT", path, session, op, "", body)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	get(session, nil)
	var beforeJSON, afterJSON []byte
	var version int
	if e = inspect.QueryRow(`SELECT e.state_version,e.before_json,e.after_json FROM operation_effects e JOIN operations o ON o.row_id=e.operation_row_id WHERE o.operation_id=?`, clearKey).Scan(&version, &beforeJSON, &afterJSON); e != nil {
		t.Fatal(e)
	}
	var before, after focus.State
	if e = json.Unmarshal(beforeJSON, &before); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(afterJSON, &after); e != nil {
		t.Fatal(e)
	}
	if version != 1 || before.Focus.Revision != 1 || before.Focus.IssueID == nil || *before.Focus.IssueID != issue || after.Focus.Revision != 2 || after.Focus.IssueID != nil || after.Focus.SessionID != session {
		t.Fatal(string(beforeJSON), string(afterJSON))
	}
	var escaped port.FocusReader
	if e = h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		escaped = r.Focus()
		f, e := escaped.Get(r.Context(), p, session)
		if e != nil {
			return e
		}
		if f.Revision != 2 || f.IssueID != nil {
			t.Fatal(f)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = escaped.Get(context.Background(), p, session); !errors.Is(e, domain.ErrScopeClosed) {
		t.Fatal("escaped", e)
	}
	// Clear a previously unset focus: it gets its own revision without touching the first Session.
	status, _, data = issueRequest(t, h, "PUT", path, otherSession, key(t), "", `{"issue_id":null}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if e = h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		f, e := r.Focus().Get(r.Context(), p, otherSession)
		if f.Revision != 1 {
			t.Fatal(f)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func TestFocusRejectsDeletedAndForeignIssues(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	status, _, data := issueRequest(t, h, "POST", root+"/issues", session, key(t), "", `{"title":"Implement focus isolation checks"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	issue := issueFrom(t, data).ID
	status, _, data = issueRequest(t, h, "DELETE", root+"/issues/"+issue, session, key(t), `"r-1"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "PUT", root+"/focus", session, key(t), "", fmt.Sprintf(`{"issue_id":%q}`, issue))
	if status != 404 {
		t.Fatal(status, string(data))
	}
	status, data = h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Other focus project"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	other := projectFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects/"+other+"/sessions", testToken, key(t), "", `{"context_id":"other-project"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	otherSession := sessionFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", "/v1/projects/"+other+"/issues", otherSession, key(t), "", `{"title":"Implement foreign focus selection"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	foreign := issueFrom(t, data).ID
	status, _, data = issueRequest(t, h, "PUT", root+"/focus", session, key(t), "", fmt.Sprintf(`{"issue_id":%q}`, foreign))
	if status != 404 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "PUT", root+"/focus", otherSession, key(t), "", `{"issue_id":null}`)
	if status != 404 {
		t.Fatal(status, string(data))
	}
}
