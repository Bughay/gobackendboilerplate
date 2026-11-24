package demo

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
)

// writeError maps a demo domain error onto an HTTP response, logs it at the
// level its cause deserves, and reports whether the caller may continue. It is
// the same pattern used by products/errors.go and orders/errors.go.
func writeError(w http.ResponseWriter, logger *slog.Logger, r *http.Request, operation string, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrInvalidInput):
		logger.WarnContext(r.Context(), "demo job request rejected",
			"operation", operation,
			"error", err,
		)
		helpers.Error(w, http.StatusBadRequest, "invalid_demo_job", "demo job input is invalid")
	case errors.Is(err, ErrNotFound):
		logger.WarnContext(r.Context(), "demo job not found", "operation", operation)
		helpers.Error(w, http.StatusNotFound, "not_found", "demo job not found")
	default:
		logger.ErrorContext(r.Context(), "demo job request failed",
			"operation", operation,
			"error", err,
		)
		helpers.Error(w, http.StatusInternalServerError, "internal_error", "could not complete request")
	}
	return false
}
