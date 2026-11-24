package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// stubUserRepository serves users from an in-memory map keyed by normalized email.
type stubUserRepository struct {
	usersByEmail   map[string]*User
	findByEmailErr error
	createErr      error

	createCalled  bool
	createdEmail  string
	createdHashed string
}

// Create records the call and inserts the user unless createErr is set.
func (s *stubUserRepository) Create(_ context.Context, email, hashedPassword string) (*User, error) {
	s.createCalled = true
	s.createdEmail = email
	s.createdHashed = hashedPassword
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.usersByEmail == nil {
		s.usersByEmail = make(map[string]*User)
	}
	user := &User{ID: "user-1", Email: email, PasswordHash: hashedPassword, Roles: []string{"user"}}
	s.usersByEmail[email] = user
	return user, nil
}

// FindByEmail looks a user up by normalized email.
func (s *stubUserRepository) FindByEmail(_ context.Context, email string) (*User, error) {
	if s.findByEmailErr != nil {
		return nil, s.findByEmailErr
	}
	if s.usersByEmail == nil {
		return nil, nil
	}
	return s.usersByEmail[normalizeEmail(email)], nil
}

// FindByID looks a user up by ID.
func (s *stubUserRepository) FindByID(_ context.Context, id string) (*User, error) {
	for _, user := range s.usersByEmail {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, nil
}

// stubRefreshTokenRepository is an in-memory allowlist of live token hashes.
type stubRefreshTokenRepository struct {
	hashes    map[string]string // token hash -> user ID
	createErr error
	deleteErr error
}

// Create records a live token hash.
func (s *stubRefreshTokenRepository) Create(_ context.Context, userID, tokenHash string, _ time.Time) error {
	if s.createErr != nil {
		return s.createErr
	}
	if s.hashes == nil {
		s.hashes = make(map[string]string)
	}
	s.hashes[tokenHash] = userID
	return nil
}

// Delete consumes a token hash and reports whether it existed.
func (s *stubRefreshTokenRepository) Delete(_ context.Context, tokenHash string) (bool, error) {
	if s.deleteErr != nil {
		return false, s.deleteErr
	}
	if s.hashes == nil {
		return false, nil
	}
	if _, ok := s.hashes[tokenHash]; !ok {
		return false, nil
	}
	delete(s.hashes, tokenHash)
	return true, nil
}

// testEnv bundles the service, handler and stubs a test needs.
type testEnv struct {
	handler *AuthHandler
	svc     AuthService
	users   *stubUserRepository
	tokens  *stubRefreshTokenRepository
	mgr     *Manager
}

// newTestEnv builds an environment over an optional set of seeded users.
func newTestEnv(users map[string]*User) *testEnv {
	repo := &stubUserRepository{usersByEmail: users}
	tokens := &stubRefreshTokenRepository{hashes: make(map[string]string)}
	mgr := NewManager("test-secret", 1, 24)
	svc := NewAuthService(repo, tokens, mgr)
	return &testEnv{
		handler: NewAuthHandler(svc, testLogger(), true),
		svc:     svc,
		users:   repo,
		tokens:  tokens,
		mgr:     mgr,
	}
}

// testLogger writes to io.Discard so test output stays clean.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// hashPasswordForTest produces a cheap bcrypt hash for fixtures.
func hashPasswordForTest(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	return string(h)
}

// postJSON marshals body, sends it to handler, and returns the recorder.
func postJSON(t *testing.T, handler http.HandlerFunc, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// findCookie returns the named cookie or nil.
func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// parseRefreshToken parses a refresh token into the custom claims, including jti.
func parseRefreshToken(t *testing.T, mgr *Manager, token string) *jwtCustomClaims {
	t.Helper()
	parsed, err := jwt.ParseWithClaims(
		token,
		&jwtCustomClaims{},
		func(*jwt.Token) (any, error) { return mgr.secretKey, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		t.Fatalf("parse refresh token: %v", err)
	}
	claims, ok := parsed.Claims.(*jwtCustomClaims)
	if !ok {
		t.Fatal("unexpected claims type")
	}
	return claims
}

// --- JWT manager ---

// TestManagerTokenRoundTrip proves access tokens carry the expected claims.
func TestManagerTokenRoundTrip(t *testing.T) {
	env := newTestEnv(nil)

	token, err := env.mgr.Generate("user-1", "user@test.com", []string{"user", "admin"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	claims, err := env.mgr.ValidateAccess(token)
	if err != nil {
		t.Fatalf("ValidateAccess() error = %v", err)
	}
	if claims.UserID != "user-1" || claims.Email != "user@test.com" {
		t.Errorf("claims = %+v, want user-1 / user@test.com", claims)
	}
	if !slices.Equal(claims.Roles, []string{"user", "admin"}) {
		t.Errorf("roles = %v, want [user admin]", claims.Roles)
	}
}

// TestManagerRefreshTokenRoundTripAndJTI proves refresh tokens validate and
// that two tokens minted in the same second still have distinct jti values.
func TestManagerRefreshTokenRoundTripAndJTI(t *testing.T) {
	env := newTestEnv(nil)

	first, err := env.mgr.GenerateRefreshToken("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error = %v", err)
	}
	second, err := env.mgr.GenerateRefreshToken("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error = %v", err)
	}

	claims, err := env.mgr.ValidateRefresh(first)
	if err != nil {
		t.Fatalf("ValidateRefresh() error = %v", err)
	}
	if claims.UserID != "user-1" || !slices.Equal(claims.Roles, []string{"user"}) {
		t.Errorf("claims = %+v, want user-1 with role user", claims)
	}

	parsedFirst := parseRefreshToken(t, env.mgr, first)
	parsedSecond := parseRefreshToken(t, env.mgr, second)
	if parsedFirst.ID == "" {
		t.Fatal("refresh token has an empty jti")
	}
	if parsedFirst.ID == parsedSecond.ID {
		t.Fatal("two refresh tokens share a jti")
	}
	if parsedFirst.Issuer != refreshIssuer {
		t.Errorf("issuer = %q, want %q", parsedFirst.Issuer, refreshIssuer)
	}
}

// TestManagerRejectsInvalidTokens covers wrong secret, garbage, expiry and
// access/refresh confusion.
func TestManagerRejectsInvalidTokens(t *testing.T) {
	env := newTestEnv(nil)

	refresh, err := env.mgr.GenerateRefreshToken("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error = %v", err)
	}
	access, err := env.mgr.Generate("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	otherSecret, err := NewManager("other-secret", 1, 24).Generate("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("Generate() with other secret error = %v", err)
	}

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"refresh token used as access", refresh},
		{"garbage", "not.a.token"},
		{"empty", ""},
		{"wrong secret", otherSecret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := env.mgr.ValidateAccess(tc.token); err == nil {
				t.Error("ValidateAccess() error = nil, want failure")
			}
		})
	}

	if _, err := env.mgr.ValidateRefresh(access); err == nil {
		t.Error("ValidateRefresh(access token) error = nil, want failure")
	}

	expired, err := NewManager("test-secret", -1, 24).Generate("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("Generate() expired token error = %v", err)
	}
	if _, err := env.mgr.ValidateAccess(expired); err == nil {
		t.Error("ValidateAccess(expired) error = nil, want failure")
	}
}

