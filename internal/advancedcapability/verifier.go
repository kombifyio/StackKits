package advancedcapability

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxDocumentBytes = 64 * 1024
	maxLifetime      = 30 * 24 * time.Hour
	futureTolerance  = 5 * time.Minute
	maxTrustKeys     = 64
)

var (
	uuidV7Pattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	stackIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	ownerRefPattern = regexp.MustCompile(`^owner/local/[0-9a-f]{32}$`)
	issuerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	keyIDPattern    = regexp.MustCompile(`^ed25519://sha256/[0-9a-f]{64}$`)
	windowsPath     = regexp.MustCompile(`^[A-Za-z]:[/\\]`)
)

var allowedOperationSet = map[string]struct{}{
	OperationDriftReconcileAdvanced:   {},
	OperationRestoreDrill:             {},
	OperationRollbackCoordinated:      {},
	OperationTerramateChangeSetApply:  {},
	OperationTerramateChangeSetCreate: {},
}

type envelope struct {
	AllowedOperations []string
	Audience          string
	CapabilityID      string
	ExpiresAt         string
	IssuedAt          string
	IssuerID          string
	KeyID             string
	OwnerRef          string
	RILRef            string
	SchemaVersion     string
	Signature         string
	StackID           string
	UIManagerRef      string
}

var exactFields = map[string]func(*envelope, json.RawMessage) error{
	"allowedOperations": func(document *envelope, raw json.RawMessage) error {
		return decodeStringArray(raw, &document.AllowedOperations)
	},
	"audience": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.Audience)
	},
	"capabilityId": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.CapabilityID)
	},
	"expiresAt": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.ExpiresAt)
	},
	"issuedAt": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.IssuedAt)
	},
	"issuerId": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.IssuerID)
	},
	"keyId": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.KeyID)
	},
	"ownerRef": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.OwnerRef)
	},
	"rilRef": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.RILRef)
	},
	"schemaVersion": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.SchemaVersion)
	},
	"signature": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.Signature)
	},
	"stackId": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.StackID)
	},
	"uiManagerRef": func(document *envelope, raw json.RawMessage) error {
		return decodeString(raw, &document.UIManagerRef)
	},
}

// Verify validates signature, trust, lifetime, and the exact caller-supplied
// local scope without network access or side effects.
func Verify(raw []byte, request Request) (Grant, error) {
	if err := validateVerificationRequest(request); err != nil {
		return Grant{}, err
	}
	document, err := decodeStrict(raw)
	if err != nil {
		return Grant{}, err
	}
	issuedAt, expiresAt, err := validateClaims(document)
	if err != nil {
		return Grant{}, err
	}
	trustedKey, err := resolveTrustedKey(request.TrustBundle, document.KeyID, document.IssuerID)
	if err != nil {
		return Grant{}, err
	}
	if err := verifySignature(document, trustedKey.PublicKey); err != nil {
		return Grant{}, err
	}
	canonical, err := canonicalDocument(document)
	if err != nil || !bytes.Equal(raw, canonical) {
		return Grant{}, deny(ReasonCapabilityMalformed, "document", "must be exact RFC8785/JCS JSON")
	}

	now := request.Now.UTC()
	if issuedAt.After(now.Add(futureTolerance)) {
		return Grant{}, deny(ReasonCapabilityNotYetValid, "issuedAt", "is more than 5 minutes after trusted verification time")
	}
	if !expiresAt.After(now) {
		return Grant{}, deny(ReasonCapabilityExpired, "expiresAt", "is not later than trusted verification time")
	}
	if expiresAt.Sub(issuedAt) > maxLifetime {
		return Grant{}, deny(ReasonCapabilityLifetimeExceeded, "expiresAt", "lifetime exceeds 30 days")
	}
	if document.StackID != request.StackID {
		return Grant{}, deny(ReasonCapabilityScopeMismatch, "stackId", "does not match the local stack")
	}
	if document.OwnerRef != request.OwnerRef {
		return Grant{}, deny(ReasonCapabilityScopeMismatch, "ownerRef", "does not match local owner custody")
	}
	if !slices.Contains(document.AllowedOperations, request.Operation) {
		return Grant{}, deny(ReasonCapabilityOperationDenied, "allowedOperations", "does not authorize the requested operation")
	}
	if request.IssuerID != "" && document.IssuerID != request.IssuerID {
		return Grant{}, deny(ReasonCapabilityScopeMismatch, "issuerId", "does not match the required issuer")
	}
	if request.UIManagerRef != "" && document.UIManagerRef != request.UIManagerRef {
		return Grant{}, deny(ReasonCapabilityScopeMismatch, "uiManagerRef", "does not match the approved UI Manager reference")
	}
	if request.RILRef != "" && document.RILRef != request.RILRef {
		return Grant{}, deny(ReasonCapabilityScopeMismatch, "rilRef", "does not match the approved RIL reference")
	}

	return Grant{
		CapabilityID:      document.CapabilityID,
		IssuerID:          document.IssuerID,
		StackID:           document.StackID,
		OwnerRef:          document.OwnerRef,
		AllowedOperations: slices.Clone(document.AllowedOperations),
		UIManagerRef:      document.UIManagerRef,
		RILRef:            document.RILRef,
		IssuedAt:          issuedAt,
		ExpiresAt:         expiresAt,
		KeyID:             document.KeyID,
	}, nil
}

