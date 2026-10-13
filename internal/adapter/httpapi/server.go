package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/application/project"
	"github.com/yoophi/td-backend/internal/domain"
)

type Options struct {
	Projects *project.Service
	Auth     port.Authenticator
}
type actorContextKey struct{}

func New(options ...Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeSuccess(w, 200, map[string]string{"status": "ok", "api_version": "v1"})
	})
	if len(options) == 0 || options[0].Projects == nil || options[0].Auth == nil {
		return mux
	}
	opts := options[0]
	protect := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			credential := r.Header.Get("Authorization")
			scheme, token, ok := strings.Cut(credential, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t\r\n") {
				writeError(w, domain.ErrUnauthorized)
				return
			}
			actor, e := opts.Auth.Resolve(r.Context(), token)
			if e != nil {
				writeError(w, e)
				return
			}
			h(w, r.WithContext(context.WithValue(r.Context(), actorContextKey{}, actor)))
		}
	}
	actor := func(r *http.Request) domain.Actor { return r.Context().Value(actorContextKey{}).(domain.Actor) }
	caller := func(r *http.Request) domain.Caller {
		return domain.Caller{Actor: actor(r), ProjectID: r.PathValue("project_id"), SessionID: r.Header.Get("X-TD-Session")}
	}
	mux.HandleFunc("GET /v1/capabilities", protect(func(w http.ResponseWriter, r *http.Request) {
		writeSuccess(w, 200, map[string]any{"api_version": "v1", "capabilities": project.Capabilities()})
	}))
	mux.HandleFunc("POST /v1/projects", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd project.CreateCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := opts.Projects.Create(r.Context(), actor(r), m, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("GET /v1/projects", protect(func(w http.ResponseWriter, r *http.Request) {
		list, e := opts.Projects.List(r.Context(), actor(r))
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, struct {
			Projects []domain.Project `json:"projects"`
		}{list})
	}))
	mux.HandleFunc("GET /v1/projects/{project_id}", protect(func(w http.ResponseWriter, r *http.Request) {
		p, e := opts.Projects.Get(r.Context(), caller(r))
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, project.ProjectResult{Project: p})
	}))
	mux.HandleFunc("POST /v1/projects/{project_id}/sessions", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd project.SessionCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		c := caller(r)
		c.SessionID = ""
		reply, e := opts.Projects.RegisterSession(r.Context(), c, m, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("GET /v1/projects/{project_id}/sessions", protect(func(w http.ResponseWriter, r *http.Request) {
		c := caller(r)
		list, e := opts.Projects.ListSessions(r.Context(), c)
		if e != nil {
			writeError(w, e)
			return
		}
		writeSuccess(w, 200, struct {
			Sessions []domain.Session `json:"sessions"`
			Current  string           `json:"current_session_id"`
		}{list, c.SessionID})
	}))
	return mux
}
func mutation(r *http.Request, cmd any) (port.Mutation, error) {
	key := r.Header.Get("Idempotency-Key")
	if !domain.ValidOperationKey(key) {
		return port.Mutation{}, invalid("Idempotency-Key", "expected lowercase canonical UUIDv4")
	}
	fingerprintInput := struct {
		Version, Method, Path, Session, Revision string
		Command                                  any
	}{"v1", r.Method, r.URL.EscapedPath(), r.Header.Get("X-TD-Session"), r.Header.Get("If-Match"), cmd}
	body, e := json.Marshal(fingerprintInput)
	if e != nil {
		return port.Mutation{}, e
	}
	digest := sha256.Sum256(body)
	return port.Mutation{Key: key, Fingerprint: digest[:], Method: r.Method, Path: r.URL.Path}, nil
}
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := strings.Split(r.Header.Get("Content-Type"), ";")[0]; ct != "application/json" {
		return invalid("body", "Content-Type application/json required")
	}
	data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if e != nil {
		return invalid("body", "request body exceeds limit or could not be read")
	}
	validator := json.NewDecoder(strings.NewReader(string(data)))
	validator.UseNumber()
	if e = walkJSON(validator); e != nil {
		return invalid("body", e.Error())
	}
	if _, e = validator.Token(); e != io.EOF {
		return invalid("body", "expected one JSON object")
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(data, &fields); e != nil || fields == nil {
		return invalid("body", "expected JSON object")
	}
	for field, value := range fields {
		if string(value) == "null" {
			return invalid(field, "null is not allowed")
		}
		if field == "title_min_length" || field == "title_max_length" {
			var n int
			if e = json.Unmarshal(value, &n); e != nil || n < 1 {
				return invalid(field, "positive integer required")
			}
		}
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(dst); e != nil {
		return invalid("body", e.Error())
	}
	return nil
}
func walkJSON(decoder *json.Decoder) error {
	token, e := decoder.Token()
	if e != nil {
		return e
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			token, e := decoder.Token()
			if e != nil {
				return e
			}
			key, ok := token.(string)
			if !ok {
				return fmt.Errorf("invalid object key")
			}
			if seen[key] {
				return fmt.Errorf("duplicate field %q", key)
			}
			seen[key] = true
			if e = walkJSON(decoder); e != nil {
				return e
			}
		}
	case '[':
		for decoder.More() {
			if e = walkJSON(decoder); e != nil {
				return e
			}
		}
	default:
		return fmt.Errorf("invalid JSON delimiter")
	}
	_, e = decoder.Token()
	return e
}
func invalid(field, message string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Rule: "invalid", Message: message}}}
}
func writeReply(w http.ResponseWriter, reply port.StoredReply) {
	for k, v := range reply.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(reply.Status)
	_, _ = w.Write(reply.Body)
}
func writeSuccess(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		OK   bool `json:"ok"`
		Data any  `json:"data"`
	}{true, data})
}
func writeError(w http.ResponseWriter, e error) {
	status, code, message := 500, "internal", "internal server error"
	var details any
	var validation *domain.ValidationError
	switch {
	case errors.As(e, &validation):
		status, code, message = 400, "validation_error", validation.Error()
		details = struct {
			Fields []domain.FieldError `json:"fields"`
		}{validation.Fields}
	case errors.Is(e, domain.ErrUnauthorized):
		status, code, message = 401, "unauthorized", "authentication required"
		w.Header().Set("WWW-Authenticate", "Bearer")
	case errors.Is(e, domain.ErrForbidden):
		status, code, message = 403, "forbidden", "project or session access denied"
	case errors.Is(e, domain.ErrNotFound):
		status, code, message = 404, "not_found", "resource not found"
	case errors.Is(e, domain.ErrConflict):
		status, code, message = 409, "conflict", "request conflicts with current state"
	case errors.Is(e, domain.ErrStale):
		status, code, message = 412, "conflict", "stale revision"
	case errors.Is(e, domain.ErrUnavailable):
		status, code, message = 503, "store_error", "store unavailable"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details any    `json:"details,omitempty"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	}{code, message, details}})
}
