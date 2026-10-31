package runtimelease

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const (
	// SnapshotVersion is the exact signed runtime-lease snapshot format.
	SnapshotVersion = "runtimelease-snapshot/v1"
	// SnapshotAlgorithm is the only signature algorithm accepted by v1.
	SnapshotAlgorithm = "Ed25519"
	// MaximumSnapshotTTL limits how long a projection can hide a later lease
	// revision or cancellation while its authority is unreachable.
	MaximumSnapshotTTL = 5 * time.Minute
	// MaximumSnapshotGrace bounds authority-signed offline fallback. Authorities
	// may issue a shorter window or no grace at all.
	MaximumSnapshotGrace = 5 * time.Minute

	snapshotSignatureDomain = "kombify/runtimelease/snapshot/v1\x00"
)

// Snapshot validation, key-resolution, and signature errors.
var (
	ErrSnapshotInvalid   = errors.New("runtimelease: invalid snapshot")
	ErrSnapshotExpired   = errors.New("runtimelease: snapshot expired")
	ErrSnapshotSignature = errors.New("runtimelease: snapshot signature invalid")
	ErrSnapshotKey       = errors.New("runtimelease: snapshot key unavailable")
	ErrSnapshotGrace     = errors.New("runtimelease: snapshot grace unavailable")
)

