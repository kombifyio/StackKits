package confinedfs

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const privateDirectoryNameAttempts = 16

// Transaction is a borrowed, handle-relative mutation boundary beneath one
// held Root. It never exposes the underlying os.Root or converts governed
// operations back to the Root's original pathname. Close releases the borrow;
// it does not close the owning Root.
//
// The named root may be checked explicitly with VerifyPathIdentity. Other
// methods deliberately continue to address the held directory object after a
// rename so rollback and cleanup cannot be redirected into a replacement at
// the old pathname. Until the coordinator adds its cross-process lock and
// recovery journal, callers must own the mutation namespace; these primitives
// do not claim to serialize an uncooperative writer already inside the same
// held root.
type Transaction struct {
	root    *Root
	release func()
	mu      sync.RWMutex
	closed  bool
}

// TreeEntry is one point-in-time observation from Walk. Path is portable and
// relative to the held root. Info always describes a plain directory or plain
// regular file; links and irregular entries fail the walk closed.
type TreeEntry struct {
	Path string
	Info os.FileInfo
}

// BeginTransaction borrows the held root until Transaction.Close. The named
// root must still identify the held directory when the borrow begins.
func (r *Root) BeginTransaction() (*Transaction, error) {
	release, err := r.acquireRead("begin-transaction")
	if err != nil {
		return nil, err
	}
	if err := r.verifyPathIdentityLocked(); err != nil {
		release()
		return nil, err
	}
	return &Transaction{root: r, release: release}, nil
}

// Close ends the transaction borrow. It is safe to call more than once.
func (t *Transaction) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	if t.release != nil {
		t.release()
		t.release = nil
	}
	t.root = nil
	return nil
}

// Name returns the original absolute display name of the held root. It is
// never used as an operating-system input by Transaction methods.
func (t *Transaction) Name() string {
	release, err := t.begin("transaction-name")
	if err != nil {
		return ""
	}
	defer release()
	return t.root.path
}

// VerifyPathIdentity proves that the original pathname still identifies the
// held root. A failure does not weaken the handle boundary: callers can still
// use the transaction to perform rollback and cleanup before Close.
func (t *Transaction) VerifyPathIdentity() error {
	release, err := t.begin("verify-transaction-root")
	if err != nil {
		return err
	}
	defer release()
	return t.root.verifyPathIdentityLocked()
}

// Lstat observes a plain directory or regular file without following the
// final component. Parent components must be plain directories.
func (t *Transaction) Lstat(relative string) (os.FileInfo, error) {
	full, release, err := t.beginPath("transaction-lstat", relative, true)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := t.requirePlainParents(full); err != nil {
		return nil, err
	}
	info, err := t.root.fs.Lstat(nativePath(full))
	if err != nil {
		return nil, wrap(ErrIO, "transaction-lstat", full, "inspect root-relative entry", err)
	}
	if !isPlainDirectory(info) && !isPlainRegular(info) {
		return nil, fail(ErrUnsafeEntry, "transaction-lstat", full, "entry must be a plain directory or regular file")
	}
	return info, nil
}

// Exists reports whether a plain directory or regular file exists. Unsafe
// entries are errors rather than successful existence observations.
func (t *Transaction) Exists(relative string) (bool, os.FileInfo, error) {
	info, err := t.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, info, nil
}

// MkdirAll creates a portable directory chain beneath the held root and
// rejects links or non-directory components before and after each creation.
func (t *Transaction) MkdirAll(relative string, mode os.FileMode) error {
	full, release, err := t.beginPath("transaction-mkdir-all", relative, true)
	if err != nil {
		return err
	}
	defer release()
	if full == "." {
		return nil
	}
	current := ""
	for _, segment := range splitPortable(full) {
		if current == "" {
			current = segment
		} else {
			current = path.Join(current, segment)
		}
		info, statErr := t.root.fs.Lstat(nativePath(current))
		if os.IsNotExist(statErr) {
			if mkdirErr := t.root.fs.Mkdir(nativePath(current), mode); mkdirErr != nil && !os.IsExist(mkdirErr) {
				return wrap(ErrIO, "transaction-mkdir-all", current, "create root-relative directory", mkdirErr)
			}
			info, statErr = t.root.fs.Lstat(nativePath(current))
		}
		if statErr != nil {
			return wrap(ErrIO, "transaction-mkdir-all", current, "inspect root-relative directory", statErr)
		}
		if !isPlainDirectory(info) {
			return fail(ErrUnsafeEntry, "transaction-mkdir-all", current, "path component must be a plain directory")
		}
	}
	return nil
}

