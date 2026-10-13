package httpapi

import (
	"net/http"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/monitor"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerStatistics(mux *http.ServeMux, svc *monitor.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	const base = "/v1/projects/{project_id}"
	mux.HandleFunc("GET "+base+"/stats", protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		stats, e := svc.Stats(r.Context(), caller(r))
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, receiptcodec.StatsDTOFrom(stats))
	}))
	mux.HandleFunc("GET "+base+"/labels", protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		labels, e := svc.Labels(r.Context(), caller(r))
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, struct {
			Labels          []string `json:"labels"`
			Workflows       []any    `json:"workflows"`
			DefaultWorkflow string   `json:"default_workflow"`
		}{labels, []any{}, "standard"})
	}))
}
