package lib

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
)

// okHandler is the terminal handler used by the middleware tests.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

// TestCORSAllowsLocalhost proves any localhost port is accepted and echoed.
func TestCORSAllowsLocalhost(t *testing.T) {
	var reached bool
	h := CORS(nil)(okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("next handler was not reached")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Allow-Origin = %q, want the echoed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Allow-Credentials = %q, want true", got)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Fatalf("Vary = %q, want it to contain Origin", rec.Header().Get("Vary"))
	}
}

// TestCORSAllowsConfiguredOrigin proves an exact configured origin is accepted
// even when the config value has a trailing slash and mixed case.
func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	var reached bool
	h := CORS([]string{"https://App.Example.com/"})(okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Allow-Origin = %q, want the configured origin", got)
	}
}

// TestCORSRejectsUnknownOrigin proves an unconfigured origin gets no CORS grant.
func TestCORSRejectsUnknownOrigin(t *testing.T) {
	var reached bool
	h := CORS(nil)(okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want it to be empty", got)
	}
}

// TestCORSPreflight proves OPTIONS is answered with 204 and never reaches next.
func TestCORSPreflight(t *testing.T) {
	var reached bool
	h := CORS(nil)(okHandler(&reached))

	req := httptest.NewRequest(http.MethodOptions, "/auth/login", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if reached {
		t.Fatal("preflight reached the next handler")
	}
}

// TestCORSNoOriginPasses proves server-to-server requests are untouched.
func TestCORSNoOriginPasses(t *testing.T) {
	var reached bool
	h := CORS(nil)(okHandler(&reached))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !reached {
		t.Fatal("next handler was not reached")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want it to be empty", got)
	}
}

// TestRequestLoggerRecordsStatus proves the logger sees the handler's real
// status and request metadata as one JSON line.
func TestRequestLoggerRecordsStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/products", nil)
	req.RemoteAddr = "192.0.2.9:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Decode the single line the middleware wrote.
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	if got := entry["status"]; got != float64(http.StatusCreated) {
		t.Fatalf("logged status = %v, want 201", got)
	}
	if got := entry["method"]; got != http.MethodPost {
		t.Fatalf("logged method = %v, want POST", got)
	}
	if got := entry["path"]; got != "/products" {
		t.Fatalf("logged path = %v, want /products", got)
	}
	// The middleware must return the correlation ID it logged on the response.
	requestID := rec.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("X-Request-ID header is missing")
	}
	if got := entry["request_id"]; got != requestID {
		t.Fatalf("logged request_id = %v, want %q", got, requestID)
	}
}

// TestRequestLoggerImplicitOK proves a handler that only writes a body still
// logs 200 rather than the zero value.
func TestRequestLoggerImplicitOK(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if got := entry["status"]; got != float64(http.StatusOK) {
		t.Fatalf("logged status = %v, want 200", got)
	}
}

// TestRequestLoggerInjectsContextLogger proves the handler receives a logger
// that already carries request_id, so business events and the completion line
// can be correlated.
func TestRequestLoggerInjectsContextLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	// Capture the logger exactly the way a real handler would obtain it, and
	// emit one business event before the middleware writes its completion line.
	var handlerLogger *slog.Logger
	h := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerLogger = platformlog.FromContext(r.Context())
		handlerLogger.Info("inside handler")
		w.WriteHeader(http.StatusOK)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	if handlerLogger == nil {
		t.Fatal("handler did not receive a logger")
	}

	// One line from the handler, one from the middleware.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2: %q", len(lines), buf.String())
	}

	// Decode the business line and the completion line.
	var business, completion map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &business); err != nil {
		t.Fatalf("business line is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &completion); err != nil {
		t.Fatalf("completion line is not JSON: %v", err)
	}

	if business["msg"] != "inside handler" {
		t.Fatalf("first line msg = %v, want inside handler", business["msg"])
	}
	if business["request_id"] == nil || business["request_id"] != completion["request_id"] {
		t.Fatalf("request ids differ: business = %v, completion = %v",
			business["request_id"], completion["request_id"])
	}
}