// CreatePrivateDirectory creates a unique 0700 directory directly beneath
// the held root. Prefix must be one portable path segment.
func (t *Transaction) CreatePrivateDirectory(prefix string) (string, error) {
	release, err := t.begin("transaction-create-private-directory")
	if err != nil {
		return "", err
	}
	defer release()
	if prefix == "" || strings.Contains(prefix, "/") {
		return "", fail(ErrInvalidPath, "transaction-create-private-directory", prefix, "prefix must be one non-empty portable path segment")
	}
	if _, err := validatePortablePath(prefix+"x", false); err != nil {
		return "", err
	}
	for attempt := 0; attempt < privateDirectoryNameAttempts; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", wrap(ErrIO, "transaction-create-private-directory", prefix, "generate private directory identity", err)
		}
		name := prefix + hex.EncodeToString(random[:])
		if err := t.root.fs.Mkdir(nativePath(name), 0o700); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", wrap(ErrIO, "transaction-create-private-directory", name, "create private root-relative directory", err)
		}
		info, err := t.root.fs.Lstat(nativePath(name))
		if err != nil {
			return "", wrap(ErrIO, "transaction-create-private-directory", name, "inspect created private directory", err)
		}
		if !isPlainDirectory(info) {
			return "", fail(ErrUnsafeEntry, "transaction-create-private-directory", name, "created entry is not a plain directory")
		}
		return name, nil
	}
	return "", fail(ErrIO, "transaction-create-private-directory", prefix, "could not allocate a unique private directory")
}

