// Package main is the API composition root: it wires configuration, storage,
// auth, background-job enqueueing and routes.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Bughay/ego-backend-boilerplate/internal/auth"
	"github.com/Bughay/ego-backend-boilerplate/internal/modules/demo"
	"github.com/Bughay/ego-backend-boilerplate/internal/modules/orders"
	"github.com/Bughay/ego-backend-boilerplate/internal/modules/products"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/cache"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/config"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/db"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/jobs"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/lib"
	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/openapi"
)

func main() {
	// Load configuration first: every runtime setting, including the listen
	// address, comes from this one source. A container with invalid configuration
	// fails fast instead of starting with a partial setup.
	cfg, err := config.Load()
	if err != nil {
		// The configured logger does not exist yet, so report the fatal config
		// error on stderr rather than through slog.
		fmt.Fprintf(os.Stderr, "could not load configuration: %v\n", err)
		os.Exit(1)
	}

	// One logger for the whole process, built from LOG_FORMAT and LOG_LEVEL.
	// Auth, request logging, startup and shutdown all share this instance.
	logger := platformlog.NewLogger(cfg.Log.Format, cfg.Log.Level)

	// Make that logger the process default too. Package-level slog calls (such as
	// helpers.JSON) and third-party libraries then write to the same stream and
	// format instead of the standard library's stderr default.
	slog.SetDefault(logger)

	// Cancelling this context on SIGINT/SIGTERM is what starts the graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The pool is shared by every repository below and closed on the way out. It
	// is built here so a bad DSN fails at boot instead of on the first request.
	pool, err := db.New(ctx, cfg.Database.DSN, db.Options{})
	if err != nil {
		logger.Error("could not connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Redis backs the rate limiter. A miss is not fatal: NopCache makes the
	// limiter fail open, so the API stays available while the cache is down.
	var appCache cache.Cache
	redisClient, err := cache.NewRedisClient(ctx, cfg.Redis)
	if err != nil {
		logger.Warn("redis unavailable — rate limiter fails open, using NopCache",
			"addr", cfg.Redis.Addr, "error", err)
		appCache = &cache.NopCache{}
	} else {
		logger.Info("redis connected", "addr", cfg.Redis.Addr)
		appCache = cache.NewRedisCache(redisClient)
		defer redisClient.Close()
	}

	// The API only enqueues background work; cmd/worker consumes it. The client
	// talks to WORKER_REDIS_DB (never the cache's REDIS_DB) and opens its
	// connection lazily on the first enqueue, so a Redis outage at boot does not
	// stop the API from serving. Close releases the pool on the way out.
	jobsClient := jobs.NewClient(jobs.RedisOpt(cfg.Redis, cfg.Worker.RedisDB))
	defer jobsClient.Close()

	// Auth wiring: manager -> repositories -> service -> handler.
	jwtManager := auth.NewManager(cfg.JWT.Secret, cfg.JWT.AccessExpiryHours, cfg.JWT.RefreshExpiryDays*24)
	userRepo := auth.NewUserRepository(pool)
	refreshTokenRepo := auth.NewRefreshTokenRepository(pool)
	authSvc := auth.NewAuthService(userRepo, refreshTokenRepo, jwtManager)
	authHandler := auth.NewAuthHandler(authSvc, logger, cfg.Cookie.Secure)

	// The resolver decides which address the rate limiter counts. It is built
	// once so the trusted-proxy list is parsed a single time, and a bad entry
	// fails boot even if configuration validation was somehow bypassed.
	clientIPResolver, err := lib.NewClientIPResolver(cfg.Proxy.TrustedProxies)
	if err != nil {
		logger.Error("could not build client IP resolver", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	// Liveness only: an empty 200 means the process is serving requests. No
	// auth, no dependency checks, and no rate-limit slot.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Interactive API docs at /docs/ and the raw OpenAPI 3.0 spec at
	// /openapi.json. Both are public and embedded in the binary, so they work
	// in the Docker image and from any working directory.
	openapi.RegisterRoutes(mux)

	// The four public auth endpoints. Register/Login create credentials;
	// Logout/Refresh operate on the refresh cookie.
	mux.HandleFunc("POST /auth/register", authHandler.Register)
	mux.HandleFunc("POST /auth/login", authHandler.Login)
	mux.HandleFunc("POST /auth/logout", authHandler.Logout)
	mux.HandleFunc("POST /auth/refresh", authHandler.Refresh)

	// Products CRUD is user-scoped and protected by the same JWT manager. It
	// receives the process logger, which the request middleware enriches with a
	// request_id for every call.
	products.NewHandler(
		products.NewService(products.NewPostgresRepository(pool)),
		logger,
	).RegisterRoutes(mux, jwtManager.Middleware)

	// Orders are user-scoped and protected by the same JWT manager. This module
	// exposes create and read only; it derives totals from the caller's catalog.
	// It receives the job client now so the first task can be added without
	// touching the composition root again.
	orderRepo := orders.NewPostgresRepository(pool)
	orderSvc := orders.NewService(orderRepo, jobsClient, logger)
	orders.NewHandler(orderSvc, logger).RegisterRoutes(mux, jwtManager.Middleware)

	// ── DEMO JOBS START ─────────────────────────────────────────────────────
	// A normal feature module wired exactly like products and orders:
	// repository -> service -> handler. The service receives the same job
	// client the orders module receives, so it can enqueue tasks.
	//
	// Delete this block, internal/modules/demo, and the other marked demo
	// blocks to remove the example completely.
	demoRepo := demo.NewRedisRepository(appCache)
	demoSvc := demo.NewService(demoRepo, jobsClient, logger)
	demo.NewHandler(demoSvc, logger).RegisterRoutes(mux, jwtManager.Middleware)
	// ── DEMO JOBS END ───────────────────────────────────────────────────────

	// config.Load already applied the :8080 default and validated the address.
	addr := cfg.Server.Addr

	srv := &http.Server{
		Addr: addr,
		// Outermost first: security headers land on every response including the
		// CORS preflight; CORS answers OPTIONS before it can consume a rate-limit
		// slot or reach JWT auth; logging and rate limiting wrap the mux.
		Handler: lib.SecurityHeaders()(
			lib.CORS(cfg.CORS.AllowedOrigins)(
				lib.RequestLogger(logger)(
					lib.RateLimit(appCache, logger, cfg.RateLimit, clientIPResolver)(mux)))),
		// Bound every phase of a request so a slow client cannot pin a connection.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Serve in the background so the select below can watch for shutdown signals.
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		// ListenAndServe reports a clean shutdown as ErrServerClosed, which is not a failure.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
		return
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// Give in-flight requests a bounded window to finish before the pool closes.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
}