// --- Register ---

// TestRegisterValidation pins the accepted and rejected registration inputs.
func TestRegisterValidation(t *testing.T) {
	tests := []struct {
		name     string
		req      *RegisterRequest
		existing map[string]*User
		wantErr  string
	}{
		{"valid", &RegisterRequest{Email: "user@test.com", Password: "supersecret"}, nil, ""},
		{"blank email", &RegisterRequest{Email: "   ", Password: "supersecret"}, nil, "valid email address"},
		{"email without @", &RegisterRequest{Email: "not-an-email", Password: "supersecret"}, nil, "valid email address"},
		{"dotless domain", &RegisterRequest{Email: "user@localhost", Password: "supersecret"}, nil, "valid email address"},
		{"password too short", &RegisterRequest{Email: "user@test.com", Password: "short"}, nil, "between 8 and 72"},
		{"password too long", &RegisterRequest{Email: "user@test.com", Password: strings.Repeat("a", 73)}, nil, "between 8 and 72"},
		{"duplicate", &RegisterRequest{Email: "user@test.com", Password: "supersecret"}, map[string]*User{
			"user@test.com": {ID: "user-1", Email: "user@test.com"},
		}, "already exists"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubUserRepository{usersByEmail: tc.existing}
			svc := NewAuthService(repo, &stubRefreshTokenRepository{hashes: map[string]string{}}, NewManager("test-secret", 1, 24))

			resp, err := svc.Register(context.Background(), tc.req)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Register() error = %v, want success", err)
				}
				if resp == nil || !resp.Success || resp.Person == nil {
					t.Fatalf("response = %+v, want success with a person", resp)
				}
				if !repo.createCalled {
					t.Fatal("Create() was not called")
				}
				return
			}

			if err == nil {
				t.Fatalf("Register() error = nil, want %q", tc.wantErr)
			}
			var ve *validationError
			if !errors.As(err, &ve) {
				t.Fatalf("Register() error = %v, want *validationError", err)
			}
			if !strings.Contains(ve.message, tc.wantErr) {
				t.Errorf("message = %q, want it to contain %q", ve.message, tc.wantErr)
			}
			if repo.createCalled {
				t.Error("Create() must not run on validation failure")
			}
		})
	}
}

