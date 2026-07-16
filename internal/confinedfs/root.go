package confinedfs

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Root owns one held os.Root for the lifetime of a governed filesystem
// operation. Relative Views borrow this root and never open a weaker path-based
// security boundary.
type Root struct {
	fs         *os.Root
	path       string
	openedInfo os.FileInfo
	identity   Identity
	mu         sync.RWMutex
	closed     bool
}

// Open validates and opens an existing plain-directory root. The before/open/
// after identity checks detect replacement of the named root while it is
// opened. Every ancestor must also be a plain directory; Windows junctions are
// therefore rejected even when Go reports them as ModeIrregular rather than
// ModeSymlink.
func Open(name string) (*Root, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fail(ErrInvalidPath, "open-root", "root", "root path is required")
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, wrap(ErrInvalidPath, "open-root", name, "resolve root path", err)
	}
	absolute = filepath.Clean(absolute)
	before, err := inspectPlainDirectoryChain(absolute)
	if err != nil {
		return nil, err
	}
	rootFS, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, wrap(ErrIO, "open-root", absolute, "open held filesystem root", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = rootFS.Close()
		}
	}()
	opened, err := rootFS.Stat(".")
	if err != nil {
		return nil, wrap(ErrIO, "open-root", absolute, "stat held filesystem root", err)
	}
	if !isPlainDirectory(opened) {
		return nil, fail(ErrUnsafeEntry, "open-root", absolute, "opened root is not a plain directory")
	}
	if !os.SameFile(before, opened) {
		return nil, fail(ErrRootChanged, "open-root", absolute, "root changed between path validation and handle open")
	}
	after, err := inspectPlainDirectoryChain(absolute)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) {
		return nil, fail(ErrRootChanged, "open-root", absolute, "root changed while its handle was opened")
	}

	root := &Root{fs: rootFS, path: absolute, openedInfo: opened}
	identity, err := root.identityWithoutBoundary(".")
	if err != nil {
		return nil, err
	}
	root.identity = identity
	keep = true
	return root, nil
}

// Name returns the absolute name originally passed to the held root.
func (r *Root) Name() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.path
}

// RootIdentity returns the platform identity captured from the held root.
func (r *Root) RootIdentity() Identity {
	if r == nil {
		return Identity{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.identity
}

// Close releases the held root. Calling Close more than once is harmless.
func (r *Root) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fs == nil || r.closed {
		return nil
	}
	r.closed = true
	if err := r.fs.Close(); err != nil {
		return wrap(ErrIO, "close-root", r.path, "close held filesystem root", err)
	}
	return nil
}

// VerifyPathIdentity proves that the original root name still resolves through
// plain ancestors to the exact directory held by this Root. Operations remain
// confined to the held object even if this check fails; callers receive a
// typed failure rather than treating a renamed workspace as committed.
func (r *Root) VerifyPathIdentity() error {
	release, err := r.acquireRead("verify-root")
	if err != nil {
		return err
	}
	defer release()
	return r.verifyPathIdentityLocked()
}

func (r *Root) verifyPathIdentityLocked() error {
	current, err := inspectPlainDirectoryChain(r.path)
	if err != nil {
		var typed *Error
		if errors.As(err, &typed) && typed.Code == ErrUnsafeEntry {
			return &Error{Code: ErrRootChanged, Op: "verify-root", Path: r.path, Message: "root path no longer resolves through plain directories", Err: err}
		}
		if os.IsNotExist(err) {
			return &Error{Code: ErrRootChanged, Op: "verify-root", Path: r.path, Message: "root path no longer exists", Err: err}
		}
		return err
	}
	held, err := r.fs.Stat(".")
	if err != nil {
		return wrap(ErrIO, "verify-root", r.path, "stat held filesystem root", err)
	}
	if !isPlainDirectory(held) || !os.SameFile(r.openedInfo, held) || !os.SameFile(held, current) {
		return fail(ErrRootChanged, "verify-root", r.path, "named root no longer identifies the held directory")
	}
	return nil
}

// View returns a portable root-relative view. Prefix may be "." for the root
// itself. A View never owns or closes the Root.
func (r *Root) View(prefix string) (View, error) {
	release, err := r.acquireRead("view")
	if err != nil {
		return View{}, err
	}
	defer release()
	canonical, err := validatePortablePath(prefix, true)
	if err != nil {
		return View{}, wrap(ErrInvalidPath, "view", prefix, "invalid portable view prefix", err)
	}
	return View{root: r, prefix: canonical}, nil
}

func (r *Root) acquireRead(op string) (func(), error) {
	if r == nil {
		return nil, fail(ErrClosed, op, "root", "held filesystem root is closed or absent")
	}
	r.mu.RLock()
	if r.fs == nil || r.closed {
		r.mu.RUnlock()
		return nil, fail(ErrClosed, op, "root", "held filesystem root is closed or absent")
	}
	return r.mu.RUnlock, nil
}

func inspectPlainDirectoryChain(absolute string) (os.FileInfo, error) {
	current := filepath.Clean(absolute)
	chain := make([]string, 0, 8)
	for {
		chain = append(chain, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	slices.Reverse(chain)
	var leaf os.FileInfo
	for _, candidate := range chain {
		info, err := os.Lstat(candidate)
		if err != nil {
			return nil, wrap(ErrIO, "inspect-root-chain", candidate, "inspect root path component", err)
		}
		if !isPlainDirectory(info) {
			return nil, fail(ErrUnsafeEntry, "inspect-root-chain", candidate, "root path component must be a plain directory")
		}
		leaf = info
	}
	return leaf, nil
}

func isPlainDirectory(info os.FileInfo) bool {
	return info != nil && info.IsDir() && info.Mode().Type() == os.ModeDir
}

func isPlainRegular(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode().Type() == 0
}
