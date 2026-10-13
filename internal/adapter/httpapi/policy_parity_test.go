package httpapi

import (
	"fmt"
	"testing"
)

func TestCreatorApprovalAcrossPolicyModes(t *testing.T) {
	for _, mode := range []string{"trusted", "delegated", "strict", "balanced"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			code, data := h.request("POST", "/v1/projects", testToken, key(t), "", fmt.Sprintf(`{"name":"Policy parity","review_policy_mode":%q}`, mode))
			if code != 201 {
				t.Fatal(code, string(data))
			}
			p := projectFrom(t, data).ID
			sessions := []string{}
			for _, context := range []string{"creator", "implementer"} {
				code, data = h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", fmt.Sprintf(`{"context_id":%q}`, context))
				if code != 201 {
					t.Fatal(code, string(data))
				}
				sessions = append(sessions, sessionFrom(t, data).ID)
			}
			creator, implementer := sessions[0], sessions[1]
			base := "/v1/projects/" + p + "/issues"
			code, _, data = issueRequest(t, h, "POST", base, creator, key(t), "", `{"title":"Creator and implementer separation"}`)
			if code != 201 {
				t.Fatal(code, string(data))
			}
			path := base + "/" + issueFrom(t, data).ID
			code, _, data = issueRequest(t, h, "POST", path+"/start", implementer, key(t), `"r-1"`, `{}`)
			if code != 200 {
				t.Fatal(code, string(data))
			}
			code, _, data = issueRequest(t, h, "POST", path+"/handoffs", implementer, key(t), `"r-2"`, `{}`)
			if code != 201 {
				t.Fatal(code, string(data))
			}
			code, _, data = issueRequest(t, h, "POST", path+"/approve", creator, key(t), `"r-3"`, `{}`)
			want := 200
			if mode == "strict" {
				want = 403
			}
			if mode == "balanced" {
				want = 400
			}
			if code != want {
				t.Fatal(mode, code, string(data))
			}
			if mode == "balanced" {
				code, _, data = issueRequest(t, h, "POST", path+"/approve", creator, key(t), `"r-3"`, `{"reason":"Creator reviewed another session's implementation"}`)
				if code != 200 {
					t.Fatal(code, string(data))
				}
			}
		})
	}
}
func TestStrictMinorAndIssueWideImplementationHistory(t *testing.T) {
	h := newHarness(t)
	code, data := h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Strict minor","review_policy_mode":"strict"}`)
	if code != 201 {
		t.Fatal(code, string(data))
	}
	p := projectFrom(t, data).ID
	sessions := []string{}
	for _, name := range []string{"creator", "worker"} {
		code, data = h.request("POST", "/v1/projects/"+p+"/sessions", testToken, key(t), "", fmt.Sprintf(`{"context_id":%q}`, name))
		if code != 201 {
			t.Fatal(code, string(data))
		}
		sessions = append(sessions, sessionFrom(t, data).ID)
	}
	base := "/v1/projects/" + p + "/issues"
	creator, worker := sessions[0], sessions[1]
	code, _, data = issueRequest(t, h, "POST", base, creator, key(t), "", `{"title":"Minor self close allowed","minor":true}`)
	if code != 201 {
		t.Fatal(code, string(data))
	}
	path := base + "/" + issueFrom(t, data).ID
	code, _, data = issueRequest(t, h, "POST", path+"/close", creator, key(t), `"r-1"`, `{}`)
	if code != 200 {
		t.Fatal(code, string(data))
	}
	code, _, data = issueRequest(t, h, "POST", base, creator, key(t), "", `{"title":"Other session implementation history"}`)
	if code != 201 {
		t.Fatal(code, string(data))
	}
	path = base + "/" + issueFrom(t, data).ID
	code, _, data = issueRequest(t, h, "POST", path+"/start", worker, key(t), `"r-1"`, `{}`)
	if code != 200 {
		t.Fatal(code, string(data))
	}
	code, _, data = issueRequest(t, h, "POST", path+"/unstart", worker, key(t), `"r-2"`, `{}`)
	if code != 200 {
		t.Fatal(code, string(data))
	}
	code, _, data = issueRequest(t, h, "POST", path+"/close", creator, key(t), `"r-3"`, `{}`)
	if code != 403 {
		t.Fatal("creator bypassed issue-wide implementation history", code, string(data))
	}
}
