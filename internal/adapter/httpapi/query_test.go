package httpapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

func TestQuerySnapshotSessionAndLifecycle(t *testing.T) {
	h, p, session := issueSetup(t)
	root := "/v1/projects/" + p
	status, data := h.request("POST", root+"/sessions", testToken, key(t), "", `{"context_id":"query-observer"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	observer := sessionFrom(t, data).ID
	var ids []string
	for _, title := range []string{"Alpha query issue", "Beta query issue", "Gamma query issue"} {
		status, _, data := issueRequest(t, h, "POST", root+"/issues", session, key(t), "", fmt.Sprintf(`{"title":%q,"minor":true}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		ids = append(ids, issueFrom(t, data).ID)
	}
	query := func(s, q string, want []string) {
		t.Helper()
		status, _, data := issueRequest(t, h, "GET", root+"/query?q="+url.QueryEscape(q), s, "", "", "")
		var result struct {
			Data struct {
				IDs []string `json:"ids"`
			} `json:"data"`
		}
		if e := json.Unmarshal(data, &result); e != nil {
			t.Fatal(e)
		}
		if status != 200 || !reflect.DeepEqual(result.Data.IDs, want) {
			t.Fatal(q, status, string(data), want)
		}
	}
	query(session, "sort:title", ids)
	status, _, data = issueRequest(t, h, "POST", root+"/issues/"+ids[0]+"/start", session, key(t), `"r-1"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	query(session, "implementer = @me sort:title", ids[:1])
	query(observer, "implementer = @me", []string{})
	status, _, data = issueRequest(t, h, "POST", root+"/issues/"+ids[0]+"/close", session, key(t), `"r-2"`, `{}`)
	if status != 200 {
		t.Fatal(status, string(data))
	}
	query(session, "status = closed", ids[:1])
	query(session, "status != closed sort:title", ids[1:])
	status, _, data = issueRequest(t, h, "DELETE", root+"/issues/"+ids[1], session, key(t), `"r-1"`, "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	query(session, "sort:title", []string{ids[0], ids[2]})
	for _, raw := range []string{"", "?q=a&q=b", "?q=&extra=x", "?q=" + url.QueryEscape("status ="), "?q=" + url.QueryEscape("note.title = sample")} {
		status, _, data = issueRequest(t, h, "GET", root+"/query"+raw, session, "", "", "")
		if status != 400 {
			t.Fatal(raw, status, string(data))
		}
	}
	status, _, data = issueRequest(t, h, "GET", root+"/query?q=", session, "", "", "")
	if status != 200 {
		t.Fatal("empty query", status, string(data))
	}
}
