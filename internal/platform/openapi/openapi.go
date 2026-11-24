// Package openapi serves the embedded OpenAPI 3.0 specification and the
// interactive Swagger UI explorer.
//
// It exposes two public routes:
//
//	GET /openapi.json  raw OpenAPI 3.0.3 document (application/json)
//	GET /docs/         Swagger UI plus its assets, all embedded in the binary
//
// The specification lives in openapi.json next to this file and is compiled
// into the binary with go:embed. That is deliberate: the Docker runtime image
// contains only the compiled api and worker binaries, so serving a file from
// the working directory would fail inside the container and change behavior
// depending on where the binary was started.
//
// # Content-Security-Policy
//
// The API's global SecurityHeaders middleware sends
// "default-src 'none'; frame-ancestors 'none'". Swagger UI needs scripts,
// styles, fonts, images, and a same-origin fetch for the spec, and its generated
// index contains inline bootstrap scripts and styles. The Swagger UI handler
// therefore overrides the CSP for /docs/ requests only. Every API response and
// /openapi.json keep the strict JSON-only policy.
//
// # Authentication in the explorer
//
// Swagger UI and the specification are same-origin. Logging in through the UI
// makes the browser store the HttpOnly refresh_token cookie from
// POST /auth/login, so /auth/refresh and /auth/logout work from the explorer
// without pasting a token. Protected routes use the Authorize button with the
// access token. If the UI is opened on a different host from the API, or
// COOKIE_SECURE=true is used over plain HTTP on a non-localhost host, the
// browser may not send the refresh cookie and refresh returns 401.
//
// # Keeping the spec in sync
//
// openapi.json is the single source of truth for the public HTTP contract. When
// a route, request body, response body, status code, or auth rule changes,
// update openapi.json in the same change. openapi_test.go enforces the current
// operation set, so adding a route without documenting it fails the test suite.
//
// # Removing the demo module
//
// The "Demo jobs" tag, its three paths, and the DemoHelloRequest,
// DemoReportRequest, and DemoJob schemas belong to the removable
// internal/modules/demo reference module. Delete them from openapi.json and
// from the expected-operation table in openapi_test.go alongside the module.
package openapi

import (
	_ "embed"
	"net/http"

	"github.com/swaggest/swgui/v5emb"
)

const (
	// Title is shown in the Swagger UI page title and should match info.title
	// in openapi.json. openapi_test.go checks that the two stay in sync.
	Title = "Ego Backend API"

	// SpecPath is the public URL of the raw OpenAPI document. Swagger UI loads
	// its schema from this same path, so it must remain reachable without auth.
	SpecPath = "/openapi.json"

	// UIPath is the base URL of the Swagger UI. The trailing slash matters: it
	// makes the ServeMux pattern match the UI and every embedded asset beneath it.
	UIPath = "/docs/"
)

// docsContentSecurityPolicy is the policy applied only to /docs/ and its
// embedded assets. 'unsafe-inline' is required because the Swagger UI index
// contains inline <style> and <script> bootstrap code. All other directives
// keep the page self-contained: no third-party scripts, fonts, or API calls.
const docsContentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// specJSON is the checked-in OpenAPI document, embedded at compile time so the
// spec always ships with the binary that serves it.
//
//go:embed openapi.json
var specJSON []byte

// RegisterRoutes mounts the raw specification and the interactive Swagger UI
// on mux. Both routes are public: the UI must be able to fetch the spec without
// authentication. The caller decides the surrounding middleware; in cmd/api the
// existing security-header, CORS, request-logging, and rate-limit chain applies.
func RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET "+SpecPath, specHandler())
	mux.Handle("GET "+UIPath, uiHandler())
}

// specHandler serves the embedded JSON document. Cache-Control is no-cache so a
// redeployed binary always revalidates the spec instead of letting a browser
// show a stale API contract. The response is small, so a full revalidation is
// cheaper than debugging a stale explorer.
func specHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		// A write error here means the client disconnected; there is no useful
		// response left to send, so the bytes are simply left unwritten.
		_, _ = w.Write(specJSON)
	})
}

// uiHandler returns the embedded Swagger UI wrapped with a docs-specific CSP.
// SecurityHeaders is outermost in the real chain and sets the strict API policy
// first; setting the header again here, before v5emb writes its response,
// replaces that value for docs requests without touching any other route.
func uiHandler() http.Handler {
	ui := v5emb.New(Title, SpecPath, UIPath)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", docsContentSecurityPolicy)
		ui.ServeHTTP(w, r)
	})
}
