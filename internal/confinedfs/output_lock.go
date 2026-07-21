package confinedfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
)

const (
	outputLockControlDirectory = ".stackkits-control"
	outputLockDirectory        = outputLockControlDirectory + "/output-locks"
)

// ErrOutputLockBusy is matched by OutputLockBusyError when another process or
// transaction already owns the lock for the same held workspace and output
// root. Acquisition is deliberately non-blocking.
var ErrOutputLockBusy = errors.New("output lock busy")

var errPlatformOutputLockBusy = errors.New("platform output lock busy")

// OutputLockBusyError reports non-blocking lock contention without collapsing
// it into an I/O failure.
type OutputLockBusyError struct {
	OutputRoot string
	LockPath   string
}

func (e *OutputLockBusyError) Error() string {
	if e == nil {
		return ErrOutputLockBusy.Error()
	}
	return fmt.Sprintf("confinedfs output lock busy for %q", e.OutputRoot)
}

// Is permits errors.Is(err, ErrOutputLockBusy).
func (e *OutputLockBusyError) Is(target error) bool {
	return target == ErrOutputLockBusy
}

// OutputLock owns one operating-system advisory lock. Lock files are retained
// after release: unlinking a lock file can split future contenders across
// different file objects while an existing owner still holds the old one.
type OutputLock struct {
	file              *os.File
	outputRoot        string
	lockPath          string
	workspaceIdentity string
	transaction       *Transaction
	mu                sync.RWMutex
	once              sync.Once
	releaseErr        error
}

type outputLockLeaseState struct {
	mu                 sync.RWMutex
	lock               *OutputLock
	transaction        *Transaction
	transactionRelease func()
	outputRoot         string
	closed             bool
}

// OutputLockLease is a copy-safe proof that one exact held transaction still
// owns the advisory lock for one output root. Copies share close state; closing
// any copy invalidates all of them and unblocks OutputLock.Release.
type OutputLockLease struct {
	state *outputLockLeaseState
}

// OutputRoot returns the canonical portable output root guarded by the lock.
func (l *OutputLock) OutputRoot() string {
	if l == nil {
		return ""
	}
	return l.outputRoot
}

// Release unlocks and closes the underlying file handle. It is safe to call
// concurrently or more than once; every call observes the same result.
func (l *OutputLock) Release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.file == nil {
			return
		}
		unlockErr := platformUnlockOutputFile(l.file)
		closeErr := l.file.Close()
		l.file = nil
		l.transaction = nil
		switch {
		case unlockErr != nil && closeErr != nil:
			l.releaseErr = errors.Join(unlockErr, closeErr)
		case unlockErr != nil:
			l.releaseErr = unlockErr
		default:
			l.releaseErr = closeErr
		}
	})
	if l.releaseErr != nil {
		return wrap(ErrIO, "release-output-lock", l.lockPath, "release advisory output lock", l.releaseErr)
	}
	return nil
}

// Close is an alias for Release and is also idempotent.
func (l *OutputLock) Close() error { return l.Release() }

// Borrow proves that transaction belongs to the same held workspace object
// and expected output root as this still-live lock. The lease holds a read
// borrow on the lock until Close, so concurrent Release cannot invalidate an
// authorization between verification and consumption.
func (l *OutputLock) Borrow(transaction *Transaction, expectedOutputRoot string) (*OutputLockLease, error) {
	if l == nil || transaction == nil {
		return nil, fail(ErrInvalidPath, "borrow-output-lock", expectedOutputRoot, "live output lock and held transaction are required")
	}
	canonical, err := validatePortablePath(expectedOutputRoot, false)
	if err != nil {
		return nil, wrap(ErrInvalidPath, "borrow-output-lock", expectedOutputRoot, "invalid expected output root", err)
	}
	l.mu.RLock()
	keep := false
	defer func() {
		if !keep {
			l.mu.RUnlock()
		}
	}()
	if l.file == nil || l.transaction == nil || l.transaction != transaction || platformOutputLockKey(canonical) != platformOutputLockKey(l.outputRoot) {
		return nil, fail(ErrInvalidPath, "borrow-output-lock", canonical, "lock is released or belongs to a different output root")
	}
	release, err := transaction.begin("borrow-output-lock")
	if err != nil {
		return nil, err
	}
	keepTransaction := false
	defer func() {
		if !keepTransaction {
			release()
		}
	}()
	if transaction.root == nil || !transaction.root.identity.Valid() || transaction.root.identity.String() != l.workspaceIdentity {
		return nil, fail(ErrRootChanged, "borrow-output-lock", canonical, "transaction belongs to a different held workspace identity")
	}
	if err := transaction.root.verifyPathIdentityLocked(); err != nil {
		return nil, err
	}
	keep = true
	keepTransaction = true
	return &OutputLockLease{state: &outputLockLeaseState{
		lock: l, transaction: transaction, transactionRelease: release, outputRoot: l.outputRoot,
	}}, nil
}

func (l *OutputLockLease) OutputRoot() string {
	if l == nil || l.state == nil {
		return ""
	}
	l.state.mu.RLock()
	defer l.state.mu.RUnlock()
	if l.state.closed {
		return ""
	}
	return l.state.outputRoot
}

// Verify re-proves the transaction path identity and exact output root while
// the underlying advisory lock remains borrowed.
func (l *OutputLockLease) Verify(transaction *Transaction, expectedOutputRoot string) error {
	if l == nil || l.state == nil {
		return fail(ErrInvalidPath, "verify-output-lock-lease", expectedOutputRoot, "live output lock lease is required")
	}
	l.state.mu.RLock()
	defer l.state.mu.RUnlock()
	if l.state.closed || l.state.transaction == nil || l.state.transaction != transaction {
		return fail(ErrRootChanged, "verify-output-lock-lease", expectedOutputRoot, "lease is closed or belongs to a different transaction")
	}
	equal, err := OutputLockRootsEqual(l.state.outputRoot, expectedOutputRoot)
	if err != nil {
		return err
	}
	if !equal {
		return fail(ErrInvalidPath, "verify-output-lock-lease", expectedOutputRoot, "lease belongs to output root %q", l.state.outputRoot)
	}
	return transaction.VerifyPathIdentity()
}

