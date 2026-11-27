package localevidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const (
	workloadRelationshipDirectory       = ".stackkit/custody/workload-relationships"
	workloadRelationshipSignatureDomain = "stackkit.owner-workload-relationship/v1\x00"

	// WorkloadRelationshipSchema versions the Owner-signed eligibility and
	// lifecycle policy of one short-lived workload identity relationship.
	WorkloadRelationshipSchema = "stackkit.workload-identity-relationship/v1"
	// WorkloadRelationshipEvidenceSchema matches the canonical Guard D
	// qualification evidence wire contract consumed by Guard.
	WorkloadRelationshipEvidenceSchema = "kombify.guard-workload-identity/v1"

	// WorkloadRelationshipAuthorityVersion identifies the existing StackKits
	// step-ca + Owner-custody workload peer authority.
	WorkloadRelationshipAuthorityVersion = "stackkit.owner-workload-peer/v1"
	// DefaultWorkloadRelationshipScope is the bounded default scope for the
	// qualified workload-to-origin relationship slice.
	DefaultWorkloadRelationshipScope = "origin:probe"

	WorkloadRelationshipMinTTLSeconds     = 300
	WorkloadRelationshipMaxTTLSeconds     = 86400
	WorkloadRelationshipMaxSessionSeconds = 60
	WorkloadRelationshipMaxClockSkew      = 30 * time.Second
	WorkloadRelationshipMaxForwardJump    = 7 * 24 * time.Hour
	WorkloadRelationshipMaxEvidenceAge    = 5 * time.Minute

	maxWorkloadRelationshipNonces = 32
)

var (
	ErrWorkloadEligibilityDenied   = errors.New("localevidence: workload identity eligibility is absent or removed")
	ErrWorkloadIdentityMismatch    = errors.New("localevidence: workload identity or scope differs from approved relationship")
	ErrWorkloadRenewalReplayed     = errors.New("localevidence: workload renewal nonce was already consumed")
	ErrWorkloadIssuerUnavailable   = errors.New("localevidence: workload identity issuer is unavailable")
	ErrWorkloadClockOutage         = errors.New("localevidence: workload identity clock is invalid or skewed")
	ErrWorkloadSessionExpired      = errors.New("localevidence: established workload session exceeded bounded lifetime")
	ErrWorkloadRecoveryKeyRejected = errors.New("localevidence: replacement workload key is invalid or previously revoked")
	ErrWorkloadRecoveryNotRequired = errors.New("localevidence: workload peer has no revoked or retired credential to recover")
)

// WorkloadCertificateIssuer issues a client-auth X.509 certificate chain for a
// validated CSR via the existing identity authority. Nil defaults to
// IssueWorkloadPeerCertificate (step-ca).
type WorkloadCertificateIssuer func(ctx context.Context, root, peerRef string, csrPEM []byte, ttl int) ([]byte, error)

// WorkloadRelationshipPolicy binds one eligible workload identity to a single
// target service, edge site, audience, scope, server name, bounded TTL and
// established-session limit.
type WorkloadRelationshipPolicy struct {
	SchemaVersion               string     `json:"schema_version"`
	PeerRef                     string     `json:"peer_ref"`
	WorkloadID                  string     `json:"workload_id"`
	ExpectedKeyFingerprint      string     `json:"expected_key_fingerprint"`
	ServiceRef                  string     `json:"service_ref"`
	EdgeSiteRef                 string     `json:"edge_site_ref"`
	Audience                    string     `json:"audience"`
	Scope                       string     `json:"scope"`
	ServerName                  string     `json:"server_name"`
	TTLSeconds                  int        `json:"ttl_seconds"`
	RenewBeforeSeconds          int        `json:"renew_before_seconds"`
	MaxSessionSeconds           int        `json:"max_session_seconds"`
	Eligible                    bool       `json:"eligible"`
	EligibilityRevision         uint64     `json:"eligibility_revision"`
	EligibilityReason           string     `json:"eligibility_reason"`
	RenewalCount                uint64     `json:"renewal_count"`
	LastRenewedAt               *time.Time `json:"last_renewed_at,omitempty"`
	LastRenewalErrorCode        string     `json:"last_renewal_error_code,omitempty"`
	IssuerStatus                string     `json:"issuer_status"`
	ClockStatus                 string     `json:"clock_status"`
	RecoveryState               string     `json:"recovery_state"`
	PostExpiryDenied            *bool      `json:"post_expiry_denied,omitempty"`
	MeasuredSessionBoundSeconds int64      `json:"measured_session_bound_seconds,omitempty"`
	UpdatedAt                   time.Time  `json:"updated_at"`
}

type workloadRelationshipRecord struct {
	Policy         WorkloadRelationshipPolicy `json:"policy"`
	ConsumedNonces []string                   `json:"consumedNonces,omitempty"`
	Signature      OwnerPolicyStateSignature  `json:"signature"`
}

