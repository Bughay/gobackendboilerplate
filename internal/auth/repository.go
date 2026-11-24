package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bughay/ego-backend-boilerplate/internal/platform/db/queries"
)

// uniqueViolation is PostgreSQL's SQLSTATE for a unique-constraint violation.
const uniqueViolation = "23505"

// errEmailTaken signals that a concurrent registration inserted the same email
// between the service's pre-check and the INSERT. The service turns it into a
// validation response instead of a 500.
var errEmailTaken = errors.New("auth: email already registered")

// UserRepository persists and looks up users. A missing row is (nil, nil), not
// an error, so callers can distinguish "not found" from a database failure.
type UserRepository interface {
	Create(ctx context.Context, email, hashedPassword string) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByID(ctx context.Context, id string) (*User, error)
}

// pgUserRepository is the sqlc-backed UserRepository.
type pgUserRepository struct {
	queries *queries.Queries
}

// NewUserRepository returns a user repository backed by pool.
func NewUserRepository(pool *pgxpool.Pool) UserRepository {
	return &pgUserRepository{queries: queries.New(pool)}
}

// Create inserts a user with the default "user" role and returns the stored row.
func (r *pgUserRepository) Create(ctx context.Context, email, hashedPassword string) (*User, error) {
	row, err := r.queries.CreateAccount(ctx, queries.CreateAccountParams{
		Email: normalizeEmail(email),
		// The schema defaults to ARRAY['user'], but the generated INSERT takes
		// the role array explicitly, so set it here to keep both paths equal.
		Roles:        []string{"user"},
		PasswordHash: hashedPassword,
	})
	if err != nil {
		return nil, mapCreateError(err)
	}
	return toUser(row.ID, row.Email, row.Roles, row.PasswordHash), nil
}

// FindByEmail loads a user by normalized email. The query uses lower(email),
// which matches the functional unique index from the migration.
func (r *pgUserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	row, err := r.queries.FindAccountByEmail(ctx, normalizeEmail(email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("userRepo.FindByEmail: %w", err)
	}
	return toUser(row.ID, row.Email, row.Roles, row.PasswordHash), nil
}

// FindByID loads a user by primary key.
func (r *pgUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	row, err := r.queries.FindAccountByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("userRepo.FindByID: %w", err)
	}
	return toUser(row.ID, row.Email, row.Roles, row.PasswordHash), nil
}

// toUser maps one stored row onto the domain model.
func toUser(id, email string, roles []string, hash string) *User {
	return &User{ID: id, Email: email, PasswordHash: hash, Roles: roles}
}

// mapCreateError turns a duplicate-email unique violation into errEmailTaken
// and wraps anything else with repository context.
func mapCreateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return fmt.Errorf("userRepo.Create: %w", errEmailTaken)
	}
	return fmt.Errorf("userRepo.Create: %w", err)
}

// Compile-time proof that the concrete type satisfies the interface.
var _ UserRepository = (*pgUserRepository)(nil)
