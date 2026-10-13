package httpapi

import (
	"net/http"

	"github.com/yoophi/td-backend/internal/application/undo"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerUndo(mux *http.ServeMux, svc *undo.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	mux.HandleFunc("POST /v1/projects/{project_id}/operations/{operation_id}/undo", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd undo.Command
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Run(r.Context(), caller(r), m, r.PathValue("operation_id"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
}