// TestRegisterNormalizesEmailAndHashesPassword proves storage never sees the
// raw password and that the email is trimmed and lowercased.
func TestRegisterNormalizesEmailAndHashesPassword(t *testing.T) {
	repo := &stubUserRepository{}
	svc := NewAuthService(repo, &stubRefreshTokenRepository{hashes: map[string]string{}}, NewManager("test-secret", 1, 24))

	const password = "supersecret"
	resp, err := svc.Register(context.Background(), &RegisterRequest{Email: "  USER@Test.COM  ", Password: password})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if repo.createdEmail != "user@test.com" {
		t.Errorf("created email = %q, want user@test.com", repo.createdEmail)
	}
	if repo.createdHashed == password {
		t.Fatal("storage received the plaintext password")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(repo.createdHashed), []byte(password)); err != nil {
		t.Errorf("stored value is not a bcrypt hash of the input: %v", err)
	}
	if !slices.Equal(resp.Person.Roles, []string{"user"}) {
		t.Errorf("roles = %v, want [user]", resp.Person.Roles)
	}
}

// TestRegisterMapsUniqueViolation proves a concurrent insert becomes a 422
// validation error rather than a 500.
func TestRegisterMapsUniqueViolation(t *testing.T) {
	repo := &stubUserRepository{createErr: fmt.Errorf("userRepo.Create: %w", errEmailTaken)}
	svc := NewAuthService(repo, &stubRefreshTokenRepository{hashes: map[string]string{}}, NewManager("test-secret", 1, 24))

	_, err := svc.Register(context.Background(), &RegisterRequest{Email: "user@test.com", Password: "supersecret"})
	var ve *validationError
	if !errors.As(err, &ve) {
		t.Fatalf("Register() error = %v, want *validationError", err)
	}
	if !strings.Contains(ve.message, "already exists") {
		t.Errorf("message = %q, want an already-exists message", ve.message)
	}
}

// --- Login ---

// TestLoginFailuresAreIdentical is the anti-enumeration guarantee.
func TestLoginFailuresAreIdentical(t *testing.T) {
	const password = "supersecret"
	env := newTestEnv(map[string]*User{
		"user@test.com": {ID: "user-1", Email: "user@test.com", PasswordHash: hashPasswordForTest(t, password), Roles: []string{"user"}},
	})

	ctx := context.Background()
	_, _, errUnknown := env.svc.Login(ctx, &LoginRequest{Email: "nobody@test.com", Password: password})
	_, _, errWrong := env.svc.Login(ctx, &LoginRequest{Email: "user@test.com", Password: "wrong-password"})

	if errUnknown == nil || errWrong == nil {
		t.Fatalf("expected both logins to fail, got %v / %v", errUnknown, errWrong)
	}
	if errUnknown.Error() != errWrong.Error() {
		t.Errorf("errors differ: %q vs %q", errUnknown, errWrong)
	}
	if !strings.Contains(errUnknown.Error(), "invalid email or password") {
		t.Errorf("error = %q, want the generic credentials message", errUnknown)
	}
}