// WorkloadRenewalRequest carries the public CSR and identity claims presented by
// a workload requesting short-lived certificate issuance or renewal.
type WorkloadRenewalRequest struct {
	PeerRef     string
	WorkloadID  string
	ServiceRef  string
	EdgeSiteRef string
	Audience    string
	ServerName  string
	CSRPEM      []byte
	Nonce       string
	ClockOffset time.Duration
}

// WorkloadRenewalResult returns the renewed leaf/chain and current policy state.
type WorkloadRenewalResult struct {
	CertificatePEM         string
	CertificateFingerprint string
	NotBefore              time.Time
	NotAfter               time.Time
	Policy                 WorkloadRelationshipPolicy
}

// WorkloadSessionAuthorization reports a verified client TLS session bound to
// the relationship's maximum established-session lifetime and certificate expiry.
type WorkloadSessionAuthorization struct {
	PeerRef         string
	WorkloadID      string
	SessionDeadline time.Time
}

// WorkloadProfile identifies the single supported Linux/systemd profile slice.
type WorkloadProfile struct {
	OS        string `json:"os"`
	Init      string `json:"init"`
	Transport string `json:"transport"`
}

// WorkloadRelationshipEvidence is the secret-free Guard D qualification
// evidence produced from existing local authority state using the canonical
// kombify.guard-workload-identity/v1 wire contract.
type WorkloadRelationshipEvidence struct {
	SchemaVersion               string          `json:"schema_version"`
	AuthorityVersion            string          `json:"authority_version"`
	GuardVersion                string          `json:"guard_version"`
	Profile                     WorkloadProfile `json:"profile"`
	PeerRef                     string          `json:"peer_ref"`
	WorkloadID                  string          `json:"workload_id"`
	ServiceRef                  string          `json:"service_ref"`
	EdgeSiteRef                 string          `json:"edge_site_ref"`
	Audience                    string          `json:"audience"`
	Scope                       string          `json:"scope"`
	ServerName                  string          `json:"server_name"`
	Eligible                    bool            `json:"eligible"`
	EligibilityRevision         uint64          `json:"eligibility_revision"`
	EligibilityReason           string          `json:"eligibility_reason,omitempty"`
	TTLSeconds                  int             `json:"ttl_seconds"`
	RenewBeforeSeconds          int             `json:"renew_before_seconds"`
	MaxSessionSeconds           int             `json:"max_session_seconds"`
	ActiveCertificateDigest     string          `json:"active_certificate_digest,omitempty"`
	NotBefore                   *time.Time      `json:"not_before,omitempty"`
	NotAfter                    *time.Time      `json:"not_after,omitempty"`
	RenewalState                string          `json:"renewal_state"`
	RenewalCount                uint64          `json:"renewal_count"`
	LastRenewedAt               *time.Time      `json:"last_renewed_at,omitempty"`
	LastRenewalErrorCode        string          `json:"last_renewal_error_code,omitempty"`
	RetiredCertificateCount     int             `json:"retired_certificate_count"`
	RevokedKeyCount             int             `json:"revoked_key_count"`
	IssuerStatus                string          `json:"issuer_status"`
	ClockStatus                 string          `json:"clock_status"`
	RecoveryState               string          `json:"recovery_state"`
	PostExpiryDenied            *bool           `json:"post_expiry_denied,omitempty"`
	MeasuredSessionBoundSeconds int64           `json:"measured_session_bound_seconds,omitempty"`
	ObservedAt                  time.Time       `json:"observed_at"`
	ExpiresAt                   time.Time       `json:"expires_at"`
	EvidenceAgeSeconds          int64           `json:"evidence_age_seconds"`
	Unknowns                    []string        `json:"unknowns,omitempty"`
}

