package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
)

func TestDependencyCyclesRemovalOwnershipAndProjectIsolation(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	ids := []string{}
	for _, title := range []string{"First relation issue", "Second relation issue", "Third relation issue"} {
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", fmt.Sprintf(`{"title":%q}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	add := func(source, target, revision string) (int, []byte) {
		t.Helper()
		status, _, data := issueRequest(t, h, "POST", base+"/"+source+"/dependencies", session, key(t), revision, fmt.Sprintf(`{"depends_on":%q}`, target))
		return status, data
	}
	status, data := add(ids[0], ids[1], `"r-1"`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	var response struct {
		Data struct{ Dependency receiptcodec.DependencyDTO }
	}
	if e := json.Unmarshal(data, &response); e != nil {
		t.Fatal(e)
	}
	edge := response.Data.Dependency
	if edge.ID == "" || edge.IssueID != ids[0] || edge.DependsOnID != ids[1] {
		t.Fatal(string(data))
	}
	status, data = add(ids[1], ids[2], `"r-1"`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	status, data = add(ids[2], ids[0], `"r-1"`)
	if status != 400 {
		t.Fatal("cycle accepted", status, string(data))
	}
	status, data = add(ids[0], ids[0], `"r-2"`)
	if status != 400 {
		t.Fatal("self cycle accepted", status, string(data))
	}
	status, _, data = issueRequest(t, h, "DELETE", base+"/"+ids[1]+"/dependencies/"+edge.ID, session, key(t), `"r-2"`, "")
	if status != 404 {
		t.Fatal("wrong source removed relation", status, string(data))
	}
	status, etag, data := issueRequest(t, h, "DELETE", base+"/"+ids[0]+"/dependencies/"+edge.ID, session, key(t), `"r-2"`, "")
	if status != 200 || etag != `"r-3"` || !strings.Contains(string(data), `"removed":true`) {
		t.Fatal(status, etag, string(data))
	}
	status, data = h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Other relation project"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	other := projectFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects/"+other+"/sessions", testToken, key(t), "", `{"context_id":"other"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	otherSession := sessionFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", "/v1/projects/"+other+"/issues", otherSession, key(t), "", `{"title":"Foreign project issue"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	foreign := issueFrom(t, data).ID
	status, data = add(ids[0], foreign, `"r-3"`)
	if status != 404 {
		t.Fatal("cross project dependency", status, string(data))
	}
}
func TestFileReplacementInvalidatesReviewAndRollsBack(t *testing.T) {
	h, _, session, path := reviewFixture(t, "trusted")
	status, _, data := issueRequest(t, h, "POST", path+"/approve", session, key(t), `"r-3"`, `{"record_only":true,"self_review":true,"reason":"Verified"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	for _, body := range []string{`{}`, `{"files":null}`, `{"files":[{"file_path":"","role":"test"}]}`, `{"files":[{"file_path":"a","role":"bad"}]}`, `{"files":[{"file_path":"a","role":"test"},{"file_path":"a","role":"test"}]}`} {
		status, _, data = issueRequest(t, h, "PUT", path+"/files", session, key(t), `"r-4"`, body)
		if status != 400 {
			t.Fatal(body, status, string(data))
		}
	}
	status, etag, data := issueRequest(t, h, "PUT", path+"/files", session, key(t), `"r-4"`, `{"files":[{"file_path":"src/main.go","role":"implementation","linked_sha":"abc"}]}`)
	if status != 200 || etag != `"r-5"` {
		t.Fatal(status, etag, string(data))
	}
	i := detailReviews(t, h, path, session)
	if i.ReviewerSession != nil || (*i.Reviews)[0].SupersededAt == nil {
		t.Fatal("file edit retained approval")
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`CREATE TRIGGER reject_file BEFORE INSERT ON issue_files BEGIN SELECT RAISE(ABORT,'file failure'); END`); e != nil {
		t.Fatal(e)
	}
	op := key(t)
	body := `{"files":[{"file_path":"new.go","role":"test"}]}`
	status, _, data = issueRequest(t, h, "PUT", path+"/files", session, op, `"r-5"`, body)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	status, etag, data = issueRequest(t, h, "GET", path, session, "", "", "")
	if status != 200 || etag != `"r-5"` {
		t.Fatal(status, etag, string(data))
	}
	var detail struct{ Data receiptcodec.IssueDetailDTO }
	if e = json.Unmarshal(data, &detail); e != nil {
		t.Fatal(e)
	}
	if len(detail.Data.Files) != 1 || detail.Data.Files[0].FilePath != "src/main.go" {
		t.Fatal("failed replacement lost previous files")
	}
	if _, e = inspect.Exec(`DROP TRIGGER reject_file`); e != nil {
		t.Fatal(e)
	}
	status, _, data = issueRequest(t, h, "PUT", path+"/files", session, op, `"r-5"`, body)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	clearKey := key(t)
	status, etag, data = issueRequest(t, h, "PUT", path+"/files", session, clearKey, `"r-6"`, `{"files":[]}`)
	if status != 200 || etag != `"r-7"` {
		t.Fatal(status, etag, string(data))
	}
	var version, before, after int
	e = inspect.QueryRow(`SELECT state_version,json_array_length(before_json,'$.files'),json_array_length(after_json,'$.files') FROM operation_effects e JOIN operations o ON e.operation_row_id=o.row_id WHERE o.operation_id=?`, clearKey).Scan(&version, &before, &after)
	if e != nil || version != 2 || before != 1 || after != 0 {
		t.Fatal("file audit state", version, before, after, e)
	}
}
