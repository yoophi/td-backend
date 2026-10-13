package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/yoophi/td-backend/internal/application/port"
)

func doUndo(t *testing.T, h *harness, p, session, op, undoKey string) (int, []byte) {
	t.Helper()
	status, _, data := issueRequest(t, h, "POST", "/v1/projects/"+p+"/operations/"+op+"/undo", session, undoKey, "", `{"reason":"Restore verified previous state"}`)
	return status, data
}
func TestUndoNoteBoardFocusAndWorkSession(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	noteKey := key(t)
	status, _, data := issueRequest(t, h, "POST", root+"/notes", session, noteKey, "", `{"title":"Original note","content":"keep content"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	n := noteFrom(t, data)
	updateKey := key(t)
	status, _, data = issueRequest(t, h, "PATCH", root+"/notes/"+n.ID, session, updateKey, `"r-1"`, `{"title":"Changed note","content":""}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	undoKey := key(t)
	status, first := doUndo(t, h, p, session, updateKey, undoKey)
	if status != 200 {
		t.Fatal(status, string(first))
	}
	status, _, data = issueRequest(t, h, "GET", root+"/notes/"+n.ID, session, "", "", "")
	got := noteFrom(t, data)
	if status != 200 || got.Title != "Original note" || got.Content != "keep content" || got.Revision != "r-3" {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, updateKey, undoKey)
	if status != 200 || string(data) != string(first) {
		t.Fatal("undo replay", status, string(data))
	}
	status, data = doUndo(t, h, p, session, updateKey, key(t))
	if status != 409 {
		t.Fatal("repeat original", status, string(data))
	}
	status, data = doUndo(t, h, p, session, undoKey, key(t))
	if status != 409 {
		t.Fatal("undo undo", status, string(data))
	}
	deleteKey := key(t)
	status, _, data = issueRequest(t, h, "DELETE", root+"/notes/"+n.ID, session, deleteKey, `"r-3"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, deleteKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", root+"/notes/"+n.ID, session, "", "", "")
	if got = noteFrom(t, data); status != 200 || got.Revision != "r-5" {
		t.Fatal(status, string(data))
	}
	// Creation undo keeps a soft-deleted aggregate with a new revision.
	createKey := key(t)
	status, _, data = issueRequest(t, h, "POST", root+"/notes", session, createKey, "", `{"title":"Undo created note"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	created := noteFrom(t, data)
	status, data = doUndo(t, h, p, session, createKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", root+"/notes/"+created.ID, session, "", "", "")
	if status != 404 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", root+"/issues", session, key(t), "", `{"title":"Card for conditional board undo"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	issue := issueFrom(t, data).ID
	boardKey := key(t)
	status, _, data = issueRequest(t, h, "POST", root+"/boards", session, boardKey, "", `{"name":"Undo ordering","query":""}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	b := boardFrom(t, data)
	pinKey := key(t)
	status, _, data = issueRequest(t, h, "POST", root+"/boards/"+b.ID+"/issues", session, pinKey, `"r-1"`, fmt.Sprintf(`{"issue_id":%q,"position":1}`, issue))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, pinKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		board, e := r.Boards().Get(r.Context(), p, b.ID, false)
		if e != nil {
			return e
		}
		positions, e := r.Boards().Positions(r.Context(), p, b.ID)
		if board.Revision != 3 || len(positions) != 0 {
			t.Fatal(board, positions)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	boardDeleteKey := key(t)
	status, _, data = issueRequest(t, h, "DELETE", root+"/boards/"+b.ID, session, boardDeleteKey, `"r-3"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, boardDeleteKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", root+"/boards/"+b.ID, session, "", "", "")
	if status != 200 || boardFrom(t, data).Revision != "r-5" {
		t.Fatal("board restore", status, string(data))
	}
	newBoardKey := key(t)
	status, _, data = issueRequest(t, h, "POST", root+"/boards", session, newBoardKey, "", `{"name":"Archive created board","query":""}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	newBoard := boardFrom(t, data)
	status, data = doUndo(t, h, p, session, newBoardKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", root+"/boards/"+newBoard.ID, session, "", "", "")
	if status != 404 {
		t.Fatal("board creation inverse", status, string(data))
	}
	observerKey := key(t)
	status, data = h.request("POST", root+"/sessions", testToken, observerKey, "", `{"context_id":"undo-observer"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	observer := sessionFrom(t, data).ID
	status, data = doUndo(t, h, p, session, observerKey, key(t))
	if status != 409 {
		t.Fatal("session creation undo", status, string(data))
	}
	focusKey := key(t)
	status, _, data = issueRequest(t, h, "PUT", root+"/focus", session, focusKey, "", fmt.Sprintf(`{"issue_id":%q}`, issue))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, observer, focusKey, key(t))
	if status != 403 {
		t.Fatal("other focus owner", status, string(data))
	}
	status, data = doUndo(t, h, p, session, focusKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		f, e := r.Focus().Get(r.Context(), p, session)
		if f.Revision != 2 || f.IssueID != nil {
			t.Fatal(f)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	wsKey := key(t)
	status, _, data = issueRequest(t, h, "POST", root+"/work-sessions", session, wsKey, "", `{"name":"Conditional work session"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	w := workSessionFrom(t, data)
	tagKey := key(t)
	status, _, data = issueRequest(t, h, "PUT", root+"/work-sessions/"+w.ID+"/issues", session, tagKey, "", fmt.Sprintf(`{"issue_ids":[%q]}`, issue))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, observer, tagKey, key(t))
	if status != 403 {
		t.Fatal("other work session owner", status, string(data))
	}
	status, data = doUndo(t, h, p, session, tagKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	endKey := key(t)
	status, _, data = issueRequest(t, h, "POST", root+"/work-sessions/"+w.ID+"/end", session, endKey, "", `{"end_sha":"finished"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, endKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		w, e := r.WorkSessions().Get(r.Context(), p, w.ID)
		if w.Revision != 5 || w.EndedAt != nil || w.EndSHA != "" {
			t.Fatal(w)
		}
		tags, e := r.WorkSessions().Tags(r.Context(), p, w.ID)
		if len(tags) != 0 {
			t.Fatal(tags)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	wsCreateKey := key(t)
	status, _, data = issueRequest(t, h, "POST", root+"/work-sessions", session, wsCreateKey, "", `{"name":"Undo created work session"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	archived := workSessionFrom(t, data)
	status, data = doUndo(t, h, p, session, wsCreateKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", root+"/work-sessions/"+archived.ID, session, "", "", "")
	if status != 404 {
		t.Fatal(status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	var reason string
	if e = inspect.QueryRow(`SELECT reason FROM operation_undos u JOIN operations o ON o.row_id=u.operation_row_id WHERE o.operation_id=?`, undoKey).Scan(&reason); e != nil || reason != "Restore verified previous state" {
		t.Fatal("undo audit reason", reason, e)
	}
}
func TestUndoPreflightAllEffectsAndRollback(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	status, _, data := issueRequest(t, h, "POST", root+"/notes", session, key(t), "", `{"title":"Note before mixed inverse"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	n := noteFrom(t, data)
	noteKey := key(t)
	status, _, data = issueRequest(t, h, "PATCH", root+"/notes/"+n.ID, session, noteKey, `"r-1"`, `{"title":"Changed mixed note"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", root+"/boards", session, key(t), "", `{"name":"Board before mixed inverse","query":""}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	b := boardFrom(t, data)
	boardKey := key(t)
	status, _, data = issueRequest(t, h, "PATCH", root+"/boards/"+b.ID, session, boardKey, `"r-1"`, `{"name":"Changed mixed board"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	// Assemble a mixed effect set to exercise the generic all-aggregate boundary.
	if _, e = inspect.Exec(`INSERT INTO operation_effects SELECT e.project_id,(SELECT row_id FROM operations WHERE operation_id=?),e.entity_kind,e.entity_id,e.after_revision,e.state_version,e.before_json,e.after_json FROM operation_effects e JOIN operations o ON o.row_id=e.operation_row_id WHERE o.operation_id=?`, noteKey, boardKey); e != nil {
		t.Fatal(e)
	}
	if _, e = inspect.Exec(`CREATE TRIGGER fail_inverse_note BEFORE UPDATE ON notes BEGIN SELECT RAISE(ABORT,'inverse failed'); END`); e != nil {
		t.Fatal(e)
	}
	undoKey := key(t)
	status, data = doUndo(t, h, p, session, noteKey, undoKey)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	read := func(revision int64, name string) {
		t.Helper()
		if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
			b, e := r.Boards().Get(r.Context(), p, b.ID, false)
			if b.Revision != revision || b.Name != name {
				t.Fatal(b)
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	read(2, "Changed mixed board")
	if _, e = inspect.Exec(`DROP TRIGGER fail_inverse_note`); e != nil {
		t.Fatal(e)
	}
	status, data = doUndo(t, h, p, session, noteKey, undoKey)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	read(3, "Board before mixed inverse")
	// A changed first aggregate cannot be undone by a new key.
	status, data = doUndo(t, h, p, session, noteKey, key(t))
	if status != 409 {
		t.Fatal(status, string(data))
	}
	read(3, "Board before mixed inverse")
}

func TestUndoChecksLaterEffectBeforeWritingFirstAggregate(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	status, _, data := issueRequest(t, h, "POST", root+"/notes", session, key(t), "", `{"title":"Preflight later note"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	n := noteFrom(t, data)
	noteKey := key(t)
	status, _, data = issueRequest(t, h, "PATCH", root+"/notes/"+n.ID, session, noteKey, `"r-1"`, `{"title":"Updated preflight note"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", root+"/boards", session, key(t), "", `{"name":"Preflight first board","query":""}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	b := boardFrom(t, data)
	boardKey := key(t)
	status, _, data = issueRequest(t, h, "PATCH", root+"/boards/"+b.ID, session, boardKey, `"r-1"`, `{"name":"Updated preflight board"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`INSERT INTO operation_effects SELECT e.project_id,(SELECT row_id FROM operations WHERE operation_id=?),e.entity_kind,e.entity_id,e.after_revision,e.state_version,e.before_json,e.after_json FROM operation_effects e JOIN operations o ON o.row_id=e.operation_row_id WHERE o.operation_id=?`, noteKey, boardKey); e != nil {
		t.Fatal(e)
	}
	status, _, data = issueRequest(t, h, "PATCH", root+"/notes/"+n.ID, session, key(t), `"r-2"`, `{"content":"Later substantive note write"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, noteKey, key(t))
	if status != 409 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", root+"/boards/"+b.ID, session, "", "", "")
	if status != 200 || boardFrom(t, data).Revision != "r-2" || boardFrom(t, data).Name != "Updated preflight board" {
		t.Fatal("first aggregate changed", status, string(data))
	}
	for _, body := range []string{`{}`, `{"reason":" "}`, `{"reason":null}`, `{"reason":"valid","extra":true}`} {
		status, _, data = issueRequest(t, h, "POST", root+"/operations/"+noteKey+"/undo", session, key(t), "", body)
		if status != 400 {
			t.Fatal(status, string(data))
		}
	}
}
