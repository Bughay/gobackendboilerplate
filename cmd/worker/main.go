// Package main is the background-worker composition root. It owns the asynq
// server and the handler registry and nothing else: the API binary enqueues
// work, this binary consumes it.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"

	"github.com/Bughay/ego-backend-boilerplate/internal/modules/demo"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/cache"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/config"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/jobs"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/jobs/handlers"
	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
)

func main() {
	// Configuration comes from the same loader as the API, so both binaries
	// agree on the Redis address and the WORKER_* settings. A bad value fails
	// here, before any queue connection is opened.
	cfg, err := config.Load()
	if err != nil {
		// The configured logger does not exist yet, so report the fatal config
		// error on stderr rather than through slog.
		fmt.Fprintf(os.Stderr, "could not load configuration: %v\n", err)
		os.Exit(1)
	}

	// One logger for the worker process. Passing it to jobs.NewServer also
	// routes asynq's own messages into the same structured stream.
	logger := platformlog.NewLogger(cfg.Log.Format, cfg.Log.Level)
	slog.SetDefault(logger)

	// Cancelling this context on SIGINT/SIGTERM is what starts the drain. It is
	// created before the demo cache connection so a boot-time Redis ping is also
	// cancellable.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Asynq keeps its own logical Redis database. config.Load already rejected
	// any configuration where WORKER_REDIS_DB equals REDIS_DB (the cache).
	redisOpt := jobs.RedisOpt(cfg.Redis, cfg.Worker.RedisDB)

	// The handler registry. Handlers are registered only through
	// handlers.RegisterAll; this file decides which module services they receive.
	mux := asynq.NewServeMux()

	// ── DEMO JOBS START ─────────────────────────────────────────────────────
	// Demo job state lives in the cache Redis database (REDIS_DB), the same one
	// the API uses for the rate limiter. It is deliberately separate from
	// asynq's WORKER_REDIS_DB above.
	//
	// A missing Redis here is not fatal: the demo jobs still run and log, but
	// their state is not persisted, so polling returns 404. A real module would
	// normally treat its primary store as required instead of using NopCache.
	var demoCache cache.Cache = &cache.NopCache{}
	if demoRedis, err := cache.NewRedisClient(ctx, cfg.Redis); err != nil {
		logger.Warn("demo job state store unavailable — jobs will run but state will not persist",
			"error", err,
		)
	} else {
		demoCache = cache.NewRedisCache(demoRedis)
		defer demoRedis.Close()
	}

	// The worker only processes tasks; a nil enqueuer makes that explicit.
	demoSvc := demo.NewService(demo.NewRedisRepository(demoCache), nil, logger)
	// ── DEMO JOBS END ───────────────────────────────────────────────────────

	handlers.RegisterAll(mux, handlers.Deps{
		Logger: logger,
		Demo:   demoSvc,
	})

	srv := jobs.NewServer(redisOpt, cfg.Worker, logger)

	// Start (not Run) on purpose: Run installs asynq's own signal handler and
	// shuts the server down concurrently with this function, which can race the
	// "worker stopped" log. Owning the lifecycle here keeps Shutdown as the only
	// shutdown path, and Shutdown blocks until in-flight tasks finish.
	if err := srv.Start(mux); err != nil {
		logger.Error("could not start worker", "error", err)
		os.Exit(1)
	}

	logger.Info("worker started",
		"queues", cfg.Worker.Queues,
		"concurrency", cfg.Worker.Concurrency,
		"redis_db", cfg.Worker.RedisDB,
	)

	// Block until the process is asked to stop.
	<-ctx.Done()

	logger.Info("shutting down worker")
	srv.Shutdown()
	logger.Info("worker stopped")
}
