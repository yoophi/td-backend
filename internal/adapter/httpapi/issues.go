package httpapi

import (
	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"io"
	"net/http"
	"strconv"

	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerIssueMutations(mux *http.ServeMux, svc *issue.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {

	registerIssueList(mux, svc, protect, caller)
	registerRelations(mux, svc, protect, caller)
	mux.HandleFunc("GET /v1/projects/{project_id}/issues/{issue_id}", protect(func(w http.ResponseWriter, r *http.Request) {
		values := r.URL.Query()
		for k, v := range values {
			if (k != "with" && k != "with_deleted") || len(v) != 1 {
				writeError(w, invalid(k, "unknown or repeated query parameter"))
				return
			}
		}
		reviews := values.Get("with") == "reviews"
		if _, present := values["with"]; present && !reviews {
			writeError(w, invalid("with", "expected reviews"))
			return
		}
		deleted := false
		if value, present := values["with_deleted"]; present {
			if value[0] != "true" && value[0] != "false" {
				writeError(w, invalid("with_deleted", "expected true or false"))
				return
			}
			deleted, _ = strconv.ParseBool(value[0])
		}
		detail, e := svc.Detail(r.Context(), caller(r), r.PathValue("issue_id"), deleted, reviews)
		if e != nil {
			writeError(w, e)
			return
		}
		w.Header().Set("ETag", `"`+domain.Revision(detail.Issue.Revision)+`"`)
		writeSuccess(w, 200, receiptcodec.DetailDTOFrom(detail, reviews))
	}))
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
