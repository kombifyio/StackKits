// Package backupsurvival is Guard G's deterministic, fail-closed proof that a
// retained backup survives compromise of its source node.
//
// The proof binds three separate identities to one existing backup backend: a
// source-node writer, an independent recovery reader and a maintenance
// authority that prunes. It commits a disposable canary snapshot, drives every
// destructive operation the writer credential could attempt, verifies with the
// recovery identity that the committed bytes and their retention are unchanged,
// restores them into an isolated empty target and seals the result in a
// hash-chained receipt with explicit not-claimed bounds.
//
// The package holds no credential values and no backend client. The caller
// supplies a Backend port that enforces the real policy; this package only
// judges what the backend observably did. It never touches real recovery
// points: every object the proof names lives under the disposable canary
// prefix.
package backupsurvival

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/contracts/canonicaljson"
)

// Operation is one backend permission a credential may hold.
type Operation string

// Operations the binding vocabulary knows. An unknown operation is rejected so
// a typo can never read as "denied".
const (
	OpPut             Operation = "put"
	OpGet             Operation = "get"
	OpGetVersion      Operation = "get_version"
	OpList            Operation = "list"
	OpListVersions    Operation = "list_versions"
	OpDelete          Operation = "delete"
	OpDeleteVersion   Operation = "delete_version"
	OpBypassRetention Operation = "bypass_retention"
	OpPutRetention    Operation = "put_retention"
	OpPutVersioning   Operation = "put_versioning"
	OpPutLockConfig   Operation = "put_lock_configuration"
	OpPrune           Operation = "prune"
)

const (
	canaryPrefixMarker  = "survival-canary/"
	maxCanaryPrefixSize = 256
)

var knownOperations = map[Operation]bool{
	OpPut: true, OpGet: true, OpGetVersion: true, OpList: true, OpListVersions: true,
	OpDelete: true, OpDeleteVersion: true, OpBypassRetention: true, OpPutRetention: true,
	OpPutVersioning: true, OpPutLockConfig: true, OpPrune: true,
}

// Role names which authority an Identity is bound as.
type Role string

// The three separated authorities.
const (
	RoleWriter      Role = "source-writer"
	RoleRecovery    Role = "recovery-reader"
	RoleMaintenance Role = "maintenance"
)

// RetentionMode is the object-lock mode the backend applies to committed data.
type RetentionMode string

// Supported modes. Governance is bypassable by a holder of bypass_retention;
// compliance is not bypassable by anyone until expiry.
const (
	ModeGovernance RetentionMode = "GOVERNANCE"
	ModeCompliance RetentionMode = "COMPLIANCE"
)

// Identity is one backend credential, described but never carried. The
// fingerprint is the SHA-256 of the credential reference, so two identities
// sharing a credential are detected without any secret entering the proof.
type Identity struct {
	ID          string      `json:"id"`
	Role        Role        `json:"role"`
	Fingerprint string      `json:"credentialFingerprint"`
	Operations  []Operation `json:"operations"`
}

// Retention is the immutable retention the backend must apply to every
// committed snapshot.
type Retention struct {
	Mode          RetentionMode `json:"mode"`
	Versioned     bool          `json:"versioned"`
	ObjectLock    bool          `json:"objectLock"`
	MinRetainDays int           `json:"minRetainDays"`
}

// Capacity binds the quota the proof must be able to read.
type Capacity struct {
	// AlertRatio is the used/quota ratio at which a warning is recorded.
	AlertRatio float64 `json:"alertRatio"`
	// FailRatio is the used/quota ratio at which the proof fails.
	FailRatio float64 `json:"failRatio"`
}

// Binding is the complete, secret-free description of one proof.
type Binding struct {
	BackendRef    string    `json:"backendRef"`
	CanaryPrefix  string    `json:"canaryPrefix"`
	Writer        Identity  `json:"writer"`
	Recovery      Identity  `json:"recovery"`
	Maintenance   Identity  `json:"maintenance"`
	Retention     Retention `json:"retention"`
	Capacity      Capacity  `json:"capacity"`
	MaxSnapshotKB int       `json:"maxSnapshotKB"`
}

// ErrInvalidBinding marks a binding that cannot support the proof.
var ErrInvalidBinding = errors.New("backupsurvival: invalid binding")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidBinding, fmt.Sprintf(format, args...))
}

func (i Identity) has(op Operation) bool {
	for _, held := range i.Operations {
		if held == op {
			return true
		}
	}
	return false
}

