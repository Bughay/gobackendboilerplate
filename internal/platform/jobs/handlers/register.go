// Package handlers contains the worker-side task handlers.
//
// It is the only package allowed to import both internal/modules and asynq's
// ServeMux, and it is imported by cmd/worker/main.go alone. Modules must never
// import this package.
package handlers

import (
	"log/slog"

	"github.com/hibiken/asynq"

	"github.com/Bughay/ego-backend-boilerplate/internal/modules/demo"
)

// Deps bundles the dependencies handlers need. Extend this struct as handlers
// are added; keeping it explicit makes the worker's composition root readable.
type Deps struct {
	// Logger receives handler and task-context messages. The worker passes the
	// same logger it builds at startup.
	Logger *slog.Logger

	// ── DEMO JOBS START ─────────────────────────────────────────────────────
	// Demo is the example module the demo worker handlers process. Remove this
	// field together with internal/platform/jobs/handlers/demo.go.
	Demo *demo.Service
	// ── DEMO JOBS END ───────────────────────────────────────────────────────
}

// RegisterAll wires every handler onto the mux. Each new task adds one call
// here, and the binary's composition root decides which module service it
// receives through Deps.
func RegisterAll(mux *asynq.ServeMux, deps Deps) {
	// ── DEMO JOBS START ─────────────────────────────────────────────────────
	registerDemoHandlers(mux, deps.Demo, deps.Logger)
	// ── DEMO JOBS END ───────────────────────────────────────────────────────
}
