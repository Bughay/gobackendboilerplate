package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/config"
)

// NewRedisClient creates a Redis client from the typed config and pings it.
// The ping makes a wrong address fail at boot instead of on the first request.
// Callers should fall back to NopCache when this returns an error.
func NewRedisClient(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// Prove the connection works before handing the client to the rest of the
	// process; a lazy client would hide a typo until traffic arrives.
	if err := client.Ping(ctx).Err(); err != nil {
		// Close the half-built client so a failed boot does not leak its pool.
		_ = client.Close()
		return nil, fmt.Errorf("redis: cannot connect to %s: %w", cfg.Addr, err)
	}
	return client, nil
}
