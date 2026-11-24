package products

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Bughay/ego-backend-boilerplate/internal/auth"
	"github.com/Bughay/ego-backend-boilerplate/internal/platform/helpers"
	platformlog "github.com/Bughay/ego-backend-boilerplate/internal/platform/log"
)

// service is the slice of Service the handler needs, kept narrow so it can be faked in tests.
type service interface {
	Create(ctx context.Context, userID string, in CreateInput) (*Product, error)
	List(ctx context.Context, userID string) ([]Product, error)
	Get(ctx context.Context, userID, id string) (*Product, error)
	Update(ctx context.Context, userID, id string, in UpdateInput) (*Product, error)
	Delete(ctx context.Context, userID, id string) error
}

// Handler serves the products CRUD endpoints.
type Handler struct {
	svc    service
	logger *slog.Logger
}

// NewHandler returns a products handler backed by svc. logger receives one
// structured event per CRUD operation; it is also the fallback when a request
// carries no request-scoped logger (unit tests, for example).
func NewHandler(svc service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the CRUD routes on mux, all protected by mw.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, mw func(http.Handler) http.Handler) {
	mux.Handle("POST /products", mw(http.HandlerFunc(h.create)))
	mux.Handle("GET /products", mw(http.HandlerFunc(h.list)))
	mux.Handle("GET /products/{id}", mw(http.HandlerFunc(h.get)))
	mux.Handle("PUT /products/{id}", mw(http.HandlerFunc(h.update)))
	mux.Handle("DELETE /products/{id}", mw(http.HandlerFunc(h.delete)))
}

// requestLogger returns the logger for one products request. It prefers the
// request-scoped logger installed by lib.RequestLogger, so every line shares the
// request's request_id, and falls back to the injected logger otherwise.
func (h *Handler) requestLogger(r *http.Request, userID string) *slog.Logger {
	// Start from the shared HTTP fields (component, method, path) and keep the
	// products-specific attributes in this module.
	logger := platformlog.FromRequest(r, h.logger, "products")
	// Attach the caller once the request is authenticated.
	if userID != "" {
		logger = logger.With("user_id", userID)
	}
	// Attach the product id for the routes that carry one in the path.
	if id := r.PathValue("id"); id != "" {
		logger = logger.With("product_id", id)
	}
	return logger
}

// create stores a new product for the authenticated user.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	var in CreateInput
	if !helpers.Decode(w, r, &in) {
		log.WarnContext(r.Context(), "product creation rejected: invalid request body")
		return
	}

	p, err := h.svc.Create(r.Context(), userID, in)
	if !writeError(w, log, r, "create", err) {
		return
	}
	// The id only exists after the insert, so it is added to this line rather
	// than to the shared request logger.
	if p != nil {
		log = log.With("product_id", p.ID)
	}
	log.InfoContext(r.Context(), "product created")
	helpers.JSON(w, http.StatusCreated, p)
}

// list returns every product owned by the authenticated user.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	items, err := h.svc.List(r.Context(), userID)
	if !writeError(w, log, r, "list", err) {
		return
	}
	// Count proves the call succeeded without dumping the caller's data.
	log.InfoContext(r.Context(), "products listed", "count", len(items))
	helpers.JSON(w, http.StatusOK, items)
}

// get returns one product owned by the authenticated user.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	p, err := h.svc.Get(r.Context(), userID, r.PathValue("id"))
	if !writeError(w, log, r, "get", err) {
		return
	}
	log.InfoContext(r.Context(), "product fetched")
	helpers.JSON(w, http.StatusOK, p)
}

// update replaces a product owned by the authenticated user.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	var in UpdateInput
	if !helpers.Decode(w, r, &in) {
		log.WarnContext(r.Context(), "product update rejected: invalid request body")
		return
	}

	p, err := h.svc.Update(r.Context(), userID, r.PathValue("id"), in)
	if !writeError(w, log, r, "update", err) {
		return
	}
	log.InfoContext(r.Context(), "product updated")
	helpers.JSON(w, http.StatusOK, p)
}

// delete removes a product owned by the authenticated user.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.RequireCallerID(w, r)
	if !ok {
		return
	}
	log := h.requestLogger(r, userID)

	if err := h.svc.Delete(r.Context(), userID, r.PathValue("id")); !writeError(w, log, r, "delete", err) {
		return
	}
	log.InfoContext(r.Context(), "product deleted")
	w.WriteHeader(http.StatusNoContent)
}
