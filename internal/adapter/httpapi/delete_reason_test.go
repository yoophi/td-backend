package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/application/port"
)

func TestDeleteRestoreReasonAtomicAndReplay(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Reason fixture"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	created := issueFrom(t, data)
	path := base + "/" + created.ID
	op := key(t)
	status, etag, data := issueRequest(t, h, "DELETE", path, session, op, `"r-1"`, `{"reason":"maintenance"}`)
	if status != 200 || etag != `"r-2"` {
		t.Fatal(status, etag, string(data))
	}
	status, replayTag, replay := issueRequest(t, h, "DELETE", path, session, op, `"r-1"`, `{"reason":"maintenance"}`)
	if status != 200 || replayTag != etag || string(replay) != string(data) {
		t.Fatal("receipt mismatch", status, string(replay))
	}
	status, _, data = issueRequest(t, h, "DELETE", path, session, op, `"r-1"`, `{"reason":"other"}`)
	if status != 409 {
		t.Fatal("reason omitted from fingerprint", status, string(data))
	}
	status, _, data = issueRequest(t, h, "POST", path+"/restore", session, key(t), `"r-2"`, `{"reason":"back in scope"}`)
	if status != 200 || issueFrom(t, data).DeletedAt != nil {
		t.Fatal(status, string(data))
	}
	if e := h.store.Snapshot(context.Background(), func(read port.ReadSnapshot) error {
		activities, e := read.Workflow().Activities(read.Context(), p, created.ID, false)
		if e != nil {
			return e
		}
		if len(activities) != 2 || !strings.HasPrefix(activities[0].Message, "Deleted: maintenance") || !strings.HasPrefix(activities[1].Message, "Restored: back in scope") {
			t.Fatalf("reason activity mismatch: %+v", activities)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	status, _, data = issueRequest(t, h, "DELETE", path, session, key(t), `"r-3"`, `{"reason":"  "}`)
	if status != 400 {
		t.Fatal("blank reason accepted", status, string(data))
	}
	status, _, data = issueRequest(t, h, "DELETE", path, session, key(t), `"r-3"`, `{"unknown":"x"}`)
	if status != 400 {
		t.Fatal("unknown field accepted", status, string(data))
	}
}

func TestUndoDeleteReasonRestoresIssueAndHidesReasonLog(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p + "/issues"
	status, _, data := issueRequest(t, h, "POST", base, session, key(t), "", `{"title":"Undo deletion reason fixture"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	id := issueFrom(t, data).ID
	operation := key(t)
	status, _, data = issueRequest(t, h, "DELETE", base+"/"+id, session, operation, `"r-1"`, `{"reason":"temporary cleanup"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	status, data = doUndo(t, h, p, session, operation, key(t))
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if got := readIssueState(t, h, p, id); got.DeletedAt != nil || got.Revision != 3 {
		t.Fatal(got)
	}
	if e := h.store.Snapshot(context.Background(), func(read port.ReadSnapshot) error {
		activities, e := read.Workflow().Activities(read.Context(), p, id, false)
		if e != nil {
			return e
		}
		for _, a := range activities {
			if a.Message == "Deleted: temporary cleanup" {
				t.Fatal("undone reason still visible")
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
