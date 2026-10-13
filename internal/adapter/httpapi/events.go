package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yoophi/td-backend/internal/application/changes"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

type EventOptions struct{ PingEvery, WriteTimeout time.Duration }

func registerEvents(mux *http.ServeMux, hub *changes.Hub, auth port.Authenticator, opts EventOptions, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	if opts.PingEvery <= 0 {
		opts.PingEvery = 15 * time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 5 * time.Second
	}
	const base = "/v1/projects/{project_id}"
	mux.HandleFunc("GET "+base+"/changes", protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		state, e := hub.Service.Current(r.Context(), caller(r))
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, struct {
			ChangeToken string `json:"change_token"`
		}{state.Token})
	}))
	mux.HandleFunc("GET "+base+"/events", protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		if len(r.Header.Values("Last-Event-ID")) > 1 || len(r.Header.Get("Last-Event-ID")) > 512 {
			writeError(w, invalid("Last-Event-ID", "expected one opaque token of at most 512 bytes"))
			return
		}
		c := caller(r)
		sub, initial, e := hub.Subscribe(r.Context(), c)
		if e != nil {
			writeError(w, e)
			return
		}
		defer sub.Cancel()
		controller := http.NewResponseController(w)
		if _, ok := w.(http.Flusher); !ok {
			writeError(w, domain.ErrUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		// Scope/transaction has ended before headers or stream bytes are flushed.
		if e = controller.SetWriteDeadline(time.Now().Add(opts.WriteTimeout)); e != nil {
			return
		}
		if e = controller.Flush(); e != nil {
			return
		}
		last := r.Header.Get("Last-Event-ID")
		send := func(name string, state changes.Snapshot) error {
			var data any = struct {
				ChangeToken string `json:"change_token"`
			}{state.Token}
			if name == "refresh" {
				data = struct {
					ChangeToken string    `json:"change_token"`
					Timestamp   time.Time `json:"timestamp"`
				}{state.Token, state.Timestamp}
			}
			body, e := json.Marshal(data)
			if e != nil {
				return e
			}
			if e = controller.SetWriteDeadline(time.Now().Add(opts.WriteTimeout)); e != nil {
				return e
			}
			if _, e = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", state.Token, name, body); e != nil {
				return e
			}
			return controller.Flush()
		}
		if last != initial.Token {
			if e = send("refresh", initial); e != nil {
				return
			}
			last = initial.Token
		}
		ticker := time.NewTicker(opts.PingEvery)
		defer ticker.Stop()
		current := func() (changes.Snapshot, error) {
			_, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
			actor, e := auth.Resolve(r.Context(), token)
			if e != nil {
				return changes.Snapshot{}, e
			}
			if actor.ID != c.Actor.ID {
				return changes.Snapshot{}, domain.ErrUnauthorized
			}
			return hub.Service.Current(r.Context(), c)
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case notice, ok := <-sub.Events:
				if !ok || notice.Err != nil {
					return
				}
				state, e := current()
				if e != nil {
					return
				}
				if state.Token != last {
					if e = send("refresh", state); e != nil {
						return
					}
					last = state.Token
				}
			case <-ticker.C:
				state, e := current()
				if e != nil {
					return
				}
				name := "ping"
				if state.Token != last {
					name = "refresh"
				}
				if e = send(name, state); e != nil {
					return
				}
				last = state.Token
			}
		}
	}))
}
