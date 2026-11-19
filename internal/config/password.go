package config

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
)

// Tags of the errors of the password.
const (
	// TagPasswordTooShort: a password of fewer than MinPasswordLength
	// characters.
	TagPasswordTooShort = "PasswordTooShort"
	// TagPasswordHashFailed: the hash could not be made.
	TagPasswordHashFailed = "PasswordHashFailed"
	// TagPasswordHashInvalid: a stored hash whose parameters are out of
	// range.
	TagPasswordHashInvalid = "PasswordHashInvalid"
)

// The password and its hash (SPEC §8.2).
const (
	// MinPasswordLength is the fewest characters a password has.
	MinPasswordLength = 8
	// PasswordIterations is the PBKDF2 iteration count of a new hash: what
	// OWASP gives for PBKDF2-HMAC-SHA256.
	PasswordIterations = 600_000
	// The salt and the hash, in bytes.
	saltSize = 16
	hashSize = 32
	// maxIterations bounds the count a stored hash may ask for, so a
	// damaged file cannot make each login take minutes.
	maxIterations = 10_000_000
)

// PasswordHash is the stored form of the password: PBKDF2-HMAC-SHA256 with
// a random salt.
type PasswordHash struct {
	Salt       []byte `json:"salt"`
	Iterations int    `json:"iterations"`
	Hash       []byte `json:"hash"`
}

// HashPassword hashes password, which has at least MinPasswordLength
// characters.
func HashPassword(password string) (*PasswordHash, error) {
	return hashPassword(password, PasswordIterations)
}

// hashPassword hashes password with the iteration count.
func hashPassword(password string, iterations int) (*PasswordHash, error) {
	if n := utf8.RuneCountInString(password); n < MinPasswordLength {
		return nil, errors.New(TagPasswordTooShort, "the password is too short", "characters", n, "min", MinPasswordLength)
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, errors.NewErr(TagPasswordHashFailed, err)
	}
	h, err := pbkdf2.Key(sha256.New, password, salt, iterations, hashSize)
	if err != nil {
		return nil, errors.NewErr(TagPasswordHashFailed, err)
	}
	return &PasswordHash{Salt: salt, Iterations: iterations, Hash: h}, nil
}

// check says whether the hash's parameters are in range.
func (p *PasswordHash) check() error {
	if len(p.Salt) < 8 || len(p.Hash) != hashSize || p.Iterations < 1 || p.Iterations > maxIterations {
		return errors.New(TagPasswordHashInvalid, "the stored password hash is damaged",
			"salt", len(p.Salt), "hash", len(p.Hash), "iterations", p.Iterations)
	}
	return nil
}

// Verify says whether password is the one hashed, in time that does not
// depend on how much of it matches.
func (p *PasswordHash) Verify(password string) bool {
	if p == nil || p.check() != nil {
		return false
	}
	h, err := pbkdf2.Key(sha256.New, password, p.Salt, p.Iterations, hashSize)
	return err == nil && subtle.ConstantTimeCompare(h, p.Hash) == 1
}

// CheckLogin says whether user and password are the ones of s, comparing
// the user name in constant time too. Settings without a password let no
// one in.
func (s Settings) CheckLogin(user, password string) bool {
	if !s.Ready() {
		return false
	}
	u, want := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(s.User))
	userOK := subtle.ConstantTimeCompare(u[:], want[:]) == 1
	// The password is checked even for a wrong user, so the time does not
	// tell which one was wrong.
	passOK := s.Password.Verify(password)
	return userOK && passOK
}
