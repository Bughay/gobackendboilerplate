package demo

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Bughay/ego-backend-boilerplate/internal/auth"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
)

// service is the slice of Service the HTTP handler needs. Keeping it narrow is
// the same pattern products and orders use, and it makes the handler testable
// with a small fake.
type service interface {
	EnqueueHello(ctx context.Context, in HelloInput) (*Job, error)
	EnqueueReport(ctx context.Context, in ReportInput) (*Job, error)
	Get(ctx context.Context, id string) (*Job, error)
}

// Handler serves the demo job endpoints.
type Handler struct {
	svc    service
	logger *slog.Logger
}

// NewHandler returns a demo handler backed by svc.
func NewHandler(svc service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the demo endpoints on mux. They are protected by the
// same JWT middleware as products and orders, so the caller must be signed in.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, mw func(http.Handler) http.Handler) {
	mux.Handle("POST /demo/jobs/hello", mw(http.HandlerFunc(h.createHello)))
	mux.Handle("POST /demo/jobs/report", mw(http.HandlerFunc(h.createReport)))
	mux.Handle("GET /demo/jobs/{id}", mw(http.HandlerFunc(h.get)))
}

// requestLogger prefers the request-scoped logger installed by
// lib.RequestLogger and adds the demo component plus the authenticated user.
func (h *Handler) requestLogger(r *http.Request, userID string) *slog.Logger {
	logger := platformlog.FromRequest(r, h.logger, "demo")
	if userID != "" {
		logger = logger.With("user_id", userID)
	}
	if id := r.PathValue("id"); id != "" {
		logger = logger.With("job_id", id)
	}
	return logger
}

// createHello accepts the simple job, stores its queued state, and enqueues it.
// The response is 202: the work has been accepted, not completed.
func (h *Handler) createHello(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	var in HelloInput
	if !helpers.Decode(w, r, &in) {
		log.WarnContext(r.Context(), "demo hello rejected: invalid request body")
		return
	}

	job, err := h.svc.EnqueueHello(r.Context(), in)
	if !writeError(w, log, r, "enqueue-hello", err) {
		return
	}
	log.InfoContext(r.Context(), "demo hello enqueued",
		"job_id", job.ID,
		"queue", job.Queue,
	)
	helpers.JSON(w, http.StatusAccepted, job)
}

// createReport accepts the complex job and enqueues it on the critical queue.
func (h *Handler) createReport(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	var in ReportInput
	if !helpers.Decode(w, r, &in) {
		log.WarnContext(r.Context(), "demo report rejected: invalid request body")
		return
	}

	job, err := h.svc.EnqueueReport(r.Context(), in)
	if !writeError(w, log, r, "enqueue-report", err) {
		return
	}
	log.InfoContext(r.Context(), "demo report enqueued",
		"job_id", job.ID,
		"queue", job.Queue,
		"steps", job.TotalSteps,
	)
	helpers.JSON(w, http.StatusAccepted, job)
}

// get returns the current Redis-backed state of one demo job.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	job, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if !writeError(w, log, r, "get", err) {
		return
	}
	log.InfoContext(r.Context(), "demo job fetched", "status", job.Status)
	helpers.JSON(w, http.StatusOK, job)
}
