package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// validationError is a user-facing problem with the request. The handler turns
// it into a 422; everything else becomes a 500 unless it is a known auth error.
type validationError struct {
	message string
}

// Error returns the message that is safe to show the client.
func (e *validationError) Error() string {
	return e.message
}

var (
	// errInvalidCredentials is shared by every login failure so an unknown email
	// and a wrong password are indistinguishable to the client.
	errInvalidCredentials = errors.New("auth: invalid email or password")
	// errTokenRevoked means the refresh token was already rotated, logged out,
	// or expired in the store.
	errTokenRevoked = errors.New("auth: refresh token is no longer valid")
	// errUserNotFound means the token is valid but its user row is gone.
	errUserNotFound = errors.New("auth: user no longer exists")
)

// AuthService defines the authentication business logic.
type AuthService interface {
	Register(ctx context.Context, req *RegisterRequest) (*RegistrationResponse, error)
	Login(ctx context.Context, req *LoginRequest) (*LoginResponse, string, error)
	Logout(ctx context.Context, refreshToken string) error
	Refresh(ctx context.Context, refreshToken string) (*RefreshResponse, string, error)
	RefreshExpiry() time.Duration
}

// authService is the concrete AuthService.
type authService struct {
	userRepo   UserRepository
	tokenRepo  RefreshTokenRepository
	jwtManager *Manager
}

// NewAuthService returns an AuthService backed by the given repositories and
// token manager.
func NewAuthService(userRepo UserRepository, tokenRepo RefreshTokenRepository, jwtManager *Manager) AuthService {
	return &authService{userRepo: userRepo, tokenRepo: tokenRepo, jwtManager: jwtManager}
}

// Register validates the input, hashes the password, and creates the account.
// It deliberately issues no tokens; the client logs in afterward.
func (s *authService) Register(ctx context.Context, req *RegisterRequest) (*RegistrationResponse, error) {
	req.Email = normalizeEmail(req.Email)

	// Validate before touching storage so malformed input costs nothing.
	if !validateEmail(req.Email) {
		return nil, &validationError{message: "a valid email address is required"}
	}
	if !validatePassword(req.Password) {
		return nil, &validationError{message: "password must be between 8 and 72 bytes"}
	}

	// A friendly pre-check catches the common duplicate case. The unique index
	// still protects the concurrent case, which is mapped below.
	existing, err := s.userRepo.FindByEmail(ctx, req.Email)
	if err != nil {
		return nil, fmt.Errorf("register: check existing user: %w", err)
	}
	if existing != nil {
		return nil, &validationError{message: "a user with this email already exists"}
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("register: hash password: %w", err)
	}

	user, err := s.userRepo.Create(ctx, req.Email, hash)
	if err != nil {
		// A concurrent insert surfaces as a unique violation, not a 500.
		if errors.Is(err, errEmailTaken) {
			return nil, &validationError{message: "a user with this email already exists"}
		}
		return nil, fmt.Errorf("register: create user: %w", err)
	}

	return &RegistrationResponse{
		Message: "You have successfully registered",
		Success: true,
		Person:  user,
	}, nil
}

// Login verifies credentials and returns an access token, the raw refresh token
// for the cookie, and the user projection.
func (s *authService) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, string, error) {
	email := normalizeEmail(req.Email)

	user, err := s.userRepo.FindByEmail(ctx, email)
	if err != nil {
		return nil, "", fmt.Errorf("login: find user: %w", err)
	}

	// Unknown email and wrong password must look identical. When no row exists,
	// compare against a dummy hash so the timing is similar too.
	if user == nil {
		verifyPassword(dummyHash(), req.Password)
		return nil, "", errInvalidCredentials
	}
	if !verifyPassword(user.PasswordHash, req.Password) {
		return nil, "", errInvalidCredentials
	}

	accessToken, err := s.jwtManager.Generate(user.ID, user.Email, user.Roles)
	if err != nil {
		return nil, "", err
	}
	refreshToken, err := s.jwtManager.GenerateRefreshToken(user.ID, user.Email, user.Roles)
	if err != nil {
		return nil, "", err
	}

	// Persist only the SHA-256 hash of the refresh token. Without this row the
	// token would be rejected by Refresh.
	if err := s.tokenRepo.Create(ctx, user.ID, hashToken(refreshToken), time.Now().Add(s.jwtManager.RefreshExpiry())); err != nil {
		return nil, "", fmt.Errorf("login: persist refresh token: %w", err)
	}

	return &LoginResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		User:        user,
	}, refreshToken, nil
}

// Refresh validates the presented refresh token, deletes its stored hash
// (single use), and issues a fresh pair.
func (s *authService) Refresh(ctx context.Context, refreshToken string) (*RefreshResponse, string, error) {
	claims, err := s.jwtManager.ValidateRefresh(refreshToken)
	if err != nil {
		return nil, "", errInvalidToken
	}

	// Reload the user so current roles and email are used, matching the target.
	// A deleted user means the session is dead even though the JWT verifies.
	user, err := s.userRepo.FindByEmail(ctx, claims.Email)
	if err != nil {
		return nil, "", fmt.Errorf("refresh: find user: %w", err)
	}
	if user == nil {
		return nil, "", errUserNotFound
	}

	// Deleting first is the single-use gate: a replay deletes nothing and fails.
	deleted, err := s.tokenRepo.Delete(ctx, hashToken(refreshToken))
	if err != nil {
		return nil, "", fmt.Errorf("refresh: delete token: %w", err)
	}
	if !deleted {
		return nil, "", errTokenRevoked
	}

	accessToken, err := s.jwtManager.Generate(user.ID, user.Email, user.Roles)
	if err != nil {
		return nil, "", err
	}
	newRefreshToken, err := s.jwtManager.GenerateRefreshToken(user.ID, user.Email, user.Roles)
	if err != nil {
		return nil, "", err
	}

	if err := s.tokenRepo.Create(ctx, user.ID, hashToken(newRefreshToken), time.Now().Add(s.jwtManager.RefreshExpiry())); err != nil {
		return nil, "", fmt.Errorf("refresh: persist refresh token: %w", err)
	}

	return &RefreshResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
	}, newRefreshToken, nil
}

// Logout revokes the presented refresh token. It is idempotent: a token whose
// row is already gone is still a successful logout.
func (s *authService) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	if _, err := s.jwtManager.ValidateRefresh(refreshToken); err != nil {
		return errInvalidToken
	}
	// Ignore the deleted boolean: the goal is "no longer usable", which is
	// already true when the row is absent.
	if _, err := s.tokenRepo.Delete(ctx, hashToken(refreshToken)); err != nil {
		return fmt.Errorf("logout: delete token: %w", err)
	}
	return nil
}

// RefreshExpiry reports how long a refresh token and its cookie live.
func (s *authService) RefreshExpiry() time.Duration {
	return s.jwtManager.RefreshExpiry()
}

// hashToken returns the hex SHA-256 digest stored in refresh_tokens.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Compile-time proof that the concrete type satisfies the interface.
var _ AuthService = (*authService)(nil)
