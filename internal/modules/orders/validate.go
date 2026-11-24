package orders

import (
	"errors"
	"strings"
)

var (
	// ErrNotFound is returned when an order does not exist or is not owned by the caller.
	ErrNotFound = errors.New("orders: not found")

	// ErrInvalidInput is returned when a request does not satisfy the order rules.
	ErrInvalidInput = errors.New("orders: invalid input")

	// ErrProductNotFound means at least one requested product is missing from the
	// caller's catalog. It deliberately does not reveal whether the row exists
	// under another user.
	ErrProductNotFound = errors.New("orders: products not found")

	// ErrUserNotFound means the authenticated token points at a user row that no
	// longer exists, so the order cannot snapshot an email.
	ErrUserNotFound = errors.New("orders: user not found")
)

const (
	// maxItems bounds one order so a single request cannot create an unbounded
	// number of rows or parameters.
	maxItems = 100
	// maxItemQty bounds one line so the cents calculation cannot overflow int64.
	maxItemQty = 1_000_000
)

// cleanCreate validates a create request and returns a copy with trimmed
// product IDs. Duplicate product IDs are rejected rather than merged so the
// client always sees exactly the lines it asked for.
func cleanCreate(in CreateInput) (CreateInput, error) {
	// An order must contain at least one line and must not be an unbounded batch.
	if len(in.Items) == 0 || len(in.Items) > maxItems {
		return CreateInput{}, ErrInvalidInput
	}

	seen := make(map[string]struct{}, len(in.Items))
	items := make([]CreateItemInput, 0, len(in.Items))
	for _, item := range in.Items {
		productID := strings.TrimSpace(item.ProductID)

		// Validate before storage so malformed IDs never reach Postgres.
		if !isUUID(productID) || item.Qty <= 0 || item.Qty > maxItemQty {
			return CreateInput{}, ErrInvalidInput
		}
		// The same product twice would make the order ambiguous to read.
		if _, exists := seen[productID]; exists {
			return CreateInput{}, ErrInvalidInput
		}

		seen[productID] = struct{}{}
		items = append(items, CreateItemInput{ProductID: productID, Qty: item.Qty})
	}

	return CreateInput{Items: items}, nil
}

// isUUID reports whether s is a canonical hyphenated UUID. Keeping this local
// avoids making the orders module depend on the products module for validation.
func isUUID(s string) bool {
	// A wrong length can never be a UUID, which also keeps the index loop simple.
	if len(s) != 36 {
		return false
	}

	for i, r := range s {
		// Positions 8, 13, 18 and 23 are the required hyphens.
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !isHexDigit(r) {
			return false
		}
	}
	return true
}

// isHexDigit reports whether r is an ASCII hexadecimal digit.
func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
