package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/board"
	"github.com/yoophi/td-backend/internal/application/port"
)

func boardFrom(t *testing.T, data []byte) receiptcodec.BoardDTO {
	t.Helper()
	var response struct {
		Data struct {
			Board receiptcodec.BoardDTO `json:"board"`
		} `json:"data"`
	}
	if e := json.Unmarshal(data, &response); e != nil || response.Data.Board.ID == "" {
		t.Fatalf("board response %s: %v", data, e)
	}
	return response.Data.Board
}
func boardsFrom(t *testing.T, h *harness, base, session string) []receiptcodec.BoardDTO {
	t.Helper()
	status, _, data := issueRequest(t, h, "GET", base, session, "", "", "")
	var response struct {
		Data struct {
			Boards []receiptcodec.BoardDTO `json:"boards"`
		} `json:"data"`
	}
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if e := json.Unmarshal(data, &response); e != nil {
		t.Fatal(e)
	}
	return response.Data.Boards
}
func TestBoardDefinitionLifecycleAndBuiltinProtection(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/boards"
	list := boardsFrom(t, h, base, session)
	if len(list) != 1 || !list[0].IsBuiltin || list[0].Revision != "r-1" || list[0].LastViewedAt != nil {
		t.Fatal(list)
	}
	builtin := base + "/" + list[0].ID
	for _, tc := range []struct{ method, body string }{{"PATCH", `{"name":"Changed"}`}, {"PATCH", `{"query":"status = open"}`}, {"DELETE", ""}} {
		status, _, data := issueRequest(t, h, tc.method, builtin, session, key(t), `"r-1"`, tc.body)
		if status != 403 {
			t.Fatal(status, string(data))
		}
	}
	status, etag, data := issueRequest(t, h, "PATCH", builtin, session, key(t), `"r-1"`, `{"view_mode":"backlog"}`)
	if status != 200 || etag != `"r-2"` || boardFrom(t, data).ViewMode != "backlog" {
		t.Fatal(status, etag, string(data))
	}
	for _, body := range []string{`{"name":"Missing query"}`, `{"name":" ","query":""}`, `{"name":"Invalid TDQ","query":"status ="}`, `{"name":"Bad field","query":"nonsense = yes"}`, `{"name":"Bad date","query":"due < someday"}`, `{"name":"Bad view","query":"","view_mode":""}`, `{"name":"Bad null","query":null}`, `{"name":"Unknown","query":"","extra":true}`} {
		status, _, data = issueRequest(t, h, "POST", base, session, key(t), "", body)
		if status != 400 {
			t.Fatal(body, status, string(data))
		}
	}
	op := key(t)
	body := `{"name":"  My work  ","query":"status != closed AND priority <= P2"}`
	status, etag, first := issueRequest(t, h, "POST", base, session, op, "", body)
	if status != 201 || etag != `"r-1"` {
		t.Fatal(status, etag, string(first))
	}
	b := boardFrom(t, first)
	if b.Name != "My work" || b.IsBuiltin || b.ViewMode != "swimlanes" {
		t.Fatal(b)
	}
	status, replay, data := issueRequest(t, h, "POST", base, session, op, "", body)
	if status != 201 || replay != etag || string(data) != string(first) {
		t.Fatal("create replay", status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", base, session, key(t), "", body)
	if status != 409 {
		t.Fatal("duplicate name", status, string(data))
	}
	path := base + "/" + b.ID
	for _, tc := range []struct {
		rev, body string
		status    int
	}{{"", `{"name":"Changed"}`, 428}, {`"r-2"`, `{"name":"Changed"}`, 412}, {`"r-1"`, `{}`, 400}} {
		status, _, data = issueRequest(t, h, "PATCH", path, session, key(t), tc.rev, tc.body)
		if status != tc.status {
			t.Fatal(tc, status, string(data))
		}
	}
	status, etag, data = issueRequest(t, h, "PATCH", path, session, key(t), `"r-1"`, `{"query":"","view_mode":"backlog"}`)
	if status != 200 || etag != `"r-2"` || boardFrom(t, data).Query != "" {
		t.Fatal(status, etag, string(data))
	}
	visitKey := key(t)
	status, etag, first = issueRequest(t, h, "POST", path+"/visit", session, visitKey, `"r-2"`, "")
	if status != 200 || etag != `"r-3"` || boardFrom(t, first).LastViewedAt == nil {
		t.Fatal(status, etag, string(first))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/visit", session, visitKey, `"r-2"`, "")
	if status != 200 || string(data) != string(first) {
		t.Fatal("visit replay", status, string(data))
	}
	deleteKey := key(t)
	status, etag, first = issueRequest(t, h, "DELETE", path, session, deleteKey, `"r-3"`, "")
	if status != 200 || etag != `"r-4"` {
		t.Fatal(status, etag, string(first))
	}
	status, _, data = issueRequest(t, h, "DELETE", path, session, deleteKey, `"r-3"`, "")
	if status != 200 || string(data) != string(first) {
		t.Fatal("delete replay", status, string(data))
	}
	if list = boardsFrom(t, h, base, session); len(list) != 1 {
		t.Fatal(list)
	}
	status, _, data = issueRequest(t, h, "POST", base, session, key(t), "", body)
	if status != 201 {
		t.Fatal("reuse deleted name", status, string(data))
	}
}

func TestBoardPositionsObservedAtomicityAndIsolation(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/boards"
	b := boardsFrom(t, h, base, session)[0]
	path := base + "/" + b.ID
	ids := []string{}
	for _, title := range []string{"First card", "Second card", "Third card"} {
		status, _, data := issueRequest(t, h, "POST", "/v1/projects/"+p+"/issues", session, key(t), "", fmt.Sprintf(`{"title":%q}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	read := func(wantRevision int64, want []string) {
		t.Helper()
		if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
			b, e := r.Boards().Get(r.Context(), p, b.ID, false)
			if e != nil {
				return e
			}
			if b.Revision != wantRevision {
				t.Fatal("revision", b.Revision, wantRevision)
			}
			positions, e := r.Boards().Positions(r.Context(), p, b.ID)
			if e != nil {
				return e
			}
			if len(positions) != len(want) {
				t.Fatal(positions, want)
			}
			for i, id := range want {
				if positions[i].IssueID != id {
					t.Fatal(positions, want)
				}
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	pin := func(id string, slot int, revision string, op string) (int, string, []byte) {
		return issueRequest(t, h, "POST", path+"/issues", session, op, revision, fmt.Sprintf(`{"issue_id":%q,"position":%d}`, id, slot))
	}
	for i, id := range ids[:2] {
		status, _, data := pin(id, i+1, fmt.Sprintf(`"r-%d"`, i+1), key(t))
		if status != 200 {
			t.Fatal(status, string(data))
		}
	}
	read(3, ids[:2])
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`CREATE TRIGGER fail_board_receipt BEFORE INSERT ON operations WHEN NEW.command_kind='board.position' BEGIN SELECT RAISE(ABORT,'receipt failed'); END`); e != nil {
		t.Fatal(e)
	}
	op := key(t)
	status, _, data := pin(ids[2], 2, `"r-3"`, op)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	read(3, ids[:2])
	if _, e = inspect.Exec(`DROP TRIGGER fail_board_receipt`); e != nil {
		t.Fatal(e)
	}
	status, etag, first := pin(ids[2], 2, `"r-3"`, op)
	if status != 200 || etag != `"r-4"` {
		t.Fatal(status, etag, string(first))
	}
	read(4, []string{ids[0], ids[2], ids[1]})
	status, _, data = pin(ids[2], 2, `"r-3"`, op)
	if status != 200 || string(data) != string(first) {
		t.Fatal("pin replay", status, string(data))
	}
	read(4, []string{ids[0], ids[2], ids[1]})
	var beforeJSON, afterJSON []byte
	var version int
	if e = inspect.QueryRow(`SELECT e.state_version,e.before_json,e.after_json FROM operation_effects e JOIN operations o ON e.project_id=o.project_id AND e.operation_row_id=o.row_id WHERE o.operation_id=?`, op).Scan(&version, &beforeJSON, &afterJSON); e != nil {
		t.Fatal(e)
	}
	var before, after board.State
	if e = json.Unmarshal(beforeJSON, &before); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(afterJSON, &after); e != nil {
		t.Fatal(e)
	}
	if version != 1 || before.Board.Revision != 3 || after.Board.Revision != 4 || len(before.Positions) != 2 || len(after.Positions) != 3 {
		t.Fatal("undo snapshots", string(beforeJSON), string(afterJSON))
	}
	status, _, data = issueRequest(t, h, "DELETE", path+"/issues/"+ids[2], session, key(t), `"r-4"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	read(5, ids[:2])
	// A second project shares the actor but must never accept another project's card.
	status, data = h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Other project"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	other := projectFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects/"+other+"/sessions", testToken, key(t), "", `{"context_id":"other"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	otherSession := sessionFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", "/v1/projects/"+other+"/issues", otherSession, key(t), "", `{"title":"Foreign project card"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	foreign := issueFrom(t, data).ID
	status, _, data = pin(foreign, 1, `"r-5"`, key(t))
	if status != 404 {
		t.Fatal("foreign pin", status, string(data))
	}
	status, _, data = issueRequest(t, h, "PATCH", "/v1/projects/"+other+"/boards/"+b.ID, otherSession, key(t), `"r-5"`, `{"view_mode":"backlog"}`)
	if status != 404 {
		t.Fatal("foreign board", status, string(data))
	}
	status, _, data = issueRequest(t, h, "PATCH", path, otherSession, key(t), `"r-5"`, `{"view_mode":"backlog"}`)
	if status != 404 {
		t.Fatal("foreign session", status, string(data))
	}
	read(5, ids[:2])
}
