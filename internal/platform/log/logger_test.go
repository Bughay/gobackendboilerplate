package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestNewLoggerLevels proves the textual level is mapped onto slog's filter,
// which is the part of NewLogger that callers depend on.
func TestNewLoggerLevels(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		level     string
		wantDebug bool
		wantInfo  bool
	}{
		{name: "debug enables debug", level: "debug", wantDebug: true, wantInfo: true},
		{name: "info is the default", level: "", wantDebug: false, wantInfo: true},
		{name: "unknown falls back to info", level: "verbose", wantDebug: false, wantInfo: true},
		{name: "error hides info", level: "error", wantDebug: false, wantInfo: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewLogger("json", tt.level)

			if got := logger.Enabled(ctx, slog.LevelDebug); got != tt.wantDebug {
				t.Fatalf("debug enabled = %v, want %v", got, tt.wantDebug)
			}
			if got := logger.Enabled(ctx, slog.LevelInfo); got != tt.wantInfo {
				t.Fatalf("info enabled = %v, want %v", got, tt.wantInfo)
			}
		})
	}
}

// TestNewLoggerRedactsSensitiveAttrs proves the handler options wired by
// newLogger mask credentials before they are encoded. The writer is injected so
// the test can inspect the real JSON that NewLogger would print to stdout.
func TestNewLoggerRedactsSensitiveAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, "json", "debug")

	// Two credential fields, one header with odd casing, and one harmless field.
	logger.Info("login",
		"user_id", "u-1",
		"password", "super-secret-password",
		"access_token", "raw-access-token-value",
		"Authorization", "Bearer raw-bearer-value",
	)

	// Decode the single line so the assertions look at the emitted values.
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}

	// Every credential-shaped key must be replaced, regardless of case.
	for _, key := range []string{"password", "access_token", "Authorization"} {
		if got := entry[key]; got != "[REDACTED]" {
			t.Errorf("%s = %v, want [REDACTED]", key, got)
		}
	}
	// A non-sensitive field must survive untouched.
	if got := entry["user_id"]; got != "u-1" {
		t.Errorf("user_id = %v, want u-1", got)
	}
	// Defence in depth: none of the original secrets may appear anywhere.
	for _, secret := range []string{"super-secret-password", "raw-access-token-value", "raw-bearer-value"} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("log output leaked %q: %q", secret, buf.String())
		}
	}
}
