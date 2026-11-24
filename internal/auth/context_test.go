package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
)

// TestCallerID covers the three states a request context can be in: no claims,
// usable claims, and claims that carry no user ID.
func TestCallerID(t *testing.T) {
	if id, ok := CallerID(context.Background()); ok || id != "" {
		t.Fatalf("CallerID(empty) = %q, %v; want empty, false", id, ok)
	}

	ctx := context.WithValue(context.Background(), claimsContextKey{}, &Claims{UserID: "u-1"})
	if id, ok := CallerID(ctx); !ok || id != "u-1" {
		t.Fatalf("CallerID(claims) = %q, %v; want u-1, true", id, ok)
	}

	// A claims value without a user ID must be treated as unauthenticated.
	empty := context.WithValue(context.Background(), claimsContextKey{}, &Claims{})
	if id, ok := CallerID(empty); ok || id != "" {
		t.Fatalf("CallerID(empty claims) = %q, %v; want empty, false", id, ok)
	}
}

// TestRequireCallerIDMissingPrincipal proves the guard logs and writes the same
// 401 envelope the products handlers returned before this helper moved.
func TestRequireCallerIDMissingPrincipal(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	// Give the guard a request-scoped logger so the warning is observable.
	req := httptest.NewRequest(http.MethodGet, "/products", nil)
	req = req.WithContext(platformlog.IntoContext(req.Context(), logger))
	rec := httptest.NewRecorder()

	id, ok := RequireCallerID(rec, req)
	if ok || id != "" {
		t.Fatalf("RequireCallerID() = %q, %v; want empty, false", id, ok)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	// The client sees the stable machine code, not the internal reason.
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body.Error.Code != "unauthorized" {
		t.Fatalf("code = %q, want unauthorized", body.Error.Code)
	}

	// The wiring problem is recorded at Warn so it is visible without alerting.
	var entry map[string]any
	if err := json.Unmarshal(logBuf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, logBuf.String())
	}
	if entry["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN", entry["level"])
	}
	if entry["msg"] != "request rejected: missing principal" {
		t.Fatalf("msg = %v, want the missing-principal warning", entry["msg"])
	}
}

// TestRequireCallerIDWithClaims proves an authenticated request passes through
// untouched.
func TestRequireCallerIDWithClaims(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/products", nil)
	req = req.WithContext(context.WithValue(req.Context(), claimsContextKey{}, &Claims{UserID: "u-1"}))
	rec := httptest.NewRecorder()

	id, ok := RequireCallerID(rec, req)
	if !ok || id != "u-1" {
		t.Fatalf("RequireCallerID() = %q, %v; want u-1, true", id, ok)
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("authenticated request was written to: code=%d body=%q", rec.Code, rec.Body.String())
	}
}
