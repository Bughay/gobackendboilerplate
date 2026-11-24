package products

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWriteError pins the response and log level for every error class the
// products handlers can produce.
func TestWriteError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantLevel  string
	}{
		{name: "nil continues", err: nil},
		{name: "invalid input", err: ErrInvalidInput, wantStatus: http.StatusBadRequest, wantCode: "invalid_product", wantLevel: "WARN"},
		{name: "not found", err: ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "not_found", wantLevel: "WARN"},
		{name: "unexpected", err: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error", wantLevel: "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/products", nil)

			cont := writeError(rec, logger, req, "create", tt.err)

			// A nil error is the only case that lets the handler continue.
			if tt.err == nil {
				if !cont {
					t.Fatal("writeError(nil) = false, want true")
				}
				if rec.Body.Len() != 0 {
					t.Fatalf("nil error wrote a body: %q", rec.Body.String())
				}
				if logBuf.Len() != 0 {
					t.Fatalf("nil error logged: %q", logBuf.String())
				}
				return
			}

			if cont {
				t.Fatal("writeError(error) = true, want false")
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			// The response carries the machine code the client sees.
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v (%q)", err, rec.Body.String())
			}
			if body.Error.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", body.Error.Code, tt.wantCode)
			}

			// The log line carries the level and operation the operator needs.
			var entry map[string]any
			if err := json.Unmarshal(logBuf.Bytes(), &entry); err != nil {
				t.Fatalf("log line is not JSON: %v (%q)", err, logBuf.String())
			}
			if entry["level"] != tt.wantLevel {
				t.Fatalf("level = %v, want %s", entry["level"], tt.wantLevel)
			}
			if entry["operation"] != "create" {
				t.Fatalf("operation = %v, want create", entry["operation"])
			}
			// Only the unexpected error is attached to the log record.
			if tt.wantLevel == "ERROR" {
				if got := entry["error"]; got != "boom" {
					t.Fatalf("error = %v, want boom", got)
				}
			}
		})
	}
}
