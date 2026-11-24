package orders

import (
	"context"
	"errors"
	"math"
	"testing"
)

// fakeRepository records calls and returns canned values for service tests.
type fakeRepository struct {
	createdUserID string
	createdInput  CreateInput
	listedUserID  string
	gotUserID     string
	gotID         string
	order         *Order
	orders        []Order
	err           error
}

// Create records the create call.
func (f *fakeRepository) Create(_ context.Context, userID string, in CreateInput) (*Order, error) {
	f.createdUserID, f.createdInput = userID, in
	if f.err != nil {
		return nil, f.err
	}
	return f.order, nil
}

// List records the list call.
func (f *fakeRepository) List(_ context.Context, userID string) ([]Order, error) {
	f.listedUserID = userID
	if f.err != nil {
		return nil, f.err
	}
	return f.orders, nil
}

// Get records the get call.
func (f *fakeRepository) Get(_ context.Context, userID, id string) (*Order, error) {
	f.gotUserID, f.gotID = userID, id
	if f.err != nil {
		return nil, f.err
	}
	return f.order, nil
}

// TestServiceCreateScopesUserAndCleansInput proves validation runs and the
// caller owns the resulting order.
func TestServiceCreateScopesUserAndCleansInput(t *testing.T) {
	repo := &fakeRepository{order: &Order{ID: "o1"}}
	svc := NewService(repo, nil, nil)

	_, err := svc.Create(context.Background(), "user-1", CreateInput{Items: []CreateItemInput{
		{ProductID: "  " + validOrderProductID + "  ", Qty: 2},
	}})
	if err != nil {
		t.Fatalf("Create() error = %v, want success", err)
	}
	if repo.createdUserID != "user-1" {
		t.Errorf("repository userID = %q, want user-1", repo.createdUserID)
	}
	if repo.createdInput.Items[0].ProductID != validOrderProductID {
		t.Errorf("repository product ID = %q, want trimmed ID", repo.createdInput.Items[0].ProductID)
	}
}

// TestServiceCreateRejectsInvalidInput proves a bad request never reaches storage.
func TestServiceCreateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		in   CreateInput
	}{
		{"empty", CreateInput{}},
		{"zero qty", CreateInput{Items: []CreateItemInput{{ProductID: validOrderProductID, Qty: 0}}}},
		{"malformed id", CreateInput{Items: []CreateItemInput{{ProductID: "bad", Qty: 1}}}},
		{"duplicate", CreateInput{Items: []CreateItemInput{
			{ProductID: validOrderProductID, Qty: 1},
			{ProductID: validOrderProductID, Qty: 2},
		}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo, nil, nil)

			if _, err := svc.Create(context.Background(), "user-1", tc.in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Create() error = %v, want ErrInvalidInput", err)
			}
			if repo.createdUserID != "" {
				t.Fatalf("repository was called with userID %q", repo.createdUserID)
			}
		})
	}
}

// TestServiceCreatePropagatesProductNotFound proves the repository sentinel
// survives the service layer.
func TestServiceCreatePropagatesProductNotFound(t *testing.T) {
	repo := &fakeRepository{err: ErrProductNotFound}
	svc := NewService(repo, nil, nil)

	_, err := svc.Create(context.Background(), "user-1", CreateInput{Items: []CreateItemInput{
		{ProductID: validOrderProductID, Qty: 1},
	}})
	if !errors.Is(err, ErrProductNotFound) {
		t.Fatalf("Create() error = %v, want ErrProductNotFound", err)
	}
}

// TestServiceListScopesUser proves list is always filtered by the authenticated user.
func TestServiceListScopesUser(t *testing.T) {
	repo := &fakeRepository{orders: []Order{}}
	svc := NewService(repo, nil, nil)

	if _, err := svc.List(context.Background(), "user-42"); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repo.listedUserID != "user-42" {
		t.Fatalf("repository userID = %q, want user-42", repo.listedUserID)
	}
}

