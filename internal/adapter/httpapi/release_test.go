package httpapi

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

func TestClaimReleaseRechecksHolderLineageLivenessAndReplays(t *testing.T) {
	h, project, holder := issueSetup(t)
	base := "/v1/projects/" + project
	register := func(contextID, previous string) string {
		t.Helper()
		body := fmt.Sprintf(`{"context_id":%q,"previous_session_id":%q}`, contextID, previous)
		status, data := h.request("POST", base+"/sessions", testToken, key(t), "", body)
		if status != 201 {
			t.Fatal(status, string(data))
		}
		return sessionFrom(t, data).ID
	}
	lineage := register("release-lineage", holder)
	unrelated := register("release-unrelated", "")
	createAndStart := func(title string) string {
		t.Helper()
		status, _, data := issueRequest(t, h, "POST", base+"/issues", holder, key(t), "", fmt.Sprintf(`{"title":%q}`, title))
		if status != 201 {
			t.Fatal(status, string(data))
		}
		id := issueFrom(t, data).ID
		status, _, data = issueRequest(t, h, "POST", base+"/issues/"+id+"/start", holder, key(t), `"r-1"`, `{}`)
		if status != 200 {
			t.Fatal(status, string(data))
		}
		return id
	}
	id := createAndStart("Release exact claim")
	path := base + "/issues/" + id + "/release"
	staleBody := fmt.Sprintf(`{"holder":%q,"mode":"stale","stale_seconds":1}`, holder)
	status, _, data := issueRequest(t, h, "POST", path, lineage, key(t), `"r-2"`, staleBody)
	if status != 409 {
		t.Fatalf("lineage stale release status=%d, body=%s", status, data)
	}
	status, _, data = issueRequest(t, h, "POST", path, unrelated, key(t), `"r-2"`, staleBody)
	if status != 409 {
		t.Fatalf("live stale release status=%d, body=%s", status, data)
	}
	wrongHolder := fmt.Sprintf(`{"holder":%q,"mode":"session"}`, unrelated)
	status, _, data = issueRequest(t, h, "POST", path, lineage, key(t), `"r-2"`, wrongHolder)
	if status != 409 {
		t.Fatalf("wrong holder status=%d, body=%s", status, data)
	}
	status, _, data = issueRequest(t, h, "PUT", base+"/focus", lineage, key(t), "", fmt.Sprintf(`{"issue_id":%q}`, id))
	if status != 200 {
		t.Fatalf("focus setup status=%d body=%s", status, data)
	}
	exactBody := fmt.Sprintf(`{"holder":%q,"mode":"session","reason":"supervisor released claim"}`, holder)
	operation := key(t)
	status, etag, first := issueRequest(t, h, "POST", path, lineage, operation, `"r-2"`, exactBody)
	if status != 200 || etag != `"r-3"` || issueFrom(t, first).Status != domain.StatusOpen || issueFrom(t, first).ImplementerSession != nil {
		t.Fatalf("exact release status=%d etag=%s body=%s", status, etag, first)
	}
	status, replayETag, replay := issueRequest(t, h, "POST", path, lineage, operation, `"r-2"`, exactBody)
	if status != 200 || replayETag != etag || string(replay) != string(first) {
		t.Fatalf("release replay changed: %d %s", status, replay)
	}
	status, _, data = issueRequest(t, h, "POST", path, lineage, key(t), `"r-2"`, exactBody)
	if status != 412 {
		t.Fatalf("stale revision status=%d, body=%s", status, data)
	}
	status, _, data = issueRequest(t, h, "GET", base+"/issues/"+id+"/activity", lineage, "", "", "")
	if status != 200 || !strings.Contains(string(data), "supervisor released claim") {
		t.Fatalf("release activity missing: %d %s", status, data)
	}
	status, _, data = issueRequest(t, h, "GET", base+"/focus", lineage, "", "", "")
	if status != 200 || !strings.Contains(string(data), `"focused_issue_id":null`) {
		t.Fatalf("released focus remained: %d %s", status, data)
	}

	staleID := createAndStart("Release stale claim")
	stalePath := base + "/issues/" + staleID + "/release"
	time.Sleep(1100 * time.Millisecond)
	status, etag, data = issueRequest(t, h, "POST", stalePath, unrelated, key(t), `"r-2"`, staleBody)
	if status != 200 || etag != `"r-3"` || issueFrom(t, data).Status != domain.StatusOpen {
		t.Fatalf("stale release status=%d etag=%s body=%s", status, etag, data)
	}
}