func validateVerificationRequest(request Request) error {
	if request.Now.IsZero() {
		return deny(ReasonCapabilityMalformed, "now", "trusted verification time is required")
	}
	if request.TrustBundle == nil {
		return deny(ReasonTrustBundleUnavailable, "trustBundle", "is required")
	}
	if !stackIDPattern.MatchString(request.StackID) {
		return deny(ReasonCapabilityScopeMismatch, "stackId", "is not canonical")
	}
	if !ownerRefPattern.MatchString(request.OwnerRef) {
		return deny(ReasonCapabilityScopeMismatch, "ownerRef", "is not canonical")
	}
	if _, ok := allowedOperationSet[request.Operation]; !ok {
		return deny(ReasonCapabilityOperationDenied, "operation", "is not an advanced operation")
	}
	return nil
}

func decodeStrict(raw []byte) (envelope, error) {
	if len(raw) > maxDocumentBytes {
		return envelope{}, deny(ReasonCapabilityMalformed, "document", "exceeds 65536 bytes")
	}
	if len(raw) == 0 {
		return envelope{}, deny(ReasonCapabilityRequired, "document", "is required")
	}
	if !utf8.Valid(raw) {
		return envelope{}, deny(ReasonCapabilityMalformed, "document", "must be valid UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil {
		return envelope{}, deny(ReasonCapabilityMalformed, "document", err.Error())
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return envelope{}, deny(ReasonCapabilityMalformed, "document", "must be one JSON object")
	}

	var document envelope
	seen := make(map[string]struct{}, len(exactFields))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return envelope{}, deny(ReasonCapabilityMalformed, "document", err.Error())
		}
		field, ok := token.(string)
		if !ok {
			return envelope{}, deny(ReasonCapabilityMalformed, "document", "contains a non-string field name")
		}
		decode, known := exactFields[field]
		if !known {
			return envelope{}, deny(ReasonCapabilityMalformed, field, "unknown field")
		}
		if _, duplicate := seen[field]; duplicate {
			return envelope{}, deny(ReasonCapabilityMalformed, field, "duplicate field")
		}
		seen[field] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return envelope{}, deny(ReasonCapabilityMalformed, field, err.Error())
		}
		if err := decode(&document, value); err != nil {
			return envelope{}, deny(ReasonCapabilityMalformed, field, "has the wrong JSON type")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return envelope{}, deny(ReasonCapabilityMalformed, "document", err.Error())
	}
	if len(seen) != len(exactFields) {
		for field := range exactFields {
			if _, ok := seen[field]; !ok {
				return envelope{}, deny(ReasonCapabilityMalformed, field, "required field is missing")
			}
		}
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return envelope{}, deny(ReasonCapabilityMalformed, "document", err.Error())
		}
		return envelope{}, deny(ReasonCapabilityMalformed, "document", fmt.Sprintf("has trailing JSON token %v", token))
	}
	return document, nil
}

func decodeString(raw json.RawMessage, destination *string) error {
	if len(raw) == 0 || raw[0] != '"' {
		return fmt.Errorf("must be a JSON string")
	}
	return json.Unmarshal(raw, destination)
}

func decodeStringArray(raw json.RawMessage, destination *[]string) error {
	if len(raw) == 0 || raw[0] != '[' {
		return fmt.Errorf("must be a JSON string array")
	}
	return json.Unmarshal(raw, destination)
}

