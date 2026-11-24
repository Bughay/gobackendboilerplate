// Package orders implements creating and reading user-owned orders. An order is
// a list of order items; each item snapshots the chosen product and quantity at
// order time, and the order total is the sum of those line costs.
package orders

import "time"

// Order is a purchase made by one authenticated user.
type Order struct {
	ID string `json:"id"`
	// UserID is always the authenticated caller for rows returned by this
	// module. The schema keeps the column nullable for a future guest-order flow.
	UserID    string      `json:"user_id"`
	Email     string      `json:"email"`
	Status    string      `json:"status"`
	Total     float64     `json:"total"`
	CreatedAt time.Time   `json:"created_at"`
	Items     []OrderItem `json:"items"`
}

// OrderItem is one product line in an order. ProductName and UnitPrice are
// snapshots, so a later catalog edit or delete cannot rewrite order history.
type OrderItem struct {
	ID string `json:"id"`
	// ProductID is nil when the catalog row was deleted after the order was
	// placed; the name and price snapshots keep the line readable.
	ProductID   *string `json:"product_id"`
	ProductName string  `json:"product_name"`
	UnitPrice   float64 `json:"unit_price"`
	Qty         int     `json:"qty"`
}

// CreateInput is the request body for POST /orders. The client sends only
// product IDs and quantities; the server owns prices, totals and ownership.
type CreateInput struct {
	Items []CreateItemInput `json:"items"`
}

// CreateItemInput is one requested product line in a new order.
type CreateItemInput struct {
	ProductID string `json:"product_id"`
	Qty       int    `json:"qty"`
}
