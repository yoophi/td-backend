package httpapi

import (
	"net/http"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/worksession"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerWorkSessions(mux *http.ServeMux, svc *worksession.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	const base = "/v1/projects/{project_id}/work-sessions"
	mux.HandleFunc("GET "+base, protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		sessions, e := svc.List(r.Context(), caller(r))
		if e != nil {
			writeError(w, e)
			return
		}
		data := []receiptcodec.WorkSessionDTO{}
		for _, s := range sessions {
			data = append(data, receiptcodec.WorkSessionDTOFrom(s))
		}
		writeSuccess(w, 200, struct {
			WorkSessions []receiptcodec.WorkSessionDTO `json:"work_sessions"`
		}{data})
	}))
	mux.HandleFunc("GET "+base+"/{work_session_id}", protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		s, e := svc.Get(r.Context(), caller(r), r.PathValue("work_session_id"))
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, struct {
			WorkSession receiptcodec.WorkSessionDTO `json:"work_session"`
			IssueIDs    []string                    `json:"issue_ids"`
		}{receiptcodec.WorkSessionDTOFrom(s.WorkSession), receiptcodec.WorkSessionIssueIDs(s)})
	}))
	mux.HandleFunc("GET "+base+"/{work_session_id}/activity", protect(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			writeError(w, invalid("query", "no query parameters supported"))
			return
		}
		list, e := svc.ListActivity(r.Context(), caller(r), r.PathValue("work_session_id"))
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
	mux.HandleFunc("GET "+base+"/{work_session_id}/logs", protect(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			writeError(w, invalid("query", "no query parameters supported"))
			return
		}
		logs, e := svc.ListLogs(r.Context(), caller(r), r.PathValue("work_session_id"))
		if e != nil {
			writeError(w, e)
			return
		}
		result := make([]receiptcodec.LogDTO, 0, len(logs))
		for _, log := range logs {
			result = append(result, receiptcodec.WorkSessionLogDTOFrom(log))
		}
		writeSuccess(w, 200, struct {
			Logs []receiptcodec.LogDTO `json:"logs"`
		}{result})
	}))
	mux.HandleFunc("POST "+base+"/{work_session_id}/logs", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd worksession.LogCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Log(r.Context(), caller(r), m, r.PathValue("work_session_id"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("POST "+base, protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd worksession.StartCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Start(r.Context(), caller(r), m, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("PUT "+base+"/{work_session_id}/issues", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd worksession.TagCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Tag(r.Context(), caller(r), m, r.PathValue("work_session_id"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("POST "+base+"/{work_session_id}/end", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd worksession.EndCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.End(r.Context(), caller(r), m, r.PathValue("work_session_id"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
}