// TestLoginSuccessReturnsTokensAndPersistsHash covers the happy path.
func TestLoginSuccessReturnsTokensAndPersistsHash(t *testing.T) {
	const password = "supersecret"
	env := newTestEnv(map[string]*User{
		"user@test.com": {ID: "user-1", Email: "user@test.com", PasswordHash: hashPasswordForTest(t, password), Roles: []string{"user"}},
	})

	resp, refreshToken, err := env.svc.Login(context.Background(), &LoginRequest{Email: "User@Test.com", Password: password})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if resp.TokenType != "Bearer" || resp.AccessToken == "" || resp.User == nil {
		t.Fatalf("response = %+v, want an access token and user", resp)
	}

	claims, err := env.mgr.ValidateAccess(resp.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccess() error = %v", err)
	}
	if claims.UserID != "user-1" {
		t.Errorf("claims user = %q, want user-1", claims.UserID)
	}
	if _, err := env.mgr.ValidateRefresh(refreshToken); err != nil {
		t.Fatalf("refresh token is invalid: %v", err)
	}

	if _, ok := env.tokens.hashes[hashToken(refreshToken)]; !ok {
		t.Fatal("token store does not contain the refresh-token hash")
	}
	if _, ok := env.tokens.hashes[refreshToken]; ok {
		t.Fatal("token store contains the raw refresh token")
	}
}

// TestLoginRejectsAccountWithoutHash proves a corrupt row cannot log in.
func TestLoginRejectsAccountWithoutHash(t *testing.T) {
	env := newTestEnv(map[string]*User{
		"nohash@test.com": {ID: "user-2", Email: "nohash@test.com", Roles: []string{"user"}},
	})

	_, _, err := env.svc.Login(context.Background(), &LoginRequest{Email: "nohash@test.com", Password: "supersecret"})
	if !errors.Is(err, errInvalidCredentials) {
		t.Fatalf("Login() error = %v, want errInvalidCredentials", err)
	}
}

// --- Refresh ---

// TestRefreshRotationIsSingleUse proves the consumed token cannot be replayed
// while the rotated token keeps working.
func TestRefreshRotationIsSingleUse(t *testing.T) {
	const password = "supersecret"
	env := newTestEnv(map[string]*User{
		"user@test.com": {ID: "user-1", Email: "user@test.com", PasswordHash: hashPasswordForTest(t, password), Roles: []string{"user"}},
	})

	ctx := context.Background()
	_, refreshToken, err := env.svc.Login(ctx, &LoginRequest{Email: "user@test.com", Password: password})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	resp, rotated, err := env.svc.Refresh(ctx, refreshToken)
	if err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	if _, err := env.mgr.ValidateAccess(resp.AccessToken); err != nil {
		t.Errorf("new access token is invalid: %v", err)
	}
	if _, err := env.mgr.ValidateRefresh(rotated); err != nil {
		t.Errorf("new refresh token is invalid: %v", err)
	}

	if _, _, err := env.svc.Refresh(ctx, refreshToken); !errors.Is(err, errTokenRevoked) {
		t.Fatalf("replayed Refresh() error = %v, want errTokenRevoked", err)
	}
	if _, _, err := env.svc.Refresh(ctx, rotated); err != nil {
		t.Fatalf("rotated Refresh() error = %v, want success", err)
	}
}

// TestRefreshRejectsAccessToken proves the issuer check separates token kinds.
func TestRefreshRejectsAccessToken(t *testing.T) {
	env := newTestEnv(nil)

	access, err := env.mgr.Generate("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if _, _, err := env.svc.Refresh(context.Background(), access); !errors.Is(err, errInvalidToken) {
		t.Fatalf("Refresh(access) error = %v, want errInvalidToken", err)
	}
}

// TestRefreshFailsWhenUserGone proves a valid token cannot outlive its user.
func TestRefreshFailsWhenUserGone(t *testing.T) {
	env := newTestEnv(nil)

	refreshToken, err := env.mgr.GenerateRefreshToken("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error = %v", err)
	}
	env.tokens.hashes[hashToken(refreshToken)] = "user-1"

	if _, _, err := env.svc.Refresh(context.Background(), refreshToken); !errors.Is(err, errUserNotFound) {
		t.Fatalf("Refresh() error = %v, want errUserNotFound", err)
	}
}

