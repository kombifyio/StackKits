package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"hash"
)

// hmacNew is a thin alias used by tests.
func hmacNew(key []byte) hash.Hash {
	return hmac.New(sha256.New, key)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
