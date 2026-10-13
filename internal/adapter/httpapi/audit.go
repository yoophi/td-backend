package httpapi

import (
	"net/http"
	"strconv"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerAudit(mux *http.ServeMux, svc *operation.AuditService, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	const base = "/v1/projects/{project_id}/operations"
	mux.HandleFunc("GET "+base+"/{operation_id}", protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		receipt, e := svc.Get(r.Context(), caller(r), r.PathValue("operation_id"))
		if e != nil {
			writeError(w, e)
			return
		}
		dto, e := receiptcodec.OperationDTOFrom(receipt)
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, struct {
			Operation receiptcodec.OperationDTO `json:"operation"`
		}{dto})
	}))
	mux.HandleFunc("GET "+base, protect(func(w http.ResponseWriter, r *http.Request) {
		f := port.AuditFilter{Limit: 200}
		for name, values := range r.URL.Query() {
			if len(values) != 1 {
				writeError(w, invalid(name, "parameter must occur once"))
				return
			}
			switch name {
			case "session_id":
				f.SessionID = values[0]
			case "issue_id":
				f.IssueID = values[0]
			case "limit", "offset":
				n, e := strconv.Atoi(values[0])
				if e != nil || n < 0 || (name == "limit" && n == 0) {
					writeError(w, invalid(name, "invalid pagination"))
					return
				}
				if name == "limit" {
					f.Limit = n
				} else {
					f.Offset = n
				}
			default:
				writeError(w, invalid(name, "unexpected query parameter"))
				return
			}
		}
		receipts, e := svc.List(r.Context(), caller(r), f)
		if e != nil {
			writeError(w, e)
			return
		}
		out := []receiptcodec.OperationDTO{}
		for _, receipt := range receipts {
			dto, e := receiptcodec.OperationDTOFrom(receipt)
			if e != nil {
				writeError(w, e)
				return
			}
			out = append(out, dto)
		}
		writeSuccess(w, 200, struct {
			Operations []receiptcodec.OperationDTO `json:"operations"`
		}{out})
	}))
}
