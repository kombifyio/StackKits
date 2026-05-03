// Package crypto provides cryptographic helpers used by stackkit
// for password generation, hashing, and bundle encryption.
package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// RandomPassword returns a base64-encoded random password derived from
// `byteLen` random bytes. byteLen must be at least 16.
func RandomPassword(byteLen int) (string, error) {
	if byteLen < 16 {
		return "", fmt.Errorf("byteLen must be >= 16, got %d", byteLen)
	}
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(buf), nil
}