func validateClaims(document envelope) (time.Time, time.Time, error) {
	if document.SchemaVersion != SchemaVersion {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "schemaVersion", "is unsupported")
	}
	if document.Audience != Audience {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "audience", "must be stackkit")
	}
	if !uuidV7Pattern.MatchString(document.CapabilityID) {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "capabilityId", "must be a lowercase UUIDv7")
	}
	if !issuerIDPattern.MatchString(document.IssuerID) {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "issuerId", "is not canonical")
	}
	if !stackIDPattern.MatchString(document.StackID) {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "stackId", "is not canonical")
	}
	if !ownerRefPattern.MatchString(document.OwnerRef) {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "ownerRef", "is not a local owner reference")
	}
	if !keyIDPattern.MatchString(document.KeyID) {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "keyId", "is not a canonical Ed25519 key ID")
	}
	if len(document.AllowedOperations) < 1 || len(document.AllowedOperations) > len(allowedOperationSet) ||
		!slices.IsSorted(document.AllowedOperations) {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "allowedOperations", "must be a non-empty sorted operation set")
	}
	for index, operation := range document.AllowedOperations {
		if _, ok := allowedOperationSet[operation]; !ok {
			return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "allowedOperations", "contains an unsupported operation")
		}
		if index > 0 && document.AllowedOperations[index-1] == operation {
			return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "allowedOperations", "contains a duplicate operation")
		}
	}
	if err := validateLogicalReference(document.UIManagerRef); err != nil {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "uiManagerRef", err.Error())
	}
	if err := validateLogicalReference(document.RILRef); err != nil {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "rilRef", err.Error())
	}
	issuedAt, err := parseCanonicalTime(document.IssuedAt)
	if err != nil {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "issuedAt", err.Error())
	}
	expiresAt, err := parseCanonicalTime(document.ExpiresAt)
	if err != nil {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "expiresAt", err.Error())
	}
	lifetime := expiresAt.Sub(issuedAt)
	if lifetime <= 0 {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityMalformed, "expiresAt", "must be later than issuedAt")
	}
	if lifetime > maxLifetime {
		return time.Time{}, time.Time{}, deny(ReasonCapabilityLifetimeExceeded, "expiresAt", "lifetime exceeds 30 days")
	}
	signature, err := base64.RawStdEncoding.DecodeString(document.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		base64.RawStdEncoding.EncodeToString(signature) != document.Signature {
		return time.Time{}, time.Time{}, deny(ReasonCapabilitySignatureInvalid, "signature", "must be an unpadded standard-Base64 Ed25519 signature")
	}
	return issuedAt, expiresAt, nil
}

func resolveTrustedKey(bundle *TrustBundle, keyID, issuerID string) (TrustedKey, error) {
	if bundle.SchemaVersion != TrustBundleSchemaVersion {
		return TrustedKey{}, deny(ReasonTrustBundleUnavailable, "schemaVersion", "trust bundle version is unsupported")
	}
	if len(bundle.Keys) == 0 || len(bundle.Keys) > maxTrustKeys {
		return TrustedKey{}, deny(ReasonTrustBundleUnavailable, "keys", "must contain 1 to 64 keys")
	}
	seen := make(map[string]struct{}, len(bundle.Keys))
	var matched *TrustedKey
	for index := range bundle.Keys {
		key := bundle.Keys[index]
		if !issuerIDPattern.MatchString(key.IssuerID) || len(key.PublicKey) != ed25519.PublicKeySize {
			return TrustedKey{}, deny(ReasonTrustBundleUnavailable, "keys", "contains a malformed issuer or public key")
		}
		digest := sha256.Sum256(key.PublicKey)
		derived := "ed25519://sha256/" + hex.EncodeToString(digest[:])
		if key.KeyID != derived {
			return TrustedKey{}, deny(ReasonTrustBundleUnavailable, "keys", "contains a key ID that is not bound to its raw public key")
		}
		identity := key.IssuerID + "\x00" + key.KeyID
		if _, duplicate := seen[identity]; duplicate {
			return TrustedKey{}, deny(ReasonTrustBundleUnavailable, "keys", "contains a duplicate issuer/key binding")
		}
		seen[identity] = struct{}{}
		if key.KeyID == keyID && key.IssuerID == issuerID {
			copyKey := TrustedKey{KeyID: key.KeyID, IssuerID: key.IssuerID, PublicKey: slices.Clone(key.PublicKey)}
			matched = &copyKey
		}
	}
	if matched == nil {
		return TrustedKey{}, deny(ReasonCapabilityUntrustedKey, "keyId", "is not trusted for this issuer")
	}
	return *matched, nil
}

func verifySignature(document envelope, publicKey ed25519.PublicKey) error {
	signature, err := base64.RawStdEncoding.DecodeString(document.Signature)
	if err != nil {
		return deny(ReasonCapabilitySignatureInvalid, "signature", "cannot be decoded")
	}
	unsigned := canonicalUnsigned(document)
	domainSeparated := make([]byte, 0, len(SchemaVersion)+1+len(unsigned))
	domainSeparated = append(domainSeparated, SchemaVersion...)
	domainSeparated = append(domainSeparated, 0)
	domainSeparated = append(domainSeparated, unsigned...)
	digest := sha256.Sum256(domainSeparated)
	if !ed25519.Verify(publicKey, digest[:], signature) {
		return deny(ReasonCapabilitySignatureInvalid, "signature", "does not verify against the trusted issuer key")
	}
	return nil
}

func parseCanonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Nanosecond() != 0 ||
		parsed.Format(time.RFC3339) != value {
		return time.Time{}, fmt.Errorf("must be UTC RFC3339 with exact-second precision")
	}
	return parsed, nil
}

func validateLogicalReference(value string) error {
	if len(value) == 0 || len(value) > 256 {
		return fmt.Errorf("must contain 1 to 256 bytes")
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("must contain printable ASCII without whitespace")
		}
	}
	if windowsPath.MatchString(value) || strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, `\`) || strings.HasPrefix(value, "./") ||
		strings.HasPrefix(value, "../") || strings.Contains(value, `\`) {
		return fmt.Errorf("must be a logical URI reference, not a local path")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("must be a valid URI reference")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.User != nil ||
		parsed.Host != "" {
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
