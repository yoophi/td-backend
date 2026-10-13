package receiptcodec

import (
	"encoding/json"
	"fmt"
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type Codec struct{}

func (Codec) Encode(kind string, outcome any) (port.StoredReply, error) {
	status := 201
	headers := map[string]string{"Content-Type": "application/json"}
	switch kind {
	case "project.create", "session.create":
	case "issue.create", "issue.update", "issue.delete", "issue.restore":
		result, ok := outcome.(issue.Result)
		if !ok {
			return port.StoredReply{}, fmt.Errorf("invalid issue outcome")
		}
		headers["ETag"] = `"` + domain.Revision(result.Issue.Revision) + `"`
		if kind != "issue.create" {
			status = 200
		}
		outcome = struct {
			Issue IssueDTO `json:"issue"`
		}{IssueDTOFrom(result.Issue)}
		if kind == "issue.delete" {
			outcome = struct {
				Deleted bool `json:"deleted"`
			}{true}
		}

	default:
		return port.StoredReply{}, fmt.Errorf("unknown reply kind %q", kind)
	}
	body, e := json.Marshal(struct {
		OK   bool `json:"ok"`
		Data any  `json:"data"`
	}{true, outcome})
	if e != nil {
		return port.StoredReply{}, e
	}
	return port.StoredReply{Status: status, Headers: headers, Body: append(body, '\n')}, nil
}