// TestServiceGetRejectsMalformedID proves a bad path ID never reaches Postgres.
func TestServiceGetRejectsMalformedID(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, nil, nil)

	if _, err := svc.Get(context.Background(), "user-1", "not-a-uuid"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Get() error = %v, want ErrInvalidInput", err)
	}
	if repo.gotID != "" {
		t.Fatalf("repository was called with id %q", repo.gotID)
	}
}

// TestServiceGetPropagatesNotFound proves the repository's sentinel survives.
func TestServiceGetPropagatesNotFound(t *testing.T) {
	repo := &fakeRepository{err: ErrNotFound}
	svc := NewService(repo, nil, nil)

	const id = "11111111-1111-1111-1111-111111111111"
	if _, err := svc.Get(context.Background(), "user-1", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

// TestCalculateTotal proves the order total is the sum of unit price times qty.
func TestCalculateTotal(t *testing.T) {
	total, err := calculateTotal([]CreateItemInput{
		{ProductID: "p1", Qty: 2},
		{ProductID: "p2", Qty: 5},
	}, map[string]float64{"p1": 9.99, "p2": 19.50})
	if err != nil {
		t.Fatalf("calculateTotal() error = %v, want success", err)
	}
	if math.Abs(total-117.48) > 0.001 {
		t.Fatalf("total = %v, want 117.48", total)
	}
}

// TestCalculateTotalRoundsCents proves the cents conversion absorbs float
// representation error for a simple repeating decimal.
func TestCalculateTotalRoundsCents(t *testing.T) {
	total, err := calculateTotal([]CreateItemInput{
		{ProductID: "p1", Qty: 3},
	}, map[string]float64{"p1": 0.10})
	if err != nil {
		t.Fatalf("calculateTotal() error = %v, want success", err)
	}
	if math.Abs(total-0.30) > 0.0001 {
		t.Fatalf("total = %v, want 0.30", total)
	}
}

// TestCalculateTotalAcceptsColumnMaximum proves a total exactly at the
// numeric(12,2) ceiling is still accepted.
func TestCalculateTotalAcceptsColumnMaximum(t *testing.T) {
	total, err := calculateTotal([]CreateItemInput{
		{ProductID: "p1", Qty: 1},
	}, map[string]float64{"p1": 9_999_999_999.99})
	if err != nil {
		t.Fatalf("calculateTotal() error = %v, want success", err)
	}
	if total <= 0 {
		t.Fatalf("total = %v, want a positive maximum", total)
	}
}

// TestCalculateTotalErrors pins every failure class the total helper can report.
func TestCalculateTotalErrors(t *testing.T) {
	tests := []struct {
		name   string
		items  []CreateItemInput
		prices map[string]float64
		want   error
	}{
		{
			name:   "missing price",
			items:  []CreateItemInput{{ProductID: "p1", Qty: 1}},
			prices: map[string]float64{},
			want:   ErrProductNotFound,
		},
		{
			name:   "negative price",
			items:  []CreateItemInput{{ProductID: "p1", Qty: 1}},
			prices: map[string]float64{"p1": -1},
			want:   ErrInvalidInput,
		},
		{
			name:   "NaN price",
			items:  []CreateItemInput{{ProductID: "p1", Qty: 1}},
			prices: map[string]float64{"p1": math.NaN()},
			want:   ErrInvalidInput,
		},
		{
			name:   "infinite price",
			items:  []CreateItemInput{{ProductID: "p1", Qty: 1}},
			prices: map[string]float64{"p1": math.Inf(1)},
			want:   ErrInvalidInput,
		},
		{
			name:   "total over column maximum",
			items:  []CreateItemInput{{ProductID: "p1", Qty: 2}},
			prices: map[string]float64{"p1": 9_999_999_999.99},
			want:   ErrInvalidInput,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := calculateTotal(tc.items, tc.prices); !errors.Is(err, tc.want) {
				t.Fatalf("calculateTotal() error = %v, want %v", err, tc.want)
			}
		})
	}
}
