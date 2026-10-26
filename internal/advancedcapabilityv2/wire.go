package advancedcapability

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ValidationError reports the first wire rule a document violates. Field is
// the JSON member name ("document" for whole-document rules); Detail is
// diagnostic text and never a policy input.
type ValidationError struct {
	Field  string
	Detail string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("advanced capability wire: %s: %s", e.Field, e.Detail)
}

func invalid(field, detail string) error {
	return &ValidationError{Field: field, Detail: detail}
}

// IsTechstackIssuerID reports whether issuerID names a Techstack issuer: the
// bare IssuerClass or IssuerClass followed by "." and a per-installation
// suffix. Advanced Mode is Techstack-only, so no other issuer is valid.
func IsTechstackIssuerID(issuerID string) bool {
	if !issuerIdPattern.MatchString(issuerID) {
		return false
	}
	return issuerID == IssuerClass || strings.HasPrefix(issuerID, IssuerClass+".") && len(issuerID) > len(IssuerClass)+1
}

// KeyID derives the key identifier bound to a raw Ed25519 public key.
func KeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return "ed25519://sha256/" + hex.EncodeToString(digest[:])
}

// SigningDigest is the domain-separated digest an issuer signs and a verifier
// checks: SHA-256(SchemaVersion || 0x00 || unsigned canonical bytes).
func SigningDigest(unsigned []byte) []byte {
	message := make([]byte, 0, len(SchemaVersion)+1+len(unsigned))
	message = append(message, SchemaVersion...)
	message = append(message, 0)
	message = append(message, unsigned...)
	digest := sha256.Sum256(message)
	return digest[:]
}

// CanonicalUnsigned returns the RFC 8785 bytes without the signature member.
func (c Capability) CanonicalUnsigned() []byte {
	return c.appendCanonical(make([]byte, 0, 1024), false)
}

// Canonical returns the exact RFC 8785 document bytes a verifier reads.
func (c Capability) Canonical() []byte {
	return c.appendCanonical(make([]byte, 0, 1024), true)
}

// Sign sets Signature from privateKey over SigningDigest(CanonicalUnsigned()).
// It does not validate claims; issuers call ValidateClaims first.
func (c *Capability) Sign(privateKey ed25519.PrivateKey) {
	c.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(privateKey, SigningDigest(c.CanonicalUnsigned())))
}

// VerifySignature checks Signature against publicKey. Callers resolve
// publicKey from an Owner-pinned TrustBundle by issuerId and keyId.
func (c Capability) VerifySignature(publicKey ed25519.PublicKey) error {
	signature, err := decodeSignature(c.Signature)
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, SigningDigest(c.CanonicalUnsigned()), signature) {
		return invalid("signature", "does not verify against the trusted issuer key")
	}
	return nil
}

// IssuedAtTime and ExpiresAtTime return the parsed validity window. They are
// meaningful only after ValidateClaims succeeds.
func (c Capability) IssuedAtTime() time.Time { parsed, _ := parseSecondTime(c.IssuedAt); return parsed }
func (c Capability) ExpiresAtTime() time.Time {
	parsed, _ := parseSecondTime(c.ExpiresAt)
	return parsed
}

