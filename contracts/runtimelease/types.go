package runtimelease

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaximumWireRevision is the largest exact JSON integer accepted for a lease
// revision. It is safe in PostgreSQL BIGINT and IEEE-754/JavaScript consumers.
const MaximumWireRevision uint64 = 1<<53 - 1

// LeaseID is an opaque, stable runtime-lease identifier.
type LeaseID string

// RuntimeServerID is the opaque identity of a durable RuntimeServer aggregate.
// It remains stable when the bound provider resource is replaced.
type RuntimeServerID string

// ResourceGenerationID is the canonical lowercase UUID of one exact,
// replaceable resource generation bound to a RuntimeServer.
type ResourceGenerationID string

// DesiredState is the caller-owned requested runtime state.
type DesiredState string

// Supported desired runtime states.
const (
	DesiredStateRunning DesiredState = "running"
	DesiredStateStopped DesiredState = "stopped"
	DesiredStateAbsent  DesiredState = "absent"
)

// Lease validation errors.
var (
	ErrInvalidLease      = errors.New("runtimelease: invalid lease")
	ErrLeaseNotYetValid  = errors.New("runtimelease: lease not yet valid")
	ErrLeaseExpired      = errors.New("runtimelease: lease expired")
	ErrLeaseCancelled    = errors.New("runtimelease: lease cancelled")
	ErrBindingMismatch   = errors.New("runtimelease: runtime binding mismatch")
	ErrRevisionMismatch  = errors.New("runtimelease: lease revision mismatch")
	ErrInvalidEnrollment = errors.New("runtimelease: invalid enrollment")
)

// Stable validation reason codes returned over the wire.
const (
	ReasonLeaseCancelled   = "lease_cancelled"
	ReasonLeaseNotYetValid = "lease_not_yet_valid"
	ReasonLeaseExpired     = "lease_expired"
	ReasonBindingMismatch  = "binding_mismatch"
	ReasonRevisionMismatch = "revision_mismatch"
	ReasonInvalidLease     = "invalid_lease"
)

// Lease is the secret-free runtime-lease projection. TenantID and OwnerID
// express ownership; ServerID identifies the durable RuntimeServer;
// ResourceGenerationID fences the exact replaceable resource generation.
// Revision is the authority-issued compare-and-swap revision and starts at 1.
// The projection intentionally carries no provider, inventory, health,
// connection, cleanup, billing, or provisioning state.
type Lease struct {
	ID                   LeaseID              `json:"id"`
	Revision             uint64               `json:"revision"`
	TenantID             string               `json:"tenant_id"`
	OwnerID              string               `json:"owner_id"`
	ServerID             RuntimeServerID      `json:"server_id"`
	ResourceGenerationID ResourceGenerationID `json:"resource_generation_id"`
	DesiredState         DesiredState         `json:"desired_state"`
	ValidFrom            time.Time            `json:"valid_from"`
	ValidUntil           time.Time            `json:"valid_until"`
	RenewedAt            *time.Time           `json:"renewed_at,omitempty"`
	CancelledAt          *time.Time           `json:"cancelled_at,omitempty"`
}

// ResolveQuery selects one lease within an exact tenant and responsible owner.
type ResolveQuery struct {
	TenantID string  `json:"tenant_id"`
	OwnerID  string  `json:"owner_id"`
	LeaseID  LeaseID `json:"lease_id"`
}

// ValidationCheck requests validation of an exact owner, lease revision, and
// runtime binding. It cannot ask the lease authority to validate provider
// state.
type ValidationCheck struct {
	TenantID             string               `json:"tenant_id"`
	OwnerID              string               `json:"owner_id"`
	LeaseID              LeaseID              `json:"lease_id"`
	LeaseRevision        uint64               `json:"lease_revision"`
	ServerID             RuntimeServerID      `json:"server_id"`
	ResourceGenerationID ResourceGenerationID `json:"resource_generation_id"`
}

// ValidationResult reports the lease authority's validity decision. It does
// not report provider, cleanup, connection, health, or inventory state.
type ValidationResult struct {
	Valid    bool      `json:"valid"`
	Lease    *Lease    `json:"lease,omitempty"`
	Snapshot *Snapshot `json:"snapshot,omitempty"`
	Reason   string    `json:"reason,omitempty"`
}

// EnrollmentRequest binds runtime enrollment to an exact lease revision,
// durable RuntimeServer, and resource generation. IdempotencyKey is scoped and
// persisted by the owning authority; it is not a provider idempotency key.
type EnrollmentRequest struct {
	TenantID             string               `json:"tenant_id"`
	OwnerID              string               `json:"owner_id"`
	LeaseID              LeaseID              `json:"lease_id"`
	LeaseRevision        uint64               `json:"lease_revision"`
	ServerID             RuntimeServerID      `json:"server_id"`
	ResourceGenerationID ResourceGenerationID `json:"resource_generation_id"`
	IdempotencyKey       string               `json:"idempotency_key"`
}

// EnrollmentResult acknowledges the exact identities accepted for runtime
// enrollment. It is not a provider receipt or an observed-state projection.
type EnrollmentResult struct {
	TenantID             string               `json:"tenant_id"`
	OwnerID              string               `json:"owner_id"`
	LeaseID              LeaseID              `json:"lease_id"`
	LeaseRevision        uint64               `json:"lease_revision"`
	ServerID             RuntimeServerID      `json:"server_id"`
	ResourceGenerationID ResourceGenerationID `json:"resource_generation_id"`
}

