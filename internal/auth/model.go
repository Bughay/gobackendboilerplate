// Package auth provides password authentication, JWT issuing and validation,
// refresh-token rotation, and the HTTP middleware that protects product routes.
package auth

// User is an authenticated application user.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	// PasswordHash is the bcrypt digest. It is tagged "-" so it can never be
	// serialized into an API response by accident.
	PasswordHash string `json:"-"`
	// Roles is the text[] stored on the users row. It stays a slice because the
	// schema uses an array, unlike the reference implementation's single role.
	Roles []string `json:"roles"`
}

// RegisterRequest is the request body for POST /auth/register.
type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginRequest is the request body for POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is the body returned by POST /auth/login.
type LoginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	User        *User  `json:"user"`
}

// RegistrationResponse is the body returned by POST /auth/register.
// Registration deliberately issues no tokens; the client logs in afterward.
type RegistrationResponse struct {
	Message string `json:"message"`
	Success bool   `json:"success"`
	Person  *User  `json:"person"`
}

// RefreshResponse is the body returned by POST /auth/refresh. The rotated
// refresh token travels only in the cookie, never in this body.
type RefreshResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// Claims is the custom payload embedded in both access and refresh tokens.
type Claims struct {
	UserID string   `json:"user_id"`
	Email  string   `json:"email"`
	Roles  []string `json:"roles"`
}
