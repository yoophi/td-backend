package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func postgresTestURL(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TD_BACKEND_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TD_BACKEND_TEST_DATABASE_URL for PostgreSQL process tests")
	}
	db, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "td_process_" + hex.EncodeToString(random[:])
	if _, err := db.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func TestPostgresConcurrentServerStartupAndSharedData(t *testing.T) {
	dsn := postgresTestURL(t)
	binary := filepath.Join(t.TempDir(), "td-backend")
	build := exec.Command("go", "build", "-o", binary, ".")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v %s", err, data)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addresses := []string{freeAddress(t), freeAddress(t)}
	logPaths := make([]string, len(addresses))
	for i, address := range addresses {
		cmd := exec.CommandContext(ctx, binary, "--addr", address)
		cmd.Env = append(os.Environ(),
			"TD_BACKEND_DB_ENGINE=postgres",
			"TD_BACKEND_DATABASE_URL="+dsn,
			"TD_BACKEND_BOOTSTRAP_TOKEN=0123456789abcdef0123456789abcdef",
			"TD_BACKEND_DB_PATH=",
		)
		cmd.Stdout = io.Discard
		logPaths[i] = filepath.Join(t.TempDir(), "server.log")
		log, err := os.Create(logPaths[i])
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		_ = log.Close()
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	}
	client := &http.Client{Timeout: time.Second}
	for i, address := range addresses {
		deadline := time.Now().Add(10 * time.Second)
		for {
			response, err := client.Get("http://" + address + "/healthz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				log, _ := os.ReadFile(logPaths[i])
				t.Fatalf("PostgreSQL server did not start at %s: %v: %s", address, err, log)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	request := func(address, method, path, key, session, body string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+address+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if session != "" {
			req.Header.Set("X-TD-Session", session)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, data
	}
	status, data := request(addresses[0], "POST", "/v1/projects", "00000000-0000-4000-8000-000000000001", "", `{"name":"Two process integration"}`)
	if status != 201 {
		t.Fatalf("create project: %d %s", status, data)
	}
	var created struct {
		Data struct {
			Project struct {
				ID string `json:"id"`
			} `json:"project"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &created); err != nil || created.Data.Project.ID == "" {
		t.Fatalf("project response: %v %s", err, data)
	}
	projectID := created.Data.Project.ID
	status, data = request(addresses[1], "GET", "/v1/projects", "", "", "")
	if status != 200 || !strings.Contains(string(data), projectID) {
		t.Fatalf("second process cannot read project: %d %s", status, data)
	}
	status, data = request(addresses[0], "POST", "/v1/projects/"+projectID+"/sessions", "00000000-0000-4000-8000-000000000002", "", `{"context_id":"two-process-test"}`)
	if status != 201 {
		t.Fatalf("register session: %d %s", status, data)
	}
	var registered struct {
		Data struct {
			Session struct {
				ID string `json:"id"`
			} `json:"session"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &registered); err != nil || registered.Data.Session.ID == "" {
		t.Fatalf("session response: %v %s", err, data)
	}
	sessionID := registered.Data.Session.ID
	status, data = request(addresses[0], "POST", "/v1/projects/"+projectID+"/issues", "00000000-0000-4000-8000-000000000003", sessionID, `{"title":"Two process PostgreSQL issue"}`)
	if status != 201 {
		t.Fatalf("create issue: %d %s", status, data)
	}
	status, data = request(addresses[1], "GET", "/v1/projects/"+projectID+"/issues", "", sessionID, "")
	if status != 200 || !strings.Contains(string(data), "Two process PostgreSQL issue") {
		t.Fatalf("second process cannot read issue: %d %s", status, data)
	}
}
