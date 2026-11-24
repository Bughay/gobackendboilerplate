package products

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/db/queries"
)

// Repository persists products, always scoped to the owning user.
type Repository interface {
	Create(ctx context.Context, userID string, in CreateInput) (*Product, error)
	List(ctx context.Context, userID string) ([]Product, error)
	Get(ctx context.Context, userID, id string) (*Product, error)
	Update(ctx context.Context, userID, id string, in UpdateInput) (*Product, error)
	Delete(ctx context.Context, userID, id string) error
}

// PostgresRepository is the sqlc-backed Repository.
type PostgresRepository struct {
	queries *queries.Queries
}

// NewPostgresRepository returns a product repository backed by pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{queries: queries.New(pool)}
}

// Create inserts a product owned by userID and returns the stored row.
func (r *PostgresRepository) Create(ctx context.Context, userID string, in CreateInput) (*Product, error) {
	row, err := r.queries.CreateProduct(ctx, queries.CreateProductParams{
		UserID: userID,
		Name:   in.Name,
		Qty:    in.Qty,
		Price:  in.Price,
	})
	if err != nil {
		return nil, err
	}
	p := toProduct(row)
	return &p, nil
}

// List returns every product owned by userID, newest first.
func (r *PostgresRepository) List(ctx context.Context, userID string) ([]Product, error) {
	rows, err := r.queries.ListProducts(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Start non-nil so an empty result encodes as [] rather than null.
	items := make([]Product, 0, len(rows))
	for _, row := range rows {
		items = append(items, toProduct(row))
	}
	return items, nil
}

// Get returns the product with the given ID, scoped to its owner.
func (r *PostgresRepository) Get(ctx context.Context, userID, id string) (*Product, error) {
	row, err := r.queries.GetProduct(ctx, queries.GetProductParams{ID: id, UserID: userID})
	if err != nil {
		return nil, mapNoRows(err)
	}
	p := toProduct(row)
	return &p, nil
}

// Update replaces the mutable fields of a product owned by userID.
func (r *PostgresRepository) Update(ctx context.Context, userID, id string, in UpdateInput) (*Product, error) {
	row, err := r.queries.UpdateProduct(ctx, queries.UpdateProductParams{
		ID:     id,
		UserID: userID,
		Name:   in.Name,
		Qty:    in.Qty,
		Price:  in.Price,
	})
	if err != nil {
		return nil, mapNoRows(err)
	}
	p := toProduct(row)
	return &p, nil
}

// Delete removes a product owned by userID.
func (r *PostgresRepository) Delete(ctx context.Context, userID, id string) error {
	affected, err := r.queries.DeleteProduct(ctx, queries.DeleteProductParams{ID: id, UserID: userID})
	if err != nil {
		return err
	}
	// Zero rows means the product is missing or owned by someone else.
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// toProduct maps a generated row onto the domain model.
func toProduct(row queries.Product) Product {
	return Product{
		ID:        row.ID,
		UserID:    row.UserID,
		Name:      row.Name,
		DateAdded: row.DateAdded,
		Qty:       row.Qty,
		Price:     row.Price,
	}
}

// mapNoRows translates pgx's "no rows" into the domain's ErrNotFound.
func mapNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
