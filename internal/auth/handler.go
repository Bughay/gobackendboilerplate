package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
)

const (
	// refreshCookieName is the single cookie name used by every auth endpoint.
	refreshCookieName = "refresh_token"
	// refreshCookiePath is "/" to match the reference implementation. It means
	// the refresh cookie is also sent to non-auth routes; see the docs.
	refreshCookiePath = "/"
)

// AuthHandler serves the four public authentication endpoints.
type AuthHandler struct {
	authSvc AuthService
	logger  *slog.Logger
	// cookieSecure controls the Secure attribute on the refresh cookie. It is
	// injected from configuration so HTTPS-only behaviour is the production
	// default while plain-HTTP local testing stays possible.
	cookieSecure bool
}

// NewAuthHandler returns a handler backed by authSvc. cookieSecure marks the
// refresh cookie Secure, so browsers send it only over HTTPS.
func NewAuthHandler(authSvc AuthService, logger *slog.Logger, cookieSecure bool) *AuthHandler {
	return &AuthHandler{authSvc: authSvc, logger: logger, cookieSecure: cookieSecure}
}

// loggerFor returns the logger for one auth request. It prefers the
// request-scoped logger installed by lib.RequestLogger, which carries
// request_id, and falls back to the handler's own logger when the handler is
// used without that middleware (unit tests, for example).
func (h *AuthHandler) loggerFor(r *http.Request) *slog.Logger {
	return platformlog.FromContext(r.Context(), h.logger).With(
		"component", "auth",
		"method", r.Method,
		"path", r.URL.Path,
	)
}

// Register creates an account. It returns 201 and deliberately issues no token
// or cookie; the client calls Login afterward.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	log := h.loggerFor(r)

	var req RegisterRequest
	if !helpers.Decode(w, r, &req) {
		log.WarnContext(r.Context(), "registration rejected: invalid request body")
		return
	}

	resp, err := h.authSvc.Register(r.Context(), &req)
	if err != nil {
		var ve *validationError
		if errors.As(err, &ve) {
			log.WarnContext(r.Context(), "registration validation failed", "error", ve.message)
			helpers.Error(w, http.StatusUnprocessableEntity, "validation_error", ve.message)
			return
		}
		log.ErrorContext(r.Context(), "registration failed", "error", err)
		helpers.Error(w, http.StatusInternalServerError, "internal_error", "registration failed")
		return
	}

	log.InfoContext(r.Context(), "user registered", "user_id", resp.Person.ID)
	helpers.JSON(w, http.StatusCreated, resp)
}

// Login verifies credentials, sets the refresh cookie, and returns the access
// token plus the user projection.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	log := h.loggerFor(r)

	var req LoginRequest
	if !helpers.Decode(w, r, &req) {
		log.WarnContext(r.Context(), "login rejected: invalid request body")
		return
	}

	resp, refreshToken, err := h.authSvc.Login(r.Context(), &req)
	if err != nil {
		if errors.Is(err, errInvalidCredentials) {
			log.WarnContext(r.Context(), "login failed", "error", err)
			helpers.Error(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		log.ErrorContext(r.Context(), "login failed", "error", err)
		helpers.Error(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	// Set the cookie before the body so the header is present on the response.
	h.setRefreshCookie(w, refreshToken)
	log.InfoContext(r.Context(), "user logged in", "user_id", resp.User.ID)
	helpers.JSON(w, http.StatusOK, resp)
}

// Logout revokes the presented refresh token when one is present and always
// clears the cookie. It is idempotent and returns 200 in every case.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	log := h.loggerFor(r)

	if token, ok := readRefreshCookie(r); ok {
		// Best effort: a storage failure must not trap the user in a session.
		_ = h.authSvc.Logout(r.Context(), token)
	}
	h.clearRefreshCookie(w)
	log.InfoContext(r.Context(), "user logged out")
	helpers.JSON(w, http.StatusOK, map[string]string{"message": "logged out successfully"})
}

// Refresh reads the refresh cookie, rotates the token pair, and replaces the
// cookie with the new refresh token.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	log := h.loggerFor(r)

	token, ok := readRefreshCookie(r)
	if !ok {
		log.WarnContext(r.Context(), "refresh rejected: missing cookie")
		helpers.Error(w, http.StatusUnauthorized, "unauthorized", "refresh token required")
		return
	}

	resp, newRefreshToken, err := h.authSvc.Refresh(r.Context(), token)
	if err != nil {
		// A dead/replayed token is a 401 and the cookie should not survive it.
		if errors.Is(err, errInvalidToken) || errors.Is(err, errTokenRevoked) || errors.Is(err, errUserNotFound) {
			log.WarnContext(r.Context(), "token refresh failed", "error", err)
			h.clearRefreshCookie(w)
			helpers.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired refresh token")
			return
		}
		log.ErrorContext(r.Context(), "token refresh failed", "error", err)
		helpers.Error(w, http.StatusInternalServerError, "internal_error", "could not refresh token")
		return
	}

	h.setRefreshCookie(w, newRefreshToken)
	log.InfoContext(r.Context(), "token refreshed")
	helpers.JSON(w, http.StatusOK, resp)
}

// setRefreshCookie writes the refresh token with the attributes documented in
// the auth README. HttpOnly keeps it away from JavaScript, Path is "/" so every
// auth route can read it, and Secure follows configuration.
func (h *AuthHandler) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		MaxAge:   int(h.authSvc.RefreshExpiry().Seconds()),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearRefreshCookie tells the browser to delete the refresh cookie now.
func (h *AuthHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// readRefreshCookie returns the refresh token if the client sent a non-empty one.
func readRefreshCookie(r *http.Request) (string, bool) {
	c, err := r.Cookie(refreshCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}
