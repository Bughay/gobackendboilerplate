package lib

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
)

// responseWriter captures the status code and body size while delegating the
// actual writes, so the request logger can report what the handler decided.
type responseWriter struct {
	http.ResponseWriter
	status  int
	written bool
	bytes   int64
}

// WriteHeader records the first status and forwards it. Later calls are ignored
// because a real ResponseWriter would ignore them too.
func (rw *responseWriter) WriteHeader(status int) {
	if rw.written {
		return
	}
	rw.status = status
	rw.written = true
	rw.ResponseWriter.WriteHeader(status)
}

// Write records an implicit 200 the first time the handler writes a body, then
// forwards the bytes and counts how many the underlying writer accepted.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.status = http.StatusOK
		rw.written = true
	}
	// Only the bytes actually accepted by the writer belong in the log line.
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += int64(n)
	return n, err
}

// Unwrap exposes the underlying writer to http.ResponseController, which keeps
// Flush, Hijack and SetDeadline working through this wrapper.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// CORS returns middleware that allows browser requests from localhost (any
// scheme and port) plus the extra origins configured in CORS_ALLOWED_ORIGINS.
//
// It echoes the specific origin rather than "*" because credentials may be sent,
// and it answers preflight OPTIONS requests itself so they never reach the JWT
// middleware or the rate limiter.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	// Normalize the configured origins once, when the middleware is built, so the
	// per-request path only does a map lookup.
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if key, ok := normalizeOrigin(origin); ok {
			allowed[key] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Vary tells caches that the response depends on the Origin header.
			w.Header().Add("Vary", "Origin")
			// These headers are safe for every origin, including denied ones.
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			origin := r.Header.Get("Origin")
			if isAllowedOrigin(origin, allowed) {
				// Echo the exact origin; "*" is illegal together with credentials.
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			// Answer preflight here and stop: an OPTIONS request must not run
			// authentication or consume a rate-limit slot.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isAllowedOrigin reports whether an Origin header may receive CORS headers.
// Localhost (IPv4 or IPv6 loopback) is always allowed; anything else must match
// one of the configured origins exactly.
func isAllowedOrigin(origin string, allowed map[string]struct{}) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}

	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}

	// Development frontends run on arbitrary ports, so match only the host.
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}

	// Production origins must match scheme + host + port exactly.
	key, ok := normalizeOrigin(origin)
	if !ok {
		return false
	}
	_, ok = allowed[key]
	return ok
}

// normalizeOrigin validates a bare origin and returns it as lowercase
// scheme://host[:port]. It rejects paths, queries and fragments because an
// Origin header never contains them.
func normalizeOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	// Only http(s) origins are meaningful for a browser CORS check.
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	// A configured value with a path or query would never match a real Origin.
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), true
}

// RequestLogger returns middleware that gives every request a correlation ID,
// stores a request-scoped logger in the context, and logs one completion line
// at Info level: method, path, status, duration, bytes, remote address and user
// agent.
//
// The ID is always generated server-side. An incoming X-Request-ID is ignored on
// purpose: trusting client input would let a caller choose another request's ID
// and poison the logs or confuse an operator tracing an incident.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Start before the handler and wrap the writer so the final status
			// and body size are available when the request finishes.
			start := time.Now()

			// Give this request its own ID and return it to the caller so a user
			// can quote it in a bug report.
			requestID := newRequestID()
			w.Header().Set("X-Request-ID", requestID)

			// The child logger carries request_id into every line written by the
			// handlers below, including their business events.
			requestLogger := logger.With("request_id", requestID)

			// Replace the request with one whose context carries that logger.
			// Handlers read it back with platformlog.FromContext.
			r = r.WithContext(platformlog.IntoContext(r.Context(), requestLogger))

			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			// One completion line per request. InfoContext keeps any future
			// context values (trace IDs) attached to the record.
			requestLogger.InfoContext(r.Context(), "request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes", rw.bytes,
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			)
		})
	}
}

// newRequestID returns a 128-bit random hexadecimal identifier. 16 bytes makes a
// collision between concurrent requests practically impossible, and hex keeps
// the value safe to put in a header and a log line.
func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing is effectively impossible on a supported platform;
		// fall back to the clock so the request still gets a unique-ish label.
		return "t" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf[:])
}
