package opentofu

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// LeaseActor identifies which host writer holds the unified node action_lease
// (ADR-0054 §1, ADR-0051).
type LeaseActor string

const (
	LeaseActorCLI             LeaseActor = "cli"
	LeaseActorHostExec        LeaseActor = "host-exec"
	LeaseActorWatchdog        LeaseActor = "watchdog"
	LeaseActorHomelabAdvanced LeaseActor = "homelab-advanced"
)

const (
	nodeLeaseOutputRoot = ".stackkit/runtime/node-action-lease"
	nodeLeaseMetaFile   = ".stackkit/runtime/.node-action-lease.json"
)

var (
	// ErrActiveLeaseProtected is returned when an agent, host-exec spool, or
	// watchdog attempts to clear an active node lease, or when a live process
	// still owns the OS advisory flock.
	ErrActiveLeaseProtected = errors.New("active node action_lease cannot be cleared")
	// ErrInvalidLeaseActor is returned when a caller passes an unrecognized actor.
	ErrInvalidLeaseActor = errors.New("invalid node action_lease actor")
)

// NodeLeaseRecord is the secret-free metadata persisted beside the OS flock
// for diagnostics and generation fencing.
type NodeLeaseRecord struct {
	SchemaVersion string     `json:"schemaVersion"`
	Generation    string     `json:"generation"`
	Actor         LeaseActor `json:"actor"`
	Operation     string     `json:"operation"`
	PID           int        `json:"pid"`
	AcquiredAt    time.Time  `json:"acquiredAt"`
}

// NodeLease holds the workspace-wide exclusive node action_lease backed by
// confinedfs advisory flock plus a cryptographic generation token.
type NodeLease struct {
	workspace string
	fs        *confinedfs.Root
	tx        *confinedfs.Transaction
	lock      *confinedfs.OutputLock
	record    NodeLeaseRecord
}

// Record returns the lease's immutable generation and actor metadata.
func (l *NodeLease) Record() NodeLeaseRecord {
	if l == nil {
		return NodeLeaseRecord{}
	}
	return l.record
}

// Generation returns the cryptographic fencing token for this lease holder.
func (l *NodeLease) Generation() string {
	if l == nil {
		return ""
	}
	return l.record.Generation
}

// ValidLeaseActor reports whether actor is an admitted node-lease writer.
func ValidLeaseActor(actor LeaseActor) bool {
	switch actor {
	case LeaseActorCLI, LeaseActorHostExec, LeaseActorWatchdog, LeaseActorHomelabAdvanced:
		return true
	default:
		return false
	}
}

// AcquireNodeLease acquires the unified non-blocking OS flock + generation
// token for workspace. Returns confinedfs.ErrOutputLockBusy if any other
// writer (CLI, host-exec, watchdog, or Homelab Advanced) currently holds it.
func AcquireNodeLease(workspace string, actor LeaseActor, operation string) (*NodeLease, error) {
	if !ValidLeaseActor(actor) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidLeaseActor, actor)
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		return nil, errors.New("node action_lease requires an operation name")
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace for node lease: %w", err)
	}
	fs, err := confinedfs.Open(absWorkspace)
	if err != nil {
		return nil, fmt.Errorf("open workspace root for node lease: %w", err)
	}
	tx, err := fs.BeginTransaction()
	if err != nil {
		_ = fs.Close()
		return nil, fmt.Errorf("begin workspace transaction for node lease: %w", err)
	}
	lock, err := tx.TryAcquireOutputLock(nodeLeaseOutputRoot)
	if err != nil {
		_ = tx.Close()
		_ = fs.Close()
		return nil, err
	}
	genBytes := make([]byte, 16)
	if _, err := rand.Read(genBytes); err != nil {
		_ = lock.Release()
		_ = tx.Close()
		_ = fs.Close()
		return nil, fmt.Errorf("generate lease fencing token: %w", err)
	}
	record := NodeLeaseRecord{
		SchemaVersion: "stackkit.node-action-lease/v1",
		Generation:    hex.EncodeToString(genBytes),
		Actor:         actor,
		Operation:     operation,
		PID:           os.Getpid(),
		AcquiredAt:    time.Now().UTC(),
	}
	metaPath := filepath.Join(absWorkspace, filepath.FromSlash(nodeLeaseMetaFile))
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o700); err != nil {
		_ = lock.Release()
		_ = tx.Close()
		_ = fs.Close()
		return nil, fmt.Errorf("prepare node lease metadata dir: %w", err)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		_ = lock.Release()
		_ = tx.Close()
		_ = fs.Close()
		return nil, err
	}
	if err := writeFileAtomic(filepath.Dir(metaPath), filepath.Base(metaPath), append(encoded, '\n'), 0o600); err != nil {
		_ = lock.Release()
		_ = tx.Close()
		_ = fs.Close()
		return nil, fmt.Errorf("write node lease metadata: %w", err)
	}
	return &NodeLease{
		workspace: absWorkspace,
		fs:        fs,
		tx:        tx,
		lock:      lock,
		record:    record,
	}, nil
}

