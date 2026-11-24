package auth

import (
	"crypto/rand"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const (
	// bcryptCost is deliberately high enough to make offline cracking expensive.
	bcryptCost = 12
	// minPasswordBytes is the shortest accepted password.
	minPasswordBytes = 8
	// maxPasswordBytes is bcrypt's hard input ceiling; longer input is rejected,
	// never silently truncated.
	maxPasswordBytes = 72
)

// dummyHash returns a valid cost-12 bcrypt hash used when no account exists,
// so an unknown email costs roughly the same wall-clock time as a wrong
// password and does not leak account existence through timing.
var dummyHash = sync.OnceValue(func() string {
	h, err := bcrypt.GenerateFromPassword([]byte(rand.Text()), bcryptCost)
	if err != nil {
		return ""
	}
	return string(h)
})

// hashPassword returns a bcrypt hash of password at the configured cost.
func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// verifyPassword reports whether password matches a stored bcrypt hash. An
// empty hash can only come from a corrupt row, so it never passes.
func verifyPassword(hash, password string) bool {
	if hash == "" || len(password) > maxPasswordBytes {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
