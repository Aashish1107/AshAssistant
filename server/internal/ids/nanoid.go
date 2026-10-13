// Package ids makes random, URL-safe identifiers.
package ids

import "crypto/rand"

// alphabet is Nano ID's default (the npm "nanoid" package's urlAlphabet):
// A-Z, a-z, 0-9, "_" and "-". 64 symbols, so each random byte maps to exactly
// one symbol with no bias.
const alphabet = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict"

// Size matches Nano ID's default length: 21 symbols carry 126 random bits,
// about as collision-resistant as a UUID v4.
const Size = 21

// New returns a random Nano ID such as "V1StGXR8_Z5jdHi6B-myT".
func New() string {
	random := make([]byte, Size)
	rand.Read(random) // never fails: since Go 1.24 it crashes the program instead
	id := make([]byte, Size)
	for i, b := range random {
		id[i] = alphabet[b&63]
	}
	return string(id)
}
