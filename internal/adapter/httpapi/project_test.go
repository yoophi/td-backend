package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/adapter/identity"
	"github.com/yoophi/td-backend/internal/adapter/sqlite"
	"github.com/yoophi/td-backend/internal/adapter/tokenauth"
	"github.com/yoophi/td-backend/internal/application/board"
	"github.com/yoophi/td-backend/internal/application/issue"
	"github.com/yoophi/td-backend/internal/application/operation"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/project"
	"github.com/yoophi/td-backend/internal/application/workflow"
	"github.com/yoophi/td-backend/internal/domain"
)

const testToken = "0123456789abcdef0123456789abcdef"

type harness struct {
	t        *testing.T
	store    *sqlite.Store
	projects *project.Service
	auth     *tokenauth.Auth
	server   *httptest.Server
	path     string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "api.db")
	store, e := sqlite.Open(ctx, path, sqlite.Options{})
	if e != nil {
		t.Fatal(e)
	}
	auth := tokenauth.New(store, store)
	if e = auth.Bootstrap(ctx, testToken); e != nil {
		t.Fatal(e)
	}
	ids := identity.New()
	svc := &project.Service{Writes: store, Reads: store, Encoder: receiptcodec.Codec{}, ID: ids.ID, Now: time.Now}
	h := &harness{t: t, store: store, projects: svc, auth: auth, path: path}
	runner := operation.Runner{Writes: store, Encoder: receiptcodec.Codec{}, ID: ids.ID, Now: time.Now}
	h.server = httptest.NewServer(New(Options{
		Projects: svc, Auth: auth,
		Boards:    &board.Service{Reads: store, ID: ids.ID, Runner: runner},
		Workflows: &workflow.Service{Reads: store, ID: ids.ID, Runner: runner},
		Issues:    &issue.Service{Reads: store, ID: ids.ID, Runner: runner},
	}))
	t.Cleanup(func() { h.server.Close(); h.store.Close() })
	return h
}
func key(t *testing.T) string {
	t.Helper()
	v, e := identity.New().UUID4()
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func (h *harness) request(method, path, token, idempotency, session, body string) (int, []byte) {
	h.t.Helper()
	req, e := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if e != nil {
		h.t.Fatal(e)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	if session != "" {
		req.Header.Set("X-TD-Session", session)
	}
	response, e := h.server.Client().Do(req)
	if e != nil {
		h.t.Fatal(e)
	}
	defer response.Body.Close()
	data, e := io.ReadAll(response.Body)
	if e != nil {
		h.t.Fatal(e)
	}
	return response.StatusCode, data
}
func projectFrom(t *testing.T, data []byte) domain.Project {
	t.Helper()
	var response struct {
		OK   bool                  `json:"ok"`
		Data project.ProjectResult `json:"data"`
	}
	if e := json.Unmarshal(data, &response); e != nil || !response.OK {
		t.Fatalf("project response %s: %v", data, e)
	}
	return response.Data.Project
}
func sessionFrom(t *testing.T, data []byte) domain.Session {
	t.Helper()
	var response struct {
		Data project.SessionResult `json:"data"`
	}
	if e := json.Unmarshal(data, &response); e != nil {
		t.Fatal(e)
	}
	return response.Data.Session
}
func (h *harness) create() domain.Project {
	status, data := h.request("POST", "/v1/projects", testToken, key(h.t), "", `{"name":"API integration project"}`)
	if status != 201 {
		h.t.Fatal(status, string(data))
	}
	return projectFrom(h.t, data)
}
func (h *harness) session(p domain.Project, token string) domain.Session {
	status, data := h.request("POST", "/v1/projects/"+p.ID+"/sessions", token, key(h.t), "", `{"context_id":"test-context"}`)
	if status != 201 {
		h.t.Fatal(status, string(data))
	}
	return sessionFrom(h.t, data)
}
func (h *harness) rawDB() *sql.DB {
	h.t.Helper()
	db, e := sql.Open("sqlite", "file:"+h.path+"?_pragma=foreign_keys(1)")
	if e != nil {
		h.t.Fatal(e)
	}
	h.t.Cleanup(func() { db.Close() })
	return db
}
func TestProjectSessionInitializationAndByteExactRetry(t *testing.T) {
	h := newHarness(t)
	k := key(t)
	status, first := h.request("POST", "/v1/projects", testToken, k, "", `{"name":"API integration project"}`)
	if status != 201 {
		t.Fatal(status, string(first))
	}
	p := projectFrom(t, first)
	if !domain.ValidProjectID(p.ID) || p.TitleMinLength != 15 || p.TitleMaxLength != 200 || len(p.Capabilities) == 0 {
		t.Fatal(p)
	}
	status, retry := h.request("POST", "/v1/projects", testToken, k, "", `{ "name" : "API integration project" }`)
	if status != 201 || string(retry) != string(first) {
		t.Fatal("nonidentical init replay", status, string(retry))
	}
	status, _ = h.request("POST", "/v1/projects", testToken, k, "", `{"name":"Different project name"}`)
	if status != 409 {
		t.Fatal("key mismatch", status)
	}
	session := h.session(p, testToken)
	status, data := h.request("GET", "/v1/projects/"+p.ID, testToken, "", session.ID, "")
	if status != 200 || projectFrom(t, data).ID != p.ID {
		t.Fatal(status, string(data))
	}
	status, _ = h.request("GET", "/v1/projects/"+p.ID, testToken, "", "", "")
	if status != 400 {
		t.Fatal("missing session", status)
	}
	status, data = h.request("GET", "/v1/projects/"+p.ID+"/sessions", testToken, "", session.ID, "")
	if status != 200 || !strings.Contains(string(data), `"current_session_id":"`+session.ID+`"`) {
		t.Fatal(status, string(data))
	}
	var projects, boards, operations int
	db := h.rawDB()
	db.QueryRow("SELECT count(*) FROM projects").Scan(&projects)
	db.QueryRow("SELECT count(*) FROM boards WHERE is_builtin=1").Scan(&boards)
	db.QueryRow("SELECT count(*) FROM operations").Scan(&operations)
	if projects != 1 || boards != 1 || operations != 2 {
		t.Fatal(projects, boards, operations)
	}
	// New pool/process equivalents must replay the persisted original body.
	h.server.Close()
	h.store.Close()
	store, e := sqlite.Open(context.Background(), h.path, sqlite.Options{})
	if e != nil {
		t.Fatal(e)
	}
	h.store = store
	h.auth = tokenauth.New(store, store)
	h.projects.Reads = store
	h.projects.Writes = store
	h.server = httptest.NewServer(New(Options{Projects: h.projects, Auth: h.auth}))
	status, retry = h.request("POST", "/v1/projects", testToken, k, "", `{"name":"API integration project"}`)
	if status != 201 || string(retry) != string(first) {
		t.Fatal("restart replay", status, string(retry))
	}
}
func TestConcurrentSameInitCommitsOnce(t *testing.T) {
	h := newHarness(t)
	k := key(t)
	responses := make(chan string, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, data := h.request("POST", "/v1/projects", testToken, k, "", `{"name":"Concurrent project"}`)
			responses <- fmt.Sprintf("%d %s", status, data)
		}()
	}
	wg.Wait()
	close(responses)
	first := ""
	for r := range responses {
		if !strings.HasPrefix(r, "201 ") {
			t.Fatal(r)
		}
		if first == "" {
			first = r
		}
		if r != first {
			t.Fatal("duplicate intents returned different project")
		}
	}
	var count int
	if e := h.rawDB().QueryRow("SELECT count(*) FROM projects").Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}
func TestAuthenticationMembershipAndSessionOwnership(t *testing.T) {
	h := newHarness(t)
	p := h.create()
	ownerSession := h.session(p, testToken)
	otherToken := "abcdef0123456789abcdef0123456789"
	digest := sha256.Sum256([]byte(otherToken))
	db := h.rawDB()
	if _, e := db.Exec(`INSERT INTO actors VALUES('other','Other actor',0,'now');`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec("INSERT INTO access_tokens VALUES('other-key','other',?,'now',NULL)", digest[:]); e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{"", "wrong-token"} {
		status, _ := h.request("GET", "/v1/capabilities", token, "", "", "")
		if status != 401 {
			t.Fatal("unauthenticated", status)
		}
	}
	status, _ := h.request("POST", "/v1/projects", otherToken, key(t), "", `{"name":"Unauthorized project"}`)
	if status != 403 {
		t.Fatal("actor cannot create", status)
	}
	status, _ = h.request("GET", "/v1/projects/"+p.ID, otherToken, "", ownerSession.ID, "")
	if status != 403 {
		t.Fatal("membership", status)
	}
	if _, e := db.Exec("INSERT INTO project_memberships VALUES(?,'other','reader','now')", p.ID); e != nil {
		t.Fatal(e)
	}
	readerSession := h.session(p, otherToken)
	status, _ = h.request("GET", "/v1/projects/"+p.ID, otherToken, "", readerSession.ID, "")
	if status != 200 {
		t.Fatal("reader access", status)
	}
	status, _ = h.request("GET", "/v1/projects/"+p.ID, otherToken, "", ownerSession.ID, "")
	if status != 403 {
		t.Fatal("forged session ownership", status)
	}
	actor, e := h.auth.Resolve(context.Background(), otherToken)
	if e != nil {
		t.Fatal(e)
	}
	e = h.store.Snapshot(context.Background(), func(read port.ReadSnapshot) error {
		return project.Authorize(context.Background(), read, domain.Caller{Actor: actor, ProjectID: p.ID, SessionID: readerSession.ID}, true, true)
	})
	if !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("reader write permission", e)
	}
	status, _ = h.request("GET", "/v1/projects/invalid", testToken, "", ownerSession.ID, "")
	if status != 400 {
		t.Fatal(status)
	}
	status, _ = h.request("GET", "/v1/projects/prj_019a7a6e-8c00-7d42-9f23-8c5e7a1b6d90", testToken, "", ownerSession.ID, "")
	if status != 404 {
		t.Fatal(status)
	}
	if _, e := db.Exec("DELETE FROM project_memberships WHERE actor_id='act-bootstrap'"); e != nil {
		t.Fatal(e)
	}
	status, _ = h.request("POST", "/v1/projects/"+p.ID+"/sessions", testToken, key(t), "", `{"context_id":"after-revoke"}`)
	if status != 403 {
		t.Fatal("revoked member", status)
	}
}
func TestStrictJSONAndCredentialStorage(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{`{"name":"one","name":"two"}`, `{"name":"project","id":"client-id"}`, `{"name":null}`, `{"name":"project","title_min_length":0}`, `{"name":"project","review_policy_mode":"unknown"}`, `[]`, `{"name":"project"} {}`} {
		status, data := h.request("POST", "/v1/projects", testToken, key(t), "", body)
		if status != 400 {
			t.Fatal(body, status, string(data))
		}
	}
	status, _ := h.request("POST", "/v1/projects", testToken, "bad-key", "", `{"name":"project"}`)
	if status != 400 {
		t.Fatal(status)
	}
	var stored []byte
	if e := h.rawDB().QueryRow("SELECT token_digest FROM access_tokens").Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if string(stored) == testToken || len(stored) != sha256.Size {
		t.Fatal("plaintext credential persisted")
	}
	if e := h.auth.Bootstrap(context.Background(), "short"); e == nil {
		t.Fatal("weak bootstrap accepted")
	}
	replacement := "fedcba9876543210fedcba9876543210"
	if e := h.auth.Bootstrap(context.Background(), replacement); e != nil {
		t.Fatal(e)
	}
	if _, e := h.auth.Resolve(context.Background(), testToken); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatal("previous bootstrap credential still valid", e)
	}
}

