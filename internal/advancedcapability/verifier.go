package advancedcapability

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	wire "github.com/kombifyio/stackkits/contracts/advancedcapability"
)

const (
	maxLifetime     = wire.MaxLifetimeSeconds * time.Second
	futureTolerance = 5 * time.Minute
	maxTrustKeys    = wire.MaxTrustBundleKeys
)

// TrustedIssuerID is the only issuer identity whose Advanced capabilities and
// execution-channel digests the standalone CLI accepts: Techstack is the
// Advanced control plane. A trust bundle can hold other issuer bindings (for
// example the kombify Cloud issuer of desired identity projections), but none
// of them is ever consulted for a capability or an execution channel.
const TrustedIssuerID = "techstack"

var (
	stackIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	ownerRefPattern = regexp.MustCompile(`^owner/local/[0-9a-f]{32}$`)
	issuerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

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
	issuedAt, expiresAt := document.IssuedAtTime(), document.ExpiresAtTime()
	if err := requireTechstackIssuer(document.IssuerID); err != nil {
		return Grant{}, err
	}
	trustedKey, err := resolveTrustedKey(request.TrustBundle, document.KeyID, document.IssuerID)
	if err != nil {
		return Grant{}, err
	}
	if err := document.VerifySignature(trustedKey.PublicKey); err != nil {
		return Grant{}, deny(ReasonCapabilitySignatureInvalid, "signature", "does not verify against the trusted issuer key")
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

	if request.Operation == OperationApplicationTransferRetire && (document.TransferRetirement == nil || *document.TransferRetirement != *request.TransferRetirement) {
		return Grant{}, deny(ReasonCapabilityScopeMismatch, "transferRetirement", "does not match the authenticated held snapshot, peer outcome and intent transition")
	}
	if document.BackupRenewal != nil {
		if request.Operation != OperationRestoreDrill || len(document.AllowedOperations) != 1 || expiresAt.Sub(issuedAt) > 15*time.Minute {
			return Grant{}, deny(ReasonCapabilityScopeMismatch, "backupRenewal", "requires one short-lived restore.drill operation")
		}
		if err := document.BackupRenewal.Validate(issuedAt); err != nil {
			return Grant{}, deny(ReasonCapabilityMalformed, "backupRenewal", err.Error())
		}
	}
	return Grant{
		TransferRetirement: document.TransferRetirement,
		BackupRenewal:      document.BackupRenewal,
		CapabilityID:       document.CapabilityID,
		IssuerID:           document.IssuerID,
		StackID:            document.StackID,
		OwnerRef:           document.OwnerRef,
		AllowedOperations:  slices.Clone(document.AllowedOperations),
		UIManagerRef:       document.UIManagerRef,
		RILRef:             document.RILRef,
		IssuedAt:           issuedAt,
		ExpiresAt:          expiresAt,
		KeyID:              document.KeyID,
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
	if request.Operation == OperationApplicationTransferRetire && request.TransferRetirement == nil {
		return deny(ReasonCapabilityScopeMismatch, "transferRetirement", "authenticated local scope is required")
	}
	if request.TransferRetirement != nil && request.Operation != OperationApplicationTransferRetire {
		return deny(ReasonCapabilityScopeMismatch, "transferRetirement", "cannot authorize another operation")
	}
	if !wire.IsOperation(request.Operation) {
		return deny(ReasonCapabilityOperationDenied, "operation", "is not an advanced operation")
	}
	return nil
}

// decodeStrict translates the central wire's validation failures to the CLI's
// existing denial codes. The diagnostic projection only classifies errors; the
// released central decoder is the authority for acceptance and canonical bytes.
func decodeStrict(raw []byte) (wire.Capability, error) {
	if len(raw) == 0 {
		return wire.Capability{}, deny(ReasonCapabilityRequired, "document", "is required")
	}
	document, err := wire.DecodeCapability(raw)
	if err == nil {
		return document, nil
	}
	reason, field, detail := ReasonCapabilityMalformed, "document", err.Error()
	var invalid *wire.ValidationError
	if errors.As(err, &invalid) {
		field, detail = invalid.Field, invalid.Detail
		switch field {
		case "signature":
			reason = ReasonCapabilitySignatureInvalid
		case "backupRenewal":
			// The central document-level renewal rule is the CLI's existing
			// operation/lifetime scope denial; renewal member errors stay malformed.
			reason = ReasonCapabilityScopeMismatch
		case "issuerId", "expiresAt":
			// Preserve product reasons for a well-formed foreign issuer or an excessive
			// lifetime. Do not depend on the central validator's diagnostic wording.
			var diagnostic struct {
				IssuerID  string `json:"issuerId"`
				IssuedAt  string `json:"issuedAt"`
				ExpiresAt string `json:"expiresAt"`
			}
			if json.Unmarshal(raw, &diagnostic) == nil {
				if field == "issuerId" && issuerIDPattern.MatchString(diagnostic.IssuerID) && !wire.IsTechstackIssuerID(diagnostic.IssuerID) {
					reason = ReasonCapabilityUntrustedKey
				}
				if field == "expiresAt" {
					issued, issuedErr := time.Parse(time.RFC3339, diagnostic.IssuedAt)
					expires, expiresErr := time.Parse(time.RFC3339, diagnostic.ExpiresAt)
					if issuedErr == nil && expiresErr == nil && expires.Sub(issued) > maxLifetime {
						reason = ReasonCapabilityLifetimeExceeded
					}
				}
			}
		}
	}
	return wire.Capability{}, deny(reason, field, detail)
}

// requireTechstackIssuer refuses any issuer other than the pinned Advanced
// control plane. It guards capabilities and execution channels only; desired
// identity projections stay bound to the Owner-approved trust-bundle binding.
func requireTechstackIssuer(issuerID string) error {
	if issuerID != TrustedIssuerID {
		return deny(ReasonCapabilityUntrustedKey, "issuerId", "is not the pinned Advanced issuer")
	}
	return nil
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
		derived := wire.KeyID(key.PublicKey)
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

// VerifyIdentityProjectionDigest verifies one domain-separated desired
// identity projection digest against the exact Owner-approved issuer/key
// binding. It intentionally exposes neither trusted public-key bytes nor a
// generic signing oracle to callers.
func VerifyIdentityProjectionDigest(
	bundle *TrustBundle,
	issuerID, keyID string,
	digest, signature []byte,
) error {
	if bundle == nil {
		return deny(ReasonTrustBundleUnavailable, "trustBundle", "is required")
	}
	trusted, err := resolveTrustedKey(bundle, keyID, issuerID)
	if err != nil {
		return err
	}
	if len(digest) != sha256.Size || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(trusted.PublicKey, digest, signature) {
		return deny(
			ReasonCapabilitySignatureInvalid,
			"signature",
			"desired identity projection signature does not verify",
		)
	}
	return nil
}

// VerifyTrustedDigest verifies one domain-separated digest against the exact
// Owner-approved issuer/key binding used by Advanced capability verification.
// It is the only additional trust seam needed by authenticated execution
// channels: callers cannot supply a second trust schema, store, or key parser.
func VerifyTrustedDigest(bundle *TrustBundle, issuerID, keyID string, digest, signature []byte) error {
	if bundle == nil {
		return deny(ReasonTrustBundleUnavailable, "trustBundle", "is required")
	}
	if err := requireTechstackIssuer(issuerID); err != nil {
		return err
	}
	trustedKey, err := resolveTrustedKey(bundle, keyID, issuerID)
	if err != nil {
		return err
	}
	if len(digest) != sha256.Size {
		return deny(ReasonCapabilitySignatureInvalid, "digest", "must be one SHA-256 digest")
	}
	if len(signature) != ed25519.SignatureSize {
		return deny(ReasonCapabilitySignatureInvalid, "signature", "must be one Ed25519 signature")
	}
	if !ed25519.Verify(trustedKey.PublicKey, digest, signature) {
		return deny(ReasonCapabilitySignatureInvalid, "signature", "does not verify against the trusted issuer key")
	}
	return nil
}
