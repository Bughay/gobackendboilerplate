package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// accessIssuer marks a token as an access token. ValidateRefresh rejects it.
	accessIssuer = "go-api"
	// refreshIssuer marks a token as a refresh token. ValidateAccess rejects it.
	refreshIssuer = "go-api-refresh"
)

// errInvalidToken is returned for any malformed, badly signed, expired, or
// wrong-issuer token. Callers map it onto a 401.
var errInvalidToken = errors.New("auth: invalid token")

// jwtCustomClaims embeds our application claims alongside the registered ones
// (exp, iat, jti) that the JWT library validates for us.
type jwtCustomClaims struct {
	Claims
	jwt.RegisteredClaims
}

// Manager signs and validates tokens. It is a typed value, not a global, so
// tests and multiple services can hold independent managers.
type Manager struct {
	secretKey     []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
}

// NewManager builds a Manager from a shared secret and lifetimes expressed in
// hours. Configuration guarantees both lifetimes are positive.
func NewManager(secret string, accessExpiryHours int, refreshExpiryHours int) *Manager {
	return &Manager{
		secretKey:     []byte(secret),
		accessExpiry:  time.Duration(accessExpiryHours) * time.Hour,
		refreshExpiry: time.Duration(refreshExpiryHours) * time.Hour,
	}
}

// RefreshExpiry returns the refresh-token lifetime so the handler can set a
// cookie Max-Age that matches the token that was persisted.
func (m *Manager) RefreshExpiry() time.Duration {
	return m.refreshExpiry
}

// Generate mints a short-lived HS256 access token carrying the user's ID,
// email and roles.
func (m *Manager) Generate(userID, email string, roles []string) (string, error) {
	now := time.Now()
	claims := jwtCustomClaims{
		Claims: Claims{UserID: userID, Email: email, Roles: roles},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(m.accessExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    accessIssuer,
		},
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secretKey)
	if err != nil {
		return "", fmt.Errorf("auth: failed to sign access token: %w", err)
	}
	return signed, nil
}

// GenerateRefreshToken mints an HS256 refresh token. The random jti keeps two
// tokens minted in the same second distinct, which single-use rotation needs.
func (m *Manager) GenerateRefreshToken(userID, email string, roles []string) (string, error) {
	tokenID, err := newTokenID()
	if err != nil {
		return "", fmt.Errorf("auth: failed to generate token id: %w", err)
	}

	now := time.Now()
	claims := jwtCustomClaims{
		Claims: Claims{UserID: userID, Email: email, Roles: roles},
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        tokenID,
			ExpiresAt: jwt.NewNumericDate(now.Add(m.refreshExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    refreshIssuer,
		},
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secretKey)
	if err != nil {
		return "", fmt.Errorf("auth: failed to sign refresh token: %w", err)
	}
	return signed, nil
}

// ValidateAccess verifies an access token and returns its application claims.
func (m *Manager) ValidateAccess(tokenStr string) (*Claims, error) {
	return m.validate(tokenStr, accessIssuer)
}

// ValidateRefresh verifies a refresh token and returns its application claims.
func (m *Manager) ValidateRefresh(tokenStr string) (*Claims, error) {
	return m.validate(tokenStr, refreshIssuer)
}

// validate parses a token, pins the algorithm to HS256, requires a future exp,
// and checks that the issuer matches the kind of token the caller expects.
func (m *Manager) validate(tokenStr, expectedIssuer string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(
		tokenStr,
		&jwtCustomClaims{},
		// The keyfunc only supplies the key; the options below enforce the algorithm.
		func(*jwt.Token) (any, error) { return m.secretKey, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(expectedIssuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		// Wrap the sentinel so callers can use errors.Is while keeping the cause.
		return nil, fmt.Errorf("%w: %v", errInvalidToken, err)
	}

	claims, ok := parsed.Claims.(*jwtCustomClaims)
	if !ok || !parsed.Valid {
		return nil, errInvalidToken
	}
	return &claims.Claims, nil
}

// newTokenID returns 128 random bits as hex, used as the refresh token's jti.
func newTokenID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