type failingEncoder struct{}

func (failingEncoder) Encode(string, any) (port.StoredReply, error) {
	return port.StoredReply{}, errors.New("encode failed")
}
func TestIdentityAndEncodingFailureRollbackInitialization(t *testing.T) {
	for _, failure := range []string{"entropy", "encoder"} {
		t.Run(failure, func(t *testing.T) {
			h := newHarness(t)
			if failure == "entropy" {
				h.projects.ID = func(string) (string, error) { return "", errors.New("entropy failed") }
			} else {
				h.projects.Encoder = failingEncoder{}
			}
			status, _ := h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Failed initialization"}`)
			if status != 500 {
				t.Fatal(status)
			}
			for _, table := range []string{"projects", "boards", "project_memberships", "operations"} {
				var count int
				if e := h.rawDB().QueryRow("SELECT count(*) FROM " + table).Scan(&count); e != nil || count != 0 {
					t.Fatal(table, count, e)
				}
			}
		})
	}
}
func TestProjectPrimaryKeyCollisionHasBoundedRecovery(t *testing.T) {
	h := newHarness(t)
	existing := h.create()
	ids := identity.New()
	attempts := 0
	h.projects.ID = func(kind string) (string, error) {
		if kind == "project" {
			attempts++
			if attempts < 3 {
				return existing.ID, nil
			}
		}
		return ids.ID(kind)
	}
	created := h.create()
	if created.ID == existing.ID || attempts != 3 {
		t.Fatal(created.ID, attempts)
	}
	attempts = 0
	h.projects.ID = func(kind string) (string, error) {
		if kind == "project" {
			attempts++
			return existing.ID, nil
		}
		return ids.ID(kind)
	}
	status, _ := h.request("POST", "/v1/projects", testToken, key(t), "", `{"name":"Exhausted ID retry"}`)
	if status != 409 || attempts != 3 {
		t.Fatal(status, attempts)
	}
}
