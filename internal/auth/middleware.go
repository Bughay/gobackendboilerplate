package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
)

// claimsContextKey is an unexported type so no other package can collide with
// the value stored in the request context.
type claimsContextKey struct{}

// Middleware authenticates an "Authorization: Bearer <access token>" header and
// stores the validated claims in the request context.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			helpers.Error(w, http.StatusUnauthorized, "unauthorized", "authorization header is required")
			return
		}

		// Split on the first space so a token containing spaces is not mangled.
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || strings.TrimSpace(parts[1]) == "" {
			helpers.Error(w, http.StatusUnauthorized, "unauthorized", "authorization header format must be: Bearer {token}")
			return
		}

		claims, err := m.ValidateAccess(strings.TrimSpace(parts[1]))
		if err != nil {
			helpers.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		// Downstream handlers only see the claims, never the raw token.
		ctx := context.WithValue(r.Context(), claimsContextKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CookieMiddleware authenticates a request from the refresh cookie. It is
// available for page-style routes; the product API uses Middleware instead.
// secure marks the clearing cookie Secure so this path cannot emit an insecure
// cookie while the login path emits a Secure one.
func (m *Manager) CookieMiddleware(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(refreshCookieName)
			if err != nil || cookie.Value == "" {
				helpers.Error(w, http.StatusUnauthorized, "unauthorized", "authentication cookie required")
				return
			}

			claims, err := m.ValidateRefresh(cookie.Value)
			if err != nil {
				// Clear a cookie that can no longer authenticate anything. The
				// attributes must match the ones used to set it, otherwise the
				// browser keeps the original cookie.
				http.SetCookie(w, &http.Cookie{
					Name:     refreshCookieName,
					Value:    "",
					Path:     refreshCookiePath,
					MaxAge:   -1,
					HttpOnly: true,
					Secure:   secure,
					SameSite: http.SameSiteLaxMode,
				})
				helpers.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired session")
				return
			}

			ctx := context.WithValue(r.Context(), claimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext returns the validated claims, or nil when the route was not
// wrapped in Middleware/CookieMiddleware.
func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsContextKey{}).(*Claims)
	return claims
}

// RequireRole wraps next with a membership check over Claims.Roles.
func (m *Manager) RequireRole(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil {
			helpers.Error(w, http.StatusForbidden, "forbidden", "you do not have permission to perform this action")
			return
		}

		// Roles is a short slice, so a linear scan is cheaper than a map.
		for _, held := range claims.Roles {
			if held == role {
				next.ServeHTTP(w, r)
				return
			}
		}
		helpers.Error(w, http.StatusForbidden, "forbidden", "you do not have permission to perform this action")
	})
}
