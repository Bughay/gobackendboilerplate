// Package db owns the PostgreSQL connection pool. It is the only package that
// knows how to open a connection; every store and module receives the pool and
// never builds its own.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// defaultConnectTimeout bounds the startup Ping, so a wrong host or a
	// database that is still booting fails fast instead of hanging the process.
	defaultConnectTimeout = 5 * time.Second
	// defaultMaxConns caps the pool when the caller does not ask for a size.
	// Postgres allows 100 connections by default and other services may share
	// the same server, so 10 leaves room to spare.
	defaultMaxConns = 10
)

// Options tunes the pool. The zero value is valid and uses the defaults above.
type Options struct {
	// MaxConns is the hard ceiling on pooled connections.
	MaxConns int32
	// ConnectTimeout bounds the startup Ping only, not individual queries.
	ConnectTimeout time.Duration
}

// New opens a pgx pool, proves the connection works, and returns it. The caller
// owns the pool and must call Close on shutdown.
func New(ctx context.Context, dsn string, opts Options) (*pgxpool.Pool, error) {
	// Parse first so a malformed DATABASE_URL is reported as a configuration
	// problem instead of a confusing connection error.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: invalid DATABASE_URL: %w", err)
	}

	// A zero or negative option means "use the default".
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	} else {
		cfg.MaxConns = defaultMaxConns
	}

	// NewWithConfig does not dial; the pool connects lazily on first use.
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: could not create pool: %w", err)
	}

	// Ping once at boot so an unreachable server or a wrong password fails here,
	// rather than on the first user request.
	timeout := opts.ConnectTimeout
	if timeout <= 0 {
		timeout = defaultConnectTimeout
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		// Release any sockets the failed attempt opened before reporting.
		pool.Close()
		return nil, fmt.Errorf("db: could not reach postgres: %w", err)
	}
	return pool, nil
}