// SetWorkloadRelationshipEligibility creates or updates the Owner-signed
// eligibility binding for one workload-to-service relationship. Caller-supplied
// recovery and measurement fields are ignored; only authority and observation
// paths populate them.
func SetWorkloadRelationshipEligibility(workspaceRoot string, policy WorkloadRelationshipPolicy, now time.Time) (WorkloadRelationshipPolicy, error) {
	if now.IsZero() {
		return WorkloadRelationshipPolicy{}, ErrWorkloadClockOutage
	}
	now = now.UTC()
	if strings.TrimSpace(policy.Scope) == "" {
		policy.Scope = DefaultWorkloadRelationshipScope
	}
	if err := validateRelationshipPolicyInput(policy); err != nil {
		return WorkloadRelationshipPolicy{}, err
	}
	var saved WorkloadRelationshipPolicy
	err := mutateWorkloadRelationship(workspaceRoot, policy.PeerRef, func(previous *workloadRelationshipRecord) (workloadRelationshipRecord, error) {
		next := WorkloadRelationshipPolicy{
			SchemaVersion:          WorkloadRelationshipSchema,
			PeerRef:                policy.PeerRef,
			WorkloadID:             policy.WorkloadID,
			ExpectedKeyFingerprint: policy.ExpectedKeyFingerprint,
			ServiceRef:             policy.ServiceRef,
			EdgeSiteRef:            policy.EdgeSiteRef,
			Audience:               policy.Audience,
			Scope:                  policy.Scope,
			ServerName:             policy.ServerName,
			TTLSeconds:             policy.TTLSeconds,
			RenewBeforeSeconds:     policy.RenewBeforeSeconds,
			MaxSessionSeconds:      policy.MaxSessionSeconds,
			Eligible:               policy.Eligible,
			EligibilityReason:      strings.TrimSpace(policy.EligibilityReason),
			IssuerStatus:           "unknown",
			ClockStatus:            "synchronized",
			RecoveryState:          "none",
			UpdatedAt:              now,
		}
		if next.EligibilityReason == "" {
			if next.Eligible {
				next.EligibilityReason = "approved"
			} else {
				next.EligibilityReason = "eligibility_removed"
			}
		}
		record := workloadRelationshipRecord{Policy: next}
		if previous != nil {
			if !now.After(previous.Policy.UpdatedAt) {
				record.Policy.UpdatedAt = previous.Policy.UpdatedAt
			}
			record.ConsumedNonces = append([]string(nil), previous.ConsumedNonces...)
			if previous.Policy.EligibilityRevision == ^uint64(0) {
				return workloadRelationshipRecord{}, errors.New("localevidence: workload eligibility revision exhausted")
			}
			record.Policy.EligibilityRevision = previous.Policy.EligibilityRevision + 1
			record.Policy.RenewalCount = previous.Policy.RenewalCount
			record.Policy.LastRenewedAt = previous.Policy.LastRenewedAt
			record.Policy.LastRenewalErrorCode = previous.Policy.LastRenewalErrorCode
			record.Policy.IssuerStatus = previous.Policy.IssuerStatus
			record.Policy.ClockStatus = previous.Policy.ClockStatus
			record.Policy.RecoveryState = previous.Policy.RecoveryState
			record.Policy.PostExpiryDenied = previous.Policy.PostExpiryDenied
			record.Policy.MeasuredSessionBoundSeconds = previous.Policy.MeasuredSessionBoundSeconds
		} else {
			record.Policy.EligibilityRevision = 1
		}
		saved = record.Policy
		return record, nil
	})
	if err != nil {
		return WorkloadRelationshipPolicy{}, err
	}
	return saved, nil
}

// RemoveWorkloadRelationshipEligibility stops future issuance and renewal for
// the named peer while keeping existing admission state intact until expiry or
// explicit revocation.
func RemoveWorkloadRelationshipEligibility(workspaceRoot, peerRef, reason string, now time.Time) (WorkloadRelationshipPolicy, error) {
	if now.IsZero() {
		return WorkloadRelationshipPolicy{}, ErrWorkloadClockOutage
	}
	now = now.UTC()
	if strings.TrimSpace(reason) == "" {
		reason = "eligibility_removed"
	}
	var updated WorkloadRelationshipPolicy
	err := mutateWorkloadRelationship(workspaceRoot, peerRef, func(previous *workloadRelationshipRecord) (workloadRelationshipRecord, error) {
		if previous == nil {
			return workloadRelationshipRecord{}, ErrWorkloadEligibilityDenied
		}
		next := *previous
		if next.Policy.EligibilityRevision == ^uint64(0) {
			return workloadRelationshipRecord{}, errors.New("localevidence: workload eligibility revision exhausted")
		}
		next.Policy.Eligible = false
		next.Policy.EligibilityRevision++
		next.Policy.EligibilityReason = strings.TrimSpace(reason)
		next.Policy.LastRenewalErrorCode = "eligibility_denied"
		if now.After(next.Policy.UpdatedAt) {
			next.Policy.UpdatedAt = now
		}
		updated = next.Policy
		return next, nil
	})
	if err != nil {
		return WorkloadRelationshipPolicy{}, err
	}
	return updated, nil
}

