package postgres_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/adapter/httpapi"
	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/adapter/identity"
	"github.com/yoophi/td-backend/internal/adapter/postgres"
	"github.com/yoophi/td-backend/internal/adapter/tokenauth"
	"github.com/yoophi/td-backend/internal/application/board"
	"github.com/yoophi/td-backend/internal/application/changes"
	"github.com/yoophi/td-backend/internal/application/focus"
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/monitor"
	"github.com/yoophi/td-backend/internal/application/note"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/project"
	"github.com/yoophi/td-backend/internal/application/undo"
	"github.com/yoophi/td-backend/internal/application/workflow"
	"github.com/yoophi/td-backend/internal/application/worksession"
)

const apiToken = "0123456789abcdef0123456789abcdef"

type apiHarness struct {
	t      *testing.T
	server *httptest.Server
}

func newAPIHarness(t *testing.T, dsn string) *apiHarness {
	t.Helper()
	store, err := postgres.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	auth := tokenauth.New(store, store)
	if err := auth.Bootstrap(context.Background(), apiToken); err != nil {
		t.Fatal(err)
	}
	ids := identity.New()
	runner := operation.Runner{Writes: store, Encoder: receiptcodec.Codec{}, ID: ids.ID, Now: time.Now}
	hub := changes.NewHub(context.Background(), &changes.Service{Reads: store}, changes.HubOptions{PollEvery: 10 * time.Millisecond})
	t.Cleanup(hub.Close)
	h := &apiHarness{t: t}
	h.server = httptest.NewServer(httpapi.New(httpapi.Options{
		Projects:     &project.Service{Writes: store, Reads: store, Encoder: receiptcodec.Codec{}, ID: ids.ID, Now: time.Now},
		Auth:         auth,
		Boards:       &board.Service{Reads: store, ID: ids.ID, Runner: runner},
		Events:       hub,
		EventOptions: httpapi.EventOptions{PingEvery: time.Second, WriteTimeout: time.Second},
		Undo:         &undo.Service{Runner: runner},
		Audit:        &operation.AuditService{Reads: store},
		Monitor:      &monitor.Service{Reads: store},
		Notes:        &note.Service{Reads: store, ID: ids.ID, Runner: runner},
		Focus:        &focus.Service{Reads: store, Runner: runner},
		WorkSessions: &worksession.Service{Reads: store, ID: ids.ID, Runner: runner},
		Workflows:    &workflow.Service{Reads: store, ID: ids.ID, Runner: runner},
		Issues:       &issue.Service{Reads: store, ID: ids.ID, Runner: runner},
	}))
	t.Cleanup(h.server.Close)
	return h
}

func opKey(t *testing.T) string {
	t.Helper()
	key, err := identity.New().UUID4()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func (h *apiHarness) request(method, path, session, operation, match, body string) (int, http.Header, []byte) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	if session != "" {
		req.Header.Set("X-TD-Session", session)
	}
	if operation != "" {
		req.Header.Set("Idempotency-Key", operation)
	}
	if match != "" {
		req.Header.Set("If-Match", match)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return response.StatusCode, response.Header, data
}

func apiID(t *testing.T, body []byte, kind string) string {
	t.Helper()
	var response struct {
		Data map[string]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	item := response.Data[kind]
	id, _ := item["id"].(string)
	if id == "" {
		t.Fatalf("missing %s id in %s", kind, body)
	}
	return id
}

func TestHTTPParityAcrossTwoPostgresStores(t *testing.T) {
	dsn := testingDatabaseURL(t)
	first := newAPIHarness(t, dsn)
	second := newAPIHarness(t, dsn)
	projectKey := opKey(t)
	status, _, created := first.request("POST", "/v1/projects", "", projectKey, "", `{"name":"PostgreSQL API parity"}`)
	if status != 201 {
		t.Fatalf("project create: %d %s", status, created)
	}
	p := apiID(t, created, "project")
	status, _, replayed := second.request("POST", "/v1/projects", "", projectKey, "", `{"name":"PostgreSQL API parity"}`)
	if status != 201 || string(replayed) != string(created) {
		t.Fatalf("project receipt replay: %d %s", status, replayed)
	}
	base := "/v1/projects/" + p
	status, _, data := second.request("POST", base+"/sessions", "", opKey(t), "", `{"context_id":"postgres-api-parity"}`)
	if status != 201 {
		t.Fatalf("session create: %d %s", status, data)
	}
	session := apiID(t, data, "session")
	status, _, data = first.request("POST", base+"/issues", session, opKey(t), "", `{"title":"PostgreSQL parity issue"}`)
	if status != 201 {
		t.Fatalf("issue create: %d %s", status, data)
	}
	issueID := apiID(t, data, "issue")
	issuePath := base + "/issues/" + issueID
	status, headers, data := second.request("GET", issuePath, session, "", "", "")
	if status != 200 || headers.Get("ETag") != `"r-1"` {
		t.Fatalf("cross-store issue read: %d %s %s", status, headers.Get("ETag"), data)
	}
	updateKey := opKey(t)
	status, headers, updated := second.request("PATCH", issuePath, session, updateKey, `"r-1"`, `{"title":"PostgreSQL parity issue updated"}`)
	if status != 200 || headers.Get("ETag") != `"r-2"` {
		t.Fatalf("issue update: %d %s %s", status, headers.Get("ETag"), updated)
	}
	status, _, replayed = first.request("PATCH", issuePath, session, updateKey, `"r-1"`, `{"title":"PostgreSQL parity issue updated"}`)
	if status != 200 || string(replayed) != string(updated) {
		t.Fatalf("issue receipt replay: %d %s", status, replayed)
	}
	status, _, data = first.request("PATCH", issuePath, session, opKey(t), `"r-1"`, `{"title":"Stale update"}`)
	if status != 412 {
		t.Fatalf("stale revision accepted: %d %s", status, data)
	}
	for _, path := range []string{base + "/monitor", base + "/changes", base + "/issues", base + "/stats"} {
		status, _, data = second.request("GET", path, session, "", "", "")
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, data)
		}
	}
	status, _, data = first.request("POST", base+"/operations/"+updateKey+"/undo", session, opKey(t), "", `{"reason":"Restore earlier title"}`)
	if status != 200 {
		t.Fatalf("undo: %d %s", status, data)
	}
	status, _, data = second.request("GET", issuePath, session, "", "", "")
	if status != 200 || !strings.Contains(string(data), "PostgreSQL parity issue\"") {
		t.Fatalf("undo not visible across stores: %d %s", status, data)
	}
}

