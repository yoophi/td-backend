package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func undoIssueList(t *testing.T, data []byte) []receiptcodec.IssueDTO {
	t.Helper()
	var out struct {
		Data struct {
			Undone bool                    `json:"undone"`
			Issues []receiptcodec.IssueDTO `json:"affected_issues"`
		}
	}
	if e := json.Unmarshal(data, &out); e != nil || !out.Data.Undone || out.Data.Issues == nil {
		t.Fatal(e, string(data))
	}
	return out.Data.Issues
}
func readIssueState(t *testing.T, h *harness, p, id string) domain.Issue {
	t.Helper()
	var i domain.Issue
	if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		v, e := r.Issues().Get(r.Context(), p, id, true)
		if e == nil {
			i = *v
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	return i
}
func TestUndoIssueFieldsFilesAndLegacyVersion(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	createKey := key(t)
	status, _, data := issueRequest(t, h, "POST", root+"/issues", session, createKey, "", `{"title":"Original issue before undo","description":"Original description"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	id := issueFrom(t, data).ID
	path := root + "/issues/" + id
	updateKey := key(t)
	status, _, data = issueRequest(t, h, "PATCH", path, session, updateKey, `"r-1"`, `{"title":"Changed issue before undo","description":"Changed description"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	before := readIssueState(t, h, p, id)
	op := key(t)
	status, first := doUndo(t, h, p, session, updateKey, op)
	if status != 200 {
		t.Fatal(status, string(first))
	}
	items := undoIssueList(t, first)
	got := readIssueState(t, h, p, id)
	if len(items) != 1 || items[0].ID != id || got.Title != "Original issue before undo" || got.Description != "Original description" || got.Revision != 3 || got.ContentVersion != before.ContentVersion+1 {
		t.Fatal(items, got)
	}
	status, data = doUndo(t, h, p, session, updateKey, op)
	if status != 200 || string(data) != string(first) {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, createKey, key(t))
	if status != 409 {
		t.Fatal("later revision", status, string(data))
	}
	status, _, data = issueRequest(t, h, "PUT", path+"/files", session, key(t), `"r-3"`, `{"files":[{"file_path":"original.go","role":"implementation","linked_sha":"abc"}]}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	filesKey := key(t)
	status, _, data = issueRequest(t, h, "PUT", path+"/files", session, filesKey, `"r-4"`, `{"files":[{"file_path":"replacement_test.go","role":"test"}]}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, filesKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	checkFiles := func() {
		t.Helper()
		if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
			files, e := r.Issues().Files(r.Context(), p, id)
			if len(files) != 1 || files[0].FilePath != "original.go" || files[0].LinkedSHA != "abc" {
				t.Fatal(files)
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	checkFiles()
	legacyKey := key(t)
	status, _, data = issueRequest(t, h, "PATCH", path, session, legacyKey, `"r-6"`, `{"description":"Legacy change"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`UPDATE operation_effects SET state_version=1,before_json=json_remove(before_json,'$.files'),after_json=json_remove(after_json,'$.files') WHERE operation_row_id=(SELECT row_id FROM operations WHERE operation_id=?)`, legacyKey); e != nil {
		t.Fatal(e)
	}
	status, data = doUndo(t, h, p, session, legacyKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	checkFiles()
	got = readIssueState(t, h, p, id)
	if got.Revision != 8 || got.Description != "Original description" {
		t.Fatal(got)
	}
	deleteKey := key(t)
	status, _, data = issueRequest(t, h, "DELETE", path, session, deleteKey, `"r-8"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, deleteKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if got = readIssueState(t, h, p, id); got.DeletedAt != nil || got.Revision != 10 {
		t.Fatal(got)
	}
	newKey := key(t)
	status, _, data = issueRequest(t, h, "POST", root+"/issues", session, newKey, "", `{"title":"Undo fresh issue creation"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	fresh := issueFrom(t, data).ID
	status, data = doUndo(t, h, p, session, newKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if got = readIssueState(t, h, p, fresh); got.DeletedAt == nil || got.Revision != 2 {
		t.Fatal(got)
	}
}
func TestUndoActivityDeltaAndApprovalNeverRevives(t *testing.T) {
	h, p, session, path := reviewFixture(t, "trusted")
	id := strings.TrimPrefix(path, "/v1/projects/"+p+"/issues/")
	status, _, data := issueRequest(t, h, "POST", path+"/approve", session, key(t), `"r-3"`, `{"record_only":true,"self_review":true,"reason":"Verified approval"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	commentKey := key(t)
	status, _, data = issueRequest(t, h, "POST", path+"/comments", session, commentKey, `"r-4"`, `{"text":"Undo this appended comment"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	var comment struct {
		Data struct{ Comment domain.Comment }
	}
	if e := json.Unmarshal(data, &comment); e != nil {
		t.Fatal(e)
	}
	status, data = doUndo(t, h, p, session, commentKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	current := detailReviews(t, h, path, session)
	if current.ReviewerSession != nil || len(*current.Reviews) != 1 || (*current.Reviews)[0].SupersededAt == nil {
		t.Fatal("approval revived", current)
	}
	checkComment := func(deleted bool) {
		t.Helper()
		if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
			all, e := r.Workflow().Activities(r.Context(), p, id, true)
			if e != nil {
				return e
			}
			found := false
			for _, a := range all {
				if a.ID == comment.Data.Comment.ID {
					found = true
					if (a.DeletedAt != nil) != deleted {
						t.Fatal(a)
					}
				}
			}
			if !found {
				t.Fatal("audit activity deleted physically")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	checkComment(true)
	nextKey := key(t)
	status, _, data = issueRequest(t, h, "POST", path+"/comments", session, nextKey, `"r-6"`, `{"text":"Restore this deleted comment"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	if e := json.Unmarshal(data, &comment); e != nil {
		t.Fatal(e)
	}
	deleteKey := key(t)
	status, _, data = issueRequest(t, h, "DELETE", path+"/comments/"+comment.Data.Comment.ID, session, deleteKey, `"r-7"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	checkComment(true)
	status, data = doUndo(t, h, p, session, deleteKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	checkComment(false)
	if got := readIssueState(t, h, p, id); got.Revision != 9 || got.ReviewerSession != "" {
		t.Fatal(got)
	}
}
func TestUndoDependencyRestorationRejectsNewCycle(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p + "/issues"
	ids := []string{}
	for _, title := range []string{"First graph undo issue", "Second graph undo issue"} {
		status, _, data := issueRequest(t, h, "POST", root, session, key(t), "", fmt.Sprintf(`{"title":%q}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	status, _, data := issueRequest(t, h, "POST", root+"/"+ids[0]+"/dependencies", session, key(t), `"r-1"`, fmt.Sprintf(`{"depends_on":%q}`, ids[1]))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	var dep struct {
		Data struct{ Dependency receiptcodec.DependencyDTO }
	}
	if e := json.Unmarshal(data, &dep); e != nil {
		t.Fatal(e)
	}
	removeKey := key(t)
	status, _, data = issueRequest(t, h, "DELETE", root+"/"+ids[0]+"/dependencies/"+dep.Data.Dependency.ID, session, removeKey, `"r-2"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", root+"/"+ids[1]+"/dependencies", session, key(t), `"r-1"`, fmt.Sprintf(`{"depends_on":%q}`, ids[0]))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, removeKey, key(t))
	if status != 409 {
		t.Fatal("restored cycle", status, string(data))
	}
	if got := readIssueState(t, h, p, ids[0]); got.Revision != 3 {
		t.Fatal("cycle rollback", got)
	}
	if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		deps, e := r.Issues().Dependencies(r.Context(), p, ids[0])
		if len(deps) != 0 {
			t.Fatal(deps)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}

func TestUndoApprovalCascadePreservesHistoryAndSupersedesReviews(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p + "/issues"
	status, _, data := issueRequest(t, h, "POST", root, session, key(t), "", `{"title":"Parent epic for approval inverse","type":"epic"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	parent := issueFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", root, session, key(t), "", fmt.Sprintf(`{"title":"Child for approval inverse","parent_id":%q}`, parent))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	child := issueFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", root+"/"+parent+"/start", session, key(t), `"r-1"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", root+"/"+parent+"/handoffs", session, key(t), `"r-2"`, `{"done":["epic implemented"]}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	approveKey := key(t)
	status, _, data = issueRequest(t, h, "POST", root+"/"+parent+"/approve", session, approveKey, `"r-3"`, `{"self_review":true,"reason":"Verified descendant approval"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	history := map[string][]domain.IssueSessionHistory{}
	if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		for _, id := range []string{parent, child} {
			v, e := r.Workflow().History(r.Context(), p, id)
			if e != nil {
				return e
			}
			history[id] = v
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	later := parent
	if child > parent {
		later = child
	}
	if _, e = inspect.Exec(fmt.Sprintf(`CREATE TRIGGER fail_cascade_inverse BEFORE UPDATE ON issues WHEN OLD.id='%s' AND NEW.status<>OLD.status BEGIN SELECT RAISE(ABORT,'cascade inverse failed'); END`, later)); e != nil {
		t.Fatal(e)
	}
	undoKey := key(t)
	status, data = doUndo(t, h, p, session, approveKey, undoKey)
	if status != 409 {
		t.Fatal(status, string(data))
	}
	for _, id := range []string{parent, child} {
		i := readIssueState(t, h, p, id)
		if i.Status != domain.StatusClosed || i.ReviewerSession == "" {
			t.Fatal("partial cascade inverse", i)
		}
	}
	if _, e = inspect.Exec(`DROP TRIGGER fail_cascade_inverse`); e != nil {
		t.Fatal(e)
	}
	status, data = doUndo(t, h, p, session, approveKey, undoKey)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if items := undoIssueList(t, data); len(items) != 2 {
		t.Fatal(items)
	}
	for _, tc := range []struct {
		id     string
		status domain.Status
		rev    int64
	}{{parent, domain.StatusInReview, 5}, {child, domain.StatusOpen, 3}} {
		i := readIssueState(t, h, p, tc.id)
		if i.Status != tc.status || i.Revision != tc.rev || i.ReviewerSession != "" || i.ReviewedAt != nil {
			t.Fatal(i)
		}
		if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
			v, e := r.Workflow().History(r.Context(), p, tc.id)
			if e != nil {
				return e
			}
			if len(v) != len(history[tc.id]) {
				t.Fatal("history removed", v, history[tc.id])
			}
			for n := range v {
				if v[n] != history[tc.id][n] {
					t.Fatal("history changed", v)
				}
			}
			active, e := r.Workflow().ActiveReview(r.Context(), p, tc.id)
			if active != nil {
				t.Fatal("old approval active", active)
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
		status, _, data = issueRequest(t, h, "GET", root+"/"+tc.id+"?with=reviews", session, "", "", "")
		if status != 200 {
			t.Fatal(status, string(data))
		}
		reviewed := issueFrom(t, data)
		if reviewed.Reviews == nil || len(*reviewed.Reviews) != 1 || (*reviewed.Reviews)[0].SupersededAt == nil {
			t.Fatal("review audit missing", reviewed)
		}
	}
	status, _, data = issueRequest(t, h, "POST", root+"/"+parent+"/close", session, key(t), `"r-5"`, `{}`)
	if status != 403 {
		t.Fatal("old approval revived by inverse", status, string(data))
	}
}
func TestUndoHandoffRestoresReworkAndParentCycleRollsBack(t *testing.T) {
	h, p, session, path := reviewFixture(t, "trusted")
	status, _, data := issueRequest(t, h, "POST", path+"/reject", session, key(t), `"r-3"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/start", session, key(t), `"r-4"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	handoffKey := key(t)
	status, _, data = issueRequest(t, h, "POST", path+"/handoffs", session, handoffKey, `"r-5"`, `{"done":["new review attempt"]}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, handoffKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if cards := bucket(t, getMonitor(t, h, p, session, ""), "needs_rework"); len(cards) != 1 {
		t.Fatal("undone review hid rework", cards)
	}
	root := "/v1/projects/" + p + "/issues"
	status, _, data = issueRequest(t, h, "POST", root, session, key(t), "", `{"title":"First parent cycle inverse"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	a := issueFrom(t, data).ID
	status, _, data = issueRequest(t, h, "POST", root, session, key(t), "", fmt.Sprintf(`{"title":"Second parent cycle inverse","parent_id":%q}`, a))
	if status != 201 {
		t.Fatal(status, string(data))
	}
	b := issueFrom(t, data).ID
	detachKey := key(t)
	status, _, data = issueRequest(t, h, "PATCH", root+"/"+b, session, detachKey, `"r-1"`, `{"parent_id":""}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "PATCH", root+"/"+a, session, key(t), `"r-1"`, fmt.Sprintf(`{"parent_id":%q}`, b))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, detachKey, key(t))
	if status != 409 {
		t.Fatal("parent cycle committed", status, string(data))
	}
	if got := readIssueState(t, h, p, b); got.ParentID != "" || got.Revision != 2 {
		t.Fatal("parent inverse rollback", got)
	}
}

func TestUndoUnsupportedVersionsAndProjectCreationDoNotWrite(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	op := key(t)
	status, _, data := issueRequest(t, h, "POST", root+"/issues", session, op, "", `{"title":"Future effect version guard"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	id := issueFrom(t, data).ID
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`UPDATE operation_effects SET state_version=99 WHERE operation_row_id=(SELECT row_id FROM operations WHERE operation_id=?)`, op); e != nil {
		t.Fatal(e)
	}
	status, data = doUndo(t, h, p, session, op, key(t))
	if status != 409 {
		t.Fatal(status, string(data))
	}
	if i := readIssueState(t, h, p, id); i.Revision != 1 || i.DeletedAt != nil {
		t.Fatal(i)
	}
	var projectKey string
	if e = inspect.QueryRow(`SELECT operation_id FROM operations WHERE project_id=? AND command_kind='project.create'`, p).Scan(&projectKey); e != nil {
		t.Fatal(e)
	}
	status, data = doUndo(t, h, p, session, projectKey, key(t))
	if status != 409 {
		t.Fatal("project creation inverse", status, string(data))
	}
}