// Close releases only the in-process borrow. The caller that acquired the
// OutputLock still owns and must release the operating-system advisory lock.
func (l *OutputLockLease) Close() error {
	if l == nil || l.state == nil {
		return nil
	}
	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	if l.state.closed {
		return nil
	}
	l.state.closed = true
	lock := l.state.lock
	transactionRelease := l.state.transactionRelease
	l.state.lock = nil
	l.state.transaction = nil
	l.state.transactionRelease = nil
	if transactionRelease != nil {
		transactionRelease()
	}
	if lock != nil {
		lock.mu.RUnlock()
	}
	return nil
}

// OutputLockRootsEqual reports whether two portable output-root spellings map
// to the same platform lock identity. Recovery admission must use this same
// comparison so a filesystem alias cannot acquire the shared lock and then
// bypass the journal belonging to that physical output.
func OutputLockRootsEqual(left, right string) (bool, error) {
	leftCanonical, err := validatePortablePath(left, false)
	if err != nil {
		return false, wrap(ErrInvalidPath, "compare-output-lock-roots", left, "invalid left output root", err)
	}
	rightCanonical, err := validatePortablePath(right, false)
	if err != nil {
		return false, wrap(ErrInvalidPath, "compare-output-lock-roots", right, "invalid right output root", err)
	}
	return platformOutputLockKey(leftCanonical) == platformOutputLockKey(rightCanonical), nil
}

// TryAcquireOutputLock acquires an exclusive advisory lock for outputRoot
// without waiting. Its identity is derived from both the held workspace object
// and the canonical output root, so different output roots remain independent.
//
// The persistent lock file lives beneath a fixed private workspace control
// directory, never beneath the output tree that an installer may swap.
func (t *Transaction) TryAcquireOutputLock(outputRoot string) (*OutputLock, error) {
	canonical, err := validatePortablePath(outputRoot, false)
	if err != nil {
		return nil, wrap(ErrInvalidPath, "acquire-output-lock", outputRoot, "invalid output root", err)
	}
	if canonical == outputLockControlDirectory || strings.HasPrefix(canonical, outputLockControlDirectory+"/") {
		return nil, fail(ErrInvalidPath, "acquire-output-lock", canonical, "output root must not overlap the private workspace control directory")
	}

	// Creation uses the held-root transaction primitives so links and irregular
	// components fail closed. Existing directories must retain private modes on
	// platforms that can prove POSIX permissions.
	if err := t.MkdirAll(outputLockDirectory, 0o700); err != nil {
		return nil, err
	}
	for _, directory := range []string{outputLockControlDirectory, outputLockDirectory} {
		info, statErr := t.Lstat(directory)
		if statErr != nil {
			return nil, statErr
		}
		if err := platformVerifyPrivateOutputLockDirectory(info); err != nil {
			return nil, wrap(ErrUnsafeEntry, "acquire-output-lock", directory, "workspace control directory is not private", err)
		}
	}

	release, err := t.begin("acquire-output-lock")
	if err != nil {
		return nil, err
	}
	defer release()

	identity := t.root.identity
	if !identity.Valid() {
		return nil, fail(ErrIdentityUnsupported, "acquire-output-lock", canonical, "held workspace has no supported stable identity")
	}
	lockKey := platformOutputLockKey(canonical)
	digest := sha256.Sum256([]byte(identity.String() + "\x00" + lockKey))
	lockPath := path.Join(outputLockDirectory, hex.EncodeToString(digest[:])+".lock")

	if err := t.requirePlainParents(lockPath); err != nil {
		return nil, err
	}
	file, err := t.root.fs.OpenFile(nativePath(lockPath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, wrap(ErrIO, "acquire-output-lock", lockPath, "open output lock file beneath held workspace", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
		}
	}()

	opened, err := file.Stat()
	if err != nil {
		return nil, wrap(ErrIO, "acquire-output-lock", lockPath, "inspect opened output lock file", err)
	}
	if !isPlainRegular(opened) {
		return nil, fail(ErrUnsafeEntry, "acquire-output-lock", lockPath, "output lock must be a plain regular file")
	}
	if err := platformVerifyPrivateOutputLockFile(opened); err != nil {
		return nil, wrap(ErrUnsafeEntry, "acquire-output-lock", lockPath, "output lock file is not private", err)
	}
	if err := t.requirePlainParents(lockPath); err != nil {
		return nil, err
	}
	named, err := t.root.fs.Lstat(nativePath(lockPath))
	if err != nil {
		return nil, wrap(ErrIO, "acquire-output-lock", lockPath, "reinspect named output lock file", err)
	}
	if !isPlainRegular(named) || !os.SameFile(opened, named) {
		return nil, fail(ErrUnsafeEntry, "acquire-output-lock", lockPath, "named output lock does not identify the opened plain file")
	}

	if err := platformTryLockOutputFile(file); err != nil {
		if errors.Is(err, errPlatformOutputLockBusy) {
			return nil, &OutputLockBusyError{OutputRoot: canonical, LockPath: lockPath}
		}
		return nil, wrap(ErrIO, "acquire-output-lock", lockPath, "acquire advisory output lock", err)
	}
	keep = true
	return &OutputLock{file: file, outputRoot: canonical, lockPath: lockPath, workspaceIdentity: identity.String(), transaction: t}, nil
}