// VerifyGeneration proves that workspace's persisted lease metadata still
// matches this lease's generation token (fencing against stale writers).
func (l *NodeLease) VerifyGeneration() error {
	if l == nil || l.lock == nil {
		return errors.New("node action_lease is not active")
	}
	cur, err := ReadNodeLeaseRecord(l.workspace)
	if err != nil {
		return fmt.Errorf("verify node lease generation: %w", err)
	}
	if cur.Generation != l.record.Generation {
		return fmt.Errorf("node lease generation mismatch: expected %s, found %s", l.record.Generation, cur.Generation)
	}
	return nil
}

// Release frees the OS advisory flock and clears the lease metadata record.
func (l *NodeLease) Release() error {
	if l == nil {
		return nil
	}
	metaPath := filepath.Join(l.workspace, filepath.FromSlash(nodeLeaseMetaFile))
	// Only remove metadata if it still belongs to our generation.
	if cur, err := ReadNodeLeaseRecord(l.workspace); err == nil && cur.Generation == l.record.Generation {
		_ = os.Remove(metaPath)
	}
	var lockErr, txErr, fsErr error
	if l.lock != nil {
		lockErr = l.lock.Release()
		l.lock = nil
	}
	if l.tx != nil {
		txErr = l.tx.Close()
		l.tx = nil
	}
	if l.fs != nil {
		fsErr = l.fs.Close()
		l.fs = nil
	}
	return errors.Join(lockErr, txErr, fsErr)
}

// ReadNodeLeaseRecord reads the current secret-free lease metadata if present.
func ReadNodeLeaseRecord(workspace string) (NodeLeaseRecord, error) {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return NodeLeaseRecord{}, err
	}
	raw, err := os.ReadFile(filepath.Join(absWorkspace, filepath.FromSlash(nodeLeaseMetaFile)))
	if err != nil {
		return NodeLeaseRecord{}, err
	}
	var rec NodeLeaseRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return NodeLeaseRecord{}, fmt.Errorf("decode node lease metadata: %w", err)
	}
	return rec, nil
}

// ClearActiveLease enforces the ADR-0054 / ADR-0051 invariant: NO agent
// (`host-exec`) or `watchdog` may EVER clear a lease, and even an interactive
// CLI call refuses to clear metadata while a live holder owns the OS flock.
func ClearActiveLease(workspace string, requester LeaseActor) error {
	if requester == LeaseActorHostExec || requester == LeaseActorWatchdog {
		return fmt.Errorf("%w: actor %q is forbidden from clearing node leases", ErrActiveLeaseProtected, requester)
	}
	if !ValidLeaseActor(requester) {
		return fmt.Errorf("%w: %q", ErrInvalidLeaseActor, requester)
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	// Probe the OS advisory flock non-blockingly: if held, refuse!
	fs, err := confinedfs.Open(absWorkspace)
	if err != nil {
		return err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(nodeLeaseOutputRoot)
	if err != nil {
		if errors.Is(err, confinedfs.ErrOutputLockBusy) {
			return fmt.Errorf("%w: a live process currently holds the OS advisory flock", ErrActiveLeaseProtected)
		}
		return err
	}
	defer lock.Release()
	metaPath := filepath.Join(absWorkspace, filepath.FromSlash(nodeLeaseMetaFile))
	if err := os.Remove(metaPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