// Resolver resolves and validates leases through their owning authority.
type Resolver interface {
	Resolve(ctx context.Context, query ResolveQuery) (*Lease, error)
	Validate(ctx context.Context, check ValidationCheck) (*ValidationResult, error)
}

// Validate checks the lease contract and its authority-owned validity at now.
func (l Lease) Validate(now time.Time) error {
	if err := l.validateShape(); err != nil {
		return err
	}
	if now.Before(l.ValidFrom) {
		return ErrLeaseNotYetValid
	}
	if l.CancelledAt != nil && !l.CancelledAt.After(now) {
		return ErrLeaseCancelled
	}
	if !now.Before(l.ValidUntil) {
		return ErrLeaseExpired
	}
	return nil
}

func (l Lease) validateShape() error {
	if !validOpaqueID(string(l.ID)) || !validWireRevision(l.Revision) ||
		!validOpaqueID(l.TenantID) || !validOpaqueID(l.OwnerID) ||
		!validOpaqueID(string(l.ServerID)) ||
		!validResourceGenerationID(l.ResourceGenerationID) {
		return ErrInvalidLease
	}
	if l.DesiredState != DesiredStateRunning &&
		l.DesiredState != DesiredStateStopped &&
		l.DesiredState != DesiredStateAbsent {
		return ErrInvalidLease
	}
	if l.ValidFrom.IsZero() || l.ValidUntil.IsZero() || !l.ValidUntil.After(l.ValidFrom) {
		return ErrInvalidLease
	}
	if l.RenewedAt != nil && (l.RenewedAt.IsZero() || l.RenewedAt.Before(l.ValidFrom) || l.RenewedAt.After(l.ValidUntil)) {
		return ErrInvalidLease
	}
	if l.CancelledAt != nil && l.CancelledAt.IsZero() {
		return ErrInvalidLease
	}
	if l.CancelledAt != nil && l.CancelledAt.After(l.ValidUntil) {
		return ErrInvalidLease
	}
	return nil
}

// ActiveAt reports whether the lease is valid and has entered its validity window.
func (l Lease) ActiveAt(now time.Time) bool {
	return l.Validate(now) == nil
}

// Validate checks that the query names one lease in one tenant.
func (q ResolveQuery) Validate() error {
	if !validOpaqueID(q.TenantID) || !validOpaqueID(q.OwnerID) ||
		!validOpaqueID(string(q.LeaseID)) {
		return ErrInvalidLease
	}
	return nil
}

// Validate checks that the request fences one exact lease projection and
// runtime binding.
func (c ValidationCheck) Validate() error {
	if !validOpaqueID(c.TenantID) || !validOpaqueID(c.OwnerID) ||
		!validOpaqueID(string(c.LeaseID)) ||
		!validWireRevision(c.LeaseRevision) || !validOpaqueID(string(c.ServerID)) ||
		!validResourceGenerationID(c.ResourceGenerationID) {
		return ErrInvalidLease
	}
	return nil
}

// Validate checks that the enrollment request is secret-free and binds one
// exact lease projection to one exact runtime generation.
func (r EnrollmentRequest) Validate() error {
	if !validOpaqueID(r.TenantID) || !validOpaqueID(r.OwnerID) ||
		!validOpaqueID(string(r.LeaseID)) || !validWireRevision(r.LeaseRevision) ||
		!validOpaqueID(string(r.ServerID)) ||
		!validResourceGenerationID(r.ResourceGenerationID) ||
		!validOpaqueID(r.IdempotencyKey) {
		return ErrInvalidEnrollment
	}
	return nil
}

// Validate checks that the enrollment result acknowledges one exact runtime
// binding and lease revision.
func (r EnrollmentResult) Validate() error {
	if !validOpaqueID(r.TenantID) || !validOpaqueID(r.OwnerID) ||
		!validOpaqueID(string(r.LeaseID)) ||
		!validWireRevision(r.LeaseRevision) || !validOpaqueID(string(r.ServerID)) ||
		!validResourceGenerationID(r.ResourceGenerationID) {
		return ErrInvalidEnrollment
	}
	return nil
}

// ValidationReasonForError maps validation errors to stable wire reason codes.
func ValidationReasonForError(err error) string {
	switch {
	case errors.Is(err, ErrLeaseCancelled):
		return ReasonLeaseCancelled
	case errors.Is(err, ErrLeaseNotYetValid):
		return ReasonLeaseNotYetValid
	case errors.Is(err, ErrLeaseExpired):
		return ReasonLeaseExpired
	case errors.Is(err, ErrBindingMismatch):
		return ReasonBindingMismatch
	case errors.Is(err, ErrRevisionMismatch):
		return ReasonRevisionMismatch
	default:
		return ReasonInvalidLease
	}
}

func validResourceGenerationID(value ResourceGenerationID) bool {
	raw := string(value)
	parsed, err := uuid.Parse(raw)
	return err == nil && parsed != uuid.Nil && parsed.String() == raw
}

func validWireRevision(value uint64) bool {
	return value > 0 && value <= MaximumWireRevision
}

func validOpaqueID(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
