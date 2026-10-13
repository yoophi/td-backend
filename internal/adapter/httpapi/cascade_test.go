package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/domain"
	"testing"
)

func TestCloseCascadeAtomicParentsAndDependentClaims(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	create := func(title, kind, parent string, deps []string) string {
		t.Helper()
		body, e := json.Marshal(map[string]any{"title": title, "type": kind, "parent_id": parent, "depends_on": deps})
		if deps == nil {
			body, e = json.Marshal(map[string]any{"title": title, "type": kind, "parent_id": parent})
		}
		if e != nil {
			t.Fatal(e)
		}
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", string(body))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		return issueFrom(t, data).ID
	}
	grand := create("Grand epic", "epic", "", nil)
	parent := create("Parent epic", "epic", grand, nil)
	one := create("First child", "task", parent, nil)
	two := create("Second child", "task", parent, nil)
	extra := create("Remaining prerequisite", "task", "", nil)
	dependent := create("Dependent issue", "task", "", []string{parent, two})
	waiting := create("Still blocked issue", "task", "", []string{parent, extra})
	for _, id := range []string{dependent, waiting} {
		status, _, data := issueRequest(t, h, "POST", base+"/"+id+"/start", session, key(t), `"r-1"`, `{}`)
		if status != 200 {
			t.Fatal(status, string(data))
		}
		status, _, data = issueRequest(t, h, "POST", base+"/"+id+"/block", session, key(t), `"r-2"`, `{}`)
		if status != 200 {
			t.Fatal(status, string(data))
		}
	}
	status, _, data := issueRequest(t, h, "POST", base+"/"+one+"/close", session, key(t), `"r-1"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(fmt.Sprintf(`CREATE TRIGGER reject_unblock BEFORE UPDATE ON issues WHEN OLD.id='%s' BEGIN SELECT RAISE(ABORT,'dependent failure'); END`, dependent)); e != nil {
		t.Fatal(e)
	}
	op := key(t)
	path := base + "/" + two + "/close"
	status, _, data = issueRequest(t, h, "POST", path, session, op, `"r-1"`, `{}`)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	for _, id := range []string{two, parent, grand} {
		status, etag, data := issueRequest(t, h, "GET", base+"/"+id, session, "", "", "")
		if status != 200 || etag != `"r-1"` || issueFrom(t, data).Status != domain.StatusOpen {
			t.Fatal("partial cascade", status, etag, string(data))
		}
	}
	if _, e = inspect.Exec(`DROP TRIGGER reject_unblock`); e != nil {
		t.Fatal(e)
	}
	status, _, first := issueRequest(t, h, "POST", path, session, op, `"r-1"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(first))
	}
	var response struct {
		Data struct {
			Parents   []receiptcodec.IssueDTO `json:"parent_status_updates"`
			Unblocked []receiptcodec.IssueDTO `json:"auto_unblocked"`
		}
	}
	if e = json.Unmarshal(first, &response); e != nil {
		t.Fatal(e)
	}
	if len(response.Data.Parents) != 2 || response.Data.Parents[0].ID != parent || response.Data.Parents[1].ID != grand || len(response.Data.Unblocked) != 1 || response.Data.Unblocked[0].ID != dependent || response.Data.Unblocked[0].ImplementerSession != nil {
		t.Fatalf("cascade response %s", first)
	}
	status, _, data = issueRequest(t, h, "POST", path, session, op, `"r-1"`, `{}`)
	if status != 200 || string(data) != string(first) {
		t.Fatal("cascade replay differs")
	}
	status, etag, data := issueRequest(t, h, "GET", base+"/"+waiting, session, "", "", "")
	if status != 200 || etag != `"r-3"` || issueFrom(t, data).Status != domain.StatusBlocked {
		t.Fatal("premature unblock", status, etag, string(data))
	}
	var effects int
	if e = inspect.QueryRow(`SELECT count(*) FROM operation_effects e JOIN operations o ON e.operation_row_id=o.row_id WHERE o.operation_id=?`, op).Scan(&effects); e != nil || effects != 4 {
		t.Fatal("aggregate audit", effects, e)
	}
}
func TestNonEpicParentDoesNotCascade(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Ordinary parent task"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	parent := issueFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", base, session, key(t), "", fmt.Sprintf(`{"title":"Ordinary child task","parent_id":%q}`, parent))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	child := issueFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", base+"/"+child+"/close", session, key(t), `"r-1"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, etag, data := issueRequest(t, h, "GET", base+"/"+parent, session, "", "", "")
	if status != 200 || etag != `"r-1"` || issueFrom(t, data).Status != domain.StatusOpen {
		t.Fatal(status, etag, string(data))
	}
}
