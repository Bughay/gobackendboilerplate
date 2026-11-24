// Package products implements the products CRUD module: model, storage,
// business rules and HTTP handlers for user-owned products.
package products

import "time"

// Product is one item owned by a single user.
type Product struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	DateAdded time.Time `json:"date_added"`
	Qty       int       `json:"qty"`
	// Price is stored as numeric(12,2) in Postgres and exposed as a JSON number.
	// sqlc maps the column to float64, and pgx converts between the two directly.
	Price float64 `json:"price"`
}

// CreateInput is the request body for creating a product.
type CreateInput struct {
	Name  string  `json:"name"`
	Qty   int     `json:"qty"`
	Price float64 `json:"price"`
}

// UpdateInput is the request body for replacing a product's mutable fields.
type UpdateInput struct {
	Name  string  `json:"name"`
	Qty   int     `json:"qty"`
	Price float64 `json:"price"`
}