// RecoverWorkloadRelationshipKey binds a new Cloud-held public key after a
// credential revocation or retirement, preserving the existing Eligible flag and
// refusing permanently revoked keys or relationships with nothing to recover.
func RecoverWorkloadRelationshipKey(workspaceRoot, peerRef, workloadID, newKeyFingerprint string, now time.Time) (WorkloadRelationshipPolicy, error) {
	if now.IsZero() {
		return WorkloadRelationshipPolicy{}, ErrWorkloadClockOutage
	}
	now = now.UTC()
	if !validWorkloadSHA256Hex(newKeyFingerprint) {
		return WorkloadRelationshipPolicy{}, ErrWorkloadRecoveryKeyRejected
	}
	peerRecord, err := loadOptionalWorkloadPeer(workspaceRoot, peerRef)
	if err != nil {
		return WorkloadRelationshipPolicy{}, err
	}
	if peerRecord == nil || (!peerRecord.Revoked && len(peerRecord.RevokedKeys) == 0 && len(peerRecord.RetiredCertificates) == 0 && len(peerRecord.RetiredCertificateExpiry) == 0) {
		return WorkloadRelationshipPolicy{}, ErrWorkloadRecoveryNotRequired
	}
	if slices.Contains(peerRecord.RevokedKeys, newKeyFingerprint) {
		return WorkloadRelationshipPolicy{}, ErrWorkloadRecoveryKeyRejected
	}
	var updated WorkloadRelationshipPolicy
	err = mutateWorkloadRelationship(workspaceRoot, peerRef, func(previous *workloadRelationshipRecord) (workloadRelationshipRecord, error) {
		if previous == nil {
			return workloadRelationshipRecord{}, ErrWorkloadEligibilityDenied
		}
		if previous.Policy.WorkloadID != workloadID {
			return workloadRelationshipRecord{}, ErrWorkloadIdentityMismatch
		}
		if previous.Policy.EligibilityRevision == ^uint64(0) {
			return workloadRelationshipRecord{}, errors.New("localevidence: workload eligibility revision exhausted")
		}
		if previous.Policy.ExpectedKeyFingerprint == newKeyFingerprint && peerRecord.Revoked {
			return workloadRelationshipRecord{}, ErrWorkloadRecoveryKeyRejected
		}
		next := *previous
		next.Policy.ExpectedKeyFingerprint = newKeyFingerprint
		next.Policy.EligibilityRevision++
		next.Policy.EligibilityReason = "credential_retirement_recovered"
		next.Policy.RecoveryState = "recovered"
		next.Policy.LastRenewalErrorCode = ""
		if now.After(next.Policy.UpdatedAt) {
			next.Policy.UpdatedAt = now
		}
		updated = next.Policy
		return next, nil
	})
	if err != nil {
		return WorkloadRelationshipPolicy{}, err
	}
	return updated, nil
}

