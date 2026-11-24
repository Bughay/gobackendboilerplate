package lib

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/cache"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/config"
)

// RateLimit returns middleware that caps how many requests a single client IP may
// make per fixed window (a counter held in Redis through the cache package). On
// exceeding the limit it responds 429 with a Retry-After header.
//
// The client IP comes from resolver, which only honours X-Forwarded-For when the
// peer is a configured trusted proxy. A nil resolver uses the socket peer, so a
// directly exposed API can never have its buckets chosen by the caller.
//
// It fails open: if the counter backend errors, the request is allowed, because a
// limiter outage must not lock every user out of the API.
func RateLimit(c cache.Cache, logger *slog.Logger, cfg config.RateLimitConfig, resolver *ClientIPResolver) func(http.Handler) http.Handler {
	// Convert the config once per middleware construction, not per request.
	window := time.Duration(cfg.WindowSec) * time.Second
	limit := int64(cfg.Requests)

	return func(next http.Handler) http.Handler {
		// A disabled or invalid limiter is the identity middleware.
		if !cfg.Enabled || limit <= 0 || window <= 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Health probes must never consume a client's quota: answering 429
			// here would make Docker restart a perfectly healthy container.
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			ip := resolver.ClientIP(r)

			// Count this request in the current window. The cache increments and
			// sets the window expiry atomically.
			count, reset, err := c.IncrementWindow(r.Context(), "ratelimit:ip:"+ip, window)
			if err != nil {
				// Fail open: allow the request, but record that the limiter is blind.
				logger.WarnContext(r.Context(), "rate limiter unavailable — allowing request",
					"ip", ip, "error", err)
				next.ServeHTTP(w, r)
				return
			}

			// Remaining is never negative, even on the request that trips the limit.
			remaining := limit - count
			if remaining < 0 {
				remaining = 0
			}
			// Round up to whole seconds and keep at least 1 so clients never retry
			// immediately and get stuck in a loop.
			resetSec := int(reset.Seconds()) + 1
			if resetSec < 1 {
				resetSec = 1
			}

			w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(limit, 10))
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
			w.Header().Set("X-RateLimit-Reset", strconv.Itoa(resetSec))

			// The counter is already past the limit, so reject before the handler.
			if count > limit {
				w.Header().Set("Retry-After", strconv.Itoa(resetSec))
				logger.WarnContext(r.Context(), "rate limit exceeded",
					"ip", ip, "count", count, "limit", limit)
				WriteError(w, http.StatusTooManyRequests, "rate limit exceeded, slow down")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
