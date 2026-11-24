// Package helpers holds the deliberately small set of shared HTTP utilities.
package helpers

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// JSON writes v to w as JSON with the given status code; a nil v writes the status alone.
func JSON(w http.ResponseWriter, status int, v any) {
	// Advertise JSON before the status line, since headers are frozen once written.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	// Encoding can only fail after headers are sent, so log instead of rewriting the response.
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response body", "error", err)
	}
}
