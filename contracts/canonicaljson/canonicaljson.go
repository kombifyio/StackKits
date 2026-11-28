// Package canonicaljson preserves the encoding/json wire format used by Guard
// receipts and computes their domain-separated SHA-256 digests.
package canonicaljson

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalJSON uses json.Marshal unchanged: struct field order, sorted map
// keys, HTML escaping and no trailing newline. It does not normalize structs
// through maps or implement RFC 8785. Callers retain their wire types and tags.
func CanonicalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}

// SHA256Hex hashes parts in order without concatenating them. Callers supply
// any domain prefix and separators; no bytes are inserted between parts.
func SHA256Hex(parts ...[]byte) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write(part)
	}
	var sum [sha256.Size]byte
	return "sha256:" + hex.EncodeToString(h.Sum(sum[:0]))
}
