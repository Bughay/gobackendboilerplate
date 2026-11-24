package lib

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// securityHeaderCases is the exact set of headers the API promises to send.
// Keeping it as data means a removed header fails the test explicitly.
var securityHeaderCases = map[string]string{
	"X-Content-Type-Options":            "nosniff",
	"X-Frame-Options":                   "DENY",
	"Content-Security-Policy":           "default-src 'none'; frame-ancestors 'none'",
	"Referrer-Policy":                   "no-referrer",
	"Strict-Transport-Security":         "max-age=31536000; includeSubDomains",
	"X-Permitted-Cross-Domain-Policies": "none",
}

// assertSecurityHeaders fails when any promised header is missing or wrong.
func assertSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for name, want := range securityHeaderCases {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestSecurityHeadersOnSuccess proves a normal response is hardened.
func TestSecurityHeadersOnSuccess(t *testing.T) {
	var reached bool
	h := SecurityHeaders()(okHandler(&reached))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if !reached {
		t.Fatal("next handler was not reached")
	}
	assertSecurityHeaders(t, rec)
}

// TestSecurityHeadersOnError proves error responses are hardened too, since they
// are the ones most likely to be rendered or cached by a client.
func TestSecurityHeadersOnError(t *testing.T) {
	h := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/products", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertSecurityHeaders(t, rec)
}

// TestSecurityHeadersOnPreflight proves the CORS 204, which is written by an
// inner middleware, still carries the headers because SecurityHeaders is outermost.
func TestSecurityHeadersOnPreflight(t *testing.T) {
	var reached bool
	h := SecurityHeaders()(CORS(nil)(okHandler(&reached)))

	req := httptest.NewRequest(http.MethodOptions, "/auth/login", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if reached {
		t.Fatal("preflight reached the next handler")
	}
	assertSecurityHeaders(t, rec)
}

// TestSecurityHeadersOmitCORP proves the cross-origin-blocking headers are NOT
// set, because this API is meant to be called from other origins.
func TestSecurityHeadersOmitCORP(t *testing.T) {
	h := SecurityHeaders()(okHandler(new(bool)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for _, name := range []string{"Cross-Origin-Resource-Policy", "Cross-Origin-Opener-Policy"} {
		if got := rec.Header().Get(name); got != "" {
			t.Errorf("%s = %q, want it unset: it would block cross-origin API use", name, got)
		}
	}
}
