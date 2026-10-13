package ids

import (
	"regexp"
	"strings"
	"testing"
)

var nanoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{21}$`)

func TestNewLooksLikeANanoID(t *testing.T) {
	for range 1000 {
		if id := New(); !nanoIDPattern.MatchString(id) {
			t.Fatalf("New() = %q, want 21 characters from A-Za-z0-9_-", id)
		}
	}
}

func TestAlphabetIs64DistinctURLSafeSymbols(t *testing.T) {
	if len(alphabet) != 64 {
		t.Fatalf("alphabet has %d symbols, want 64", len(alphabet))
	}
	for i, r := range alphabet {
		if strings.ContainsRune(alphabet[i+1:], r) {
			t.Errorf("%q appears twice", r)
		}
	}
}

func TestNewIsUnique(t *testing.T) {
	seen := make(map[string]bool, 100_000)
	for range 100_000 {
		id := New()
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestEverySymbolIsUsedEvenly(t *testing.T) {
	counts := map[rune]int{}
	const ids = 20_000
	for range ids {
		for _, r := range New() {
			counts[r]++
		}
	}
	expected := ids * Size / 64 // ~6560 per symbol
	for _, r := range alphabet {
		if c := counts[r]; c < expected*8/10 || c > expected*12/10 {
			t.Errorf("symbol %q appeared %d times, want about %d", r, c, expected)
		}
	}
}
