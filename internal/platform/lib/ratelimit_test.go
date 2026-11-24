package lib

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/cache"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/config"
)

// fakeCache implements cache.Cache; only IncrementWindow is exercised by the
// rate limiter, so the rest are no-op stubs.
type fakeCache struct {
	count int64         // the count IncrementWindow reports
	reset time.Duration // the window-remaining IncrementWindow reports
	err   error         // when set, IncrementWindow fails (limiter must fail open)
}

func (f *fakeCache) Get(context.Context, string) (string, error) { return "", cache.ErrCacheMiss }

func (f *fakeCache) Set(context.Context, string, any, time.Duration) error { return nil }

func (f *fakeCache) Delete(context.Context, ...string) error { return nil }

func (f *fakeCache) DeleteByPattern(context.Context, string) error { return nil }

func (f *fakeCache) IncrementWindow(context.Context, string, time.Duration) (int64, time.Duration, error) {
	return f.count, f.reset, f.err
}

// quietLogger discards log output so the tests only assert HTTP behaviour.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRateLimit proves the limiter passes under/at the limit, rejects over it,
// honours the disabled flag and fails open when the cache errors.
func TestRateLimit(t *testing.T) {
	cfg := config.RateLimitConfig{Enabled: true, Requests: 100, WindowSec: 60}

	tests := []struct {
		name       string
		cfg        config.RateLimitConfig
		cache      *fakeCache
		wantStatus int
		wantRetry  bool // expect a Retry-After header
	}{
		{
			name:       "under limit passes",
			cfg:        cfg,
			cache:      &fakeCache{count: 1, reset: 60 * time.Second},
			wantStatus: http.StatusOK,
		},
		{
			name:       "at limit passes",
			cfg:        cfg,
			cache:      &fakeCache{count: 100, reset: 30 * time.Second},
			wantStatus: http.StatusOK,
		},
		{
			name:       "over limit is rejected",
			cfg:        cfg,
			cache:      &fakeCache{count: 101, reset: 30 * time.Second},
			wantStatus: http.StatusTooManyRequests,
			wantRetry:  true,
		},
		{
			name:       "disabled always passes",
			cfg:        config.RateLimitConfig{Enabled: false, Requests: 1, WindowSec: 60},
			cache:      &fakeCache{count: 9999, reset: 60 * time.Second},
			wantStatus: http.StatusOK,
		},
		{
			name:       "cache error fails open",
			cfg:        cfg,
			cache:      &fakeCache{err: errors.New("redis down")},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// reached records whether the wrapped handler ran, which proves the
			// limiter either passed the request through or short-circuited it.
			var reached bool
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			// A nil resolver means no proxies are trusted, so the socket peer is
			// used. That is the shipped default and is covered in clientip_test.go.
			h := RateLimit(tt.cache, quietLogger(), tt.cfg, nil)(next)

			req := httptest.NewRequest(http.MethodGet, "/anything", nil)
			req.RemoteAddr = "203.0.113.7:54321"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			wantReached := tt.wantStatus == http.StatusOK
			if reached != wantReached {
				t.Fatalf("next handler reached = %v, want %v", reached, wantReached)
			}
			if got := rec.Header().Get("Retry-After"); (got != "") != tt.wantRetry {
				t.Fatalf("Retry-After present = %v (%q), want %v", got != "", got, tt.wantRetry)
			}
		})
	}
}

// TestRateLimitSkipsHealth proves /health never consumes quota: a 429 on the
// liveness probe would make Docker restart a healthy container.
func TestRateLimitSkipsHealth(t *testing.T) {
	// The fake cache always reports a count far past the limit, so any request
	// that reached the limiter would be rejected.
	cfg := config.RateLimitConfig{Enabled: true, Requests: 1, WindowSec: 60}
	cache := &fakeCache{count: 9999, reset: 60 * time.Second}

	var reached bool
	h := RateLimit(cache, quietLogger(), cfg, nil)(okHandler(&reached))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if !reached {
		t.Fatal("health request was rate limited, want it to pass through")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
