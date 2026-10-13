package httpapi

import (
	"net/http"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/monitor"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerMonitor(mux *http.ServeMux, svc *monitor.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	mux.HandleFunc("GET /v1/projects/{project_id}/monitor", protect(func(w http.ResponseWriter, r *http.Request) {
		opts := monitor.Options{}
		for name, values := range r.URL.Query() {
			if len(values) != 1 {
				writeError(w, invalid(name, "parameter must occur once"))
				return
			}
			value := values[0]
			switch name {
			case "include_closed":
				if value != "true" && value != "false" {
					writeError(w, invalid(name, "expected true or false"))
					return
				}
				opts.IncludeClosed = value == "true"
			case "search":
				opts.Search = value
			case "search_mode":
				opts.SearchMode = value
			case "sort":
				opts.Sort = value
			case "board_id":
				opts.BoardID = value
			default:
				writeError(w, invalid(name, "unexpected query parameter"))
				return
			}
		}
		out, e := svc.Get(r.Context(), caller(r), opts)
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, receiptcodec.MonitorDTOFrom(out))
	}))
}