func TestCrossStoreSSERefresh(t *testing.T) {
	dsn := testingDatabaseURL(t)
	first := newAPIHarness(t, dsn)
	second := newAPIHarness(t, dsn)
	status, _, data := first.request("POST", "/v1/projects", "", opKey(t), "", `{"name":"PostgreSQL SSE parity"}`)
	if status != 201 {
		t.Fatalf("create project: %d %s", status, data)
	}
	base := "/v1/projects/" + apiID(t, data, "project")
	status, _, data = first.request("POST", base+"/sessions", "", opKey(t), "", `{"context_id":"postgres-sse"}`)
	if status != 201 {
		t.Fatalf("create session: %d %s", status, data)
	}
	session := apiID(t, data, "session")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", second.server.URL+base+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("X-TD-Session", session)
	response, err := second.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("SSE: %s", response.Status)
	}
	scan := bufio.NewScanner(response.Body)
	firstID := scanEventID(t, scan)
	status, _, data = first.request("POST", base+"/issues", session, opKey(t), "", `{"title":"PostgreSQL SSE changed issue"}`)
	if status != 201 {
		t.Fatalf("create issue: %d %s", status, data)
	}
	if next := scanEventID(t, scan); next == firstID {
		t.Fatalf("SSE token did not advance: %s", next)
	}
}

func TestConcurrentStaleRevisionAcrossStores(t *testing.T) {
	dsn := testingDatabaseURL(t)
	first := newAPIHarness(t, dsn)
	second := newAPIHarness(t, dsn)
	status, _, data := first.request("POST", "/v1/projects", "", opKey(t), "", `{"name":"PostgreSQL concurrency"}`)
	if status != 201 {
		t.Fatalf("create project: %d %s", status, data)
	}
	base := "/v1/projects/" + apiID(t, data, "project")
	status, _, data = first.request("POST", base+"/sessions", "", opKey(t), "", `{"context_id":"postgres-concurrency"}`)
	if status != 201 {
		t.Fatalf("create session: %d %s", status, data)
	}
	session := apiID(t, data, "session")
	status, _, data = first.request("POST", base+"/issues", session, opKey(t), "", `{"title":"Before concurrent updates"}`)
	if status != 201 {
		t.Fatalf("create issue: %d %s", status, data)
	}
	path := base + "/issues/" + apiID(t, data, "issue")
	var group sync.WaitGroup
	results := make(chan int, 2)
	for _, h := range []*apiHarness{first, second} {
		group.Add(1)
		go func(h *apiHarness, key string) {
			defer group.Done()
			code, _, _ := h.request("PATCH", path, session, key, `"r-1"`, `{"title":"Concurrent update"}`)
			results <- code
		}(h, opKey(t))
	}
	group.Wait()
	close(results)
	success := 0
	for code := range results {
		if code == 200 {
			success++
		} else if code != 412 && code != 503 {
			t.Fatalf("unexpected concurrent update status: %d", code)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly one committed update, got %d", success)
	}
	status, headers, data := second.request("GET", path, session, "", "", "")
	if status != 200 || headers.Get("ETag") != `"r-2"` {
		t.Fatalf("concurrent final revision: %d %s %s", status, headers.Get("ETag"), data)
	}
}

func scanEventID(t *testing.T, scan *bufio.Scanner) string {
	t.Helper()
	id := ""
	name := ""
	for scan.Scan() {
		line := scan.Text()
		if value, ok := strings.CutPrefix(line, "id: "); ok {
			id = value
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			name = value
		}
		if line == "" && name == "refresh" {
			if id == "" {
				t.Fatal("refresh event without ID")
			}
			return id
		}
	}
	t.Fatal(fmt.Errorf("SSE ended: %w", scan.Err()))
	return ""
}
