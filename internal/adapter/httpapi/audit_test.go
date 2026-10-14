package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func TestOperationAuditReceiptFiltersEffectsAndIsolation(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	op := key(t)
	status, etag, original := issueRequest(t, h, "POST", root+"/issues", session, op, "", `{"title":"Implement durable receipt audit"}`)
	if status != 201 {
		t.Fatal(status, string(original))
	}
	id := issueFrom(t, original).ID
	status, _, data := issueRequest(t, h, "GET", root+"/operations/"+op, session, "", "", "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	var get struct {
		Data struct {
			Operation receiptcodec.OperationDTO `json:"operation"`
		}
	}
	if e := json.Unmarshal(data, &get); e != nil {
		t.Fatal(e)
	}
	got := get.Data.Operation
	if got.ID != op || got.SessionID != session || got.Kind != "issue.create" || !got.HasEffects || got.Undone || got.Method != "POST" || got.Path != root+"/issues" || got.Status != "committed" || got.ResponseStatus != 201 || issueFrom(t, got.Response).ID != id || strings.Contains(string(data), "fingerprint") || strings.Contains(string(data), "actor_id") {
		t.Fatal(string(data))
	}
	list := func(q string) []receiptcodec.OperationDTO {
		t.Helper()
		status, _, data := issueRequest(t, h, "GET", root+"/operations"+q, session, "", "", "")
		if status != 200 {
			t.Fatal(status, string(data))
		}
		var out struct {
			Data struct {
				Operations []receiptcodec.OperationDTO `json:"operations"`
			}
		}
		if e := json.Unmarshal(data, &out); e != nil {
			t.Fatal(e)
		}
		if out.Data.Operations == nil {
			t.Fatal("null list")
		}
		return out.Data.Operations
	}
	if out := list("?issue_id=" + id); len(out) != 1 || out[0].ID != op {
		t.Fatal(out)
	}
	if out := list("?issue_id=" + id + "&session_id=missing"); len(out) != 0 {
		t.Fatal(out)
	}
	if out := list("?offset=9223372036854775807"); len(out) != 0 {
		t.Fatal(out)
	}
	status, _, data = issueRequest(t, h, "PATCH", root+"/issues/"+id, session, key(t), etag, `{"description":"More durable context"}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	if out := list("?issue_id=" + id + "&limit=1&offset=1"); len(out) != 1 || out[0].ID != op {
		t.Fatal("page order", out)
	}
	var escaped port.OperationReader
	if e := h.store.Snapshot(context.Background(), func(r port.ReadSnapshot) error {
		escaped = r.Operations()
		receipt, e := escaped.AuditGet(r.Context(), p, op)
		if e != nil {
			return e
		}
		effects, e := escaped.Effects(r.Context(), p, receipt.RowID)
		if e != nil {
			return e
		}
		if len(effects) != 1 || effects[0].EntityID != id || effects[0].AfterRevision != "r-1" || effects[0].StateVersion != 2 || effects[0].BeforeJSON != nil || !json.Valid(effects[0].AfterJSON) {
			t.Fatal(effects)
		}
		other, e := escaped.Effects(r.Context(), "other-project", receipt.RowID)
		if e != nil {
			return e
		}
		if len(other) != 0 {
			t.Fatal("effect project leak", other)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := escaped.List(context.Background(), p, port.AuditFilter{Limit: 1}); !errors.Is(e, domain.ErrScopeClosed) {
		t.Fatal(e)
	}
	for _, q := range []string{"?limit=0", "?limit=1001", "?offset=-1", "?limit=1&limit=2", "?unknown=1"} {
		status, _, data = issueRequest(t, h, "GET", root+"/operations"+q, session, "", "", "")
		if status != 400 {
			t.Fatal(q, status, string(data))
		}
	}
	status, _, data = issueRequest(t, h, "GET", root+"/operations/invalid", session, "", "", "")
	if status != 400 {
		t.Fatal(status, string(data))
	}
	status, _, data = issueRequest(t, h, "GET", root+"/operations/"+key(t), session, "", "", "")
	if status != 404 {
		t.Fatal(status, string(data))
	}
	status, data = h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Other audit project"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	other := projectFrom(t, data).ID
	status, data = h.request("POST", "/v1/projects/"+other+"/sessions", testToken, key(t), "", `{"context_id":"other-audit"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	otherSession := sessionFrom(t, data).ID
	status, _, data = issueRequest(t, h, "GET", "/v1/projects/"+other+"/operations/"+op, otherSession, "", "", "")
	if status != 404 {
		t.Fatal(status, string(data))
	}
}

func TestAuditRejectsAmbiguousActorScopedOperationKey(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	op := key(t)
	status, _, data := issueRequest(t, h, "POST", root+"/issues", session, op, "", `{"title":"Verify actor scoped audit identity"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`INSERT INTO actors(id,name,can_create_project,created_at) VALUES('audit-other-actor','Other actor',0,'2026-10-13T00:00:00Z')`); e != nil {
		t.Fatal(e)
	}
	if _, e = inspect.Exec(`INSERT INTO operations(row_id,project_id,actor_id,session_id,scope_key,operation_id,fingerprint,command_kind,request_method,request_path,representation_version,response_status,response_headers,response_body,created_at) SELECT 'audit-other-row',project_id,'audit-other-actor',NULL,scope_key,operation_id,fingerprint,command_kind,request_method,request_path,representation_version,response_status,response_headers,response_body,created_at FROM operations WHERE project_id=? AND operation_id=?`, p, op); e != nil {
		t.Fatal(e)
	}
	status, _, data = issueRequest(t, h, "GET", root+"/operations/"+op, session, "", "", "")
	if status != 409 {
		t.Fatal("ambiguous receipt selected", status, string(data))
	}
	// Normal actor-scoped idempotency remains available and does not repeat writes.
	status, _, data = issueRequest(t, h, "POST", root+"/issues", session, op, "", `{"title":"Verify actor scoped audit identity"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
}
