package products

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
)

// writeError maps a domain error onto a response, logs it at the level its cause
// deserves, and reports whether the caller may continue.
//
// It lives in this module because the mapping from product sentinel errors to
// HTTP status codes is transport policy, not logging and not validation.
func writeError(w http.ResponseWriter, logger *slog.Logger, r *http.Request, operation string, err error) bool {
	switch {
	case err == nil:
		// No error means the handler should keep going.
		return true
	case errors.Is(err, ErrInvalidInput):
		// A malformed request is the caller's mistake: warn, do not page anyone.
		logger.WarnContext(r.Context(), "product request rejected", "operation", operation, "error", err)
		helpers.Error(w, http.StatusBadRequest, "invalid_product", "product input is invalid")
	case errors.Is(err, ErrNotFound):
		// Missing and not-owned are answered identically so ownership cannot be probed.
		logger.WarnContext(r.Context(), "product not found", "operation", operation)
		helpers.Error(w, http.StatusNotFound, "not_found", "product not found")
	default:
		// Anything unexpected is a server-side failure worth investigating.
		logger.ErrorContext(r.Context(), "product request failed", "operation", operation, "error", err)
		helpers.Error(w, http.StatusInternalServerError, "internal_error", "could not complete request")
	}
	return false
}
