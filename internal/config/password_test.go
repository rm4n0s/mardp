package config

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"testing"
)

func TestHashPassword(t *testing.T) {
	p, err := HashPassword("a password")
	if err != nil {
		t.Fatal(err)
	}
	if p.Iterations != PasswordIterations || len(p.Salt) != saltSize || len(p.Hash) != hashSize {
		t.Errorf("hash %+v", p)
	}
	// It is PBKDF2-HMAC-SHA256 of the salt and count it keeps.
	want, err := pbkdf2.Key(sha256.New, "a password", p.Salt, p.Iterations, hashSize)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Hash, want) {
		t.Error("the hash is not PBKDF2-HMAC-SHA256")
	}
	if !p.Verify("a password") || p.Verify("a passworD") || p.Verify("") {
		t.Error("Verify")
	}
	q, err := HashPassword("a password")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(p.Salt, q.Salt) || bytes.Equal(p.Hash, q.Hash) {
		t.Error("two hashes of one password share their salt")
	}
}

func TestPasswordLength(t *testing.T) {
	tests := []struct {
		password string
		ok       bool
	}{
		{"", false},
		{"1234567", false},
		{"12345678", true},
		{"éééé", false},    // 8 bytes, 4 characters
		{"éééééééé", true}, // 8 characters
	}
	for _, tt := range tests {
		_, err := hashPassword(tt.password, 1000)
		if tt.ok && err != nil {
			t.Errorf("%q: %v", tt.password, err)
		}
		if !tt.ok {
			assertRoute(t, err, "hashPassword."+TagPasswordTooShort)
		}
	}
}

func TestVerifyDamaged(t *testing.T) {
	good, err := hashPassword("12345678", 1000)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		p    *PasswordHash
	}{
		{"nil", nil},
		{"no salt", &PasswordHash{Iterations: 1000, Hash: good.Hash}},
		{"short hash", &PasswordHash{Salt: good.Salt, Iterations: 1000, Hash: good.Hash[:5]}},
		{"no iterations", &PasswordHash{Salt: good.Salt, Hash: good.Hash}},
		{"too many iterations", &PasswordHash{Salt: good.Salt, Iterations: maxIterations + 1, Hash: good.Hash}},
	}
	for _, tt := range tests {
		if tt.p.Verify("12345678") {
			t.Errorf("%s: a damaged hash let the password in", tt.name)
		}
	}
}

func TestCheckLogin(t *testing.T) {
	p, err := hashPassword("correct horse", 1000)
	if err != nil {
		t.Fatal(err)
	}
	s := Defaults()
	s.User, s.Password = "manos", p
	noPassword := Defaults()
	noPassword.User = "manos"
	noUser := s
	noUser.User = ""
	tests := []struct {
		name           string
		s              Settings
		user, password string
		ok             bool
	}{
		{"right", s, "manos", "correct horse", true},
		{"wrong password", s, "manos", "correct horsE", false},
		{"wrong user", s, "Manos", "correct horse", false},
		{"empty", s, "", "", false},
		{"no password set", noPassword, "manos", "", false},
		{"no user set", noUser, "", "correct horse", false},
	}
	for _, tt := range tests {
		if got := tt.s.CheckLogin(tt.user, tt.password); got != tt.ok {
			t.Errorf("%s: CheckLogin = %v", tt.name, got)
		}
	}
}
