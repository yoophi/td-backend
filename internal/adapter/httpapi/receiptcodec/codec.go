package receiptcodec

import (
	"encoding/json"
	"fmt"
	"github.com/yoophi/td-backend/internal/application/board"
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/workflow"
	"github.com/yoophi/td-backend/internal/domain"
)

type Codec struct{}

func (Codec) Encode(kind string, outcome any) (port.StoredReply, error) {
	status := 201
	headers := map[string]string{"Content-Type": "application/json"}
	switch kind {
	case "project.create", "session.create":
	case "board.create", "board.update", "board.delete", "board.visit", "board.position", "board.unpin", "board.move":
		result, ok := outcome.(board.Result)
		if !ok {
			return port.StoredReply{}, fmt.Errorf("invalid board outcome")
		}
		headers["ETag"] = `"` + domain.Revision(result.Board.Revision) + `"`
		if kind != "board.create" {
			status = 200
		}
		outcome = struct {
			Board BoardDTO `json:"board"`
		}{BoardDTOFrom(result.Board)}
		switch kind {
		case "board.delete":
			outcome = struct {
				Deleted bool `json:"deleted"`
			}{true}
		case "board.position", "board.move":
			outcome = struct {
				Positioned bool `json:"positioned"`
			}{true}
		case "board.unpin":
			outcome = struct {
				Removed bool `json:"removed"`
			}{true}
		}
	case "issue.create", "issue.update", "issue.delete", "issue.restore", "relation.remove", "relation.files":
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
		if kind == "relation.remove" {
			outcome = struct {
				Removed bool `json:"removed"`
			}{true}
		}
		if kind == "issue.delete" {
			outcome = struct {
				Deleted bool `json:"deleted"`
			}{true}
		}

	case "relation.add":
		result, ok := outcome.(issue.DependencyResult)
		if !ok {
			return port.StoredReply{}, fmt.Errorf("invalid dependency outcome")
		}
		headers["ETag"] = `"` + domain.Revision(result.Issue.Revision) + `"`
		outcome = struct {
			Dependency DependencyDTO `json:"dependency"`
		}{dependenciesDTO([]port.Dependency{result.Dependency})[0]}
	case "activity.append", "activity.comment", "activity.log", "activity.delete":
		result, ok := outcome.(workflow.ActivityResult)
		if !ok {
			return port.StoredReply{}, fmt.Errorf("invalid activity outcome")
		}
		headers["ETag"] = `"` + domain.Revision(result.Issue.Revision) + `"`
		a := result.Activity
		switch kind {
		case "activity.append":
			outcome = struct {
				Activity ActivityDTO `json:"activity"`
			}{ActivityDTOFrom(a)}
		case "activity.comment":
			outcome = struct {
				Comment domain.Comment `json:"comment"`
			}{domain.Comment{ID: a.ID, IssueID: a.IssueID, SessionID: a.SessionID, Text: a.Message, CreatedAt: a.CreatedAt}}
		case "activity.log":
			outcome = struct {
				Log LogDTO `json:"log"`
			}{LogDTO{a.ID, a.IssueID, a.SessionID, a.WorkSessionID, a.Message, a.LogType, a.CreatedAt}}
		case "activity.delete":
			status = 200
			outcome = struct {
				Deleted bool `json:"deleted"`
			}{true}
		}
	case "workflow.review":
		result, ok := outcome.(workflow.ReviewResult)
		if !ok {
			return port.StoredReply{}, fmt.Errorf("invalid review outcome")
		}
		headers["ETag"] = `"` + domain.Revision(result.Issue.Revision) + `"`
		outcome = struct {
			Issue  IssueDTO           `json:"issue"`
			Review domain.IssueReview `json:"review"`
		}{IssueDTOFrom(result.Issue), result.Review}
	case "workflow.transition":
		result, ok := outcome.(workflow.TransitionResult)
		if !ok {
			return port.StoredReply{}, fmt.Errorf("invalid transition outcome")
		}
		status = 200
		headers["ETag"] = `"` + domain.Revision(result.Issue.Revision) + `"`
		parents, unblocked, descendants := []IssueDTO{}, []IssueDTO{}, []IssueDTO{}
		for _, i := range result.DescendantStatusUpdates {
			descendants = append(descendants, IssueDTOFrom(i))
		}
		for _, i := range result.ParentStatusUpdates {
			parents = append(parents, IssueDTOFrom(i))
		}
		for _, i := range result.AutoUnblocked {
			unblocked = append(unblocked, IssueDTOFrom(i))
		}
		outcome = struct {
			Issue       IssueDTO   `json:"issue"`
			Noop        bool       `json:"noop"`
			Parents     []IssueDTO `json:"parent_status_updates"`
			Descendants []IssueDTO `json:"descendant_status_updates"`
			Unblocked   []IssueDTO `json:"auto_unblocked"`
		}{IssueDTOFrom(result.Issue), result.Noop, parents, descendants, unblocked}
	case "workflow.handoff":
		result, ok := outcome.(workflow.HandoffResult)
		if !ok {
			return port.StoredReply{}, fmt.Errorf("invalid handoff outcome")
		}
		headers["ETag"] = `"` + domain.Revision(result.Issue.Revision) + `"`
		h := result.Handoff
		handoff := HandoffDTO{h.ID, h.IssueID, h.SessionID, append([]string{}, h.Done...), append([]string{}, h.Remaining...), append([]string{}, h.Decisions...), append([]string{}, h.Uncertain...), h.CreatedAt}
		outcome = struct {
			Issue           IssueDTO   `json:"issue"`
			Handoff         HandoffDTO `json:"handoff"`
			ReviewRequested bool       `json:"review_requested"`
		}{IssueDTOFrom(result.Issue), handoff, result.ReviewRequested}
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
