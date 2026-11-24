package orders

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Bughay/ego-backend-boilerplate/internal/auth"
)

// testOrderUserID is a canonical UUID standing in for the authenticated caller.
const testOrderUserID = "11111111-1111-1111-1111-111111111111"

// fakeService returns canned values for handler tests and records the caller the
// handler took from the JWT claims.
type fakeService struct {
	order      *Order
	orders     []Order
	err        error
	lastUserID string
	lastOrder  string
	lastInput  CreateInput
}

// Create returns the canned order and records the caller.
func (f *fakeService) Create(_ context.Context, userID string, in CreateInput) (*Order, error) {
	f.lastUserID, f.lastInput = userID, in
	return f.order, f.err
}

// List returns the canned orders and records the caller.
func (f *fakeService) List(_ context.Context, userID string) ([]Order, error) {
	f.lastUserID = userID
	return f.orders, f.err
}

// Get returns the canned order and records the caller and path ID.
func (f *fakeService) Get(_ context.Context, userID, id string) (*Order, error) {
	f.lastUserID, f.lastOrder = userID, id
	return f.order, f.err
}

// newTestMux mounts the order routes behind the real auth middleware. When
// authenticated is true it also returns a valid access token for the caller.
func newTestMux(t *testing.T, svc service, authenticated bool) (*http.ServeMux, string) {
	t.Helper()

	mgr := auth.NewManager("test-secret", 1, 24)
	mux := http.NewServeMux()
	NewHandler(svc, testOrderLogger()).RegisterRoutes(mux, mgr.Middleware)

	if !authenticated {
		return mux, ""
	}

	token, err := mgr.Generate(testOrderUserID, "test@example.com", []string{"user"})
	if err != nil {
		t.Fatalf("generate test token: %v", err)
	}
	return mux, token
}

// testOrderLogger discards output so handler tests stay quiet.
func testOrderLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// doOrderRequest runs one request and returns the recorded response. A non-empty
// token is sent as an Authorization: Bearer header.
func doOrderRequest(mux *http.ServeMux, token, method, path, body string) *httptest.ResponseRecorder {
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

// TestHandlerRequiresPrincipal proves every order route needs a valid bearer token.
func TestHandlerRequiresPrincipal(t *testing.T) {
	mux, token := newTestMux(t, &fakeService{}, false)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/orders"},
		{http.MethodGet, "/orders"},
		{http.MethodGet, "/orders/" + testOrderUserID},
	} {
		rec := doOrderRequest(mux, token, tc.method, tc.path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// TestHandlerCreateReturns201 covers the happy path and ownership scoping.
func TestHandlerCreateReturns201(t *testing.T) {
	productID := validOrderProductID
	svc := &fakeService{order: &Order{
		ID:     "33333333-3333-3333-3333-333333333333",
		UserID: testOrderUserID,
		Status: "pending",
		Total:  9.99,
		Items: []OrderItem{{
			ID:          "44444444-4444-4444-4444-444444444444",
			ProductID:   &productID,
			ProductName: "Widget",
			UnitPrice:   9.99,
			Qty:         1,
		}},
	}}
	mux, token := newTestMux(t, svc, true)

	rec := doOrderRequest(mux, token, http.MethodPost, "/orders",
		`{"items":[{"product_id":"`+validOrderProductID+`","qty":1}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"Widget"`) {
		t.Fatalf("body = %s, want it to contain the item name", rec.Body.String())
	}
	// Ownership comes from the validated token, never from the request body.
	if svc.lastUserID != testOrderUserID {
		t.Fatalf("service received user id %q, want %q", svc.lastUserID, testOrderUserID)
	}
	if len(svc.lastInput.Items) != 1 || svc.lastInput.Items[0].Qty != 1 {
		t.Fatalf("service input = %+v, want one item with qty 1", svc.lastInput)
	}
}

// TestHandlerCreateRejectsMalformedJSON proves helpers.Decode guards the body.
func TestHandlerCreateRejectsMalformedJSON(t *testing.T) {
	mux, token := newTestMux(t, &fakeService{}, true)

	if rec := doOrderRequest(mux, token, http.MethodPost, "/orders", "{"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestHandlerListReturns200 covers the list happy path.
func TestHandlerListReturns200(t *testing.T) {
	svc := &fakeService{orders: []Order{{ID: "o1", Status: "pending", Items: []OrderItem{}}}}
	mux, token := newTestMux(t, svc, true)

	rec := doOrderRequest(mux, token, http.MethodGet, "/orders", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"o1"`) {
		t.Fatalf("body = %s, want it to contain the order ID", rec.Body.String())
	}
	if svc.lastUserID != testOrderUserID {
		t.Fatalf("service received user id %q, want %q", svc.lastUserID, testOrderUserID)
	}
}

// TestHandlerGetReturns200 covers the single-read happy path.
func TestHandlerGetReturns200(t *testing.T) {
	svc := &fakeService{order: &Order{ID: "o1", Status: "pending", Items: []OrderItem{}}}
	mux, token := newTestMux(t, svc, true)

	rec := doOrderRequest(mux, token, http.MethodGet, "/orders/"+testOrderUserID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if svc.lastOrder != testOrderUserID {
		t.Fatalf("service received order id %q, want %q", svc.lastOrder, testOrderUserID)
	}
}

// TestHandlerMapsDomainErrors proves sentinels become the documented status codes.
func TestHandlerMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"invalid input", ErrInvalidInput, http.StatusBadRequest},
		{"not found", ErrNotFound, http.StatusNotFound},
		{"product not found", ErrProductNotFound, http.StatusNotFound},
		{"user not found", ErrUserNotFound, http.StatusUnauthorized},
		{"unexpected", errors.New("boom"), http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mux, token := newTestMux(t, &fakeService{err: tc.err}, true)

			rec := doOrderRequest(mux, token, http.MethodGet, "/orders/"+testOrderUserID, "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