// TestRefreshRejectsUnknownStoreRow proves the allowlist is required.
func TestRefreshRejectsUnknownStoreRow(t *testing.T) {
	env := newTestEnv(map[string]*User{
		"user@test.com": {ID: "user-1", Email: "user@test.com", Roles: []string{"user"}},
	})

	refreshToken, err := env.mgr.GenerateRefreshToken("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error = %v", err)
	}

	if _, _, err := env.svc.Refresh(context.Background(), refreshToken); !errors.Is(err, errTokenRevoked) {
		t.Fatalf("Refresh() error = %v, want errTokenRevoked", err)
	}
}

// --- Logout ---

// TestLogoutRevokesRefreshToken proves the token is truly dead afterward.
func TestLogoutRevokesRefreshToken(t *testing.T) {
	const password = "supersecret"
	env := newTestEnv(map[string]*User{
		"user@test.com": {ID: "user-1", Email: "user@test.com", PasswordHash: hashPasswordForTest(t, password), Roles: []string{"user"}},
	})

	ctx := context.Background()
	_, refreshToken, err := env.svc.Login(ctx, &LoginRequest{Email: "user@test.com", Password: password})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if err := env.svc.Logout(ctx, refreshToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, ok := env.tokens.hashes[hashToken(refreshToken)]; ok {
		t.Fatal("logout left the token hash in the store")
	}
	if _, _, err := env.svc.Refresh(ctx, refreshToken); !errors.Is(err, errTokenRevoked) {
		t.Fatalf("Refresh() after logout error = %v, want errTokenRevoked", err)
	}
}

// TestLogoutIsIdempotent proves logout succeeds with no row and rejects garbage.
func TestLogoutIsIdempotent(t *testing.T) {
	env := newTestEnv(nil)

	refreshToken, err := env.mgr.GenerateRefreshToken("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error = %v", err)
	}

	if err := env.svc.Logout(context.Background(), refreshToken); err != nil {
		t.Errorf("Logout() with no store row error = %v, want nil", err)
	}
	if err := env.svc.Logout(context.Background(), ""); err != nil {
		t.Errorf("Logout(empty) error = %v, want nil", err)
	}
	if err := env.svc.Logout(context.Background(), "garbage"); !errors.Is(err, errInvalidToken) {
		t.Errorf("Logout(garbage) error = %v, want errInvalidToken", err)
	}
}

// --- Handlers ---

