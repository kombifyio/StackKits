// Package advancedcapability verifies the short-lived, secret-free
// capabilities which authorize individual StackKits advanced operations.
//
// Verification is deliberately offline. Callers must inject a versioned trust
// bundle and the exact local stack, owner, and operation scope being admitted.
package advancedcapability

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"
)

const (
	SchemaVersion            = "stackkit.advanced-capability/v1"
	TrustBundleSchemaVersion = "stackkit.advanced-trust-bundle/v1"
	Audience                 = "stackkit"
)

const (
	OperationDriftReconcileAdvanced   = "drift.reconcile.advanced"
	OperationRestoreDrill             = "restore.drill"
	OperationRollbackCoordinated      = "rollback.coordinated"
	OperationTerramateChangeSetApply  = "terramate.change-set.apply"
	OperationTerramateChangeSetCreate = "terramate.change-set.create"
)

// ReasonCode is a stable, machine-readable fail-closed denial reason.
type ReasonCode string

const (
	ReasonTrustBundleUnavailable     ReasonCode = "advanced_trust_bundle_unavailable"
	ReasonCapabilityRequired         ReasonCode = "advanced_capability_required"
	ReasonCapabilityMalformed        ReasonCode = "advanced_capability_malformed"
	ReasonCapabilityUntrustedKey     ReasonCode = "advanced_capability_untrusted_key"
	ReasonCapabilitySignatureInvalid ReasonCode = "advanced_capability_signature_invalid"
	ReasonCapabilityNotYetValid      ReasonCode = "advanced_capability_not_yet_valid"
	ReasonCapabilityExpired          ReasonCode = "advanced_capability_expired"
	ReasonCapabilityLifetimeExceeded ReasonCode = "advanced_capability_lifetime_exceeded"
	ReasonCapabilityScopeMismatch    ReasonCode = "advanced_capability_scope_mismatch"
	ReasonCapabilityOperationDenied  ReasonCode = "advanced_capability_operation_denied"
	ReasonAdvancedChangeSetInvalid   ReasonCode = "advanced_change_set_invalid"
	ReasonAdvancedChangeSetStale     ReasonCode = "advanced_change_set_stale"
	ReasonCapabilityUnavailable      ReasonCode = "advanced_capability_unavailable"
)

// Denial is returned for every Verify failure. Code is stable; Field and
// Detail are diagnostic and must not be used as policy inputs.
type Denial struct {
	Code   ReasonCode
	Field  string
	Detail string
}

func (d *Denial) Error() string {
	if d.Field == "" {
		return fmt.Sprintf("advanced capability denied: %s: %s", d.Code, d.Detail)
	}
	return fmt.Sprintf("advanced capability denied: %s: %s: %s", d.Code, d.Field, d.Detail)
}

// Reason returns a stable reason code for a denial.
func Reason(err error) (ReasonCode, bool) {
	var denial *Denial
	if !errors.As(err, &denial) {
		return "", false
	}
	return denial.Code, true
}

// TrustedKey is one explicitly trusted Techstack issuer key. PublicKey is the
// raw 32-byte Ed25519 public key, never a credential or private key.
type TrustedKey struct {
	KeyID     string
	IssuerID  string
	PublicKey ed25519.PublicKey
}

// TrustBundle is injected by the StackKits composition root. Verification
// performs no network, environment, file, or Techstack-client discovery.
type TrustBundle struct {
	SchemaVersion string
	Keys          []TrustedKey
}

// Request binds verification to the exact local operation being admitted.
// Optional expected issuer/approval references add narrower caller scope.
type Request struct {
	Now          time.Time
	TrustBundle  *TrustBundle
	StackID      string
	OwnerRef     string
	Operation    string
	IssuerID     string
	UIManagerRef string
	RILRef       string
}

// Grant is the fully verified and locally scoped capability.
type Grant struct {
	CapabilityID      string
	IssuerID          string
	StackID           string
	OwnerRef          string
	AllowedOperations []string
	UIManagerRef      string
	RILRef            string
	IssuedAt          time.Time
	ExpiresAt         time.Time
	KeyID             string
}

func deny(code ReasonCode, field, detail string) error {
	return &Denial{Code: code, Field: field, Detail: detail}
}
