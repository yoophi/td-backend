package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/domain"
)

func issueListOptions(v url.Values) (issue.ListOptions, error) {
	q := issue.ListOptions{Limit: 200}
	arrays := map[string]*[]string{"status": &q.Statuses, "type": &q.Types, "priority": &q.Priorities, "labels": &q.Labels}
	strings := map[string]*string{"epic": &q.Epic, "parent_id": &q.ParentID, "search": &q.Search, "search_mode": &q.SearchMode, "sort": &q.Sort, "order": &q.Order}
	for name, values := range v {
		if field, ok := arrays[name]; ok {
			*field = values
			continue
		}
		if len(values) != 1 {
			return q, invalid(name, "parameter must occur once")
		}
		value := values[0]
		if field, ok := strings[name]; ok {
			*field = value
			continue
		}
		switch name {
		case "limit", "offset":
			n, e := strconv.Atoi(value)
			if e != nil || n < 0 || (name == "limit" && n == 0) {
				return q, invalid(name, "invalid pagination value")
			}
			if name == "limit" {
				q.Limit = n
			} else {
				q.Offset = n
			}
		case "include_closed", "with_deleted":
			if value != "true" && value != "false" {
				return q, invalid(name, "expected true or false")
			}
			if name == "include_closed" {
				q.IncludeClosed = value == "true"
			} else {
				q.WithDeleted = value == "true"
			}
		default:
			return q, invalid(name, "unknown query parameter")
		}
	}
	return q, nil
}
func registerIssueList(mux *http.ServeMux, svc *issue.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	mux.HandleFunc("GET /v1/projects/{project_id}/issues", protect(func(w http.ResponseWriter, r *http.Request) {
		q, e := issueListOptions(r.URL.Query())
		if e != nil {
			writeError(w, e)
			return
		}
		list, e := svc.List(r.Context(), caller(r), q)
		if e != nil {
			writeError(w, e)
			return
		}
		records := make([]receiptcodec.IssueDTO, 0, len(list.Issues))
		for _, i := range list.Issues {
			records = append(records, receiptcodec.IssueDTOFrom(i))
		}
		writeSuccess(w, 200, struct {
			Issues      []receiptcodec.IssueDTO `json:"issues"`
			Total       int                     `json:"total"`
			Limit       int                     `json:"limit"`
			Offset      int                     `json:"offset"`
			HasMore     bool                    `json:"has_more"`
			ChangeToken string                  `json:"change_token"`
		}{records, list.Total, list.Limit, list.Offset, list.HasMore, list.ChangeToken})
	}))
}