// RenewWorkloadRelationship atomically validates identity, scope, replay
// protection, clock health, eligibility, and issuer availability under a single
// custody lock, consuming the nonce before calling the issuer/admission so
// concurrent or replayed renewals can never admit or retire certificates.
func RenewWorkloadRelationship(ctx context.Context, workspaceRoot string, req WorkloadRenewalRequest, issuer WorkloadCertificateIssuer, now time.Time) (WorkloadRenewalResult, error) {
	if ctx == nil {
		return WorkloadRenewalResult{}, errors.New("localevidence: workload renewal requires a context")
	}
	if err := ctx.Err(); err != nil {
		return WorkloadRenewalResult{}, err
	}
	if !validWorkloadPeerRef(req.PeerRef) || strings.TrimSpace(req.WorkloadID) == "" ||
		!validWorkloadPeerRef(req.ServiceRef) || !validWorkloadPeerRef(req.EdgeSiteRef) ||
		strings.TrimSpace(req.Audience) == "" || !validWorkloadServerName(req.ServerName) || !validWorkloadNonce(req.Nonce) {
		return WorkloadRenewalResult{}, ErrWorkloadIdentityMismatch
	}
	csr, err := validateWorkloadPeerCSR(req.PeerRef, req.CSRPEM)
	if err != nil {
		return WorkloadRenewalResult{}, ErrWorkloadIdentityMismatch
	}
	keySum := sha256.Sum256(csr.RawSubjectPublicKeyInfo)
	keyFingerprint := hex.EncodeToString(keySum[:])

	path, err := workloadRelationshipPath(req.PeerRef)
	if err != nil {
		return WorkloadRenewalResult{}, err
	}
	rootFS, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return WorkloadRenewalResult{}, err
	}
	defer rootFS.Close()
	tx, err := rootFS.BeginTransaction()
	if err != nil {
		return WorkloadRenewalResult{}, err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(workloadRelationshipDirectory)
	if err != nil {
		return WorkloadRenewalResult{}, err
	}
	defer lock.Release()

	existing, err := readWorkloadRelationship(workspaceRoot, tx, req.PeerRef)
	if err != nil {
		return WorkloadRenewalResult{}, err
	}
	policy := existing.Policy

	// 1. Verify exact identity, scope, server name, and key binding before
	// mutating any signed custody state.
	if req.WorkloadID != policy.WorkloadID || req.ServiceRef != policy.ServiceRef ||
		req.EdgeSiteRef != policy.EdgeSiteRef || req.Audience != policy.Audience ||
		req.ServerName != policy.ServerName || keyFingerprint != policy.ExpectedKeyFingerprint {
		return WorkloadRenewalResult{}, ErrWorkloadIdentityMismatch
	}

	// 2. Reject replayed nonces before any clock/issuer/admission side effect.
	if slices.Contains(existing.ConsumedNonces, req.Nonce) {
		return WorkloadRenewalResult{}, ErrWorkloadRenewalReplayed
	}

	// 3. Enforce clock sanity (skew, no rollback, bounded forward jump) without
	// rolling back UpdatedAt on failure.
	if now.IsZero() {
		_ = persistWorkloadRelationshipLocked(workspaceRoot, tx, path, withFailure(existing, "skewed", "", "clock_outage", existing.Policy.UpdatedAt))
		return WorkloadRenewalResult{}, ErrWorkloadClockOutage
	}
	now = now.UTC()
	if req.ClockOffset > WorkloadRelationshipMaxClockSkew || req.ClockOffset < -WorkloadRelationshipMaxClockSkew ||
		now.Before(policy.UpdatedAt) || now.Sub(policy.UpdatedAt) > WorkloadRelationshipMaxForwardJump {
		_ = persistWorkloadRelationshipLocked(workspaceRoot, tx, path, withFailure(existing, "skewed", "", "clock_outage", policy.UpdatedAt))
		return WorkloadRenewalResult{}, ErrWorkloadClockOutage
	}

	// 4. Enforce eligibility and revocation state.
	if !policy.Eligible {
		_ = persistWorkloadRelationshipLocked(workspaceRoot, tx, path, withFailure(existing, "synchronized", "", "eligibility_denied", now))
		return WorkloadRenewalResult{}, ErrWorkloadEligibilityDenied
	}
	peerRecord, err := loadOptionalWorkloadPeer(workspaceRoot, req.PeerRef)
	if err != nil {
		return WorkloadRenewalResult{}, err
	}
	if peerRecord != nil {
		if slices.Contains(peerRecord.RevokedKeys, keyFingerprint) {
			_ = persistWorkloadRelationshipLocked(workspaceRoot, tx, path, withFailure(existing, "synchronized", "", "key_revoked", now))
			return WorkloadRenewalResult{}, ErrWorkloadEligibilityDenied
		}
		if peerRecord.Revoked && policy.RecoveryState != "recovered" {
			_ = persistWorkloadRelationshipLocked(workspaceRoot, tx, path, withFailure(existing, "synchronized", "", "key_revoked", now))
			return WorkloadRenewalResult{}, ErrWorkloadEligibilityDenied
		}
	}

	// 5. Consume and persist the nonce BEFORE calling issuer or admitting the
	// certificate so a concurrent or replayed request is blocked immediately.
	reserved := existing
	reserved.ConsumedNonces = append(append([]string(nil), existing.ConsumedNonces...), req.Nonce)
	if len(reserved.ConsumedNonces) > maxWorkloadRelationshipNonces {
		reserved.ConsumedNonces = reserved.ConsumedNonces[len(reserved.ConsumedNonces)-maxWorkloadRelationshipNonces:]
	}
	reserved.Policy.ClockStatus = "synchronized"
	reserved.Policy.UpdatedAt = now
	if err := persistWorkloadRelationshipLocked(workspaceRoot, tx, path, reserved); err != nil {
		return WorkloadRenewalResult{}, err
	}

	// 6. Issue and verify the replacement short-lived certificate.
	if issuer == nil {
		issuer = IssueWorkloadPeerCertificate
	}
	certPEM, issueErr := issuer(ctx, workspaceRoot, req.PeerRef, req.CSRPEM, policy.TTLSeconds)
	if issueErr != nil {
		_ = persistWorkloadRelationshipLocked(workspaceRoot, tx, path, withFailure(reserved, "synchronized", "outage", "issuer_outage", now))
		return WorkloadRenewalResult{}, fmt.Errorf("%w: %v", ErrWorkloadIssuerUnavailable, issueErr)
	}
	chain, err := workloadPeerCertificates(certPEM)
	if err != nil {
		_ = persistWorkloadRelationshipLocked(workspaceRoot, tx, path, withFailure(reserved, "synchronized", "outage", "issuer_invalid_certificate", now))
		return WorkloadRenewalResult{}, fmt.Errorf("%w: %v", ErrWorkloadIssuerUnavailable, err)
	}
	if err := verifyWorkloadPeerCertificate(workspaceRoot, chain, req.PeerRef, now); err != nil {
		_ = persistWorkloadRelationshipLocked(workspaceRoot, tx, path, withFailure(reserved, "synchronized", "outage", "issuer_untrusted_certificate", now))
		return WorkloadRenewalResult{}, fmt.Errorf("%w: %v", ErrWorkloadIssuerUnavailable, err)
	}
	leaf := chain[0]
	if !bytes.Equal(leaf.RawSubjectPublicKeyInfo, csr.RawSubjectPublicKeyInfo) ||
		leaf.NotAfter.Sub(leaf.NotBefore) > time.Duration(policy.TTLSeconds)*time.Second ||
		!leaf.NotAfter.After(now) {
		_ = persistWorkloadRelationshipLocked(workspaceRoot, tx, path, withFailure(reserved, "synchronized", "outage", "issuer_ttl_violation", now))
		return WorkloadRenewalResult{}, fmt.Errorf("%w: issued certificate violates bounded TTL or key binding", ErrWorkloadIssuerUnavailable)
	}

	// 7. Admit the newly issued leaf (retiring the previous certificate) and
	// finalize the relationship state, clearing RecoveryState on success so it
	// cannot bypass any future revocation.
	admission := WorkloadPeerAdmission{
		PeerRef:        policy.PeerRef,
		ServiceRef:     policy.ServiceRef,
		EdgeSiteRef:    policy.EdgeSiteRef,
		Audience:       policy.Audience,
		CertificatePEM: string(certPEM),
		ValidUntil:     leaf.NotAfter.UTC(),
	}
	if err := admitWorkloadPeerAt(workspaceRoot, admission, nil, now); err != nil {
		return WorkloadRenewalResult{}, err
	}

	renewedAt := now
	final := reserved
	final.Policy.RenewalCount++
	final.Policy.LastRenewedAt = &renewedAt
	final.Policy.LastRenewalErrorCode = ""
	final.Policy.IssuerStatus = "available"
	final.Policy.ClockStatus = "synchronized"
	final.Policy.RecoveryState = "none"
	final.Policy.UpdatedAt = now
	if err := persistWorkloadRelationshipLocked(workspaceRoot, tx, path, final); err != nil {
		return WorkloadRenewalResult{}, err
	}

	return WorkloadRenewalResult{
		CertificatePEM:         string(certPEM),
		CertificateFingerprint: workloadPeerFingerprint(leaf),
		NotBefore:              leaf.NotBefore.UTC(),
		NotAfter:               leaf.NotAfter.UTC(),
		Policy:                 final.Policy,
	}, nil
}

