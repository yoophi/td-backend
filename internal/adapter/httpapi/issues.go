package httpapi

import (
	"io"
	"net/http"

	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerIssueMutations(mux *http.ServeMux, svc *issue.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	finish := func(w http.ResponseWriter, reply port.StoredReply, e error) {
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}
	mux.HandleFunc("POST /v1/projects/{project_id}/issues", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd issue.CreateCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Create(r.Context(), caller(r), m, cmd)
		finish(w, reply, e)
	}))
	mux.HandleFunc("PATCH /v1/projects/{project_id}/issues/{issue_id}", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd issue.PatchCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Patch(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), cmd)
		finish(w, reply, e)
	}))
	for _, restore := range []bool{false, true} {
		route := "DELETE /v1/projects/{project_id}/issues/{issue_id}"
		if restore {
			route = "POST /v1/projects/{project_id}/issues/{issue_id}/restore"
		}
		mux.HandleFunc(route, protect(func(w http.ResponseWriter, r *http.Request) {
			// These operations have no body. Reject ignored payloads rather than allowing
			// different requests to share a fingerprint which discarded their content.
			data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
			if e != nil || len(data) > 0 {
				writeError(w, invalid("body", "request body must be empty"))
				return
			}
			m, e := mutation(r, struct{}{})
			if e != nil {
				writeError(w, e)
				return
			}
			var reply port.StoredReply
			if restore {
				reply, e = svc.Restore(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"))
			} else {
				reply, e = svc.Delete(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"))
			}
			finish(w, reply, e)
		}))
	}
}
