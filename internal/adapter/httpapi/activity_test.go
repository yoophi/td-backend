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

func TestActivityApprovalPreservationDeleteAndPassiveHandoff(t *testing.T) {
	h, _, session, path := reviewFixture(t, "trusted")
	status, _, data := issueRequest(t, h, "POST", path+"/approve", session, key(t), `"r-3"`, `{"record_only":true,"self_review":true,"reason":"Tested"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	commentKey := key(t)
	status, etag, first := issueRequest(t, h, "POST", path+"/comments", session, commentKey, `"r-4"`, `{"text":"Comment after approval"}`)
	if status != 201 || etag != `"r-5"` {
		t.Fatal(status, etag, string(first))
	}
	var comment struct {
		Data struct{ Comment domain.Comment }
	}
	if e := json.Unmarshal(first, &comment); e != nil {
		t.Fatal(e)
	}
	if comment.Data.Comment.SessionID != session || comment.Data.Comment.ID == "" {
		t.Fatal(string(first))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/comments", session, commentKey, `"r-4"`, `{"text":"Comment after approval"}`)
	if status != 201 || string(data) != string(first) {
		t.Fatal("comment replay changed")
	}
	status, etag, data = issueRequest(t, h, "POST", path+"/logs", session, key(t), `"r-5"`, `{"message":"Post-review check","type":"result"}`)
	if status != 201 || etag != `"r-6"` {
		t.Fatal(status, etag, string(data))
	}
	current := detailReviews(t, h, path, session)
	if current.ReviewerSession == nil || (*current.Reviews)[0].SupersededAt != nil {
		t.Fatal("passive log invalidated approval")
	}
	deleteKey := key(t)
	status, etag, data = issueRequest(t, h, "DELETE", path+"/comments/"+comment.Data.Comment.ID, session, deleteKey, `"r-6"`, "")
	if status != 200 || etag != `"r-7"` {
		t.Fatal(status, etag, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", path+"/activity", session, "", "", "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	var list struct {
		Data struct{ Activity []receiptcodec.ActivityDTO }
	}
	if e := json.Unmarshal(data, &list); e != nil {
		t.Fatal(e)
	}
	for _, a := range list.Data.Activity {
		if a.ID == comment.Data.Comment.ID {
			t.Fatal("deleted comment visible")
		}
		if !domain.ValidOperationKey(a.OperationID) {
			t.Fatalf("internal operation ID exposed: %s", a.OperationID)
		}
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	var count, version int
	if e = inspect.QueryRow(`SELECT count(*) FROM activities WHERE id=? AND deleted_at IS NOT NULL`, comment.Data.Comment.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("comment audit lost", count, e)
	}
	if e = inspect.QueryRow(`SELECT state_version FROM operation_effects e JOIN operations o ON e.operation_row_id=o.row_id WHERE o.operation_id=?`, deleteKey).Scan(&version); e != nil || version != 3 {
		t.Fatal("activity delta version", version, e)
	}
	status, etag, data = issueRequest(t, h, "POST", path+"/activity", session, key(t), `"r-7"`, `{"kind":"handoff","done":["Updated context"],"snapshot":{"branch":"main","commit_sha":"abc"}}`)
	if status != 201 || etag != `"r-8"` {
		t.Fatal(status, etag, string(data))
	}
	current = detailReviews(t, h, path, session)
	if current.Status != domain.StatusInReview || current.ReviewerSession != nil || (*current.Reviews)[0].SupersededAt == nil {
		t.Fatal("handoff did not replace review basis")
	}
}
func TestActivityWorkSessionOwnershipAndValidation(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Work session activity"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	path := base + "/" + issueFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", `{"context_id":"other"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	other := sessionFrom(t, data).ID
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	for _, row := range []struct {
		id, owner string
		ended     any
	}{{"own", session, nil}, {"foreign", other, nil}, {"ended", session, "2026-10-13T00:00:00Z"}} {
		if _, e = inspect.Exec(`INSERT INTO work_sessions(project_id,id,session_id,name,started_at,ended_at) VALUES(?,?,?,'test','2026-10-13T00:00:00Z',?)`, p, row.id, row.owner, row.ended); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		id     string
		status int
	}{{"foreign", 403}, {"ended", 409}, {"missing", 404}} {
		status, _, data = issueRequest(t, h, "POST", path+"/logs", session, key(t), `"r-1"`, fmt.Sprintf(`{"message":"Log entry","type":"progress","work_session_id":%q}`, tc.id))
		if status != tc.status {
			t.Fatal(tc, status, string(data))
		}
	}
	for _, body := range []string{`{"kind":"invalid"}`, `{"kind":"log","message":"missing type"}`, `{"kind":"comment","message":" "}`, `{"kind":"handoff","done":[null]}`, `{"kind":"handoff","snapshot":{"branch":null}}`, `{"kind":"log","message":"test","log_type":"result","done":["ignored"]}`} {
		status, _, data = issueRequest(t, h, "POST", path+"/activity", session, key(t), `"r-1"`, body)
		if status != 400 {
			t.Fatal(body, status, string(data))
		}
	}
	status, etag, data := issueRequest(t, h, "POST", path+"/logs", session, key(t), `"r-1"`, `{"message":"Owned log","type":"progress","work_session_id":"own"}`)
	if status != 201 || etag != `"r-2"` || !strings.Contains(string(data), `"work_session_id":"own"`) {
		t.Fatal(status, etag, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/start", session, key(t), `"r-2"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/activity", session, key(t), `"r-3"`, `{"kind":"handoff","done":[]}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", path, session, "", "", "")
	if status != 200 || issueFrom(t, data).Status != domain.StatusInProgress {
		t.Fatal("passive handoff submitted review", status, string(data))
	}
}