// AuthorizeWorkloadRelationship checks the client TLS chain against active
// admission and enforces the relationship's bounded established-session window.
func AuthorizeWorkloadRelationship(workspaceRoot string, chain []*x509.Certificate, scope WorkloadPeerScope, sessionStartedAt, now time.Time) (WorkloadSessionAuthorization, error) {
	if now.IsZero() || sessionStartedAt.IsZero() || sessionStartedAt.After(now.Add(WorkloadRelationshipMaxClockSkew)) {
		return WorkloadSessionAuthorization{}, ErrWorkloadClockOutage
	}
	now = now.UTC()
	sessionStartedAt = sessionStartedAt.UTC()
	peerRef, err := AuthorizeWorkloadPeer(workspaceRoot, chain, scope, now)
	if err != nil {
		return WorkloadSessionAuthorization{}, err
	}
	rel, err := loadWorkloadRelationship(workspaceRoot, peerRef)
	if err != nil {
		return WorkloadSessionAuthorization{}, err
	}
	policy := rel.Policy
	if policy.ServiceRef != scope.ServiceRef || policy.EdgeSiteRef != scope.EdgeSiteRef || policy.Audience != scope.Audience {
		return WorkloadSessionAuthorization{}, ErrWorkloadIdentityMismatch
	}
	maxSess := time.Duration(policy.MaxSessionSeconds) * time.Second
	if maxSess <= 0 || maxSess > time.Duration(WorkloadRelationshipMaxSessionSeconds)*time.Second {
		maxSess = time.Duration(WorkloadRelationshipMaxSessionSeconds) * time.Second
	}
	deadline := sessionStartedAt.Add(maxSess)
	if chain[0].NotAfter.UTC().Before(deadline) {
		deadline = chain[0].NotAfter.UTC()
	}
	if !now.Before(deadline) {
		return WorkloadSessionAuthorization{}, ErrWorkloadSessionExpired
	}
	return WorkloadSessionAuthorization{
		PeerRef:         peerRef,
		WorkloadID:      policy.WorkloadID,
		SessionDeadline: deadline,
	}, nil
}

// RecordWorkloadPostExpiryObservation records measured post-expiry denial and
// established-session lifetime observation for qualification evidence.
func RecordWorkloadPostExpiryObservation(workspaceRoot, peerRef string, postExpiryDenied bool, measuredSessionBoundSeconds int64, now time.Time) error {
	if now.IsZero() {
		return ErrWorkloadClockOutage
	}
	now = now.UTC()
	return mutateWorkloadRelationship(workspaceRoot, peerRef, func(previous *workloadRelationshipRecord) (workloadRelationshipRecord, error) {
		if previous == nil {
			return workloadRelationshipRecord{}, ErrWorkloadEligibilityDenied
		}
		if measuredSessionBoundSeconds < 0 {
			return workloadRelationshipRecord{}, errors.New("localevidence: measured session bound must be non-negative")
		}
		next := *previous
		denied := postExpiryDenied
		next.Policy.PostExpiryDenied = &denied
		next.Policy.MeasuredSessionBoundSeconds = measuredSessionBoundSeconds
		if now.After(next.Policy.UpdatedAt) {
			next.Policy.UpdatedAt = now
		}
		return next, nil
	})
}

