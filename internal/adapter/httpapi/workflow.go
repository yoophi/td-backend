package httpapi

import (
	"github.com/yoophi/td-backend/internal/application/workflow"
	"github.com/yoophi/td-backend/internal/domain"
	"net/http"
)

func registerWorkflow(mux *http.ServeMux, svc *workflow.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	mux.HandleFunc("POST /v1/projects/{project_id}/issues/{issue_id}/release", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd workflow.ReleaseCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.ReleaseClaim(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))

	mux.HandleFunc("POST /v1/projects/{project_id}/issues/{issue_id}/status", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd workflow.StatusCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.SetStatus(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("POST /v1/projects/{project_id}/issues/{issue_id}/reviews", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd workflow.ReviewCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.RecordReview(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	registerActivities(mux, svc, protect, caller)
	for _, action := range []string{"start", "unstart", "block", "unblock", "reopen", "review", "approve", "close", "reject"} {
		mux.HandleFunc("POST /v1/projects/{project_id}/issues/{issue_id}/"+action, protect(func(w http.ResponseWriter, r *http.Request) {
			var cmd workflow.TransitionCommand
			if e := decode(w, r, &cmd); e != nil {
				writeError(w, e)
				return
			}
			m, e := mutation(r, cmd)
			if e != nil {
				writeError(w, e)
				return
			}
			reply, e := svc.Transition(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), action, cmd)
			if e != nil {
				writeError(w, e)
				return
			}
			writeReply(w, reply)
		}))
	}
	mux.HandleFunc("POST /v1/projects/{project_id}/issues/{issue_id}/handoffs", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd workflow.HandoffCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Handoff(r.Context(), caller(r), m, r.PathValue("issue_id"), r.Header.Get("If-Match"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
}