func validFingerprint(v string) bool {
	if len(v) != len("sha256:")+64 || !strings.HasPrefix(v, "sha256:") {
		return false
	}
	for _, c := range v[len("sha256:"):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (i Identity) validate(role Role) error {
	if strings.TrimSpace(i.ID) == "" || i.ID != strings.TrimSpace(i.ID) {
		return invalid("%s identity needs a trimmed id", role)
	}
	if i.Role != role {
		return invalid("identity %q is bound as %q, want %q", i.ID, i.Role, role)
	}
	if !validFingerprint(i.Fingerprint) {
		return invalid("identity %q needs a sha256 credential fingerprint", i.ID)
	}
	seen := map[Operation]bool{}
	for _, op := range i.Operations {
		if !knownOperations[op] {
			return invalid("identity %q holds unknown operation %q", i.ID, op)
		}
		if seen[op] {
			return invalid("identity %q lists operation %q twice", i.ID, op)
		}
		seen[op] = true
	}
	return nil
}

// Validate fails closed: any missing, shared or over-broad authority rejects
// the whole binding before a single backend call happens.
func (b Binding) Validate() error {
	if strings.TrimSpace(b.BackendRef) == "" {
		return invalid("backend reference is required")
	}
	if !validCanaryPrefix(b.CanaryPrefix) {
		return invalid("canary prefix must be a disposable %q path of at most %d bytes", canaryPrefixMarker, maxCanaryPrefixSize)
	}
	if err := b.Writer.validate(RoleWriter); err != nil {
		return err
	}
	if err := b.Recovery.validate(RoleRecovery); err != nil {
		return err
	}
	if err := b.Maintenance.validate(RoleMaintenance); err != nil {
		return err
	}
	fingerprints := map[string]string{}
	for _, id := range []Identity{b.Writer, b.Recovery, b.Maintenance} {
		if other, dup := fingerprints[id.Fingerprint]; dup {
			return invalid("identities %q and %q share one credential", other, id.ID)
		}
		fingerprints[id.Fingerprint] = id.ID
	}
	if err := b.Retention.validate(); err != nil {
		return err
	}
	if err := b.validateWriter(); err != nil {
		return err
	}
	if err := b.validateRecovery(); err != nil {
		return err
	}
	if err := b.validateMaintenance(); err != nil {
		return err
	}
	c := b.Capacity
	if !(c.AlertRatio > 0 && c.AlertRatio < c.FailRatio && c.FailRatio <= 1) {
		return invalid("capacity ratios need 0 < alert < fail <= 1")
	}
	if b.MaxSnapshotKB <= 0 || b.MaxSnapshotKB > 1024 {
		return invalid("canary snapshot size must be 1..1024 KiB")
	}
	return nil
}

func validCanaryPrefix(p string) bool {
	if len(p) > maxCanaryPrefixSize || !strings.HasPrefix(p, canaryPrefixMarker) || !strings.HasSuffix(p, "/") {
		return false
	}
	if strings.Contains(p, "..") || strings.Contains(p, "//") || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	return len(p) > len(canaryPrefixMarker)
}

func (r Retention) validate() error {
	if r.Mode != ModeGovernance && r.Mode != ModeCompliance {
		return invalid("retention mode must be GOVERNANCE or COMPLIANCE")
	}
	if !r.Versioned || !r.ObjectLock {
		return invalid("retention needs a versioned, object-locked bucket")
	}
	if r.MinRetainDays < 1 || r.MinRetainDays > 3650 {
		return invalid("minimum retention must be 1..3650 days")
	}
	return nil
}

// writerForbidden are operations the source node must never hold. A plain
// delete is allowed only because a versioned, locked bucket turns it into a
// delete marker and Kopia must be able to remove its own session markers.
var writerForbidden = []Operation{OpDeleteVersion, OpBypassRetention, OpPutRetention, OpPutVersioning, OpPutLockConfig, OpPrune}

func (b Binding) validateWriter() error {
	w := b.Writer
	for _, op := range []Operation{OpPut, OpGet, OpList} {
		if !w.has(op) {
			return invalid("writer %q cannot back up without %q", w.ID, op)
		}
	}
	for _, op := range writerForbidden {
		if w.has(op) {
			return invalid("writer %q must not hold %q", w.ID, op)
		}
	}
	return nil
}

func (b Binding) validateRecovery() error {
	rec := b.Recovery
	for _, op := range []Operation{OpGet, OpGetVersion, OpList, OpListVersions} {
		if !rec.has(op) {
			return invalid("recovery %q needs %q for point-in-time restore", rec.ID, op)
		}
	}
	for _, op := range rec.Operations {
		switch op {
		case OpGet, OpGetVersion, OpList, OpListVersions:
		default:
			return invalid("recovery %q must be read-only, found %q", rec.ID, op)
		}
	}
	return nil
}

func (b Binding) validateMaintenance() error {
	m := b.Maintenance
	for _, op := range []Operation{OpPrune, OpDeleteVersion, OpPutRetention} {
		if !m.has(op) {
			return invalid("maintenance %q needs %q to prune and renew", m.ID, op)
		}
	}
	if m.has(OpPut) {
		return invalid("maintenance %q must not write backup data", m.ID)
	}
	return nil
}

// Digest is the stable SHA-256 of the binding: the receipt's anchor.
func (b Binding) Digest() (string, error) {
	ops := func(i Identity) Identity {
		sorted := append([]Operation(nil), i.Operations...)
		sort.Slice(sorted, func(a, c int) bool { return sorted[a] < sorted[c] })
		i.Operations = sorted
		return i
	}
	b.Writer, b.Recovery, b.Maintenance = ops(b.Writer), ops(b.Recovery), ops(b.Maintenance)
	raw, err := canonicaljson.CanonicalJSON(b)
	if err != nil {
		return "", err
	}
	return canonicaljson.SHA256Hex(raw), nil
}

func (r Retention) minRetain() time.Duration {
	return time.Duration(r.MinRetainDays) * 24 * time.Hour
}
