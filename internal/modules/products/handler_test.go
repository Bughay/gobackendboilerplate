package products

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Bughay/ego-backend-boilerplate/internal/auth"
)

// testUserID is a canonical UUID standing in for the authenticated caller.
const testUserID = "11111111-1111-1111-1111-111111111111"

// fakeService returns canned values for handler tests and records the user ID
// the handler took from the JWT claims.
type fakeService struct {
	product    *Product
	items      []Product
	err        error
	lastUserID string
}

// Create returns the canned product and records the caller.
func (f *fakeService) Create(_ context.Context, userID string, _ CreateInput) (*Product, error) {
	f.lastUserID = userID
	return f.product, f.err
}

// List returns the canned list and records the caller.
func (f *fakeService) List(_ context.Context, userID string) ([]Product, error) {
	f.lastUserID = userID
	return f.items, f.err
}

// Get returns the canned product and records the caller.
func (f *fakeService) Get(_ context.Context, userID, _ string) (*Product, error) {
	f.lastUserID = userID
	return f.product, f.err
}

// Update returns the canned product and records the caller.
func (f *fakeService) Update(_ context.Context, userID, _ string, _ UpdateInput) (*Product, error) {
	f.lastUserID = userID
	return f.product, f.err
}

// Delete returns the canned error and records the caller.
func (f *fakeService) Delete(_ context.Context, userID, _ string) error {
	f.lastUserID = userID
	return f.err
}

// newTestMux mounts the routes behind the real auth middleware. When
// authenticated is true it also returns a valid access token for the caller.
func newTestMux(t *testing.T, svc service, authenticated bool) (*http.ServeMux, string) {
	t.Helper()

	mgr := auth.NewManager("test-secret", 1, 24)
	mux := http.NewServeMux()
	NewHandler(svc, testLogger()).RegisterRoutes(mux, mgr.Middleware)

	if !authenticated {
		return mux, ""
	}

	token, err := mgr.Generate(testUserID, "test@example.com", []string{"user"})
	if err != nil {
		t.Fatalf("generate test token: %v", err)
	}
	return mux, token
}

// testLogger discards output so handler tests stay quiet unless a test supplies
// its own logger.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// doRequest runs one request and returns the recorded response. A non-empty
// token is sent as an Authorization: Bearer header.
func doRequest(mux *http.ServeMux, token, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestHandlerRequiresPrincipal proves every route needs a valid bearer token.
func TestHandlerRequiresPrincipal(t *testing.T) {
	mux, token := newTestMux(t, &fakeService{}, false)

	// All five methods must fail the same way without an Authorization header.
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/products"},
		{http.MethodGet, "/products"},
		{http.MethodGet, "/products/" + testUserID},
		{http.MethodPut, "/products/" + testUserID},
		{http.MethodDelete, "/products/" + testUserID},
	} {
		rec := doRequest(mux, token, tc.method, tc.path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// TestHandlerCreateReturns201 covers the happy path.
func TestHandlerCreateReturns201(t *testing.T) {
	svc := &fakeService{product: &Product{ID: "p1", Name: "Widget", Qty: 2, Price: 9.99}}
	mux, token := newTestMux(t, svc, true)

	rec := doRequest(mux, token, http.MethodPost, "/products", `{"name":"Widget","qty":2,"price":9.99}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"Widget"`) {
		t.Fatalf("body = %s, want it to contain the product name", rec.Body.String())
	}
	// Ownership comes from the validated token, never from the request body.
	if svc.lastUserID != testUserID {
		t.Fatalf("service received user id %q, want %q", svc.lastUserID, testUserID)
	}
}

// TestHandlerRejectsMalformedJSON proves helpers.Decode guards the body.
func TestHandlerRejectsMalformedJSON(t *testing.T) {
	mux, token := newTestMux(t, &fakeService{}, true)

	if rec := doRequest(mux, token, http.MethodPost, "/products", "{"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestHandlerMapsDomainErrors proves sentinels become the documented status codes.
func TestHandlerMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"not found", ErrNotFound, http.StatusNotFound},
		{"invalid input", ErrInvalidInput, http.StatusBadRequest},
		{"unexpected", errors.New("boom"), http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mux, token := newTestMux(t, &fakeService{err: tc.err}, true)

			rec := doRequest(mux, token, http.MethodGet, "/products/"+testUserID, "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// TestHandlerDeleteReturns204 covers the delete happy path.
func TestHandlerDeleteReturns204(t *testing.T) {
	mux, token := newTestMux(t, &fakeService{}, true)

	rec := doRequest(mux, token, http.MethodDelete, "/products/"+testUserID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

// TestHandlerLogsProductCreated proves the create path writes one structured
// event carrying the caller and the newly created product id.
func TestHandlerLogsProductCreated(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	// Build the route with a logger the test can inspect.
	mgr := auth.NewManager("test-secret", 1, 24)
	mux := http.NewServeMux()
	NewHandler(&fakeService{product: &Product{ID: "p1", Name: "Widget"}}, logger).
		RegisterRoutes(mux, mgr.Middleware)

	token, err := mgr.Generate(testUserID, "test@example.com", []string{"user"})
	if err != nil {
		t.Fatalf("generate test token: %v", err)
	}

	rec := doRequest(mux, token, http.MethodPost, "/products", `{"name":"Widget","qty":2,"price":9.99}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	// No RequestLogger is mounted here, so the handler writes exactly one line.
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	if got := entry["msg"]; got != "product created" {
		t.Fatalf("msg = %v, want product created", got)
	}
	if got := entry["component"]; got != "products" {
		t.Fatalf("component = %v, want products", got)
	}
	if got := entry["user_id"]; got != testUserID {
		t.Fatalf("user_id = %v, want %q", got, testUserID)
	}
	if got := entry["product_id"]; got != "p1" {
		t.Fatalf("product_id = %v, want p1", got)
	}
}
