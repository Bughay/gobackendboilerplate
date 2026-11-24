package auth

import (
	"net/mail"
	"strings"
)

// normalizeEmail lowercases and trims an address so storage, lookup, and
// uniqueness all agree on the same value.
func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// validateEmail reports whether s is a plausible, bare email address. It
// rejects display-name forms such as "Name <user@example.com>" and
// dotless domains such as "user@localhost".
func validateEmail(s string) bool {
	// Trim first so validation and normalization agree on the value checked.
	trimmed := strings.TrimSpace(s)

	addr, err := mail.ParseAddress(trimmed)
	if err != nil {
		return false
	}
	// mail.ParseAddress also accepts "Name <a@b>"; require a bare address.
	if addr.Address != trimmed {
		return false
	}

	// Require a local part, an "@", and a non-empty domain.
	at := strings.LastIndex(addr.Address, "@")
	if at < 1 || at == len(addr.Address)-1 {
		return false
	}
	// A dotted domain rejects the "user@localhost" shapes that are never real signups.
	if !strings.Contains(addr.Address[at+1:], ".") {
		return false
	}
	return true
}

// validatePassword enforces the minimum length and bcrypt's 72-byte ceiling.
// len counts bytes, which is the unit bcrypt's limit is defined in.
func validatePassword(s string) bool {
	return len(s) >= minPasswordBytes && len(s) <= maxPasswordBytes
}
