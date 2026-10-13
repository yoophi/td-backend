package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestHealthAndNoUnimplementedRoutesAdvertised(t *testing.T) {
	handler := New()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/healthz", nil))
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/json" {
		t.Fatal(response)
	}
	var data map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data["ok"] != true || data["data"].(map[string]any)["status"] != "ok" {
		t.Fatal(data)
	}
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest("GET", "/v1/issues", nil))
	if unknown.Code != 404 {
		t.Fatal("unimplemented route success")
	}
}
