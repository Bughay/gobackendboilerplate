package products

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	// ErrNotFound is returned when a product does not exist or is not owned by the caller.
	ErrNotFound = errors.New("products: not found")

	// ErrInvalidInput is returned when a request does not satisfy the domain rules.
	ErrInvalidInput = errors.New("products: invalid input")
)

const (
	// maxNameLength bounds the name so a single row cannot be used as a storage amplifier.
	maxNameLength = 200
	// uuidLength is the canonical hyphenated UUID length.
	uuidLength = 36
	// maxPrice is the largest value a numeric(12,2) column can hold.
	maxPrice = 9_999_999_999.99
)

// ValidateName rejects blank and over-long product names.
func ValidateName(s string) error {
	// Count runes, not bytes, so non-ASCII names get the same budget.
	name := strings.TrimSpace(s)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return ErrInvalidInput
	}
	return nil
}

// ValidateQty rejects negative quantities.
func ValidateQty(qty int) error {
	if qty < 0 {
		return ErrInvalidInput
	}
	return nil
}

// ValidatePrice rejects negative and out-of-range prices.
func ValidatePrice(price float64) error {
	// NaN fails neither comparison, so reject it explicitly.
	if price < 0 || price > maxPrice || price != price {
		return ErrInvalidInput
	}
	return nil
}

// IsUUID reports whether s looks like a canonical hyphenated UUID.
func IsUUID(s string) bool {
	// A wrong length can never be a UUID, which also keeps the index loop simple.
	if len(s) != uuidLength {
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
