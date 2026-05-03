package cue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalJSON returns a deterministic JSON encoding of v with sorted map
// keys, no indentation, and stable array ordering for primitive-only arrays.
// This is the canonical form used to compute module contract hashes.
func CanonicalJSON(v interface{}) ([]byte, error) {
	normalized, err := normalize(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

// ContractHash computes the SHA256 of the canonical-JSON encoding of v.
// Return is the 64-character lowercase hex string stored in
// sk_module_version.contract_hash.
func ContractHash(v interface{}) (string, error) {
	b, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// normalize walks v and returns a structure that marshals deterministically.
// Maps become sorted-key maps (encoded as json.RawMessage order via slice of
// key/value pairs if needed). json.Marshal already sorts map[string]T keys,
// so we only need to recursively normalize nested values.
func normalize(v interface{}) (interface{}, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case map[string]interface{}:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]interface{}, len(x))
		for _, k := range keys {
			n, err := normalize(x[k])
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, it := range x {
			n, err := normalize(it)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	case string, bool, float64, json.Number:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	}
	// Fall back: round-trip through JSON to normalize structs/slices.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonical json marshal: %w", err)
	}
	var any interface{}
	if err := json.Unmarshal(raw, &any); err != nil {
		return nil, fmt.Errorf("canonical json unmarshal: %w", err)
	}
	return normalize(any)
}
