package products

import (
	"context"
	"errors"
	"testing"
)

// fakeRepository records calls and returns canned values.
type fakeRepository struct {
	createdUserID string
	createdInput  CreateInput
	listedUserID  string
	gotUserID     string
	gotID         string
	updatedInput  UpdateInput
	deletedUserID string
	deletedID     string
	product       *Product
	err           error
}

// Create records the create call.
func (f *fakeRepository) Create(_ context.Context, userID string, in CreateInput) (*Product, error) {
	f.createdUserID, f.createdInput = userID, in
	if f.err != nil {
		return nil, f.err
	}
	return f.product, nil
}

// List records the list call.
func (f *fakeRepository) List(_ context.Context, userID string) ([]Product, error) {
	f.listedUserID = userID
	if f.err != nil {
		return nil, f.err
	}
	return []Product{}, nil
}

// Get records the get call.
func (f *fakeRepository) Get(_ context.Context, userID, id string) (*Product, error) {
	f.gotUserID, f.gotID = userID, id
	if f.err != nil {
		return nil, f.err
	}
	return f.product, nil
}

// Update records the update call.
func (f *fakeRepository) Update(_ context.Context, userID, id string, in UpdateInput) (*Product, error) {
	f.gotUserID, f.gotID, f.updatedInput = userID, id, in
	if f.err != nil {
		return nil, f.err
	}
	return f.product, nil
}

// Delete records the delete call.
func (f *fakeRepository) Delete(_ context.Context, userID, id string) error {
	f.deletedUserID, f.deletedID = userID, id
	return f.err
}

// TestServiceCreateTrimsAndScopesUser proves the name is cleaned and the caller owns the row.
func TestServiceCreateTrimsAndScopesUser(t *testing.T) {
	repo := &fakeRepository{product: &Product{ID: "p1"}}
	svc := NewService(repo)

	if _, err := svc.Create(context.Background(), "user-1", CreateInput{
		Name:  "  Widget  ",
		Qty:   2,
		Price: 9.99,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if repo.createdUserID != "user-1" {
		t.Errorf("repository userID = %q, want user-1", repo.createdUserID)
	}
	if repo.createdInput.Name != "Widget" {
		t.Errorf("repository name = %q, want trimmed Widget", repo.createdInput.Name)
	}
}

// TestServiceCreateRejectsInvalidInput proves validation happens before any storage call.
func TestServiceCreateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		in   CreateInput
	}{
		{"blank name", CreateInput{Name: "   ", Qty: 1, Price: 1}},
		{"negative qty", CreateInput{Name: "Widget", Qty: -1, Price: 1}},
		{"negative price", CreateInput{Name: "Widget", Qty: 1, Price: -0.01}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo)

			if _, err := svc.Create(context.Background(), "user-1", tc.in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Create() error = %v, want ErrInvalidInput", err)
			}
			// A rejected request must never reach the repository.
			if repo.createdUserID != "" {
				t.Fatalf("repository was called with userID %q", repo.createdUserID)
			}
		})
	}
}

// TestServiceGetRejectsMalformedID proves a bad path ID never reaches Postgres.
func TestServiceGetRejectsMalformedID(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	if _, err := svc.Get(context.Background(), "user-1", "not-a-uuid"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Get() error = %v, want ErrInvalidInput", err)
	}
	if repo.gotID != "" {
		t.Fatalf("repository was called with id %q", repo.gotID)
	}
}

// TestServiceDeleteRejectsMalformedID covers the same guard on delete.
func TestServiceDeleteRejectsMalformedID(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	if err := svc.Delete(context.Background(), "user-1", "123"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Delete() error = %v, want ErrInvalidInput", err)
	}
	if repo.deletedID != "" {
		t.Fatalf("repository was called with id %q", repo.deletedID)
	}
}

// TestServicePropagatesNotFound proves the repository's sentinel survives the service layer.
func TestServicePropagatesNotFound(t *testing.T) {
	repo := &fakeRepository{err: ErrNotFound}
	svc := NewService(repo)

	const id = "11111111-1111-1111-1111-111111111111"
	if _, err := svc.Get(context.Background(), "user-1", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

// TestServiceListScopesUser proves list is always filtered by the authenticated user.
func TestServiceListScopesUser(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)

	if _, err := svc.List(context.Background(), "user-42"); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repo.listedUserID != "user-42" {
		t.Fatalf("repository userID = %q, want user-42", repo.listedUserID)
	}
}
