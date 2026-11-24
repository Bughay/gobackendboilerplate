package orders

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
)

// writeError maps an order domain error onto a response, logs it at the level
// its cause deserves, and reports whether the caller may continue.
func writeError(w http.ResponseWriter, logger *slog.Logger, r *http.Request, operation string, err error) bool {
	switch {
	case err == nil:
		// No error means the handler should keep going.
		return true
	case errors.Is(err, ErrInvalidInput):
		// A malformed request is the caller's mistake: warn, do not page anyone.
		logger.WarnContext(r.Context(), "order request rejected", "operation", operation, "error", err)
		helpers.Error(w, http.StatusBadRequest, "invalid_order", "order input is invalid")
	case errors.Is(err, ErrNotFound):
		// Missing and not-owned are answered identically so ownership cannot be probed.
		logger.WarnContext(r.Context(), "order not found", "operation", operation)
		helpers.Error(w, http.StatusNotFound, "not_found", "order not found")
	case errors.Is(err, ErrProductNotFound):
		// A product missing from the caller's catalog is also indistinguishable
		// from one owned by another user.
		logger.WarnContext(r.Context(), "order products not found", "operation", operation)
		helpers.Error(w, http.StatusNotFound, "not_found", "one or more products were not found")
	case errors.Is(err, ErrUserNotFound):
		// The token is valid but its account is gone, so the session is dead.
		logger.WarnContext(r.Context(), "order caller no longer exists", "operation", operation)
		helpers.Error(w, http.StatusUnauthorized, "unauthorized", "user no longer exists")
	default:
		// Anything unexpected is a server-side failure worth investigating.
		logger.ErrorContext(r.Context(), "order request failed", "operation", operation, "error", err)
		helpers.Error(w, http.StatusInternalServerError, "internal_error", "could not complete request")
	}
	return false
}
