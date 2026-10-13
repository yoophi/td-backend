package httpapi

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/workflow"
	"github.com/yoophi/td-backend/internal/domain"
	"github.com/yoophi/td-backend/internal/domain/reviewpolicy"
)

func TestBatchPolicyFactsAgreeWithWorkflowReads(t *testing.T) {
	h, p, session, path := reviewFixture(t, "trusted")
	id := strings.TrimPrefix(path, "/v1/projects/"+p+"/issues/")
	var escaped port.WorkflowReader
	check := func(wantApproval, deleted bool) {
		t.Helper()
		if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
			escaped = r.Workflow()
			batch, e := escaped.Facts(r.Context(), p)
			if e != nil {
				return e
			}
			if deleted {
				if len(batch.History) != 0 || len(batch.ActiveReviews) != 0 || len(batch.LatestHandoffs) != 0 {
					t.Fatal("deleted facts leaked", batch)
				}
				return nil
			}
			history, e := escaped.History(r.Context(), p, id)
			if e != nil {
				return e
			}
			review, e := escaped.ActiveReview(r.Context(), p, id)
			if e != nil {
				return e
			}
			handoff, e := escaped.LatestHandoff(r.Context(), p, id)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(batch.History[id], history) || !reflect.DeepEqual(batch.ActiveReviews[id], review) || !reflect.DeepEqual(batch.LatestHandoffs[id], handoff) {
				t.Fatal("batch differs from single reads", batch, history, review, handoff)
			}
			i, e := r.Issues().Get(r.Context(), p, id, false)
			if e != nil {
				return e
			}
			f := workflow.Facts{Issue: *i, SessionID: session, Mode: reviewpolicy.ModeTrusted, History: batch.History[id], ActiveReview: batch.ActiveReviews[id], Handoff: batch.LatestHandoffs[id]}
			if f.HasActiveApproval() != wantApproval {
				t.Fatal("approval freshness", f)
			}
			other, e := escaped.Facts(r.Context(), "other-project")
			if e != nil {
				return e
			}
			if len(other.History) != 0 || len(other.ActiveReviews) != 0 || len(other.LatestHandoffs) != 0 {
				t.Fatal("project leak", other)
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	check(false, false)
	status, _, data := issueRequest(t, h, "POST", path+"/approve", session, key(t), `"r-3"`, `{"self_review":true,"reason":"Verified batch inputs","record_only":true}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	check(true, false)
	status, _, data = issueRequest(t, h, "PATCH", path, session, key(t), `"r-4"`, `{"description":"Invalidate prior review basis"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	check(false, false)
	status, _, data = issueRequest(t, h, "DELETE", path, session, key(t), `"r-5"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	check(false, true)
	if _, e := escaped.Facts(context.Background(), p); !errors.Is(e, domain.ErrScopeClosed) {
		t.Fatal("escaped facts reader", e)
	}
}