// ObserveWorkloadRelationship emits a read-only, secret-free structured
// qualification evidence document matching kombify.guard-workload-identity/v1.
func ObserveWorkloadRelationship(workspaceRoot, peerRef, authorityVersion, guardVersion string, now time.Time) (WorkloadRelationshipEvidence, error) {
	if now.IsZero() {
		return WorkloadRelationshipEvidence{}, ErrWorkloadClockOutage
	}
	now = now.UTC()
	if strings.TrimSpace(authorityVersion) == "" {
		authorityVersion = WorkloadRelationshipAuthorityVersion
	}
	if strings.TrimSpace(guardVersion) == "" {
		guardVersion = "unknown"
	}
	rel, err := loadWorkloadRelationship(workspaceRoot, peerRef)
	if err != nil {
		return WorkloadRelationshipEvidence{}, err
	}
	p := rel.Policy
	peerRecord, err := loadOptionalWorkloadPeer(workspaceRoot, peerRef)
	if err != nil {
		return WorkloadRelationshipEvidence{}, err
	}

	ev := WorkloadRelationshipEvidence{
		SchemaVersion:               WorkloadRelationshipEvidenceSchema,
		AuthorityVersion:            authorityVersion,
		GuardVersion:                guardVersion,
		Profile:                     WorkloadProfile{OS: "linux", Init: "systemd", Transport: "mtls-loopback"},
		PeerRef:                     p.PeerRef,
		WorkloadID:                  p.WorkloadID,
		ServiceRef:                  p.ServiceRef,
		EdgeSiteRef:                 p.EdgeSiteRef,
		Audience:                    p.Audience,
		Scope:                       p.Scope,
		ServerName:                  p.ServerName,
		Eligible:                    p.Eligible,
		EligibilityRevision:         p.EligibilityRevision,
		EligibilityReason:           p.EligibilityReason,
		TTLSeconds:                  p.TTLSeconds,
		RenewBeforeSeconds:          p.RenewBeforeSeconds,
		MaxSessionSeconds:           p.MaxSessionSeconds,
		RenewalCount:                p.RenewalCount,
		LastRenewedAt:               p.LastRenewedAt,
		LastRenewalErrorCode:        p.LastRenewalErrorCode,
		IssuerStatus:                p.IssuerStatus,
		ClockStatus:                 p.ClockStatus,
		RecoveryState:               p.RecoveryState,
		PostExpiryDenied:            p.PostExpiryDenied,
		MeasuredSessionBoundSeconds: p.MeasuredSessionBoundSeconds,
		ObservedAt:                  now,
		ExpiresAt:                   now.Add(WorkloadRelationshipMaxEvidenceAge),
	}
	if !now.Before(p.UpdatedAt) {
		ev.EvidenceAgeSeconds = int64(now.Sub(p.UpdatedAt) / time.Second)
	}
	var unknowns []string
	if guardVersion == "unknown" {
		unknowns = append(unknowns, "guard_version_unreported")
	}
	if peerRecord == nil {
		ev.RenewalState = "unknown"
		unknowns = append(unknowns, "active_admission_absent")
	} else {
		ev.RetiredCertificateCount = len(peerRecord.RetiredCertificates) + len(peerRecord.RetiredCertificateExpiry)
		ev.RevokedKeyCount = len(peerRecord.RevokedKeys)
		chain, chainErr := workloadPeerCertificates([]byte(peerRecord.Admission.CertificatePEM))
		if chainErr != nil || len(chain) == 0 {
			ev.RenewalState = "unknown"
			unknowns = append(unknowns, "active_certificate_unreadable")
		} else {
			leaf := chain[0]
			notBefore, notAfter := leaf.NotBefore.UTC(), leaf.NotAfter.UTC()
			ev.ActiveCertificateDigest = "sha256:" + workloadPeerFingerprint(leaf)
			ev.NotBefore = &notBefore
			ev.NotAfter = &notAfter
			switch {
			case peerRecord.Revoked:
				ev.RenewalState = "revoked"
			case !now.Before(notAfter) || !now.Before(peerRecord.Admission.ValidUntil):
				ev.RenewalState = "expired"
			case p.ClockStatus == "skewed":
				ev.RenewalState = "clock_outage"
			case p.IssuerStatus == "outage":
				ev.RenewalState = "issuer_outage"
			case !p.Eligible:
				ev.RenewalState = "renewal_stopped"
			default:
				ev.RenewalState = "healthy"
			}
		}
	}
	if p.IssuerStatus == "" || p.IssuerStatus == "unknown" {
		unknowns = append(unknowns, "issuer_status_unobserved")
	}
	if p.PostExpiryDenied == nil {
		unknowns = append(unknowns, "post_expiry_denial_unmeasured")
	}
	if p.MeasuredSessionBoundSeconds <= 0 {
		unknowns = append(unknowns, "established_session_bound_unmeasured")
	}
	ev.Unknowns = unknowns
	return ev, nil
}

func withFailure(base workloadRelationshipRecord, clockStatus, issuerStatus, errorCode string, ts time.Time) workloadRelationshipRecord {
	next := base
	if clockStatus != "" {
		next.Policy.ClockStatus = clockStatus
	}
	if issuerStatus != "" {
		next.Policy.IssuerStatus = issuerStatus
	}
	next.Policy.LastRenewalErrorCode = errorCode
	if ts.After(next.Policy.UpdatedAt) {
		next.Policy.UpdatedAt = ts.UTC()
	}
	return next
}

