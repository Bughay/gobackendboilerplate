-- auth.sql — every query the authentication stores run.
--
-- Covers the two storage concerns that share this module:
--   accounts        — user rows (repository.go)
--   refresh tokens  — hashed, single-use session records (refresh_token_repository.go)
--
-- Password hashing, token signing and session policy live in Go, not here.

-- ============================================================================
-- Accounts
-- ============================================================================

-- name: FindAccountByID :one
-- Loads an account by primary key. Useful when a caller needs the current row
-- for a user identified by ID.
SELECT id, email, roles, password_hash
FROM users
WHERE id = $1;

-- name: FindAccountByEmail :one
-- lower(email) matches the functional unique index from the auth migration, so
-- lookups are case-insensitive regardless of how the address was stored.
SELECT id, email, roles, password_hash
FROM users
WHERE lower(email) = $1;

-- name: CreateAccount :one
-- Inserts a new account. A duplicate email surfaces as a unique violation which
-- the repository maps onto its duplicate-email sentinel.
INSERT INTO users (email, roles, password_hash)
VALUES ($1, $2, $3)
RETURNING id, email, roles, password_hash;

-- ============================================================================
-- Refresh tokens
-- ============================================================================

-- name: CreateRefreshToken :exec
-- Stores only the SHA-256 hex digest of a refresh token, never the token itself.
INSERT INTO refresh_tokens (token_hash, user_id, expires_at)
VALUES ($1, $2, $3);

-- name: ConsumeRefreshToken :execrows
-- Deletes a refresh token and returns how many rows were removed. The
-- expires_at predicate makes an expired token indistinguishable from an
-- already-used one, so reuse and expiry both fail the same way: 0 rows.
DELETE FROM refresh_tokens
WHERE token_hash = $1 AND expires_at > now();

-- name: DeleteRefreshTokensForUser :exec
-- Revokes every outstanding refresh token for a user.
DELETE FROM refresh_tokens
WHERE user_id = $1;
