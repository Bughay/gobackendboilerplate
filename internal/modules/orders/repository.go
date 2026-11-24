package orders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/db/queries"
)

// Repository persists and loads orders, always scoped to the owning user.
type Repository interface {
	Create(ctx context.Context, userID string, in CreateInput) (*Order, error)
	List(ctx context.Context, userID string) ([]Order, error)
	Get(ctx context.Context, userID, id string) (*Order, error)
}

// PostgresRepository is the sqlc-backed Repository. It keeps the pool so Create
// can run its product lookup, order insert and item inserts in one transaction.
type PostgresRepository struct {
	pool    *pgxpool.Pool
	queries *queries.Queries
}

// NewPostgresRepository returns an order repository backed by pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool, queries: queries.New(pool)}
}

// Create builds an order from the caller's catalog rows in a single transaction.
// It loads the products, computes the total from their current prices, inserts
// the order and its item snapshots, and commits. Any failure rolls everything
// back, so a failed request never leaves a partial order.
func (r *PostgresRepository) Create(ctx context.Context, userID string, in CreateInput) (*Order, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders.Create: begin: %w", err)
	}
	// Rollback is a no-op after a successful commit.
	defer func() { _ = tx.Rollback(ctx) }()

	q := queries.New(tx)

	// Collect the requested IDs once for the batch product lookup.
	ids := make([]string, len(in.Items))
	for i, item := range in.Items {
		ids[i] = item.ProductID
	}

	productRows, err := q.GetOrderProducts(ctx, queries.GetOrderProductsParams{
		UserID:     userID,
		ProductIds: ids,
	})
	if err != nil {
		return nil, fmt.Errorf("orders.Create: load products: %w", err)
	}
	// The query is already scoped to this user; a short result means at least one
	// ID is missing or belongs to someone else, and both cases look identical.
	if len(productRows) != len(ids) {
		return nil, ErrProductNotFound
	}

	byID := make(map[string]queries.GetOrderProductsRow, len(productRows))
	prices := make(map[string]float64, len(productRows))
	for _, row := range productRows {
		byID[row.ID] = row
		prices[row.ID] = row.Price
	}

	total, err := calculateTotal(in.Items, prices)
	if err != nil {
		return nil, err
	}

	orderRow, err := q.CreateOrder(ctx, queries.CreateOrderParams{
		UserID: userID,
		Total:  total,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The INSERT ... SELECT found no users row, which means the JWT points at
		// an account that no longer exists.
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("orders.Create: insert order: %w", err)
	}

	items := make([]OrderItem, 0, len(in.Items))
	for _, item := range in.Items {
		product := byID[item.ProductID]
		itemID, err := q.CreateOrderItem(ctx, queries.CreateOrderItemParams{
			OrderID:     orderRow.ID,
			ProductID:   product.ID,
			ProductName: product.Name,
			UnitPrice:   product.Price,
			Qty:         item.Qty,
		})
		if err != nil {
			return nil, fmt.Errorf("orders.Create: insert item: %w", err)
		}

		// Every line the module inserts has a product, so the pointer is non-nil.
		productID := product.ID
		items = append(items, OrderItem{
			ID:          itemID,
			ProductID:   &productID,
			ProductName: product.Name,
			UnitPrice:   product.Price,
			Qty:         item.Qty,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("orders.Create: commit: %w", err)
	}

	order := toOrder(orderRow.ID, orderRow.UserID, orderRow.Email, orderRow.Status, orderRow.Total, orderRow.CreatedAt, items)
	return &order, nil
}

// List returns the caller's orders, newest first, each with its items. Items are
// fetched in one query and grouped in memory to avoid a per-order query.
func (r *PostgresRepository) List(ctx context.Context, userID string) ([]Order, error) {
	rows, err := r.queries.ListOrdersByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("orders.List: load orders: %w", err)
	}

	itemRows, err := r.queries.ListOrderItems(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("orders.List: load items: %w", err)
	}

	itemsByOrder := make(map[string][]OrderItem, len(rows))
	for _, row := range itemRows {
		itemsByOrder[row.OrderID] = append(itemsByOrder[row.OrderID],
			toOrderItem(row.ID, row.ProductID, row.ProductName, row.UnitPrice, row.Qty))
	}

	// Start non-nil so an empty result encodes as [] rather than null.
	orders := make([]Order, 0, len(rows))
	for _, row := range rows {
		items := itemsByOrder[row.ID]
		if items == nil {
			items = []OrderItem{}
		}
		orders = append(orders, toOrder(row.ID, row.UserID, row.Email, row.Status, row.Total, row.CreatedAt, items))
	}
	return orders, nil
}

// Get returns one order owned by userID. A missing order and an order owned by
// someone else both become ErrNotFound, so ownership cannot be probed.
func (r *PostgresRepository) Get(ctx context.Context, userID, id string) (*Order, error) {
	row, err := r.queries.GetOrder(ctx, queries.GetOrderParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("orders.Get: load order: %w", err)
	}

	itemRows, err := r.queries.ListOrderItemsByOrder(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("orders.Get: load items: %w", err)
	}

	// Start non-nil so an itemless order encodes as [] rather than null.
	items := make([]OrderItem, 0, len(itemRows))
	for _, item := range itemRows {
		items = append(items, toOrderItem(item.ID, item.ProductID, item.ProductName, item.UnitPrice, item.Qty))
	}

	order := toOrder(row.ID, row.UserID, row.Email, row.Status, row.Total, row.CreatedAt, items)
	return &order, nil
}

// toOrder maps one stored order row and its items onto the domain model.
func toOrder(id string, userID *string, email, status string, total float64, createdAt time.Time, items []OrderItem) Order {
	return Order{
		ID:        id,
		UserID:    derefString(userID),
		Email:     email,
		Status:    status,
		Total:     total,
		CreatedAt: createdAt,
		Items:     items,
	}
}

// toOrderItem maps one stored item row onto the domain model.
func toOrderItem(id string, productID *string, name string, price float64, qty int) OrderItem {
	return OrderItem{
		ID:          id,
		ProductID:   productID,
		ProductName: name,
		UnitPrice:   price,
		Qty:         qty,
	}
}

// derefString returns the pointed-to string or "" for a NULL column.
func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// Compile-time proof that the concrete type satisfies the interface.
var _ Repository = (*PostgresRepository)(nil)
