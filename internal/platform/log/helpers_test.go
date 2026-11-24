package log

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestContextLogger proves a request-scoped logger round-trips through a
// context and that every fallback path returns the expected logger.
func TestContextLogger(t *testing.T) {
	stored := slog.New(slog.NewTextHandler(io.Discard, nil))
	fallback := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	// The logger stored in the context wins over the fallback.
	if got := FromContext(IntoContext(ctx, stored), fallback); got != stored {
		t.Fatal("stored logger was not returned")
	}
	// Without a stored logger the explicit fallback is used.
	if got := FromContext(ctx, fallback); got != fallback {
		t.Fatal("fallback logger was not returned")
	}
	// A nil context is tolerated and still resolves to the fallback.
	if got := FromContext(nil, fallback); got != fallback {
		t.Fatal("nil context did not return the fallback")
	}
	// With nothing available, the process default is the last resort.
	if got := FromContext(ctx); got != slog.Default() {
		t.Fatal("missing logger did not fall back to slog.Default")
	}
	// Storing nil must not shadow a real fallback.
	if got := FromContext(IntoContext(ctx, nil), fallback); got != fallback {
		t.Fatal("nil logger was stored in the context")
	}
}

// TestFromRequestAddsHTTPFields proves the helper keeps request_id from the
// context logger and adds the standard component, method and path fields.
func TestFromRequestAddsHTTPFields(t *testing.T) {
	var buf bytes.Buffer
	// Build the same shape lib.RequestLogger stores in the request context.
	base := slog.New(slog.NewJSONHandler(&buf, nil)).With("request_id", "rid-1")
	req := httptest.NewRequest(http.MethodPost, "/products/42", nil)
	req = req.WithContext(IntoContext(req.Context(), base))

	// The fallback must be ignored because the context already has a logger.
	fallback := slog.New(slog.NewTextHandler(io.Discard, nil))
	FromRequest(req, fallback, "products").Info("hello")

	// Decode the single line and check every standard field.
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	want := map[string]string{
		"request_id": "rid-1",
		"component":  "products",
		"method":     http.MethodPost,
		"path":       "/products/42",
	}
	for key, value := range want {
		if got := entry[key]; got != value {
			t.Errorf("%s = %v, want %q", key, got, value)
		}
	}
}

// TestFromRequestUsesFallback proves that without a context logger the helper
// still writes through the injected fallback and adds no request_id.
func TestFromRequestUsesFallback(t *testing.T) {
	var buf bytes.Buffer
	fallback := slog.New(slog.NewJSONHandler(&buf, nil))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	FromRequest(req, fallback, "health").Info("hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	if got := entry["component"]; got != "health" {
		t.Errorf("component = %v, want health", got)
	}
	// No middleware ran, so there is no correlation ID to report.
	if _, ok := entry["request_id"]; ok {
		t.Error("request_id present without a context logger")
	}
}
