package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/note"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func noteFrom(t *testing.T, data []byte) receiptcodec.NoteDTO {
	t.Helper()
	var response struct {
		Data struct {
			Note receiptcodec.NoteDTO `json:"note"`
		} `json:"data"`
	}
	if e := json.Unmarshal(data, &response); e != nil || response.Data.Note.ID == "" {
		t.Fatalf("note response %s: %v", data, e)
	}
	return response.Data.Note
}

func TestDeletedNotesCanBeReadAndRestored(t *testing.T) {
	h, project, session := issueSetup(t)
	base := "/v1/projects/" + project + "/notes"
	status, _, body := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Recoverable note"}`)
	if status != 201 {
		t.Fatal(status, string(body))
	}
	n := noteFrom(t, body)
	path := base + "/" + n.ID
	status, etag, body := issueRequest(t, h, "DELETE", path, session, key(t), `"`+n.Revision+`"`, "")
	if status != 200 || etag != `"r-2"` {
		t.Fatal(status, etag, string(body))
	}
	for _, url := range []string{base, path} {
		status, _, body = issueRequest(t, h, "GET", url+"?include_deleted=invalid", session, "", "", "")
		if status != 400 {
			t.Fatal(url, status, string(body))
		}
	}
	status, _, body = issueRequest(t, h, "GET", path, session, "", "", "")
	if status != 404 {
		t.Fatal(status, string(body))
	}
	status, _, body = issueRequest(t, h, "GET", path+"?include_deleted=true", session, "", "", "")
	if status != 200 || noteFrom(t, body).DeletedAt == nil {
		t.Fatal(status, string(body))
	}
	status, _, body = issueRequest(t, h, "GET", base+"?include_deleted=true", session, "", "", "")
	if status != 200 || !strings.Contains(string(body), n.ID) {
		t.Fatal(status, string(body))
	}
	op := key(t)
	status, etag, body = issueRequest(t, h, "POST", path+"/restore", session, op, `"r-2"`, "")
	if status != 200 || etag != `"r-3"` || noteFrom(t, body).DeletedAt != nil {
		t.Fatal(status, etag, string(body))
	}
	status, replay, again := issueRequest(t, h, "POST", path+"/restore", session, op, `"r-2"`, "")
	if status != 200 || replay != etag || string(again) != string(body) {
		t.Fatal("restore replay", status, replay, string(again))
	}
}
func TestNotesLifecycleRevisionAndAtomicReceipt(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/notes"
	for _, body := range []string{`{}`, `{"title":" "}`, `{"title":null}`, `{"title":"X","archived":true}`, `{"title":"X","pinned":null}`} {
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", body)
		if status != 400 {
			t.Fatal(status, string(data))
		}
	}
	op := key(t)
	body := `{"title":"  Design  ","content":"Keep this context","pinned":true}`
	status, etag, first := issueRequest(t, h, "POST", base, session, op, "", body)
	if status != 201 || etag != `"r-1"` {
		t.Fatal(status, etag, string(first))
	}
	n := noteFrom(t, first)
	if n.Title != "Design" || n.Content != "Keep this context" || !n.Pinned || n.Archived || n.DeletedAt != nil {
		t.Fatal(n)
	}
	status, replay, data := issueRequest(t, h, "POST", base, session, op, "", body)
	if status != 201 || replay != etag || string(data) != string(first) {
		t.Fatal("replay", status, string(data))
	}
	path := base + "/" + n.ID
	for _, tc := range []struct {
		rev, body string
		status    int
	}{{"", `{"content":""}`, 428}, {`"r-2"`, `{"content":""}`, 412}, {`"r-1"`, `{}`, 400}, {`"r-1"`, `{"content":null}`, 400}, {`"r-1"`, `{"title":" "}`, 400}} {
		status, _, data = issueRequest(t, h, "PATCH", path, session, key(t), tc.rev, tc.body)
		if status != tc.status {
			t.Fatal(tc, status, string(data))
		}
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	counter := func() int64 {
		t.Helper()
		var v int64
		if e := inspect.QueryRow(`SELECT change_seq FROM projects WHERE id=?`, p).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	initial := counter()
	if _, e = inspect.Exec(`CREATE TRIGGER fail_note_receipt BEFORE INSERT ON operations WHEN NEW.command_kind='note.update' BEGIN SELECT RAISE(ABORT,'receipt failed'); END`); e != nil {
		t.Fatal(e)
	}
	updateKey := key(t)
	patch := `{"content":"","pinned":false,"archived":true}`
	status, _, data = issueRequest(t, h, "PATCH", path, session, updateKey, `"r-1"`, patch)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	status, etag, data = issueRequest(t, h, "GET", path, session, "", "", "")
	if status != 200 || etag != `"r-1"` || noteFrom(t, data).Content != n.Content || counter() != initial {
		t.Fatal("rollback", status, string(data))
	}
	if _, e = inspect.Exec(`DROP TRIGGER fail_note_receipt`); e != nil {
		t.Fatal(e)
	}
	status, etag, first = issueRequest(t, h, "PATCH", path, session, updateKey, `"r-1"`, patch)
	if status != 200 || etag != `"r-2"` {
		t.Fatal(status, etag, string(first))
	}
	updated := noteFrom(t, first)
	if updated.Content != "" || updated.Pinned || !updated.Archived || counter() != initial+1 {
		t.Fatal(updated)
	}
	status, _, data = issueRequest(t, h, "PATCH", path, session, updateKey, `"r-1"`, patch)
	if status != 200 || string(data) != string(first) || counter() != initial+1 {
		t.Fatal("update replay", status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", base, session, "", "", "")
	var list struct {
		Data struct {
			Notes []receiptcodec.NoteDTO `json:"notes"`
		} `json:"data"`
	}
	if e = json.Unmarshal(data, &list); e != nil {
		t.Fatal(e)
	}
	if status != 200 || len(list.Data.Notes) != 1 || !list.Data.Notes[0].Archived {
		t.Fatal("archive discoverable", status, string(data))
	}
	var previous, next []byte
	var version int
	if e = inspect.QueryRow(`SELECT e.state_version,e.before_json,e.after_json FROM operation_effects e JOIN operations o ON o.row_id=e.operation_row_id WHERE o.operation_id=?`, updateKey).Scan(&version, &previous, &next); e != nil {
		t.Fatal(e)
	}
	var before, after note.State
	if e = json.Unmarshal(previous, &before); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(next, &after); e != nil {
		t.Fatal(e)
	}
	if version != 1 || before.Note.Content != n.Content || after.Note.Content != "" || after.Note.Revision != 2 {
		t.Fatal("snapshot", string(previous), string(next))
	}
	deleteKey := key(t)
	status, etag, first = issueRequest(t, h, "DELETE", path, session, deleteKey, `"r-2"`, "")
	if status != 200 || etag != `"r-3"` {
		t.Fatal(status, string(first))
	}
	status, _, data = issueRequest(t, h, "DELETE", path, session, deleteKey, `"r-2"`, "")
	if status != 200 || string(data) != string(first) || counter() != initial+2 {
		t.Fatal("delete replay", status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", path, session, "", "", "")
	if status != 404 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", base, session, "", "", "")
	if e = json.Unmarshal(data, &list); e != nil {
		t.Fatal(e)
	}
	if status != 200 || len(list.Data.Notes) != 0 {
		t.Fatal(status, string(data))
	}
	var escaped port.NoteReader
	if e = h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		escaped = r.Notes()
		stored, e := escaped.Get(r.Context(), p, n.ID, true)
		if e != nil {
			return e
		}
		if stored.DeletedAt == nil || stored.Revision != 3 {
			t.Fatal(stored)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = escaped.List(context.Background(), p); !errors.Is(e, domain.ErrScopeClosed) {
		t.Fatal("escaped reader", e)
	}
}
func TestNotesProjectIsolationAndDeterministicPinOrder(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/notes"
	var pinned string
	for _, body := range []string{`{"title":"Pinned","pinned":true}`, `{"title":"Recent"}`} {
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", body)
		if status != 201 {
			t.Fatal(status, string(data))
		}
		if pinned == "" {
			pinned = noteFrom(t, data).ID
		}
	}
	status, _, data := issueRequest(t, h, "GET", base, session, "", "", "")
	var list struct {
		Data struct {
			Notes []receiptcodec.NoteDTO `json:"notes"`
		} `json:"data"`
	}
	if e := json.Unmarshal(data, &list); e != nil {
		t.Fatal(e)
	}
	if status != 200 || len(list.Data.Notes) != 2 || list.Data.Notes[0].ID != pinned {
		t.Fatal(status, string(data))
	}
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
	for _, tc := range []struct{ method, path, session, body string }{{"GET", "/v1/projects/" + other + "/notes/" + pinned, otherSession, ""}, {"PATCH", "/v1/projects/" + other + "/notes/" + pinned, otherSession, `{"content":"foreign"}`}, {"DELETE", base + "/" + pinned, otherSession, ""}} {
		status, _, data = issueRequest(t, h, tc.method, tc.path, tc.session, key(t), `"r-1"`, tc.body)
		if status != 404 {
			t.Fatal(tc, status, string(data))
		}
	}
}
