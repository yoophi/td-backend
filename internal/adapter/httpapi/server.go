package httpapi

import (
	"encoding/json"
	"net/http"
)

// Foundation handler. Application routes are mounted as their capabilities land.
func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]string{"status": "ok", "api_version": "v1"}})
	})
	return mux
}
