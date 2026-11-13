package decisionreceipt

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// The published purpose keys are the platform's existing decision-receipt
// signer keys. The privileged refresher caches them as one snapshot in node
// custody (custody.go); a command reads that snapshot and never fetches a key
// from an address a receipt names, and never from the network.
const (
	// maxPurposeKeyWindow is the longest window a published snapshot may
	// carry. It is the window the privileged refresher admits and the RIL
	// proof verifier enforces (techstack/pkg/ril/nodeproof).
	maxPurposeKeyWindow = 45
)

// keyIDPattern is the key id shape the ledger publishes.
var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_:-]{1,128}$`)

// PurposeKey is one published public key of the decision-receipt purpose.
type PurposeKey struct {
	Kid    string   `json:"kid"`
	Kty    string   `json:"kty"`
	Crv    string   `json:"crv"`
	X      string   `json:"x"`
	Y      string   `json:"y,omitempty"`
	Alg    string   `json:"alg,omitempty"`
	Use    string   `json:"use,omitempty"`
	KeyOps []string `json:"key_ops,omitempty"`
	Ext    *bool    `json:"ext,omitempty"`
}

// KeySet is the published decision-receipt key set a consumer verifies with.
type KeySet struct {
	Keys []PurposeKey
}

type keySetDocument struct {
	Keys []PurposeKey `json:"keys"`
}

// purposeKeyDocument is the published snapshot, in exactly the shape the
// privileged refresher admits before it writes the file (nodeproof
// parseKeyFile, which refuses an unknown member). The reader is as strict as
// the writer, so a snapshot the writer would refuse is never trusted here.
type purposeKeyDocument struct {
	Generation      int64          `json:"generation"`
	IssuedAt        int64          `json:"issuedAt"`
	ValidUntil      int64          `json:"validUntil"`
	RILClaim        keySetDocument `json:"rilClaim"`
	RILExecution    keySetDocument `json:"rilExecution"`
	DecisionReceipt keySetDocument `json:"decisionReceipt"`
}

// loadPurposeKeys reads the node's purpose-key snapshot from custody and
// returns its decision-receipt key set. The snapshot must be inside its
// window: issuedAt <= now < validUntil, with a window of at most 45 seconds.
// A key the platform rotated out stops verifying when the refresh stops.
func (c custody) loadPurposeKeys(now time.Time) (*KeySet, error) {
	raw, err := c.readCustodyFile(c.keysPath)
	if err != nil {
		return nil, fmt.Errorf("purpose key snapshot unavailable: %w", err)
	}
	return parsePurposeKeys(raw, now)
}

// parsePurposeKeys checks one snapshot document and its window.
func parsePurposeKeys(raw []byte, now time.Time) (*KeySet, error) {
	var document purposeKeyDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("purpose key snapshot invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("purpose key snapshot carries trailing data")
	}
	if document.Generation < 1 || len(document.DecisionReceipt.Keys) == 0 || len(document.DecisionReceipt.Keys) > 100 {
		return nil, errors.New("purpose key snapshot invalid")
	}
	at := now.Unix()
	if document.IssuedAt > at || document.ValidUntil <= at || document.ValidUntil <= document.IssuedAt ||
		document.ValidUntil-document.IssuedAt > maxPurposeKeyWindow {
		return nil, errors.New("purpose key snapshot is outside its window")
	}
	seen := map[string]bool{}
	for _, key := range document.DecisionReceipt.Keys {
		if err := validatePurposeKey(key); err != nil {
			return nil, err
		}
		if seen[key.Kid] {
			return nil, errors.New("purpose key snapshot carries an ambiguous key id")
		}
		seen[key.Kid] = true
	}
	return &KeySet{Keys: document.DecisionReceipt.Keys}, nil
}

// validatePurposeKey checks one published key: its id, its use, its shape and
// that its material is a usable public key.
func validatePurposeKey(key PurposeKey) error {
	if !keyIDPattern.MatchString(key.Kid) {
		return errors.New("purpose key snapshot carries an invalid key id")
	}
	if key.Use != "" && key.Use != "sig" {
		return errors.New("purpose key snapshot carries a key that is not for signatures")
	}
	if len(key.KeyOps) > 1 || (len(key.KeyOps) == 1 && key.KeyOps[0] != "verify") {
		return errors.New("purpose key snapshot carries a key that is not for verification")
	}
	algorithm, err := purposeAlgorithm(key)
	if err != nil {
		return err
	}
	if key.Alg != "" && key.Alg != algorithm {
		return errors.New("purpose key snapshot carries a key with another algorithm")
	}
	return nil
}

// purposeAlgorithm is the signing algorithm a published key belongs to.
func purposeAlgorithm(key PurposeKey) (string, error) {
	switch {
	case key.Kty == "EC" && key.Crv == "P-256":
		return "ES256", nil
	case key.Kty == "OKP" && key.Crv == "Ed25519":
		return "EdDSA", nil
	default:
		return "", errors.New("purpose key snapshot carries an unsupported key")
	}
}

// signatureHeader is the receipt's protected header.
type signatureHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

// verifySignature verifies the compact JWS against the published purpose keys
// and returns its protected header and its canonical payload.
func verifySignature(token string, keys *KeySet) (signatureHeader, []byte, error) {
	var empty signatureHeader
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return empty, nil, refuse(RefusalInvalid, "the receipt is not a compact JWS")
	}
	rawHeader, err := decodeBase64URL(parts[0])
	if err != nil {
		return empty, nil, refuse(RefusalInvalid, "the receipt header is invalid")
	}
	var header signatureHeader
	decoder := json.NewDecoder(strings.NewReader(string(rawHeader)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&header); err != nil {
		return empty, nil, refuse(RefusalInvalid, "the receipt header is invalid")
	}
	if header.Algorithm != "ES256" && header.Algorithm != "EdDSA" {
		return empty, nil, refuse(RefusalInvalid, "the receipt header names an unsupported algorithm")
	}
	if !keyIDPattern.MatchString(header.KeyID) {
		return empty, nil, refuse(RefusalInvalid, "the receipt header names an invalid key")
	}
	canonicalHeader, err := canonicalJSON(map[string]any{
		"alg": header.Algorithm,
		"kid": header.KeyID,
		"typ": header.Type,
	})
	if err != nil || canonicalHeader != string(rawHeader) {
		return empty, nil, refuse(RefusalInvalid, "the receipt header is not canonical")
	}
	if keys == nil {
		return empty, nil, refuse(RefusalInvalid, "no published purpose key is available")
	}
	published, err := keys.key(header.KeyID, header.Algorithm)
	if err != nil {
		return empty, nil, refuse(RefusalInvalid, "the receipt names no published purpose key")
	}
	verificationKey, err := published.verificationKey(header.Algorithm)
	if err != nil {
		return empty, nil, refuse(RefusalInvalid, "the published purpose key is unusable")
	}
	algorithm := jose.ES256
	if header.Algorithm == "EdDSA" {
		algorithm = jose.EdDSA
	}
	signed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{algorithm})
	if err != nil {
		return empty, nil, refuse(RefusalInvalid, "the receipt signature is invalid")
	}
	payload, err := signed.Verify(verificationKey)
	if err != nil {
		return empty, nil, refuse(RefusalInvalid, "the receipt signature does not verify")
	}
	return header, payload, nil
}

// key returns the one published key with this id, when it belongs to this
// algorithm.
func (k *KeySet) key(kid, algorithm string) (PurposeKey, error) {
	var found PurposeKey
	matches := 0
	for _, key := range k.Keys {
		if key.Kid == kid {
			found = key
			matches++
		}
	}
	if matches != 1 {
		return PurposeKey{}, errors.New("purpose key unavailable")
	}
	published, err := purposeAlgorithm(found)
	if err != nil || published != algorithm {
		return PurposeKey{}, errors.New("purpose key unavailable")
	}
	return found, nil
}

// verificationKey turns a published key into the key a signature verifies
// with.
func (k PurposeKey) verificationKey(algorithm string) (*jose.JSONWebKey, error) {
	switch algorithm {
	case "ES256":
		x, err := decodeBase64URL(k.X)
		if err != nil {
			return nil, err
		}
		y, err := decodeBase64URL(k.Y)
		if err != nil {
			return nil, err
		}
		if len(x) != 32 || len(y) != 32 {
			return nil, errors.New("purpose key material is invalid")
		}
		public := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !public.Curve.IsOnCurve(public.X, public.Y) {
			return nil, errors.New("purpose key material is invalid")
		}
		return &jose.JSONWebKey{Key: public, KeyID: k.Kid, Algorithm: algorithm}, nil
	case "EdDSA":
		raw, err := decodeBase64URL(k.X)
		if err != nil {
			return nil, err
		}
		if len(raw) != ed25519.PublicKeySize || k.Y != "" {
			return nil, errors.New("purpose key material is invalid")
		}
		return &jose.JSONWebKey{Key: ed25519.PublicKey(raw), KeyID: k.Kid, Algorithm: algorithm}, nil
	default:
		return nil, errors.New("unsupported purpose key algorithm")
	}
}

// decodeBase64URL reads one canonical base64url value: no padding, and the
// decoded bytes must encode back to the same text.
func decodeBase64URL(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("invalid base64url")
	}
	for _, character := range value {
		if (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return nil, errors.New("invalid base64url")
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if base64.RawURLEncoding.EncodeToString(raw) != value {
		return nil, errors.New("invalid base64url")
	}
	return raw, nil
}
