// Package lib holds the small cross-cutting HTTP building blocks shared by every
// module: response helpers, middleware and the rate limiter.
package lib

import (
	"net/http"
	"strings"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
)

// WriteJSON writes v as JSON with the given status code. It delegates to
// helpers.JSON so the boilerplate keeps exactly one response writer.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	helpers.JSON(w, status, v)
}

// WriteError writes a JSON error envelope using a machine code derived from the
// HTTP status. It exists so middleware can call the short reference-style
// signature while the body keeps the repo's {"error":{...}} shape.
func WriteError(w http.ResponseWriter, status int, message string) {
	helpers.Error(w, status, errorCode(status), message)
}

// WriteErrorCode writes a JSON error envelope with an explicit machine code,
// for callers that need a domain-specific code rather than the status default.
func WriteErrorCode(w http.ResponseWriter, status int, code, message string) {
	helpers.Error(w, status, code, message)
}

// errorCode turns an HTTP status into a stable snake_case code, e.g. 429 becomes
// "too_many_requests". Unknown statuses map to the generic "error".
func errorCode(status int) string {
	text := http.StatusText(status)
	if text == "" {
		return "error"
	}
	return strings.ReplaceAll(strings.ToLower(text), " ", "_")
}
