package log

import (
	"context"
	"log/slog"
	"net/http"
)

// contextKey is an empty, unexported struct used as the context key for a
// request-scoped logger. Because the type is unexported, no other package can
// create the same key, so the stored logger cannot collide with unrelated
// context values.
type contextKey struct{}

// IntoContext returns a copy of ctx carrying logger. HTTP middleware calls it
// once per request so handlers and any deeper layer can emit logs enriched with
// the same request-scoped attributes (for example request_id).
//
// Storing a *slog.Logger in a context is safe: a Logger is immutable, and each
// With call returns a new Logger rather than mutating the stored one.
func IntoContext(ctx context.Context, logger *slog.Logger) context.Context {
	// A nil logger would panic on first use, so leave the context untouched.
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, logger)
}

// FromContext returns the logger stored by IntoContext.
//
// When ctx carries no logger it returns the first non-nil fallback, or
// slog.Default() when no fallback is supplied. The variadic form lets a handler
// keep its constructor-injected logger for tests while preferring the
// request-scoped logger in production.
func FromContext(ctx context.Context, fallback ...*slog.Logger) *slog.Logger {
	// ctx.Value panics on a nil context, so guard before touching it.
	if ctx != nil {
		if logger, ok := ctx.Value(contextKey{}).(*slog.Logger); ok && logger != nil {
			return logger
		}
	}
	// Prefer an explicit fallback (the component's own logger) over the global.
	if len(fallback) > 0 && fallback[0] != nil {
		return fallback[0]
	}
	// The process default is the last resort; each binary sets it at boot.
	return slog.Default()
}

// FromRequest returns the request-scoped logger enriched with the standard
// fields every HTTP handler logs: component, method and path. request_id is
// already on the logger when lib.RequestLogger stored it in the context.
//
// The module keeps ownership of its domain fields (user_id, product_id, ...)
// and adds them with With after calling this helper.
func FromRequest(r *http.Request, fallback *slog.Logger, component string) *slog.Logger {
	// Start from the request-scoped logger so every line stays correlated, then
	// add the fields that are the same for every request in this component.
	return FromContext(r.Context(), fallback).With(
		"component", component,
		"method", r.Method,
		"path", r.URL.Path,
	)
}
