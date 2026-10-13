package httpapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/application/changes"
	"github.com/yoophi/td-backend/internal/domain"
)

type streamedEvent struct {
	Name, ID string
	Data     map[string]json.RawMessage
}

func nextEvent(t *testing.T, scan *bufio.Scanner) streamedEvent {
	t.Helper()
	event := streamedEvent{}
	for scan.Scan() {
		line := scan.Text()
		if line == "" && event.Name != "" {
			return event
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch field {
		case "id":
			event.ID = value
		case "event":
			event.Name = value
		case "data":
			if e := json.Unmarshal([]byte(value), &event.Data); e != nil {
				t.Fatal(e)
			}
		}
	}
	t.Fatalf("event stream ended: %v", scan.Err())
	return event
}
func changesToken(t *testing.T, h *harness, p, session string) string {
	t.Helper()
	status, _, data := issueRequest(t, h, "GET", "/v1/projects/"+p+"/changes", session, "", "", "")
	if status != 200 {
		t.Fatal(status, string(data))
	}
	var out struct {
		Data struct {
			Token string `json:"change_token"`
		}
	}
	if e := json.Unmarshal(data, &out); e != nil || out.Data.Token == "" {
		t.Fatal(e, string(data))
	}
	return out.Data.Token
}
func openEvents(t *testing.T, h *harness, p, session, cursor string) (*http.Response, *bufio.Scanner, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	req, e := http.NewRequestWithContext(ctx, "GET", h.server.URL+"/v1/projects/"+p+"/events", nil)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-TD-Session", session)
	if cursor != "" {
		req.Header.Set("Last-Event-ID", cursor)
	}
	response, e := h.server.Client().Do(req)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		response.Body.Close()
		cancel()
		t.Fatal(response.Status, response.Header)
	}
	return response, bufio.NewScanner(response.Body), cancel
}
func TestChangesSSERefreshPingReplayAndReconnect(t *testing.T) {
	h, p, session := issueSetup(t)
	initial := changesToken(t, h, p, session)
	response, scan, cancel := openEvents(t, h, p, session, "")
	defer cancel()
	defer response.Body.Close()
	event := nextEvent(t, scan)
	if event.Name != "refresh" || event.ID != initial || len(event.Data) != 2 || event.Data["timestamp"] == nil {
		t.Fatal(event)
	}
	op := key(t)
	status, _, data := issueRequest(t, h, "POST", "/v1/projects/"+p+"/notes", session, op, "", `{"title":"Refresh committed note"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	changed := changesToken(t, h, p, session)
	if changed == initial {
		t.Fatal("counter did not change")
	}
	for {
		event = nextEvent(t, scan)
		if event.Name == "refresh" {
			break
		}
	}
	var token string
	if e := json.Unmarshal(event.Data["change_token"], &token); e != nil {
		t.Fatal(e)
	}
	if event.ID != changed || token != changed {
		t.Fatal(event)
	}
	status, _, data = issueRequest(t, h, "POST", "/v1/projects/"+p+"/notes", session, op, "", `{"title":"Refresh committed note"}`)
	if status != 201 || changesToken(t, h, p, session) != changed {
		t.Fatal("replay token changed", status, string(data))
	}
	event = nextEvent(t, scan)
	if event.Name != "ping" || event.ID != changed || len(event.Data) != 1 {
		t.Fatal(event)
	}
	response.Body.Close()
	cancel()
	reconnect, reScan, reCancel := openEvents(t, h, p, session, initial)
	defer reCancel()
	defer reconnect.Body.Close()
	event = nextEvent(t, reScan)
	if event.Name != "refresh" || event.ID != changed {
		t.Fatal("reconnect refresh", event)
	}
	reconnect.Body.Close()
	reCancel()
	same, sameScan, sameCancel := openEvents(t, h, p, session, changed)
	defer sameCancel()
	defer same.Body.Close()
	event = nextEvent(t, sameScan)
	if event.Name != "ping" || event.ID != changed {
		t.Fatal("same cursor refresh", event)
	}
}
func TestHubCoalescesSlowSubscriberAndShutdown(t *testing.T) {
	h, p, session := issueSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := changes.NewHub(ctx, &changes.Service{Reads: h.store}, changes.HubOptions{PollEvery: 2 * time.Millisecond, MaxSubscribers: 2})
	defer hub.Close()
	c := domain.Caller{ProjectID: p, SessionID: session, Actor: domain.Actor{ID: "act-bootstrap"}}
	slow, _, e := hub.Subscribe(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer slow.Cancel()
	fast, _, e := hub.Subscribe(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer fast.Cancel()
	if _, _, e = hub.Subscribe(ctx, c); e != domain.ErrUnavailable {
		t.Fatal("subscriber cap", e)
	}
	for n := 0; n < 4; n++ {
		status, _, data := issueRequest(t, h, "POST", "/v1/projects/"+p+"/notes", session, key(t), "", `{"title":"Hub coalescing note"}`)
		if status != 201 {
			t.Fatal(status, string(data))
		}
		latest := changesToken(t, h, p, session)
		deadline := time.After(time.Second)
		found := false
		for !found {
			select {
			case notice := <-fast.Events:
				if notice.Err != nil {
					t.Fatal(notice.Err)
				}
				found = notice.Token == latest
			case <-deadline:
				t.Fatal("hub did not observe committed counter")
			}
		}
	}
	latest := changesToken(t, h, p, session)
	select {
	case notice := <-slow.Events:
		if notice.Token != latest {
			t.Fatal("obsolete notice retained", notice, latest)
		}
	case <-time.After(time.Second):
		t.Fatal("slow subscriber stalled hub")
	}
	cancel()
	hub.Close()
	select {
	case _, ok := <-fast.Events:
		if ok {
			t.Fatal("subscriber not closed")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown stalled")
	}
	if _, _, e = hub.Subscribe(context.Background(), c); e != domain.ErrUnavailable {
		t.Fatal("subscribe after close", e)
	}
}
func TestSSERechecksMembershipAndDoesNotHoldTransaction(t *testing.T) {
	h, p, session := issueSetup(t)
	response, scan, cancel := openEvents(t, h, p, session, "")
	defer cancel()
	defer response.Body.Close()
	nextEvent(t, scan)
	// A live stream cannot occupy the writer or a read snapshot across waits.
	status, _, data := issueRequest(t, h, "POST", "/v1/projects/"+p+"/issues", session, key(t), "", `{"title":"Write while stream remains open"}`)
	if status != 201 {
		t.Fatal(status, string(data))
	}
	// Revoke project membership using its existing writer through an inspection
	// connection; the next heartbeat must close the stream.
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`DELETE FROM project_memberships WHERE project_id=? AND actor_id='act-bootstrap'`, p); e != nil {
		t.Fatal(e)
	}
	for scan.Scan() {
	}
	if scan.Err() != nil {
		t.Fatal("stream did not close on revoked membership", scan.Err())
	}
}

func TestMultipleStreamsReleaseReaderConnectionsAndCloseOnHubShutdown(t *testing.T) {
	h, p, session := issueSetup(t)
	for n := 0; n < 8; n++ {
		response, _, cancel := openEvents(t, h, p, session, "")
		defer cancel()
		defer response.Body.Close()
	}
	// More open subscribers than the four-connection reader pool remain usable.
	if token := changesToken(t, h, p, session); token == "" {
		t.Fatal("read pool unavailable")
	}
	response, scan, cancel := openEvents(t, h, p, session, "")
	defer cancel()
	defer response.Body.Close()
	nextEvent(t, scan)
	h.events.Close()
	for scan.Scan() {
	}
	if scan.Err() != nil {
		t.Fatal("hub shutdown did not close HTTP stream", scan.Err())
	}
	status, _, data := issueRequest(t, h, "GET", "/v1/projects/"+p+"/events", session, "", "", "")
	if status != 503 {
		t.Fatal("subscribe after shutdown", status, string(data))
	}
}
func TestSSERejectsInvalidParametersAndRevokedToken(t *testing.T) {
	h, p, session := issueSetup(t)
	base := "/v1/projects/" + p
	for _, path := range []string{"/changes?unknown=1", "/events?unknown=1"} {
		status, _, data := issueRequest(t, h, "GET", base+path, session, "", "", "")
		if status != 400 {
			t.Fatal(status, string(data))
		}
	}
	request, e := http.NewRequest("GET", h.server.URL+base+"/events", nil)
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+testToken)
	request.Header.Set("X-TD-Session", session)
	request.Header.Set("Last-Event-ID", strings.Repeat("x", 513))
	response, e := h.server.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal(response.Status)
	}
	stream, scan, cancel := openEvents(t, h, p, session, "")
	defer cancel()
	defer stream.Body.Close()
	nextEvent(t, scan)
	inspect, e := sql.Open("sqlite", h.path)
	if e != nil {
		t.Fatal(e)
	}
	defer inspect.Close()
	if _, e = inspect.Exec(`UPDATE access_tokens SET revoked_at='2026-10-13T00:00:00Z' WHERE actor_id='act-bootstrap'`); e != nil {
		t.Fatal(e)
	}
	for scan.Scan() {
	}
	if scan.Err() != nil {
		t.Fatal("revoked credential stream remained open", scan.Err())
	}
}

// Simulate a transport which cannot accept event bytes until its deadline.
// The handler must exit and release the bounded hub subscription on timeout.
type stalledEventWriter struct {
	header   http.Header
	deadline time.Time
}

func (w *stalledEventWriter) Header() http.Header { return w.header }
func (w *stalledEventWriter) WriteHeader(int)     {}
func (w *stalledEventWriter) Flush()              {}
func (w *stalledEventWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *stalledEventWriter) Write([]byte) (int, error) {
	if w.deadline.IsZero() {
		time.Sleep(time.Second)
	} else {
		timer := time.NewTimer(time.Until(w.deadline))
		defer timer.Stop()
		<-timer.C
	}
	return 0, os.ErrDeadlineExceeded
}
func TestSSEWriteTimeoutReleasesSubscriber(t *testing.T) {
	h, p, session := issueSetup(t)
	hub := changes.NewHub(context.Background(), &changes.Service{Reads: h.store}, changes.HubOptions{PollEvery: time.Millisecond, MaxSubscribers: 1})
	defer hub.Close()
	handler := New(Options{Projects: h.projects, Auth: h.auth, Events: hub, EventOptions: EventOptions{WriteTimeout: 10 * time.Millisecond}})
	req := httptest.NewRequest("GET", "/v1/projects/"+p+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-TD-Session", session)
	w := &stalledEventWriter{header: http.Header{}}
	done := make(chan struct{})
	go func() { handler.ServeHTTP(w, req); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("slow SSE write did not honor deadline")
	}
	sub, _, e := hub.Subscribe(context.Background(), domain.Caller{ProjectID: p, SessionID: session, Actor: domain.Actor{ID: "act-bootstrap"}})
	if e != nil {
		t.Fatal("timed out handler retained subscriber", e)
	}
	sub.Cancel()
}
