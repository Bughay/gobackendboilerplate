package products

import (
	"context"
	"strings"
)

// Service applies the product domain rules on top of the repository.
type Service struct {
	repo Repository
}

// NewService returns a product service backed by repo.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Create validates in and stores a new product owned by userID.
func (s *Service) Create(ctx context.Context, userID string, in CreateInput) (*Product, error) {
	clean, err := cleanCreate(in)
	if err != nil {
		return nil, err
	}
	return s.repo.Create(ctx, userID, clean)
}

// List returns every product owned by userID.
func (s *Service) List(ctx context.Context, userID string) ([]Product, error) {
	return s.repo.List(ctx, userID)
}

// Get returns one product owned by userID.
func (s *Service) Get(ctx context.Context, userID, id string) (*Product, error) {
	// Reject a malformed ID here so Postgres never sees an invalid uuid cast.
	if !IsUUID(id) {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, userID, id)
}

// Update validates in and replaces a product owned by userID.
func (s *Service) Update(ctx context.Context, userID, id string, in UpdateInput) (*Product, error) {
	if !IsUUID(id) {
		return nil, ErrInvalidInput
	}
	clean, err := cleanUpdate(in)
	if err != nil {
		return nil, err
	}
	return s.repo.Update(ctx, userID, id, clean)
}

// Delete removes a product owned by userID.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	if !IsUUID(id) {
		return ErrInvalidInput
	}
	return s.repo.Delete(ctx, userID, id)
}

// cleanCreate trims and validates a create request.
func cleanCreate(in CreateInput) (CreateInput, error) {
	name := strings.TrimSpace(in.Name)
	if err := ValidateName(name); err != nil {
		return CreateInput{}, err
	}
	if err := ValidateQty(in.Qty); err != nil {
		return CreateInput{}, err
	}
	if err := ValidatePrice(in.Price); err != nil {
		return CreateInput{}, err
	}
	return CreateInput{Name: name, Qty: in.Qty, Price: in.Price}, nil
}

// cleanUpdate trims and validates an update request.
func cleanUpdate(in UpdateInput) (UpdateInput, error) {
	name := strings.TrimSpace(in.Name)
	if err := ValidateName(name); err != nil {
		return UpdateInput{}, err
	}
	if err := ValidateQty(in.Qty); err != nil {
		return UpdateInput{}, err
	}
	if err := ValidatePrice(in.Price); err != nil {
		return UpdateInput{}, err
	}
	return UpdateInput{Name: name, Qty: in.Qty, Price: in.Price}, nil
}
