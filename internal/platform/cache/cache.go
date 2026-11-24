// Package cache owns the Redis-backed key/value store used by cross-cutting
// features such as the rate limiter. Nothing outside this package imports
// go-redis directly.
package cache

import (
	"context"
	"errors"
	"time"
)

// ErrCacheMiss is returned by Get when a key does not exist. Callers must compare
// with errors.Is — never by string comparison — and treat a miss as "compute it".
var ErrCacheMiss = errors.New("cache: key not found")

// Cache is the interface every consumer depends on. Keeping it small makes a
// test double trivial to write (see internal/platform/lib/ratelimit_test.go).
type Cache interface {
	// Get retrieves a raw JSON string by key. Returns ErrCacheMiss when absent.
	Get(ctx context.Context, key string) (string, error)

	// Set stores any value as JSON with the given TTL. TTL must always be
	// explicit; a zero TTL would create a key that never expires.
	Set(ctx context.Context, key string, value any, ttl time.Duration) error

	// Delete removes one or more keys.
	Delete(ctx context.Context, keys ...string) error

	// DeleteByPattern removes every key matching a glob using SCAN. Never use
	// Redis KEYS in production: it blocks the server for the whole scan.
	DeleteByPattern(ctx context.Context, pattern string) error

	// IncrementWindow atomically increments a fixed-window counter and returns
	// the new count plus the time left in the window. The increment and the
	// window expiry are applied atomically, so a counter can never be left
	// without a TTL (which would block a client forever).
	IncrementWindow(ctx context.Context, key string, window time.Duration) (count int64, reset time.Duration, err error)
}

// NopCache is a no-op implementation: reads always miss and writes are discarded.
// It is the startup fallback when Redis is unreachable, which makes the rate
// limiter fail open instead of taking the API down with it.
type NopCache struct{}

// Get always reports a miss because nothing is ever stored.
func (n *NopCache) Get(_ context.Context, _ string) (string, error) {
	return "", ErrCacheMiss
}

// Set discards the value.
func (n *NopCache) Set(_ context.Context, _ string, _ any, _ time.Duration) error {
	return nil
}

// Delete discards the request.
func (n *NopCache) Delete(_ context.Context, _ ...string) error { return nil }

// DeleteByPattern discards the request.
func (n *NopCache) DeleteByPattern(_ context.Context, _ string) error { return nil }

// IncrementWindow always reports a count of zero, so every request looks under
// the limit and the rate limiter fails open while Redis is unavailable.
func (n *NopCache) IncrementWindow(_ context.Context, _ string, window time.Duration) (int64, time.Duration, error) {
	return 0, window, nil
}
