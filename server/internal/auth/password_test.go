package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashThenVerify(t *testing.T) {
	hash := HashPassword("correct horse battery staple")

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("right password: ok=%v err=%v, want true nil", ok, err)
	}
	ok, err = VerifyPassword("correct horse battery stapler", hash)
	if err != nil || ok {
		t.Fatalf("wrong password: ok=%v err=%v, want false nil", ok, err)
	}
}

func TestHashFormat(t *testing.T) {
	hash := HashPassword("correct horse battery staple")
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("hash = %q, want argon2id PHC format with m=65536,t=3,p=4", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Error("hash contains the password")
	}
}

func TestSamePasswordHashesDifferently(t *testing.T) {
	if HashPassword("same password here") == HashPassword("same password here") {
		t.Error("two hashes of one password are identical; the salt isn't random")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	good := HashPassword("correct horse battery staple")
	parts := strings.Split(good, "$")
	for name, hash := range map[string]string{
		"empty":           "",
		"not phc":         "plaintext",
		"wrong algorithm": strings.Replace(good, "argon2id", "argon2i", 1),
		"wrong version":   strings.Replace(good, "v=19", "v=16", 1),
		"bad params":      strings.Replace(good, "m=65536,t=3,p=4", "m=x,t=3,p=4", 1),
		"huge memory":     strings.Replace(good, "m=65536", "m=99999999", 1),
		"zero time":       strings.Replace(good, "t=3", "t=0", 1),
		"bad salt":        strings.Join([]string{"", parts[1], parts[2], parts[3], "!!!", parts[5]}, "$"),
		"empty key":       strings.Join([]string{"", parts[1], parts[2], parts[3], parts[4], ""}, "$"),
		"extra field":     good + "$extra",
	} {
		ok, err := VerifyPassword("correct horse battery staple", hash)
		if ok || !errors.Is(err, ErrInvalidHash) {
			t.Errorf("%s: ok=%v err=%v, want false ErrInvalidHash", name, ok, err)
		}
	}
}

func TestCheckPasswordRules(t *testing.T) {
	for password, wantOK := range map[string]bool{
		"short":                     false,
		"elevenchars":               false,
		"twelve chars":              true,
		"ünïcödé pässwörd":          true, // counted in characters, not bytes
		strings.Repeat("a", 1024):   true,
		strings.Repeat("a", 1025):   false,
		"            12 spaces    ": true,
	} {
		err := CheckPasswordRules(password)
		if (err == nil) != wantOK {
			t.Errorf("CheckPasswordRules(%.20q) = %v, want ok=%v", password, err, wantOK)
		}
	}
}
