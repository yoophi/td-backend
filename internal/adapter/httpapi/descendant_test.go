package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/domain"
)

func TestParentApprovalDescendantsAndAtomicRollback(t *testing.T) {
	h, p, session, parentPath := reviewFixture(t, "trusted")
	base := "/v1/projects/" + p + "/issues"
	parent := parentPath[strings.LastIndex(parentPath, "/")+1:]
	create := func(title, parent string) string {
		t.Helper()
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", fmt.Sprintf(`{"title":%q,"parent_id":%q}`, title, parent))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		return issueFrom(t, data).ID
	}
	open := create("Open descendant issue", parent)
	progress := create("Working descendant issue", parent)
	blocked := create("Blocked descendant issue", parent)
	nested := create("Nested below blocked issue", blocked)
	for _, id := range []string{progress, blocked} {
		status, _, data := issueRequest(t, h, "POST", base+"/"+id+"/start", session, key(t), `"r-1"`, `{}`)
		if status != 200 {
			t.Fatal(status, string(data))
		}
	}
	status, _, data := issueRequest(t, h, "POST", base+"/"+blocked+"/block", session, key(t), `"r-2"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(fmt.Sprintf(`CREATE TRIGGER reject_descendant BEFORE UPDATE ON issues WHEN OLD.id='%s' BEGIN SELECT RAISE(ABORT,'descendant failure'); END`, nested)); e != nil {
		t.Fatal(e)
	}
	op := key(t)
	body := `{"self_review":true,"reason":"Reviewed parent scope"}`
	status, _, data = issueRequest(t, h, "POST", parentPath+"/approve", session, op, `"r-3"`, body)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	current := detailReviews(t, h, parentPath, session)
	if current.Status != domain.StatusInReview || current.Revision != "r-3" || len(*current.Reviews) != 0 {
		t.Fatal("partial parent approval")
	}
	for _, id := range []string{open, nested} {
		status, etag, data := issueRequest(t, h, "GET", base+"/"+id, session, "", "", "")
		if status != 200 || etag != `"r-1"` || issueFrom(t, data).Status != domain.StatusOpen {
			t.Fatal("partial descendant", status, etag, string(data))
		}
	}
	if _, e = inspect.Exec(`DROP TRIGGER reject_descendant`); e != nil {
		t.Fatal(e)
	}
	status, _, first := issueRequest(t, h, "POST", parentPath+"/approve", session, op, `"r-3"`, body)
	if status != 200 {
		t.Fatal(status, string(first))
	}
	var response struct {
		Data struct {
			Descendants []receiptcodec.IssueDTO `json:"descendant_status_updates"`
		}
	}
	if e = json.Unmarshal(first, &response); e != nil {
		t.Fatal(e)
	}
	if len(response.Data.Descendants) != 3 {
		t.Fatal(string(first))
	}
	for _, id := range []string{open, progress, nested} {
		i := detailReviews(t, h, base+"/"+id, session)
		if i.Status != domain.StatusClosed || i.ClosedBySession == nil || *i.ClosedBySession != session || len(*i.Reviews) != 1 || (*i.Reviews)[0].Decision != "approved_by_parent_cascade" {
			t.Fatalf("descendant %+v", i)
		}
	}
	status, etag, data := issueRequest(t, h, "GET", base+"/"+blocked, session, "", "", "")
	if status != 200 || etag != `"r-3"` || issueFrom(t, data).Status != domain.StatusBlocked {
		t.Fatal("blocked descendant closed", status, etag, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", parentPath+"/approve", session, op, `"r-3"`, body)
	if status != 200 || string(data) != string(first) {
		t.Fatal("descendant replay changed")
	}
}
func TestExplicitCloseExceptionsCannotBypassReview(t *testing.T) {
	h, _, session, path := reviewFixture(t, "trusted")
	for _, body := range []string{`{"admin_reason":"cleanup"}`, `{"self_close_exception":"my cleanup"}`} {
		status, _, data := issueRequest(t, h, "POST", path+"/close", session, key(t), `"r-3"`, body)
		if status != 403 {
			t.Fatal("review bypass", status, string(data))
		}
	}
	status, _, data := issueRequest(t, h, "POST", path+"/reject", session, key(t), `"r-3"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/close", session, key(t), `"r-4"`, `{}`)
	if status != 403 {
		t.Fatal("implementation history bypass", status, string(data))
	}
	status, etag, data := issueRequest(t, h, "POST", path+"/close", session, key(t), `"r-4"`, `{"self_close_exception":"Obsolete work; intentionally abandoned"}`)
	if status != 200 || etag != `"r-5"` {
		t.Fatal(status, etag, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", path+"/activity", session, "", "", "")
	if status != 200 || !strings.Contains(string(data), `"log_type":"security"`) || !strings.Contains(string(data), "Self-close exception") {
		t.Fatal("missing exception audit", status, string(data))
	}
}