func validateRelationshipPolicyInput(policy WorkloadRelationshipPolicy) error {
	if !validWorkloadPeerRef(policy.PeerRef) || !validWorkloadPeerRef(policy.ServiceRef) || !validWorkloadPeerRef(policy.EdgeSiteRef) {
		return ErrWorkloadIdentityMismatch
	}
	if strings.TrimSpace(policy.WorkloadID) == "" || len(policy.WorkloadID) > 255 || strings.ContainsAny(policy.WorkloadID, "\r\n\x00") {
		return ErrWorkloadIdentityMismatch
	}
	if !validWorkloadSHA256Hex(policy.ExpectedKeyFingerprint) {
		return ErrWorkloadIdentityMismatch
	}
	if strings.TrimSpace(policy.Audience) == "" || len(policy.Audience) > 512 || strings.TrimSpace(policy.Scope) == "" || len(policy.Scope) > 128 {
		return ErrWorkloadIdentityMismatch
	}
	if !validWorkloadServerName(policy.ServerName) {
		return ErrWorkloadIdentityMismatch
	}
	if policy.TTLSeconds < WorkloadRelationshipMinTTLSeconds || policy.TTLSeconds > WorkloadRelationshipMaxTTLSeconds {
		return errors.New("localevidence: workload relationship TTL is outside [300, 86400] seconds")
	}
	if policy.RenewBeforeSeconds <= 0 || policy.RenewBeforeSeconds >= policy.TTLSeconds {
		return errors.New("localevidence: workload relationship renewBeforeSeconds must be inside (0, ttlSeconds)")
	}
	if policy.MaxSessionSeconds <= 0 || policy.MaxSessionSeconds > WorkloadRelationshipMaxSessionSeconds {
		return errors.New("localevidence: workload relationship maxSessionSeconds must be inside [1, 60]")
	}
	return nil
}

func validWorkloadServerName(value string) bool {
	if value == "" || len(value) > 255 || value != strings.TrimSpace(value) || strings.ContainsAny(value, " \t\r\n\x00/\\") {
		return false
	}
	return true
}

func validWorkloadSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func validWorkloadNonce(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func workloadRelationshipPath(peerRef string) (string, error) {
	if !validWorkloadPeerRef(peerRef) {
		return "", errors.New("localevidence: invalid workload peer reference")
	}
	digest := sha256.Sum256([]byte(peerRef))
	return workloadRelationshipDirectory + "/" + hex.EncodeToString(digest[:]) + ".json", nil
}

func loadOptionalWorkloadPeer(workspaceRoot, peerRef string) (*workloadPeerRecord, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer tx.Close()
	record, err := readWorkloadPeer(workspaceRoot, tx, peerRef)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func loadWorkloadRelationship(workspaceRoot, peerRef string) (workloadRelationshipRecord, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return workloadRelationshipRecord{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return workloadRelationshipRecord{}, err
	}
	defer tx.Close()
	return readWorkloadRelationship(workspaceRoot, tx, peerRef)
}

func readWorkloadRelationship(workspaceRoot string, tx *confinedfs.Transaction, peerRef string) (workloadRelationshipRecord, error) {
	var record workloadRelationshipRecord
	path, err := workloadRelationshipPath(peerRef)
	if err != nil {
		return record, err
	}
	raw, _, err := tx.ReadStableBounded(path, 128<<10)
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, err
	}
	signature := record.Signature
	record.Signature = OwnerPolicyStateSignature{}
	canonical, err := json.Marshal(record)
	record.Signature = signature
	if err != nil {
		return record, err
	}
	if err := verifyOwnerRestore(workspaceRoot, canonical, workloadRelationshipSignatureDomain, signature.OwnerRef, signature.KeyID, signature.Value, "workload relationship"); err != nil {
		return record, err
	}
	if record.Policy.SchemaVersion != WorkloadRelationshipSchema || record.Policy.PeerRef != peerRef {
		return record, errors.New("localevidence: workload relationship record identity mismatch")
	}
	return record, nil
}

func mutateWorkloadRelationship(workspaceRoot, peerRef string, change func(*workloadRelationshipRecord) (workloadRelationshipRecord, error)) error {
	path, err := workloadRelationshipPath(peerRef)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(workloadRelationshipDirectory)
	if err != nil {
		return err
	}
	defer lock.Release()
	old, err := readWorkloadRelationship(workspaceRoot, tx, peerRef)
	var previous *workloadRelationshipRecord
	if err == nil {
		previous = &old
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	record, err := change(previous)
	if err != nil {
		return err
	}
	return persistWorkloadRelationshipLocked(workspaceRoot, tx, path, record)
}

func persistWorkloadRelationshipLocked(workspaceRoot string, tx *confinedfs.Transaction, path string, record workloadRelationshipRecord) error {
	record.Signature = OwnerPolicyStateSignature{}
	canonical, err := json.Marshal(record)
	if err != nil {
		return err
	}
	value, ownerRef, keyID, err := signOwnerRestore(workspaceRoot, canonical, workloadRelationshipSignatureDomain, "workload relationship")
	if err != nil {
		return err
	}
	record.Signature = OwnerPolicyStateSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(encoded) > 128<<10 {
		return errors.New("localevidence: workload relationship custody exceeds bounded readback size")
	}
	absolute, err := confinedCustodyPath(workspaceRoot, path)
	if err != nil {
		return err
	}
	if err := writePrivateJSON(absolute, record); err != nil {
		return fmt.Errorf("persist workload relationship: %w", err)
	}
	_, err = tx.SyncDirectory(workloadRelationshipDirectory)
	return err
}
