package lib

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWriteJSON proves the helper sets the JSON content type and status.
func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]bool{"ok": true})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}

	var body map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if !body["ok"] {
		t.Fatal("body.ok = false, want true")
	}
}

// TestWriteError proves the short signature keeps the repo's existing error
// envelope and derives a snake_case machine code from the status.
func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusTooManyRequests, "slow down")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body.Error.Code != "too_many_requests" {
		t.Fatalf("code = %q, want too_many_requests", body.Error.Code)
	}
	if body.Error.Message != "slow down" {
		t.Fatalf("message = %q, want slow down", body.Error.Message)
	}
}
