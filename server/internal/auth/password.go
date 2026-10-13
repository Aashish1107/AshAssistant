// Package auth handles passwords. Sessions are added in Phase 1, step 3.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// MinPasswordLength is the shortest password `ashcloud user create` accepts.
const MinPasswordLength = 12

// MaxPasswordLength stops absurdly long inputs from being hashed.
const MaxPasswordLength = 1024

// argon2id settings: RFC 9106's second recommended option (64 MiB, 3 passes,
// 4 lanes). Hashing takes about 0.1 s, which slows down password guessing
// without making logins feel slow.
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 4
	argonKeyLen    = 32
	saltLen        = 16
)

// ErrInvalidHash means a stored password hash is not one this package wrote.
var ErrInvalidHash = errors.New("invalid password hash")

// CheckPasswordRules returns a readable error if password is too short or too long.
func CheckPasswordRules(password string) error {
	length := utf8.RuneCountInString(password)
	if length < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if length > MaxPasswordLength {
		return fmt.Errorf("password must be at most %d characters", MaxPasswordLength)
	}
	return nil
}

// HashPassword returns an argon2id hash of password in the standard PHC
// format, e.g. $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>.
func HashPassword(password string) string {
	salt := make([]byte, saltLen)
	rand.Read(salt) // never fails: since Go 1.24 it crashes the program instead
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

// VerifyPassword reports whether password matches a hash from HashPassword.
// It reads the settings from the hash itself, so older hashes keep working if
// the settings above change.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, ErrInvalidHash
	}
	// Refuse settings that would make a single check take minutes or crash.
	if memory == 0 || memory > 4*1024*1024 || time == 0 || time > 100 || threads == 0 {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrInvalidHash
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