// Snapshot is a versioned, audience-bound, asymmetrically signed lease
// projection. GraceUntil is authority-issued and may extend ExpiresAt by at
// most MaximumSnapshotGrace; consumers cannot choose their own grace.
type Snapshot struct {
	Version    string     `json:"version"`
	Algorithm  string     `json:"algorithm"`
	KeyID      string     `json:"key_id"`
	Issuer     string     `json:"issuer"`
	Audience   string     `json:"audience"`
	Lease      Lease      `json:"lease"`
	IssuedAt   time.Time  `json:"issued_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	GraceUntil *time.Time `json:"grace_until,omitempty"`
	Signature  string     `json:"signature"`
}

// SnapshotBinding is the exact authority, consumer, and runtime identity a
// caller expects before it may use a verified snapshot.
type SnapshotBinding struct {
	Issuer               string
	Audience             string
	TenantID             string
	OwnerID              string
	LeaseID              LeaseID
	LeaseRevision        uint64
	ServerID             RuntimeServerID
	ResourceGenerationID ResourceGenerationID
}

// Validate rejects an incomplete expected snapshot binding.
func (binding SnapshotBinding) Validate() error {
	if !validOpaqueID(binding.Issuer) || !validOpaqueID(binding.Audience) ||
		!validOpaqueID(binding.TenantID) || !validOpaqueID(binding.OwnerID) ||
		!validOpaqueID(string(binding.LeaseID)) || binding.LeaseRevision == 0 ||
		!validOpaqueID(string(binding.ServerID)) ||
		!validResourceGenerationID(binding.ResourceGenerationID) {
		return ErrSnapshotInvalid
	}
	return nil
}

// SnapshotVerificationKey is one immutable authority verification-key record.
// NotBefore and NotAfter bound signing time; Revoked keys fail closed even for
// otherwise valid snapshots.
type SnapshotVerificationKey struct {
	KeyID     string
	Issuer    string
	PublicKey ed25519.PublicKey
	NotBefore time.Time
	NotAfter  time.Time
	Revoked   bool
}

// SnapshotKeyResolver resolves a public verification key by signed key ID.
// Implementations must be concurrency-safe and return an independent key copy.
type SnapshotKeyResolver interface {
	ResolveSnapshotVerificationKey(keyID string) (SnapshotVerificationKey, error)
}

// SnapshotKeySet is an immutable, concurrency-safe in-memory key resolver.
// Rotation constructs a new key set and atomically swaps the owning pointer;
// callers cannot mutate the set's private map or stored key bytes.
type SnapshotKeySet struct {
	keys map[string]SnapshotVerificationKey
}

// NewSnapshotKeySet validates and copies one or more authority verification
// keys. Duplicate key IDs are rejected.
func NewSnapshotKeySet(keys []SnapshotVerificationKey) (*SnapshotKeySet, error) {
	if len(keys) == 0 || len(keys) > 64 {
		return nil, ErrSnapshotKey
	}
	set := &SnapshotKeySet{keys: make(map[string]SnapshotVerificationKey, len(keys))}
	for _, key := range keys {
		key = normalizeVerificationKey(key)
		if err := validateVerificationKey(key); err != nil {
			return nil, err
		}
		if _, exists := set.keys[key.KeyID]; exists {
			return nil, ErrSnapshotKey
		}
		set.keys[key.KeyID] = cloneVerificationKey(key)
	}
	return set, nil
}

// ResolveSnapshotVerificationKey returns an independent copy of one key.
func (set *SnapshotKeySet) ResolveSnapshotVerificationKey(keyID string) (SnapshotVerificationKey, error) {
	if set == nil {
		return SnapshotVerificationKey{}, ErrSnapshotKey
	}
	key, ok := set.keys[strings.TrimSpace(keyID)]
	if !ok {
		return SnapshotVerificationKey{}, ErrSnapshotKey
	}
	return cloneVerificationKey(key), nil
}

// SignSnapshot seals one canonical snapshot with an authority-owned Ed25519
// private key. The key ID, issuer, audience, and complete runtime binding are
// signed; private signing material never leaves the lease authority.
func SignSnapshot(snapshot Snapshot, keyID string, privateKey ed25519.PrivateKey) (Snapshot, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Snapshot{}, ErrSnapshotKey
	}
	snapshot.Version = SnapshotVersion
	snapshot.Algorithm = SnapshotAlgorithm
	snapshot.KeyID = strings.TrimSpace(keyID)
	snapshot.Signature = ""
	snapshot = normalizeSnapshot(snapshot)
	if err := validateSnapshotShape(snapshot); err != nil {
		return Snapshot{}, err
	}
	payload, err := snapshotSigningPayload(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return snapshot, nil
}

// VerifySnapshot verifies a snapshot for one exact expected binding and
// rejects it at ExpiresAt.
func VerifySnapshot(snapshot Snapshot, resolver SnapshotKeyResolver, expected SnapshotBinding, now time.Time) (*Lease, error) {
	return verifySnapshot(snapshot, resolver, expected, now, false)
}

// VerifySnapshotFallback verifies a snapshot against its signed, bounded
// GraceUntil only after FallbackEligible classifies the live authority failure
// as an outage. A caller cannot turn an authoritative rejection into offline
// grace by invoking snapshot verification directly.
func VerifySnapshotFallback(snapshot Snapshot, resolver SnapshotKeyResolver, expected SnapshotBinding, now time.Time, authorityErr error) (*Lease, error) {
	if !FallbackEligible(authorityErr) {
		return nil, ErrSnapshotGrace
	}
	return verifySnapshot(snapshot, resolver, expected, now, true)
}

func verifySnapshot(snapshot Snapshot, resolver SnapshotKeyResolver, expected SnapshotBinding, now time.Time, allowGrace bool) (*Lease, error) {
	if nilSnapshotKeyResolver(resolver) {
		return nil, ErrSnapshotKey
	}
	if expected.Validate() != nil {
		return nil, ErrSnapshotInvalid
	}
	if err := validateSnapshotShape(snapshot); err != nil {
		return nil, err
	}
	if snapshot.Issuer != expected.Issuer || snapshot.Audience != expected.Audience ||
		snapshot.Lease.TenantID != expected.TenantID || snapshot.Lease.OwnerID != expected.OwnerID ||
		snapshot.Lease.ID != expected.LeaseID || snapshot.Lease.Revision != expected.LeaseRevision ||
		snapshot.Lease.ServerID != expected.ServerID ||
		snapshot.Lease.ResourceGenerationID != expected.ResourceGenerationID {
		return nil, ErrBindingMismatch
	}
	key, err := resolver.ResolveSnapshotVerificationKey(snapshot.KeyID)
	if err != nil {
		return nil, ErrSnapshotKey
	}
	key = normalizeVerificationKey(key)
	if err := validateVerificationKey(key); err != nil || key.KeyID != snapshot.KeyID ||
		key.Issuer != snapshot.Issuer || key.Revoked || snapshot.IssuedAt.Before(key.NotBefore) ||
		!snapshot.IssuedAt.Before(key.NotAfter) {
		return nil, ErrSnapshotKey
	}
	if len(snapshot.Signature) != base64.RawURLEncoding.EncodedLen(ed25519.SignatureSize) {
		return nil, ErrSnapshotSignature
	}
	presented, err := base64.RawURLEncoding.Strict().DecodeString(snapshot.Signature)
	if err != nil || len(presented) != ed25519.SignatureSize {
		return nil, ErrSnapshotSignature
	}
	payload, err := snapshotSigningPayload(snapshot)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(key.PublicKey, payload, presented) {
		return nil, ErrSnapshotSignature
	}
	now = canonicalTime(now)
	if snapshot.IssuedAt.After(now) {
		return nil, ErrSnapshotInvalid
	}
	if !now.Before(snapshot.ExpiresAt) {
		if !allowGrace {
			return nil, ErrSnapshotExpired
		}
		if snapshot.GraceUntil == nil || !now.Before(*snapshot.GraceUntil) {
			return nil, ErrSnapshotGrace
		}
	}
	if err := snapshot.Lease.Validate(now); err != nil {
		return nil, err
	}
	lease := snapshot.Lease
	return &lease, nil
}

func nilSnapshotKeyResolver(resolver SnapshotKeyResolver) bool {
	if resolver == nil {
		return true
	}
	value := reflect.ValueOf(resolver)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func snapshotSigningPayload(snapshot Snapshot) ([]byte, error) {
	unsigned := snapshot
	unsigned.Signature = ""
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("runtimelease: marshal snapshot: %w", err)
	}
	return append([]byte(snapshotSignatureDomain), payload...), nil
}

func validateSnapshotShape(snapshot Snapshot) error {
	if snapshot.Version != SnapshotVersion || snapshot.Algorithm != SnapshotAlgorithm ||
		!validOpaqueID(snapshot.KeyID) || !validOpaqueID(snapshot.Issuer) ||
		!validOpaqueID(snapshot.Audience) ||
		snapshot.IssuedAt.IsZero() || snapshot.ExpiresAt.IsZero() ||
		!isCanonicalTime(snapshot.IssuedAt) || !isCanonicalTime(snapshot.ExpiresAt) ||
		!snapshot.ExpiresAt.After(snapshot.IssuedAt) ||
		snapshot.ExpiresAt.After(snapshot.IssuedAt.Add(MaximumSnapshotTTL)) {
		return ErrSnapshotInvalid
	}
	if err := snapshot.Lease.validateShape(); err != nil || !leaseTimesCanonical(snapshot.Lease) {
		return ErrSnapshotInvalid
	}
	if snapshot.IssuedAt.Before(snapshot.Lease.ValidFrom) ||
		!snapshot.ExpiresAt.After(snapshot.Lease.ValidFrom) ||
		snapshot.ExpiresAt.After(snapshot.Lease.ValidUntil) {
		return ErrSnapshotInvalid
	}
	if snapshot.Lease.CancelledAt != nil && snapshot.ExpiresAt.After(*snapshot.Lease.CancelledAt) {
		return ErrSnapshotInvalid
	}
	if snapshot.GraceUntil != nil {
		if !isCanonicalTime(*snapshot.GraceUntil) || !snapshot.GraceUntil.After(snapshot.ExpiresAt) ||
			snapshot.GraceUntil.After(snapshot.ExpiresAt.Add(MaximumSnapshotGrace)) ||
			snapshot.GraceUntil.After(snapshot.Lease.ValidUntil) ||
			(snapshot.Lease.CancelledAt != nil && snapshot.GraceUntil.After(*snapshot.Lease.CancelledAt)) {
			return ErrSnapshotInvalid
		}
	}
	return nil
}

func normalizeSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Version = strings.TrimSpace(snapshot.Version)
	snapshot.Algorithm = strings.TrimSpace(snapshot.Algorithm)
	snapshot.KeyID = strings.TrimSpace(snapshot.KeyID)
	snapshot.Issuer = strings.TrimSpace(snapshot.Issuer)
	snapshot.Audience = strings.TrimSpace(snapshot.Audience)
	snapshot.IssuedAt = canonicalTime(snapshot.IssuedAt)
	snapshot.ExpiresAt = canonicalTime(snapshot.ExpiresAt)
	snapshot.Lease = normalizeLeaseTimes(snapshot.Lease)
	if snapshot.GraceUntil != nil {
		grace := canonicalTime(*snapshot.GraceUntil)
		snapshot.GraceUntil = &grace
	}
	return snapshot
}

func normalizeVerificationKey(key SnapshotVerificationKey) SnapshotVerificationKey {
	key.KeyID = strings.TrimSpace(key.KeyID)
	key.Issuer = strings.TrimSpace(key.Issuer)
	key.NotBefore = canonicalTime(key.NotBefore)
	key.NotAfter = canonicalTime(key.NotAfter)
	return key
}

func validateVerificationKey(key SnapshotVerificationKey) error {
	if !validOpaqueID(key.KeyID) || !validOpaqueID(key.Issuer) ||
		len(key.PublicKey) != ed25519.PublicKeySize || !isCanonicalTime(key.NotBefore) ||
		!isCanonicalTime(key.NotAfter) || !key.NotAfter.After(key.NotBefore) {
		return ErrSnapshotKey
	}
	return nil
}

func cloneVerificationKey(key SnapshotVerificationKey) SnapshotVerificationKey {
	key.PublicKey = append(ed25519.PublicKey(nil), key.PublicKey...)
	return key
}

func normalizeLeaseTimes(lease Lease) Lease {
	lease.ValidFrom = canonicalTime(lease.ValidFrom)
	lease.ValidUntil = canonicalTime(lease.ValidUntil)
	if lease.RenewedAt != nil {
		renewed := canonicalTime(*lease.RenewedAt)
		lease.RenewedAt = &renewed
	}
	if lease.CancelledAt != nil {
		cancelled := canonicalTime(*lease.CancelledAt)
		lease.CancelledAt = &cancelled
	}
	return lease
}

func leaseTimesCanonical(lease Lease) bool {
	if !isCanonicalTime(lease.ValidFrom) || !isCanonicalTime(lease.ValidUntil) {
		return false
	}
	if lease.RenewedAt != nil && !isCanonicalTime(*lease.RenewedAt) {
		return false
	}
	return lease.CancelledAt == nil || isCanonicalTime(*lease.CancelledAt)
}

func canonicalTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.Round(0).UTC()
}

func isCanonicalTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999 && value == canonicalTime(value)
}
