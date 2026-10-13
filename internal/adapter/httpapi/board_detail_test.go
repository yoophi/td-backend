package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
)

func boardDetail(t *testing.T, h *harness, path, session string) receiptcodec.BoardDetailDTO {
	t.Helper()
	status, _, data := issueRequest(t, h, "GET", path, session, "", "", "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	var result struct {
		Data receiptcodec.BoardDetailDTO `json:"data"`
	}
	if e := json.Unmarshal(data, &result); e != nil {
		t.Fatal(e)
	}
	return result.Data
}
func assertCards(t *testing.T, d receiptcodec.BoardDetailDTO, want []string) {
	t.Helper()
	ids := []string{}
	for _, v := range d.Issues {
		ids = append(ids, v.Issue.ID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatal(ids, want)
	}
}

func TestBoardQueryDetailAndMoveAtomicReplay(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	ids := []string{}
	for n, title := range []string{"Alpha bug", "Beta bug", "Charlie bug", "Hidden task"} {
		kind := "bug"
		if n == 3 {
			kind = "task"
		}
		status, _, data := issueRequest(t, h, "POST", root+"/issues", session, key(t), "", fmt.Sprintf(`{"title":%q,"type":%q,"description":"full description","acceptance":"full acceptance","minor":true}`, title, kind))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	status, _, data := issueRequest(t, h, "POST", root+"/boards", session, key(t), "", `{"name":"Bugs","query":"type = bug sort:title"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	b := boardFrom(t, data)
	path := root + "/boards/" + b.ID
	d := boardDetail(t, h, path, session)
	assertCards(t, d, ids[:3])
	if d.Issues[0].Issue.Description != "full description" || d.Issues[0].Issue.Acceptance != "full acceptance" {
		t.Fatal("incomplete card DTO", d.Issues[0])
	}
	status, _, data = issueRequest(t, h, "POST", path+"/issues", session, key(t), `"r-1"`, fmt.Sprintf(`{"issue_id":%q,"position":1}`, ids[3]))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	moveKey := key(t)
	body := fmt.Sprintf(`{"issue_id":%q,"before_id":%q}`, ids[2], ids[1])
	status, etag, first := issueRequest(t, h, "POST", path+"/move", session, moveKey, `"r-2"`, body)
	if status != 200 || etag != `"r-3"` {
		t.Fatal(status, etag, string(first))
	}
	d = boardDetail(t, h, path, session)
	assertCards(t, d, []string{ids[0], ids[2], ids[1]})
	if !d.Issues[0].HasPosition || !d.Issues[1].HasPosition || d.Issues[2].HasPosition {
		t.Fatal("incorrect positioned prefix", d.Issues)
	}
	status, _, data = issueRequest(t, h, "POST", path+"/move", session, moveKey, `"r-2"`, body)
	if status != 200 || string(data) != string(first) {
		t.Fatal("move replay", status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/move", session, key(t), `"r-2"`, body)
	if status != 412 {
		t.Fatal("stale move", status, string(data))
	}
	for _, body := range []string{fmt.Sprintf(`{"issue_id":%q,"before_id":%q}`, ids[0], ids[3]), fmt.Sprintf(`{"issue_id":%q}`, ids[3])} {
		status, _, data = issueRequest(t, h, "POST", path+"/move", session, key(t), `"r-3"`, body)
		if status != 409 {
			t.Fatal("hidden card accepted", status, string(data))
		}
	}
	status, _, data = issueRequest(t, h, "POST", root+"/issues/"+ids[0]+"/close", session, key(t), `"r-1"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	assertCards(t, boardDetail(t, h, path, session), []string{ids[2], ids[1]})
	body = fmt.Sprintf(`{"issue_id":%q}`, ids[0])
	status, _, data = issueRequest(t, h, "POST", path+"/move", session, key(t), `"r-3"`, body)
	if status != 409 {
		t.Fatal("closed hidden card moved", status, string(data))
	}
	body = fmt.Sprintf(`{"issue_id":%q,"include_closed":true}`, ids[0])
	status, _, data = issueRequest(t, h, "POST", path+"/move", session, key(t), `"r-3"`, body)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	assertCards(t, boardDetail(t, h, path+"?include_closed=true", session), []string{ids[2], ids[1], ids[0]})
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(fmt.Sprintf(`CREATE TRIGGER reject_move BEFORE INSERT ON board_positions WHEN NEW.issue_id='%s' BEGIN SELECT RAISE(ABORT,'move failed'); END`, ids[1])); e != nil {
		t.Fatal(e)
	}
	op := key(t)
	body = fmt.Sprintf(`{"issue_id":%q,"before_id":%q}`, ids[1], ids[2])
	status, _, data = issueRequest(t, h, "POST", path+"/move", session, op, `"r-4"`, body)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	d = boardDetail(t, h, path, session)
	assertCards(t, d, []string{ids[2], ids[1]})
	if d.Board.Revision != "r-4" {
		t.Fatal(d.Board)
	}
	if _, e = inspect.Exec(`DROP TRIGGER reject_move`); e != nil {
		t.Fatal(e)
	}
	status, _, data = issueRequest(t, h, "POST", path+"/move", session, op, `"r-4"`, body)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	assertCards(t, boardDetail(t, h, path, session), []string{ids[1], ids[2]})
	var hiddenKey int
	if e = inspect.QueryRow(`SELECT position FROM board_positions WHERE project_id=? AND board_id=? AND issue_id=?`, p, b.ID, ids[3]).Scan(&hiddenKey); e != nil {
		t.Fatal(e)
	}
	if hiddenKey != 65536 {
		t.Fatal("hidden key changed", hiddenKey)
	}
	for _, query := range []string{"?include_closed=1", "?include_closed=true&include_closed=false", "?other=true"} {
		status, _, data = issueRequest(t, h, "GET", path+query, session, "", "", "")
		if status != 400 {
			t.Fatal(query, status, string(data))
		}
	}
}

func TestBoardQueriesReadActivityRelationsAndRework(t *testing.T) {
	h, p, session, issuePath := reviewFixture(t, "trusted")
	root := "/v1/projects/" + p
	issueID := strings.TrimPrefix(issuePath, root+"/issues/")
	status, _, data := issueRequest(t, h, "POST", issuePath+"/reject", session, key(t), `"r-3"`, `{"reason":"Fix tests"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", issuePath+"/comments", session, key(t), `"r-4"`, `{"text":"needs regression tests"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "PUT", issuePath+"/files", session, key(t), `"r-5"`, `{"files":[{"file_path":"tests/api.go","role":"test"}]}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	query := `rework() AND comment.text ~ regression AND file.role = test AND handoff.done ~ implemented`
	status, _, data = issueRequest(t, h, "POST", root+"/boards", session, key(t), "", fmt.Sprintf(`{"name":"Rework","query":%q}`, query))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	path := root + "/boards/" + boardFrom(t, data).ID
	assertCards(t, boardDetail(t, h, path, session), []string{issueID})
	// A new handoff submission clears the prior rejection. Returning to work
	// without a second rejection must not resurrect the rework category.
	status, _, data = issueRequest(t, h, "POST", issuePath+"/start", session, key(t), `"r-6"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", issuePath+"/handoffs", session, key(t), `"r-7"`, `{"done":["implemented again"]}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", issuePath+"/status", session, key(t), `"r-8"`, `{"status":"in_progress"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	assertCards(t, boardDetail(t, h, path, session), []string{})
}
