package kitio

import (
	"crypto/sha256"
	"encoding/hex"
)

// hashSHA256 is the test helper for canonical_cross_lang_test.go.
// Production hashing lives in CanonicalHash (which calls canonicalJSON
// then SHA256). We duplicate the SHA256 step here only because the test
// also needs to hash the fixture-as-decoded-map directly (without going
// through the KitDefinition struct).
func hashSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
