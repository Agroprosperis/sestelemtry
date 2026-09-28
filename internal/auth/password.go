package auth

import (
	"fmt"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const (
	// MinPasswordLen is counted in characters, not bytes, so a Cyrillic
	// password isn't accepted at half the intended length.
	MinPasswordLen = 10
	// bcrypt reads only the first 72 bytes; a longer password would
	// silently share its hash with every password of the same prefix.
	maxPasswordBytes = 72
	bcryptCost       = 12
)

var (
	ErrPasswordTooShort = fmt.Errorf("пароль має містити щонайменше %d символів", MinPasswordLen)
	ErrPasswordTooLong  = fmt.Errorf("пароль не може бути довшим за %d байтів", maxPasswordBytes)
)

// ValidatePassword enforces the length policy on a new password.
func ValidatePassword(password string) error {
	if len([]rune(password)) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	if len(password) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

// HashPassword validates and hashes a new password.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	return hash(password)
}

// HashTemporaryPassword hashes a password that has to be replaced at the
// first sign-in (the factory admin/admin), so the length policy does not
// apply to it.
func HashTemporaryPassword(password string) (string, error) {
	return hash(password)
}

func hash(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(h), nil
}

var (
	dummyOnce sync.Once
	dummyHash []byte
)

// CheckPassword reports whether password matches hash. An empty hash
// (unknown email, or an account without a local password) still pays
// for one bcrypt comparison, so response time doesn't reveal which
// emails exist. So does a password longer than any stored one could
// be: bcrypt compares only its first 72 bytes and would accept it.
func CheckPassword(hash, password string) bool {
	if hash == "" || len(password) > maxPasswordBytes {
		dummyOnce.Do(func() {
			dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer"), bcryptCost)
		})
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
