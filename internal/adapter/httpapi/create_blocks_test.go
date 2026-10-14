package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/yoophi/td-backend/internal/application/port"
)

func TestCreateBlocksAtomicReplayStaleRollbackAndUndo(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	makeIssue := func(title string) string {
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", fmt.Sprintf(`{"title":%q}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		return issueFrom(t, data).ID
	}
	a, b := makeIssue("First blocked issue"), makeIssue("Second blocked issue")
	requestKey := key(t)
	body := fmt.Sprintf(`{"title":"New blocker issue","blocks":[{"issue_id":%q,"revision":"r-1"},{"issue_id":%q,"revision":"r-2"}]}`, a, b)
	status, _, data := issueRequest(t, h, "POST", base, session, requestKey, "", body)
	if status != 412 {
		t.Fatal(status, string(data))
	}
	assertState := func(total, edges int, aRev, bRev int64) {
		if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
			all, e := r.Issues().All(r.Context(), p, false)
			if e != nil {
				return e
			}
			if len(all) != total {
				t.Fatal("partial creation", len(all), total)
			}
			for _, v := range []struct {
				id  string
				rev int64
			}{{a, aRev}, {b, bRev}} {
				i, e := r.Issues().Get(r.Context(), p, v.id, false)
				if e != nil {
					return e
				}
				deps, e := r.Issues().Dependencies(r.Context(), p, v.id)
				if e != nil {
					return e
				}
				if i.Revision != v.rev || len(deps) != edges {
					t.Fatal("partial relation", i.Revision, deps)
				}
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	assertState(2, 0, 1, 1)
	status, _, data = issueRequest(t, h, "PATCH", base+"/"+b, session, key(t), `"r-1"`, `{"title":"Second issue updated"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, etag, first := issueRequest(t, h, "POST", base, session, requestKey, "", body)
	if status != 201 {
		t.Fatal(status, string(first))
	}
	created := issueFrom(t, first)
	assertState(3, 1, 2, 3)
	status, replayedETag, replayed := issueRequest(t, h, "POST", base, session, requestKey, "", body)
	if status != 201 || etag != replayedETag || string(first) != string(replayed) {
		t.Fatal("replay did not preserve receipt", status, string(replayed))
	}
	assertState(3, 1, 2, 3)
	status, data = doUndo(t, h, p, session, requestKey, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if len(undoIssueList(t, data)) != 3 {
		t.Fatal("not all effects undone", string(data))
	}
	if readIssueState(t, h, p, created.ID).DeletedAt == nil {
		t.Fatal("created issue remained live")
	}
	assertState(2, 0, 3, 4)
}
func TestCreateBlocksRejectsCycleAndDuplicateObservations(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		h, p, session := issueSetup(t)
		base := "/v1/projects/" + p + "/issues"
		status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Existing target issue"}`)
		if status != 201 {
			t.Fatal(status, string(data))
		}
		id := issueFrom(t, data).ID
		body := fmt.Sprintf(`{"title":"Cycle blocker issue","depends_on":[%q],"blocks":[{"issue_id":%q,"revision":"r-1"}]}`, id, id)
		if duplicate {
			body = fmt.Sprintf(`{"title":"Duplicate observations","blocks":[{"issue_id":%q,"revision":"r-1"},{"issue_id":%q,"revision":"r-1"}]}`, id, id)
		}
		status, _, data = issueRequest(t, h, "POST", base, session, key(t), "", body)
		if status != 400 {
			t.Fatal(status, string(data))
		}
		if i := readIssueState(t, h, p, id); i.Revision != 1 {
			t.Fatal("failed create changed target", i)
		}
		if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
			all, e := r.Issues().All(r.Context(), p, false)
			if len(all) != 1 {
				t.Fatal("failed create persisted root", all)
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
}
