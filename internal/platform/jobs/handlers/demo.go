package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"

	"github.com/Bughay/ego-backend-boilerplate/internal/modules/demo"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/jobs/tasks"
)

// ── DEMO JOBS START ─────────────────────────────────────────────────────────
// This file is the worker-side adapter between asynq and internal/modules/demo.
// It is the only place that combines the demo payloads, asynq's task context,
// and the demo service. Delete this file together with the demo module.
//
// The pattern to copy for a real job:
//  1. asynq gives the handler a *asynq.Task.
//  2. Decode t.Payload() into the struct from internal/platform/jobs/tasks.
//  3. Extract task metadata with the asynq.Get* helpers.
//  4. Call a module method; return an error to ask asynq to retry, or wrap it
//     with asynq.SkipRetry for a permanent failure.

// registerDemoHandlers wires the demo task types onto the worker mux. It does
// nothing when svc is nil, which keeps the package safe if the demo module is
// removed without deleting this file first.
func registerDemoHandlers(mux *asynq.ServeMux, svc *demo.Service, logger *slog.Logger) {
	if svc == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Simple job: decode a name, run one second of work, return.
	mux.HandleFunc(tasks.TypeDemoHello, func(ctx context.Context, t *asynq.Task) error {
		meta := demoTaskMeta(ctx)
		log := logger.With(
			"component", "demo-worker",
			"task_type", t.Type(),
			"task_id", meta.ID,
			"queue", meta.Queue,
			"attempt", meta.Attempt,
		)
		log.InfoContext(ctx, "demo hello task received")

		var payload tasks.DemoHelloPayload
		if len(t.Payload()) > 0 {
			if err := json.Unmarshal(t.Payload(), &payload); err != nil {
				cause := fmt.Errorf("decode hello payload: %w", err)
				svc.MarkFailed(ctx, meta, cause)
				return fmt.Errorf("%w: %v", asynq.SkipRetry, cause)
			}
		}

		if err := svc.RunHello(ctx, meta, payload); err != nil {
			// Invalid input can never succeed by retrying, so tell asynq to
			// stop after recording the failure.
			if errors.Is(err, demo.ErrInvalidInput) {
				svc.MarkFailed(ctx, meta, err)
				return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
			}
			return err
		}
		return nil
	})

	// Complex job: multi-step progress, an optional simulated failure, and
	// automatic retry on the next attempt.
	mux.HandleFunc(tasks.TypeDemoReport, func(ctx context.Context, t *asynq.Task) error {
		meta := demoTaskMeta(ctx)
		log := logger.With(
			"component", "demo-worker",
			"task_type", t.Type(),
			"task_id", meta.ID,
			"queue", meta.Queue,
			"attempt", meta.Attempt,
		)
		log.InfoContext(ctx, "demo report task received")

		var payload tasks.DemoReportPayload
		if len(t.Payload()) > 0 {
			if err := json.Unmarshal(t.Payload(), &payload); err != nil {
				cause := fmt.Errorf("decode report payload: %w", err)
				svc.MarkFailed(ctx, meta, cause)
				return fmt.Errorf("%w: %v", asynq.SkipRetry, cause)
			}
		}

		if err := svc.RunReport(ctx, meta, payload); err != nil {
			if errors.Is(err, demo.ErrInvalidInput) {
				svc.MarkFailed(ctx, meta, err)
				return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
			}
			return err
		}
		return nil
	})
}

// demoTaskMeta extracts the asynq facts the demo module understands. Attempt
// is 1-based for humans: retry count 0 means the first execution.
func demoTaskMeta(ctx context.Context) demo.TaskMeta {
	id, _ := asynq.GetTaskID(ctx)
	queue, _ := asynq.GetQueueName(ctx)
	retry, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	return demo.TaskMeta{
		ID:       id,
		Queue:    queue,
		Attempt:  retry + 1,
		MaxRetry: maxRetry,
	}
}

// ── DEMO JOBS END ───────────────────────────────────────────────────────────