// ValidateClaims checks every time-independent wire rule of the unsigned
// document: constants, patterns, a Techstack issuer, a non-empty sorted
// operation set, logical references, exact-second UTC times, the 30-day
// lifetime bound, and the backup renewal and transfer retirement shapes. It does not check the
// signature, trusted time, or the caller's local scope.
func (c Capability) ValidateClaims() error {
	if err := c.checkFormats(); err != nil {
		return err
	}
	if !IsTechstackIssuerID(c.IssuerID) {
		return invalid("issuerId", "must name a "+IssuerClass+" issuer")
	}
	if len(c.AllowedOperations) == 0 || !slices.IsSorted(c.AllowedOperations) {
		return invalid("allowedOperations", "must be a non-empty sorted operation set")
	}
	for index, operation := range c.AllowedOperations {
		if !IsOperation(operation) {
			return invalid("allowedOperations", "contains an unsupported operation")
		}
		if index > 0 && c.AllowedOperations[index-1] == operation {
			return invalid("allowedOperations", "contains a duplicate operation")
		}
	}
	if err := validateLogicalReference(c.UIManagerRef); err != nil {
		return invalid("uiManagerRef", err.Error())
	}
	if err := validateLogicalReference(c.RILRef); err != nil {
		return invalid("rilRef", err.Error())
	}
	issuedAt, err := parseSecondTime(c.IssuedAt)
	if err != nil {
		return invalid("issuedAt", err.Error())
	}
	expiresAt, err := parseSecondTime(c.ExpiresAt)
	if err != nil {
		return invalid("expiresAt", err.Error())
	}
	lifetime := expiresAt.Sub(issuedAt)
	if lifetime <= 0 {
		return invalid("expiresAt", "must be later than issuedAt")
	}
	if lifetime > MaxLifetimeSeconds*time.Second {
		return invalid("expiresAt", "lifetime exceeds the maximum")
	}
	retirementOperation := slices.Contains(c.AllowedOperations, OperationApplicationTransferRetire)
	if retirementOperation || c.TransferRetirement != nil {
		if !retirementOperation || len(c.AllowedOperations) != 1 || c.TransferRetirement == nil || c.BackupRenewal != nil || lifetime > TransferRetirementMaxLifetimeSeconds*time.Second {
			return invalid("transferRetirement", "requires one short-lived application-transfer.retire operation and its exact scope")
		}
		if err := c.TransferRetirement.checkFormats(); err != nil {
			validation := err.(*ValidationError)
			return invalid("transferRetirement."+validation.Field, validation.Detail)
		}
		if c.TransferRetirement.BaselineSpecHash == c.TransferRetirement.CandidateSpecHash {
			return invalid("transferRetirement.candidateSpecHash", "must retire selected intent from a different baseline")
		}
	}
	if c.BackupRenewal != nil {
		if len(c.AllowedOperations) != 1 || c.AllowedOperations[0] != BackupRenewalOperation || lifetime > BackupRenewalMaxLifetimeSeconds*time.Second {
			return invalid("backupRenewal", "requires one short-lived "+BackupRenewalOperation+" operation")
		}
		if err := c.BackupRenewal.Validate(issuedAt); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks ValidateClaims and the signature encoding.
func (c Capability) Validate() error {
	if err := c.ValidateClaims(); err != nil {
		return err
	}
	_, err := decodeSignature(c.Signature)
	return err
}

// DecodeCapability strictly decodes one capability document. It accepts only
// exact canonical RFC 8785 bytes of a document that passes Validate, so unknown,
// duplicate, missing, reordered or re-encoded members are all rejected.
func DecodeCapability(raw []byte) (Capability, error) {
	var capability Capability
	if err := decodeExact(raw, &capability); err != nil {
		return Capability{}, err
	}
	if err := capability.Validate(); err != nil {
		return Capability{}, err
	}
	if !bytes.Equal(raw, capability.Canonical()) {
		return Capability{}, invalid("document", "must be exact RFC 8785 (JCS) JSON")
	}
	return capability, nil
}

// Validate checks the backup renewal against the capability's issuedAt: the
// usage measurement is canonical UTC, not after issuedAt (one second of
// clock tolerance), and at most BackupRenewalMaxMeasurementAgeSeconds old;
// usage is below a positive quota.
func (r BackupRenewal) Validate(issuedAt time.Time) error {
	if err := r.checkFormats(); err != nil {
		return invalid("backupRenewal."+err.(*ValidationError).Field, err.(*ValidationError).Detail)
	}
	measured, err := time.Parse(time.RFC3339Nano, r.MeasuredAt)
	if err != nil || measured.UTC().Format(time.RFC3339Nano) != r.MeasuredAt {
		return invalid("backupRenewal.measuredAt", "must be canonical UTC RFC 3339")
	}
	if measured.After(issuedAt.Add(time.Second)) || issuedAt.Sub(measured) > BackupRenewalMaxMeasurementAgeSeconds*time.Second {
		return invalid("backupRenewal.measuredAt", "is stale")
	}
	quota, err := strconv.ParseInt(r.QuotaBytes, 10, 64)
	if err != nil || quota <= 0 {
		return invalid("backupRenewal.quotaBytes", "must be a positive int64")
	}
	used, err := strconv.ParseInt(r.UsedBytes, 10, 64)
	if err != nil || used >= quota {
		return invalid("backupRenewal.usedBytes", "must be an int64 below quotaBytes")
	}
	return nil
}

// NewTrustBundleKey binds issuerID to the raw Ed25519 publicKey.
func NewTrustBundleKey(issuerID string, publicKey ed25519.PublicKey) TrustBundleKey {
	return TrustBundleKey{IssuerID: issuerID, KeyID: KeyID(publicKey), PublicKey: base64.RawStdEncoding.EncodeToString(publicKey)}
}

// RawPublicKey decodes PublicKey after checking that KeyID is bound to it.
func (k TrustBundleKey) RawPublicKey() (ed25519.PublicKey, error) {
	if err := k.checkFormats(); err != nil {
		return nil, err
	}
	publicKey, err := base64.RawStdEncoding.Strict().DecodeString(k.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize || base64.RawStdEncoding.EncodeToString(publicKey) != k.PublicKey {
		return nil, invalid("publicKey", "must be an unpadded standard-Base64 raw Ed25519 public key")
	}
	if KeyID(publicKey) != k.KeyID {
		return nil, invalid("keyId", "is not bound to publicKey")
	}
	return ed25519.PublicKey(publicKey), nil
}

// Canonical returns the exact RFC 8785 trust bundle bytes.
func (b TrustBundle) Canonical() []byte {
	return b.appendCanonical(make([]byte, 0, 256))
}

// Digest is the sha256:<hex> pin an Owner approves when importing the bundle.
func (b TrustBundle) Digest() string {
	digest := sha256.Sum256(b.Canonical())
	return "sha256:" + hex.EncodeToString(digest[:])
}

// Validate checks the bundle version, the key count, Techstack issuers,
// key-id binding, and that no issuer/key binding repeats.
func (b TrustBundle) Validate() error {
	if err := b.checkFormats(); err != nil {
		return err
	}
	if len(b.Keys) == 0 || len(b.Keys) > MaxTrustBundleKeys {
		return invalid("keys", "must contain 1 to "+strconv.Itoa(MaxTrustBundleKeys)+" keys")
	}
	seen := make(map[string]struct{}, len(b.Keys))
	for _, key := range b.Keys {
		if _, err := key.RawPublicKey(); err != nil {
			return err
		}
		if !IsTechstackIssuerID(key.IssuerID) {
			return invalid("issuerId", "must name a "+IssuerClass+" issuer")
		}
		identity := key.IssuerID + "\x00" + key.KeyID
		if _, duplicate := seen[identity]; duplicate {
			return invalid("keys", "contains a duplicate issuer/key binding")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

// PublicKey returns the trusted key for the exact issuer/key binding.
func (b TrustBundle) PublicKey(issuerID, keyID string) (ed25519.PublicKey, bool) {
	for _, key := range b.Keys {
		if key.IssuerID == issuerID && key.KeyID == keyID {
			publicKey, err := key.RawPublicKey()
			return publicKey, err == nil
		}
	}
	return nil, false
}

// DecodeTrustBundle strictly decodes one trust bundle; only exact canonical
// bytes of a bundle that passes Validate are accepted.
func DecodeTrustBundle(raw []byte) (TrustBundle, error) {
	var bundle TrustBundle
	if err := decodeExact(raw, &bundle); err != nil {
		return TrustBundle{}, err
	}
	if err := bundle.Validate(); err != nil {
		return TrustBundle{}, err
	}
	if !bytes.Equal(raw, bundle.Canonical()) {
		return TrustBundle{}, invalid("document", "must be exact RFC 8785 (JCS) JSON")
	}
	return bundle, nil
}

func decodeExact(raw []byte, destination any) error {
	if len(raw) == 0 {
		return invalid("document", "is required")
	}
	if len(raw) > MaxDocumentBytes {
		return invalid("document", "exceeds the maximum size")
	}
	if !utf8.Valid(raw) {
		return invalid("document", "must be valid UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalid("document", "must be one JSON object of the published shape")
	}
	if decoder.More() {
		return invalid("document", "has trailing JSON")
	}
	return nil
}

func decodeSignature(value string) ([]byte, error) {
	signature, err := base64.RawStdEncoding.Strict().DecodeString(value)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawStdEncoding.EncodeToString(signature) != value {
		return nil, invalid("signature", "must be an unpadded standard-Base64 Ed25519 signature")
	}
	return signature, nil
}

func parseSecondTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Nanosecond() != 0 || parsed.Format(time.RFC3339) != value {
		return time.Time{}, fmt.Errorf("must be UTC RFC 3339 with exact-second precision")
	}
	return parsed, nil
}

var windowsPath = regexp.MustCompile(`^[A-Za-z]:[/\\]`)

// validateLogicalReference accepts a secret-free logical URI reference such as
// a URN. It rejects whitespace, local paths, credentials, hosts, queries,
// fragments, network or file schemes, and path traversal.
func validateLogicalReference(value string) error {
	if len(value) == 0 || len(value) > MaxLogicalReferenceBytes {
		return fmt.Errorf("must contain 1 to %d bytes", MaxLogicalReferenceBytes)
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("must contain printable ASCII without whitespace")
		}
	}
	if windowsPath.MatchString(value) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "./") ||
		strings.HasPrefix(value, "../") || strings.Contains(value, `\`) {
		return fmt.Errorf("must be a logical URI reference, not a local path")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("must be a valid URI reference")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.User != nil || parsed.Host != "" {
		return fmt.Errorf("must not identify credentials, a network endpoint, query, or fragment")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "file", "ftp", "http", "https", "ssh", "ws", "wss":
		return fmt.Errorf("must not identify a network or local-file endpoint")
	}
	unescaped, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return fmt.Errorf("contains invalid percent encoding")
	}
	for _, segment := range strings.Split(strings.ReplaceAll(unescaped, `\`, "/"), "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("must not contain local path traversal")
		}
	}
	return nil
}
