// Package advancedtrust imports and owns the local, Owner-approved trust roots
// used by offline Advanced capability verification.
package advancedtrust

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/kombifyio/stackkits/internal/advancedcapability"

	wire "github.com/kombifyio/stackkits/contracts/advancedcapability"
)

const (
	BundleSchemaVersion = "stackkit.advanced-trust-bundle/v1"
	maxDocumentBytes    = 64 * 1024
	maxTrustKeys        = 64
)

var (
	issuerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	keyIDPattern    = regexp.MustCompile(`^ed25519://sha256/[0-9a-f]{64}$`)
	sha256Pin       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type decodedBundle struct {
	keys      []wire.TrustBundleKey
	canonical []byte
	digest    string
}

// Decode strictly decodes the canonical public trust-bundle wire contract.
// The returned bundle is detached from raw and contains public verification
// keys only.
func Decode(raw []byte) (advancedcapability.TrustBundle, error) {
	decoded, err := decodeBundle(raw)
	if err != nil {
		return advancedcapability.TrustBundle{}, err
	}
	return decoded.verifierBundle(), nil
}

func decodeBundle(raw []byte) (decodedBundle, error) {
	if len(raw) == 0 {
		return decodedBundle{}, errors.New("advancedtrust: trust bundle is required")
	}
	if len(raw) > maxDocumentBytes {
		return decodedBundle{}, errors.New("advancedtrust: trust bundle exceeds 65536 bytes")
	}
	if !utf8.Valid(raw) {
		return decodedBundle{}, errors.New("advancedtrust: trust bundle must be valid UTF-8 JSON")
	}
	fields, err := decodeExactObject(raw, map[string]struct{}{
		"keys": {}, "schemaVersion": {},
	})
	if err != nil {
		return decodedBundle{}, fmt.Errorf("advancedtrust: malformed trust bundle: %w", err)
	}
	schemaVersion, err := strictString(fields["schemaVersion"])
	if err != nil || schemaVersion != BundleSchemaVersion {
		return decodedBundle{}, errors.New("advancedtrust: unsupported trust bundle schemaVersion")
	}
	keys, err := decodeKeys(fields["keys"])
	if err != nil {
		return decodedBundle{}, err
	}
	document := wire.TrustBundle{SchemaVersion: BundleSchemaVersion, Keys: keys}
	canonical := document.Canonical()
	if !bytes.Equal(raw, canonical) {
		return decodedBundle{}, errors.New("advancedtrust: trust bundle must be exact canonical JSON")
	}
	return decodedBundle{
		keys:      keys,
		canonical: canonical,
		digest:    document.Digest(),
	}, nil
}

func decodeKeys(raw []byte) ([]wire.TrustBundleKey, error) {
	var keys []wire.TrustBundleKey
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, errors.New("advancedtrust: keys must be a JSON array")
	}
	if len(keys) < 1 || len(keys) > maxTrustKeys {
		return nil, errors.New("advancedtrust: keys must contain 1 to 64 entries")
	}
	seen := make(map[string]struct{}, len(keys))
	for index, key := range keys {
		if _, err := key.RawPublicKey(); err != nil {
			return nil, fmt.Errorf("advancedtrust: key %d: %w", index, err)
		}
		identity := key.IssuerID + "\x00" + key.KeyID
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("advancedtrust: key %d duplicates an issuer/keyId binding", index)
		}
		seen[identity] = struct{}{}
	}
	return keys, nil
}

func (bundle decodedBundle) verifierBundle() advancedcapability.TrustBundle {
	keys := make([]advancedcapability.TrustedKey, 0, len(bundle.keys))
	for _, key := range bundle.keys {
		publicKey, _ := base64.RawStdEncoding.DecodeString(key.PublicKey)
		keys = append(keys, advancedcapability.TrustedKey{
			IssuerID: key.IssuerID,
			KeyID:    key.KeyID,
			PublicKey: ed25519.PublicKey(
				slices.Clone(publicKey),
			),
		})
	}
	return advancedcapability.TrustBundle{
		SchemaVersion: advancedcapability.TrustBundleSchemaVersion,
		Keys:          keys,
	}
}

func decodeExactObject(raw []byte, expected map[string]struct{}) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("must be one JSON object")
	}
	fields := make(map[string]json.RawMessage, len(expected))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("contains a non-string field name")
		}
		if _, known := expected[name]; !known {
			return nil, fmt.Errorf("unknown field %q", name)
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, fmt.Errorf("duplicate field %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	for field := range expected {
		if _, present := fields[field]; !present {
			return nil, fmt.Errorf("missing field %q", field)
		}
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("trailing JSON token %v", token)
	}
	return fields, nil
}

func strictString(raw []byte) (string, error) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", errors.New("must be a JSON string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}
