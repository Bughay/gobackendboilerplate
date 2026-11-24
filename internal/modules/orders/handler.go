package orders

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Bughay/ego-backend-boilerplate/internal/auth"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
)

// service is the slice of Service the handler needs, kept narrow so it can be
// faked in tests.
type service interface {
	Create(ctx context.Context, userID string, in CreateInput) (*Order, error)
	List(ctx context.Context, userID string) ([]Order, error)
	Get(ctx context.Context, userID, id string) (*Order, error)
}

// Handler serves the create/read order endpoints.
type Handler struct {
	svc    service
	logger *slog.Logger
}

// NewHandler returns an order handler backed by svc. logger receives one
// structured event per operation and is the fallback when a request carries no
// request-scoped logger (unit tests, for example).
func NewHandler(svc service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the order routes on mux, all protected by mw. There is
// deliberately no update or delete route.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, mw func(http.Handler) http.Handler) {
	mux.Handle("POST /orders", mw(http.HandlerFunc(h.create)))
	mux.Handle("GET /orders", mw(http.HandlerFunc(h.list)))
	mux.Handle("GET /orders/{id}", mw(http.HandlerFunc(h.get)))
}

// requestLogger returns the logger for one order request. It prefers the
// request-scoped logger installed by lib.RequestLogger so every line shares the
// request's request_id, and adds the order component and caller ID.
func (h *Handler) requestLogger(r *http.Request, userID string) *slog.Logger {
	logger := platformlog.FromRequest(r, h.logger, "orders")
	if userID != "" {
		logger = logger.With("user_id", userID)
	}
	if id := r.PathValue("id"); id != "" {
		logger = logger.With("order_id", id)
	}
	return logger
}

// create validates the body, creates an order for the authenticated user, and
// returns it with status 201.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	var in CreateInput
	if !helpers.Decode(w, r, &in) {
		log.WarnContext(r.Context(), "order creation rejected: invalid request body")
		return
	}

	order, err := h.svc.Create(r.Context(), userID, in)
	if !writeError(w, log, r, "create", err) {
		return
	}

	// The order ID only exists after the transaction commits.
	if order != nil {
		log = log.With("order_id", order.ID)
		log.InfoContext(r.Context(), "order created", "total", order.Total)
	}
	helpers.JSON(w, http.StatusCreated, order)
}

// list returns every order owned by the authenticated user, each with its items.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	orders, err := h.svc.List(r.Context(), userID)
	if !writeError(w, log, r, "list", err) {
		return
	}
	log.InfoContext(r.Context(), "orders listed", "count", len(orders))
	helpers.JSON(w, http.StatusOK, orders)
}

// get returns one order owned by the authenticated user.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	order, err := h.svc.Get(r.Context(), userID, r.PathValue("id"))
	if !writeError(w, log, r, "get", err) {
		return
	}
	log.InfoContext(r.Context(), "order fetched")
	helpers.JSON(w, http.StatusOK, order)
}
