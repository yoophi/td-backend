package httpapi

import (
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/domain"
	"io"
	"net/http"
)

func registerRelations(mux *http.ServeMux, svc *issue.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	mux.HandleFunc("POST /v1/projects/{project_id}/issues/{issue_id}/dependencies", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd issue.DependencyCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.AddDependency(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("DELETE /v1/projects/{project_id}/issues/{issue_id}/dependencies/{dep_id}", protect(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if e != nil || len(body) > 0 {
			writeError(w, invalid("body", "request body must be empty"))
			return
		}
		m, e := mutation(r, struct{}{})
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.RemoveDependency(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), r.PathValue("dep_id"))
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("PUT /v1/projects/{project_id}/issues/{issue_id}/files", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd issue.FilesCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.ReplaceFiles(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
}
