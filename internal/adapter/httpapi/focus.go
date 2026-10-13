package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/yoophi/td-backend/internal/application/focus"
	"github.com/yoophi/td-backend/internal/domain"
)

type focusDTO struct {
	FocusedIssueID *string `json:"focused_issue_id"`
}

func registerFocus(mux *http.ServeMux, svc *focus.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	const path = "/v1/projects/{project_id}/focus"
	mux.HandleFunc("GET "+path, protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		f, e := svc.Get(r.Context(), caller(r))
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, focusDTO{f.IssueID})
	}))
	mux.HandleFunc("PUT "+path, protect(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IssueID json.RawMessage `json:"issue_id"`
		}
		if e := decodeNullable(w, r, &body, map[string]bool{"issue_id": true}); e != nil {
			writeError(w, e)
			return
		}
		if len(body.IssueID) == 0 {
			writeError(w, invalid("issue_id", "issue_id required; null clears focus"))
			return
		}
		var cmd focus.SetCommand
		if e := json.Unmarshal(body.IssueID, &cmd.IssueID); e != nil {
			writeError(w, invalid("issue_id", "expected string or null"))
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Set(r.Context(), caller(r), m, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
}
