package httpapi

import (
	"io"
	"net/http"
	"net/url"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/workflow"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerActivities(mux *http.ServeMux, svc *workflow.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	for _, kind := range []string{"comments", "logs", "activity"} {
		mux.HandleFunc("POST /v1/projects/{project_id}/issues/{issue_id}/"+kind, protect(func(w http.ResponseWriter, r *http.Request) {
			var cmd any
			switch kind {
			case "comments":
				cmd = &workflow.CommentCommand{}
			case "logs":
				cmd = &workflow.LogCommand{}
			case "activity":
				cmd = &workflow.ActivityCommand{}
			}
			if e := decode(w, r, cmd); e != nil {
				writeError(w, e)
				return
			}
			m, e := mutation(r, cmd)
			if e != nil {
				writeError(w, e)
				return
			}
			c := caller(r)
			id, revision := r.PathValue("issue_id"), r.Header.Get("If-Match")
			var reply port.StoredReply
			switch v := cmd.(type) {
			case *workflow.CommentCommand:
				reply, e = svc.Comment(r.Context(), c, m, id, revision, *v)
			case *workflow.LogCommand:
				reply, e = svc.Log(r.Context(), c, m, id, revision, *v)
			case *workflow.ActivityCommand:
				reply, e = svc.AppendActivity(r.Context(), c, m, id, revision, *v)
			}
			if e != nil {
				writeError(w, e)
				return
			}
			writeReply(w, reply)
		}))
	}
	mux.HandleFunc("GET /v1/projects/{project_id}/issues/{issue_id}/activity", protect(func(w http.ResponseWriter, r *http.Request) {
		withDeleted := false
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeError(w, invalid("query", "invalid query parameters"))
			return
		}
		for name, values := range query {
			if name != "with_deleted" || len(values) != 1 || values[0] != "true" && values[0] != "false" {
				writeError(w, invalid("query", "expected one optional with_deleted=true or false"))
				return
			}
			withDeleted = values[0] == "true"
		}
		list, e := svc.ListActivity(r.Context(), caller(r), r.PathValue("issue_id"), withDeleted)
		if e != nil {
			writeError(w, e)
			return
		}
		result := make([]receiptcodec.ActivityDTO, 0, len(list))
		for _, a := range list {
			result = append(result, receiptcodec.ActivityDTOFrom(a))
		}
		writeSuccess(w, 200, struct {
			Activity []receiptcodec.ActivityDTO `json:"activity"`
		}{result})
	}))
	mux.HandleFunc("DELETE /v1/projects/{project_id}/issues/{issue_id}/comments/{comment_id}", protect(func(w http.ResponseWriter, r *http.Request) {
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
		reply, e := svc.DeleteComment(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), r.PathValue("comment_id"))
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
}