// WriteFileExclusive creates, writes, syncs, chmods, and closes one new plain
// regular file beneath an existing directory chain. It never truncates or
// replaces an existing entry.
func (t *Transaction) WriteFileExclusive(relative string, data []byte, mode os.FileMode) error {
	full, release, err := t.beginPath("transaction-write-exclusive", relative, false)
	if err != nil {
		return err
	}
	defer release()
	if err := t.requirePlainParents(full); err != nil {
		return err
	}
	if _, err := t.root.fs.Lstat(nativePath(full)); err == nil {
		return fail(ErrUnsafeEntry, "transaction-write-exclusive", full, "target already exists")
	} else if !os.IsNotExist(err) {
		return wrap(ErrIO, "transaction-write-exclusive", full, "inspect exclusive target", err)
	}
	file, err := t.root.fs.OpenFile(nativePath(full), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return wrap(ErrIO, "transaction-write-exclusive", full, "create exclusive root-relative file", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return wrap(ErrIO, "transaction-write-exclusive", full, "set file permissions through held handle", err)
	}
	if _, err := io.Copy(file, bytes.NewReader(append([]byte(nil), data...))); err != nil {
		return wrap(ErrIO, "transaction-write-exclusive", full, "write held file", err)
	}
	if err := file.Sync(); err != nil {
		return wrap(ErrIO, "transaction-write-exclusive", full, "sync held file", err)
	}
	opened, err := file.Stat()
	if err != nil {
		return wrap(ErrIO, "transaction-write-exclusive", full, "stat held file", err)
	}
	if !isPlainRegular(opened) {
		return fail(ErrUnsafeEntry, "transaction-write-exclusive", full, "created handle is not a plain regular file")
	}
	if runtime.GOOS != "windows" && opened.Mode().Perm() != mode.Perm() {
		return fail(ErrUnsafeEntry, "transaction-write-exclusive", full, "created file mode is %04o, requested %04o", opened.Mode().Perm(), mode.Perm())
	}
	if err := file.Close(); err != nil {
		return wrap(ErrIO, "transaction-write-exclusive", full, "close held file", err)
	}
	closed = true
	current, err := t.root.fs.Lstat(nativePath(full))
	if err != nil {
		return wrap(ErrIO, "transaction-write-exclusive", full, "reinspect created file", err)
	}
	if !isPlainRegular(current) || !os.SameFile(opened, current) {
		return fail(ErrRootChanged, "transaction-write-exclusive", full, "created file path changed while it was written")
	}
	return nil
}

// ReadStable reads one plain regular file and proves that both its held handle
// and root-relative name remained bound to the same object for the read.
func (t *Transaction) ReadStable(relative string) ([]byte, os.FileInfo, error) {
	full, release, err := t.beginPath("transaction-read-stable", relative, false)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	if err := t.requirePlainParents(full); err != nil {
		return nil, nil, err
	}
	before, err := t.root.fs.Lstat(nativePath(full))
	if err != nil {
		return nil, nil, wrap(ErrIO, "transaction-read-stable", full, "inspect file before open", err)
	}
	if !isPlainRegular(before) {
		return nil, nil, fail(ErrUnsafeEntry, "transaction-read-stable", full, "file must be a plain regular file")
	}
	file, err := t.root.fs.Open(nativePath(full))
	if err != nil {
		return nil, nil, wrap(ErrIO, "transaction-read-stable", full, "open root-relative file", err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return nil, nil, wrap(ErrIO, "transaction-read-stable", full, "stat opened file", err)
	}
	if !isPlainRegular(opened) || !os.SameFile(before, opened) {
		return nil, nil, fail(ErrRootChanged, "transaction-read-stable", full, "file changed between inspection and open")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, wrap(ErrIO, "transaction-read-stable", full, "read held file", err)
	}
	afterRead, err := file.Stat()
	if err != nil {
		return nil, nil, wrap(ErrIO, "transaction-read-stable", full, "restat held file", err)
	}
	if !os.SameFile(opened, afterRead) || opened.Size() != afterRead.Size() || !opened.ModTime().Equal(afterRead.ModTime()) {
		return nil, nil, fail(ErrRootChanged, "transaction-read-stable", full, "file changed while it was read")
	}
	current, err := t.root.fs.Lstat(nativePath(full))
	if err != nil {
		return nil, nil, wrap(ErrIO, "transaction-read-stable", full, "reinspect file path", err)
	}
	if !isPlainRegular(current) || !os.SameFile(opened, current) {
		return nil, nil, fail(ErrRootChanged, "transaction-read-stable", full, "file path changed while it was read")
	}
	return data, afterRead, nil
}

// Rename moves one plain entry between two absent/present root-relative names.
// The destination must not exist. installed is true once the confined rename
// succeeded, including when a later identity check fails.
func (t *Transaction) Rename(oldRelative, newRelative string) (installed bool, returnErr error) {
	oldFull, release, err := t.beginPath("transaction-rename", oldRelative, false)
	if err != nil {
		return false, err
	}
	defer release()
	newFull, err := validatePortablePath(newRelative, false)
	if err != nil {
		return false, wrap(ErrInvalidPath, "transaction-rename", newRelative, "invalid destination path", err)
	}
	if err := t.requirePlainParents(oldFull); err != nil {
		return false, err
	}
	if err := t.requirePlainParents(newFull); err != nil {
		return false, err
	}
	source, err := t.root.fs.Lstat(nativePath(oldFull))
	if err != nil {
		return false, wrap(ErrIO, "transaction-rename", oldFull, "inspect rename source", err)
	}
	if !isPlainDirectory(source) && !isPlainRegular(source) {
		return false, fail(ErrUnsafeEntry, "transaction-rename", oldFull, "rename source must be a plain directory or regular file")
	}
	if _, err := t.root.fs.Lstat(nativePath(newFull)); err == nil {
		return false, fail(ErrUnsafeEntry, "transaction-rename", newFull, "rename destination already exists")
	} else if !os.IsNotExist(err) {
		return false, wrap(ErrIO, "transaction-rename", newFull, "inspect rename destination", err)
	}
	if err := t.root.fs.Rename(nativePath(oldFull), nativePath(newFull)); err != nil {
		return false, wrap(ErrIO, "transaction-rename", oldFull, "rename root-relative entry", err)
	}
	installed = true
	destination, err := t.root.fs.Lstat(nativePath(newFull))
	if err != nil {
		return true, wrap(ErrIO, "transaction-rename", newFull, "inspect renamed entry", err)
	}
	if (!isPlainDirectory(destination) && !isPlainRegular(destination)) || !os.SameFile(source, destination) {
		return true, fail(ErrRootChanged, "transaction-rename", newFull, "destination does not identify the renamed entry")
	}
	if _, err := t.root.fs.Lstat(nativePath(oldFull)); !os.IsNotExist(err) {
		if err == nil {
			return true, fail(ErrRootChanged, "transaction-rename", oldFull, "source name still exists after rename")
		}
		return true, wrap(ErrIO, "transaction-rename", oldFull, "reinspect rename source", err)
	}
	return true, nil
}

// Walk returns a deterministic point-in-time traversal rooted at relative.
// It rejects symlinks, reparse-like irregular entries, devices, and sockets.
func (t *Transaction) Walk(relative string) ([]TreeEntry, error) {
	full, release, err := t.beginPath("transaction-walk", relative, true)
	if err != nil {
		return nil, err
	}
	defer release()
	entries := make([]TreeEntry, 0, 16)
	if err := t.walk(full, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (t *Transaction) walk(full string, entries *[]TreeEntry) error {
	if err := t.requirePlainParents(full); err != nil {
		return err
	}
	info, err := t.root.fs.Lstat(nativePath(full))
	if err != nil {
		return wrap(ErrIO, "transaction-walk", full, "inspect tree entry", err)
	}
	if !isPlainDirectory(info) && !isPlainRegular(info) {
		return fail(ErrUnsafeEntry, "transaction-walk", full, "tree may contain only plain directories and regular files")
	}
	*entries = append(*entries, TreeEntry{Path: full, Info: info})
	if isPlainRegular(info) {
		return nil
	}
	return t.walkDirectory(full, info, entries)
}

func (t *Transaction) walkDirectory(full string, info os.FileInfo, entries *[]TreeEntry) error {
	directory, err := t.root.fs.Open(nativePath(full))
	if err != nil {
		return wrap(ErrIO, "transaction-walk", full, "open tree directory", err)
	}
	defer func() { _ = directory.Close() }()
	opened, err := directory.Stat()
	if err != nil {
		return wrap(ErrIO, "transaction-walk", full, "stat opened tree directory", err)
	}
	if !isPlainDirectory(opened) || !os.SameFile(info, opened) {
		return fail(ErrRootChanged, "transaction-walk", full, "directory changed while it was opened")
	}
	children, err := directory.ReadDir(-1)
	if err != nil {
		return wrap(ErrIO, "transaction-walk", full, "read tree directory", err)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		canonical, err := portableChildPath(full, child.Name(), "transaction-walk")
		if err != nil {
			return err
		}
		if err := t.walk(canonical, entries); err != nil {
			return err
		}
	}
	after, err := directory.Stat()
	if err != nil {
		return wrap(ErrIO, "transaction-walk", full, "restat opened tree directory", err)
	}
	current, err := t.root.fs.Lstat(nativePath(full))
	if err != nil {
		return wrap(ErrIO, "transaction-walk", full, "reinspect tree directory path", err)
	}
	if !isPlainDirectory(after) || !isPlainDirectory(current) || !os.SameFile(opened, after) || !os.SameFile(opened, current) {
		return fail(ErrRootChanged, "transaction-walk", full, "tree directory changed during traversal")
	}
	return nil
}

// RemoveTree removes a private plain tree beneath the held root. It never
// follows links and verifies each directory handle against its name before
// removing that name. The root itself (.) cannot be removed.
func (t *Transaction) RemoveTree(relative string) error {
	full, release, err := t.beginPath("transaction-remove-tree", relative, false)
	if err != nil {
		return err
	}
	defer release()
	return t.removeTree(full)
}

func (t *Transaction) removeTree(full string) error {
	if err := t.requirePlainParents(full); err != nil {
		return err
	}
	info, err := t.root.fs.Lstat(nativePath(full))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return wrap(ErrIO, "transaction-remove-tree", full, "inspect tree entry", err)
	}
	if isPlainRegular(info) {
		if err := t.root.fs.Remove(nativePath(full)); err != nil {
			return wrap(ErrIO, "transaction-remove-tree", full, "remove regular file", err)
		}
		return nil
	}
	if !isPlainDirectory(info) {
		return fail(ErrUnsafeEntry, "transaction-remove-tree", full, "cleanup tree may contain only plain directories and regular files")
	}
	return t.removeDirectory(full, info)
}

func (t *Transaction) removeDirectory(full string, info os.FileInfo) error {
	directory, err := t.root.fs.Open(nativePath(full))
	if err != nil {
		return wrap(ErrIO, "transaction-remove-tree", full, "open cleanup directory", err)
	}
	opened, err := directory.Stat()
	if err != nil {
		_ = directory.Close()
		return wrap(ErrIO, "transaction-remove-tree", full, "stat cleanup directory", err)
	}
	if !isPlainDirectory(opened) || !os.SameFile(info, opened) {
		_ = directory.Close()
		return fail(ErrRootChanged, "transaction-remove-tree", full, "cleanup directory changed while it was opened")
	}
	children, err := directory.ReadDir(-1)
	if err != nil {
		_ = directory.Close()
		return wrap(ErrIO, "transaction-remove-tree", full, "read cleanup directory", err)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		childPath, err := portableChildPath(full, child.Name(), "transaction-remove-tree")
		if err != nil {
			_ = directory.Close()
			return err
		}
		if err := t.removeTree(childPath); err != nil {
			_ = directory.Close()
			return err
		}
	}
	after, err := directory.Stat()
	closeErr := directory.Close()
	if err != nil {
		return wrap(ErrIO, "transaction-remove-tree", full, "restat cleanup directory", err)
	}
	if closeErr != nil {
		return wrap(ErrIO, "transaction-remove-tree", full, "close cleanup directory", closeErr)
	}
	current, err := t.root.fs.Lstat(nativePath(full))
	if err != nil {
		return wrap(ErrIO, "transaction-remove-tree", full, "reinspect cleanup directory path", err)
	}
	if !isPlainDirectory(after) || !isPlainDirectory(current) || !os.SameFile(opened, after) || !os.SameFile(opened, current) {
		return fail(ErrRootChanged, "transaction-remove-tree", full, "cleanup directory changed before removal")
	}
	if err := t.root.fs.Remove(nativePath(full)); err != nil {
		return wrap(ErrIO, "transaction-remove-tree", full, "remove empty cleanup directory", err)
	}
	return nil
}

func portableChildPath(parent, name, op string) (string, error) {
	child := name
	if parent != "." {
		child = path.Join(parent, name)
	}
	canonical, err := validatePortablePath(child, false)
	if err != nil {
		return "", wrap(ErrInvalidPath, op, child, "tree entry name is not portable", err)
	}
	return canonical, nil
}

func (t *Transaction) begin(op string) (func(), error) {
	if t == nil {
		return nil, fail(ErrClosed, op, "transaction", "transaction is closed or absent")
	}
	t.mu.RLock()
	if t.closed || t.root == nil {
		t.mu.RUnlock()
		return nil, fail(ErrClosed, op, "transaction", "transaction is closed or absent")
	}
	return t.mu.RUnlock, nil
}

func (t *Transaction) beginPath(op, relative string, allowDot bool) (string, func(), error) {
	release, err := t.begin(op)
	if err != nil {
		return "", nil, err
	}
	full, err := validatePortablePath(relative, allowDot)
	if err != nil {
		release()
		return "", nil, wrap(ErrInvalidPath, op, relative, "invalid portable root-relative path", err)
	}
	return full, release, nil
}

func (t *Transaction) requirePlainParents(full string) error {
	parent := path.Dir(full)
	if parent == "." {
		return nil
	}
	current := ""
	for _, segment := range splitPortable(parent) {
		if current == "" {
			current = segment
		} else {
			current = path.Join(current, segment)
		}
		info, err := t.root.fs.Lstat(nativePath(current))
		if err != nil {
			return wrap(ErrIO, "transaction-inspect-parent", current, "inspect root-relative parent", err)
		}
		if !isPlainDirectory(info) {
			return fail(ErrUnsafeEntry, "transaction-inspect-parent", current, "parent component must be a plain directory")
		}
	}
	return nil
}
