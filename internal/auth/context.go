package auth

import (
	"context"
	"net/http"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
)

// CallerID returns the authenticated user's ID from ctx.
//
// It reports false when the request was not wrapped in Middleware or
// CookieMiddleware, or when the stored claims carry no user ID.
func CallerID(ctx context.Context) (string, bool) {
	// ClaimsFromContext returns nil for unauthenticated routes.
	claims := ClaimsFromContext(ctx)
	if claims == nil || claims.UserID == "" {
		return "", false
	}
	return claims.UserID, true
}

// RequireCallerID returns the authenticated caller's ID, or writes a 401 and
// reports false when the request has no validated claims.
//
// This is a guard for handlers, not a middleware: by the time a protected
// handler runs the JWT middleware should already have stored the claims, so a
// miss here means the route was mounted without that middleware.
func RequireCallerID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := CallerID(r.Context())
	if !ok {
		// Log the wiring problem with the request-scoped logger so the warning
		// shares request_id with the request's completion line.
		platformlog.FromContext(r.Context()).WarnContext(r.Context(), "request rejected: missing principal")
		helpers.Error(w, http.StatusUnauthorized, "unauthorized", "missing principal")
		return "", false
	}
	return userID, true
}
