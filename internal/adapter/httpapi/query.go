package httpapi

import (
	"net/http"

	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerQuery(mux *http.ServeMux, svc *issue.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	mux.HandleFunc("GET /v1/projects/{project_id}/query", protect(func(w http.ResponseWriter, r *http.Request) {
		values := r.URL.Query()
		if len(values) != 1 || len(values["q"]) != 1 {
			writeError(w, invalid("q", "exactly one q parameter required"))
			return
		}
		ids, e := svc.Query(r.Context(), caller(r), values.Get("q"))
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, struct {
			IDs []string `json:"ids"`
		}{ids})
	}))
}