// TestHandlerRegisterEndpoint covers 201, 422 and 400 for register.
func TestHandlerRegisterEndpoint(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		env := newTestEnv(nil)
		rec := postJSON(t, env.handler.Register, "/auth/register", RegisterRequest{Email: "user@test.com", Password: "supersecret"})

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, "supersecret") {
			t.Error("response body contains the plaintext password")
		}
		if strings.Contains(body, "$2a$") || strings.Contains(body, "$2b$") {
			t.Error("response body contains a bcrypt hash")
		}

		var resp RegistrationResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !resp.Success || resp.Person == nil || resp.Person.Email != "user@test.com" {
			t.Errorf("response = %+v, want success with the user", resp)
		}
	})

	t.Run("validation", func(t *testing.T) {
		env := newTestEnv(nil)
		rec := postJSON(t, env.handler.Register, "/auth/register", RegisterRequest{Email: "user@test.com", Password: "short"})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		env := newTestEnv(nil)
		req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader("{not json"))
		rec := httptest.NewRecorder()
		env.handler.Register(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// TestHandlerLoginEndpoint covers the cookie attributes and identical failures.
func TestHandlerLoginEndpoint(t *testing.T) {
	const password = "supersecret"
	newEnv := func(t *testing.T) *testEnv {
		return newTestEnv(map[string]*User{
			"user@test.com": {ID: "user-1", Email: "user@test.com", PasswordHash: hashPasswordForTest(t, password), Roles: []string{"user"}},
		})
	}

	t.Run("success sets the refresh cookie", func(t *testing.T) {
		env := newEnv(t)
		rec := postJSON(t, env.handler.Login, "/auth/login", LoginRequest{Email: "User@Test.com", Password: password})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}

		var resp LoginResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.AccessToken == "" || resp.TokenType != "Bearer" || resp.User == nil {
			t.Errorf("response = %+v, want an access token and user", resp)
		}

		cookie := findCookie(rec.Result().Cookies(), refreshCookieName)
		if cookie == nil || cookie.Value == "" {
			t.Fatal("refresh cookie was not set")
		}
		// Secure must be true by default: a refresh token sent over plain HTTP
		// is readable by anyone on the network path.
		if !cookie.HttpOnly || !cookie.Secure {
			t.Errorf("cookie HttpOnly=%v Secure=%v, want true / true", cookie.HttpOnly, cookie.Secure)
		}
		if cookie.Path != refreshCookiePath || cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie Path=%q SameSite=%v, want %q / Lax", cookie.Path, cookie.SameSite, refreshCookiePath)
		}
		if want := int(env.mgr.RefreshExpiry().Seconds()); cookie.MaxAge != want {
			t.Errorf("cookie MaxAge = %d, want %d", cookie.MaxAge, want)
		}
	})

	t.Run("failures are byte-identical", func(t *testing.T) {
		env := newEnv(t)
		unknown := postJSON(t, env.handler.Login, "/auth/login", LoginRequest{Email: "nobody@test.com", Password: password})
		wrong := postJSON(t, env.handler.Login, "/auth/login", LoginRequest{Email: "user@test.com", Password: "wrong-password"})

		if unknown.Code != http.StatusUnauthorized || wrong.Code != http.StatusUnauthorized {
			t.Fatalf("status codes = %d and %d, want 401", unknown.Code, wrong.Code)
		}
		if unknown.Body.String() != wrong.Body.String() {
			t.Fatalf("bodies differ:\n unknown: %s\n wrong:   %s", unknown.Body.String(), wrong.Body.String())
		}
	})
}

