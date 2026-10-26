package applicationlifecycle

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// leaseRoot holds one marker directory per running lifecycle operation. The
// process that began the operation keeps an exclusive kernel advisory lock
// keyed by that marker for as long as it lives; the kernel releases the lock
// when the process dies, SIGKILL and power loss included. A later Begin that
// finds the marker but can take the lock has proved that the owner is gone.
// An operation without a marker (begun by an older CLI) proves nothing and
// keeps blocking, exactly as before.
const leaseRoot = ".stackkit/lifecycle-leases"

type heldLease struct {
	refs        int
	root        *confinedfs.Root
	transaction *confinedfs.Transaction
	lock        *confinedfs.OutputLock
}

var (
	leaseMu sync.Mutex
	leases  = map[string]heldLease{}
)

func leaseMarker(id string) string { return path.Join(leaseRoot, id) }

func leaseKey(workspace, id string) string { return filepath.Clean(workspace) + "\x00" + id }

// ensureOperationLease takes one reference on this process's lease for id,
// acquiring it on first use. One run may begin the same operation ID for
// several workloads.
func (store Store) ensureOperationLease(id string) error {
	leaseMu.Lock()
	if held, ok := leases[leaseKey(store.Workspace, id)]; ok {
		held.refs++
		leases[leaseKey(store.Workspace, id)] = held
		leaseMu.Unlock()
		return nil
	}
	leaseMu.Unlock()
	return store.acquireOperationLease(id)
}

// acquireOperationLease creates the marker and holds its lock for the life of
// this process, or until releaseOperationLease runs.
func (store Store) acquireOperationLease(id string) error {
	root, err := confinedfs.Open(filepath.Clean(store.Workspace))
	if err != nil {
		return fmt.Errorf("open application lifecycle lease workspace: %w", err)
	}
	transaction, err := root.BeginTransaction()
	if err != nil {
		root.Close()
		return fmt.Errorf("begin application lifecycle lease: %w", err)
	}
	fail := func(err error) error {
		transaction.Close()
		root.Close()
		return err
	}
	if err := transaction.MkdirAll(leaseMarker(id), 0o700); err != nil {
		return fail(fmt.Errorf("create application lifecycle lease marker: %w", err))
	}
	lock, err := transaction.TryAcquireOutputLock(leaseMarker(id))
	if err != nil {
		if errors.Is(err, confinedfs.ErrOutputLockBusy) {
			return fail(fmt.Errorf("%w: operation %s", errLeaseHeld, id))
		}
		return fail(fmt.Errorf("acquire application lifecycle lease: %w", err))
	}
	if err := recordLeaseOwner(transaction, id); err != nil {
		_ = lock.Release()
		return fail(fmt.Errorf("record application lifecycle lease owner: %w", err))
	}
	leaseMu.Lock()
	leases[leaseKey(store.Workspace, id)] = heldLease{refs: 1, root: root, transaction: transaction, lock: lock}
	leaseMu.Unlock()
	return nil
}

// releaseOperationLease drops one reference and, with the last one, the lease
// and its marker.
func (store Store) releaseOperationLease(id string) {
	leaseMu.Lock()
	held, ok := leases[leaseKey(store.Workspace, id)]
	if ok && held.refs > 1 {
		held.refs--
		leases[leaseKey(store.Workspace, id)] = held
		leaseMu.Unlock()
		return
	}
	delete(leases, leaseKey(store.Workspace, id))
	leaseMu.Unlock()
	if !ok {
		return
	}
	_ = held.transaction.RemoveTree(leaseMarker(id))
	_ = held.lock.Release()
	held.transaction.Close()
	held.root.Close()
}

// operationOwnerEnded reports true only when the operation's marker exists and
// its lock is free, i.e. the process that began it no longer runs. A held
// lock, a missing marker or any inspection error reports false.
//
// On true, the caller holds the dead owner's lock and must call release only
// after the interruption is committed, so no ClaimRunningOperation can take
// the lease while the record still says running.
func (store Store) operationOwnerEnded(id string) (bool, string, func()) {
	none := func() {}
	leaseMu.Lock()
	_, ownedHere := leases[leaseKey(store.Workspace, id)]
	leaseMu.Unlock()
	if ownedHere {
		return false, "", none
	}
	root, err := confinedfs.Open(filepath.Clean(store.Workspace))
	if err != nil {
		return false, "", none
	}
	transaction, err := root.BeginTransaction()
	if err != nil {
		root.Close()
		return false, "", none
	}
	closeAll := func() {
		transaction.Close()
		root.Close()
	}
	info, err := transaction.Lstat(leaseMarker(id))
	if err != nil || !info.IsDir() {
		closeAll()
		return false, "", none
	}
	lock, err := transaction.TryAcquireOutputLock(leaseMarker(id))
	if err != nil {
		closeAll()
		return false, "", none
	}
	// Still holding the dead owner's lock, stop children that outlived it
	// before the successor can start mutating.
	note := stopOrphansOfDeadOwner(transaction, id)
	// The marker stays: one run may have begun this operation ID for several
	// workloads, and each of their records must reach the same proof.
	return true, note, func() {
		_ = lock.Release()
		closeAll()
	}
}

// interruptOperation records, in the digest-chained history, that a running
// operation lost its owner process without recording an outcome. Its effects
// are unknown: the record keeps that uncertainty and names the successor.
func interruptOperation(operation *Operation, supersededBy, children string, now time.Time) {
	operation.Status = StatusFailed
	operation.UpdatedAt = now
	operation.CompletedAt = now
	operation.LastError = "interrupted: the owning process ended without recording an outcome; its effects are uncertain and must be reviewed; " + children + "; superseded by " + supersededBy
	operation.Digest = operationDigest(*operation)
}

// ClaimRunningOperation lets a command continue an operation that is already
// recorded as running (an interrupted setup, application action or removal
// resumed under its original ID). It takes the operation's lease first and
// fails closed while another live process holds it, then confirms that the
// operation is still the current running one, so a concurrent Begin can no
// longer record it as interrupted while this process mutates.
func (store Store) ClaimRunningOperation(contract Contract, id string) error {
	if !operationIDPattern.MatchString(id) {
		return errors.New("application lifecycle operation ID is invalid")
	}
	if err := store.ensureOperationLease(id); err != nil {
		return err
	}
	// Check under the state lock, the same lock a concurrent Begin holds while
	// it records an interruption, and write nothing.
	_, err := store.mutate(contract, func(state *State) error {
		current, status := "", ""
		for _, operation := range state.Operations {
			if operation.ID == state.CurrentOperation {
				current, status = operation.ID, operation.Status
			}
		}
		if current != id || status != StatusRunning {
			return fmt.Errorf("application lifecycle operation %s is no longer the current running operation", id)
		}
		return errClaimChecked
	})
	if errors.Is(err, errClaimChecked) {
		err = nil
	}
	if err != nil {
		store.releaseOperationLease(id)
	}
	return err
}

// errClaimChecked aborts the read-only claim check inside mutate so that no
// state is written.
var errClaimChecked = errors.New("application lifecycle claim checked")
