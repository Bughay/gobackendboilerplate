package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/db/queries"
)

// RefreshTokenRepository is the allowlist of live refresh tokens. Only the
// SHA-256 hash is stored, so a leaked table cannot be used to refresh sessions.
type RefreshTokenRepository interface {
	// Create records a freshly issued refresh token by its hash.
	Create(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error
	// Delete removes the token and reports whether a live row was actually
	// removed. The boolean is the single-use/revocation gate.
	Delete(ctx context.Context, tokenHash string) (bool, error)
}

// pgRefreshTokenRepository is the sqlc-backed RefreshTokenRepository.
type pgRefreshTokenRepository struct {
	queries *queries.Queries
}

// NewRefreshTokenRepository returns a refresh-token repository backed by pool.
func NewRefreshTokenRepository(pool *pgxpool.Pool) RefreshTokenRepository {
	return &pgRefreshTokenRepository{queries: queries.New(pool)}
}

// Create stores the hash and expiry of a refresh token.
func (r *pgRefreshTokenRepository) Create(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	if err := r.queries.CreateRefreshToken(ctx, queries.CreateRefreshTokenParams{
		TokenHash: tokenHash,
		UserID:    userID,
		ExpiresAt: expiresAt,
	}); err != nil {
		return fmt.Errorf("refreshTokenRepo.Create: %w", err)
	}
	return nil
}

// Delete consumes a refresh token. ConsumeRefreshToken also filters on
// expires_at > now(), so an expired token and a replayed token both return false.
func (r *pgRefreshTokenRepository) Delete(ctx context.Context, tokenHash string) (bool, error) {
	rows, err := r.queries.ConsumeRefreshToken(ctx, tokenHash)
	if err != nil {
		return false, fmt.Errorf("refreshTokenRepo.Delete: %w", err)
	}
	return rows > 0, nil
}

// Compile-time proof that the concrete type satisfies the interface.
var _ RefreshTokenRepository = (*pgRefreshTokenRepository)(nil)