// TestHandlerRefreshEndpoint covers missing, valid and replayed cookies.
func TestHandlerRefreshEndpoint(t *testing.T) {
	const password = "supersecret"
	newEnv := func(t *testing.T) *testEnv {
		return newTestEnv(map[string]*User{
			"user@test.com": {ID: "user-1", Email: "user@test.com", PasswordHash: hashPasswordForTest(t, password), Roles: []string{"user"}},
		})
	}

	t.Run("missing cookie", func(t *testing.T) {
		env := newEnv(t)
		rec := httptest.NewRecorder()
		env.handler.Refresh(rec, httptest.NewRequest(http.MethodPost, "/auth/refresh", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("valid cookie rotates", func(t *testing.T) {
		env := newEnv(t)
		login := postJSON(t, env.handler.Login, "/auth/login", LoginRequest{Email: "user@test.com", Password: password})
		loginCookie := findCookie(login.Result().Cookies(), refreshCookieName)
		if loginCookie == nil {
			t.Fatal("login did not set a refresh cookie")
		}

		req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
		req.AddCookie(loginCookie)
		rec := httptest.NewRecorder()
		env.handler.Refresh(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var resp RefreshResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if _, err := env.mgr.ValidateAccess(resp.AccessToken); err != nil {
			t.Errorf("new access token is invalid: %v", err)
		}

		rotated := findCookie(rec.Result().Cookies(), refreshCookieName)
		if rotated == nil || rotated.Value == "" {
			t.Fatal("refresh did not set a rotated cookie")
		}
		if rotated.Value == loginCookie.Value {
			t.Error("refresh reused the old refresh token")
		}

		// Replaying the consumed cookie must fail and clear the browser cookie.
		replayReq := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
		replayReq.AddCookie(loginCookie)
		replayRec := httptest.NewRecorder()
		env.handler.Refresh(replayRec, replayReq)
		if replayRec.Code != http.StatusUnauthorized {
			t.Fatalf("replay status = %d, want 401", replayRec.Code)
		}
		if cleared := findCookie(replayRec.Result().Cookies(), refreshCookieName); cleared == nil || cleared.MaxAge >= 0 {
			t.Error("replay did not clear the refresh cookie")
		}
	})
}

// TestHandlerLogoutEndpoint proves logout is always 200 and clears the cookie.
func TestHandlerLogoutEndpoint(t *testing.T) {
	const password = "supersecret"
	env := newTestEnv(map[string]*User{
		"user@test.com": {ID: "user-1", Email: "user@test.com", PasswordHash: hashPasswordForTest(t, password), Roles: []string{"user"}},
	})

	login := postJSON(t, env.handler.Login, "/auth/login", LoginRequest{Email: "user@test.com", Password: password})
	loginCookie := findCookie(login.Result().Cookies(), refreshCookieName)
	if loginCookie == nil {
		t.Fatal("login did not set a refresh cookie")
	}

	t.Run("with cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		req.AddCookie(loginCookie)
		rec := httptest.NewRecorder()
		env.handler.Logout(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		cleared := findCookie(rec.Result().Cookies(), refreshCookieName)
		if cleared == nil || cleared.MaxAge >= 0 {
			t.Error("logout did not clear the refresh cookie")
		}
		if _, ok := env.tokens.hashes[hashToken(loginCookie.Value)]; ok {
			t.Error("logout left the token hash in the store")
		}
	})

	t.Run("without cookie", func(t *testing.T) {
		rec := httptest.NewRecorder()
		env.handler.Logout(rec, httptest.NewRequest(http.MethodPost, "/auth/logout", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if cleared := findCookie(rec.Result().Cookies(), refreshCookieName); cleared == nil || cleared.MaxAge >= 0 {
			t.Error("logout without a cookie did not clear it")
		}
	})
}

// --- Middleware ---

// TestMiddlewareRequiresBearerToken covers the header parsing paths.
func TestMiddlewareRequiresBearerToken(t *testing.T) {
	env := newTestEnv(nil)
	token, err := env.mgr.Generate("user-1", "user@test.com", []string{"user", "admin"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil {
			t.Error("claims missing from context")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if claims.UserID != "user-1" || !slices.Equal(claims.Roles, []string{"user", "admin"}) {
			t.Errorf("claims = %+v, want user-1 with [user admin]", claims)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	protected := env.mgr.Middleware(next)

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong scheme", "Token abc", http.StatusUnauthorized},
		{"empty token", "Bearer   ", http.StatusUnauthorized},
		{"bad token", "Bearer not-a-token", http.StatusUnauthorized},
		{"valid", "Bearer " + token, http.StatusNoContent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/products", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}

	if ClaimsFromContext(context.Background()) != nil {
		t.Error("ClaimsFromContext() on a bare context = non-nil, want nil")
	}
}

// TestRequireRoleChecksMembership covers the roles-array adaptation.
func TestRequireRoleChecksMembership(t *testing.T) {
	env := newTestEnv(nil)
	token, err := env.mgr.Generate("user-1", "user@test.com", []string{"user", "admin"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	t.Run("matching role", func(t *testing.T) {
		protected := env.mgr.Middleware(env.mgr.RequireRole("admin", next))
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("missing role", func(t *testing.T) {
		protected := env.mgr.Middleware(env.mgr.RequireRole("superuser", next))
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("no claims", func(t *testing.T) {
		rec := httptest.NewRecorder()
		env.mgr.RequireRole("admin", next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
}

// TestCookieMiddleware covers the optional refresh-cookie authentication path.
func TestCookieMiddleware(t *testing.T) {
	env := newTestEnv(nil)
	refreshToken, err := env.mgr.GenerateRefreshToken("user-1", "user@test.com", []string{"user"})
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error = %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ClaimsFromContext(r.Context()) == nil {
			t.Error("claims missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	protected := env.mgr.CookieMiddleware(true)(next)

	t.Run("missing cookie", func(t *testing.T) {
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/page", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("valid cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/page", nil)
		req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: refreshToken})
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("invalid cookie clears it", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/page", nil)
		req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "garbage"})
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if cleared := findCookie(rec.Result().Cookies(), refreshCookieName); cleared == nil || cleared.MaxAge >= 0 {
			t.Error("invalid cookie was not cleared")
		}
	})
}
