package confinedfs

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// View is a portable relative prefix borrowed from one held Root.
type View struct {
	root   *Root
	prefix string
}

// Prefix returns the portable root-relative prefix represented by the View.
func (v View) Prefix() string { return v.prefix }

// Sub returns another View beneath the same held Root.
func (v View) Sub(relative string) (View, error) {
	if v.root == nil {
		return View{}, fail(ErrClosed, "sub", "root", "view has no held filesystem root")
	}
	release, err := v.root.acquireRead("sub")
	if err != nil {
		return View{}, err
	}
	defer release()
	if err := v.root.verifyPathIdentityLocked(); err != nil {
		return View{}, err
	}
	full, err := v.resolve(relative, true)
	if err != nil {
		return View{}, err
	}
	return View{root: v.root, prefix: full}, nil
}

// Lstat returns a point-in-time observation without following the final link.
// It is not a tree snapshot or authorization proof. All parent components
// must be plain directories while this operation holds its root lease.
func (v View) Lstat(relative string) (os.FileInfo, error) {
	full, release, err := v.begin("lstat", relative, true)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := v.requirePlainParents(full); err != nil {
		return nil, err
	}
	info, err := v.root.fs.Lstat(nativePath(full))
	if err != nil {
		return nil, wrap(ErrIO, "lstat", full, "inspect root-relative entry", err)
	}
	if err := v.root.verifyPathIdentityLocked(); err != nil {
		return nil, err
	}
	return info, nil
}

// Open opens a plain regular file for reading beneath the held Root.
func (v View) Open(relative string) (*os.File, error) {
	full, release, err := v.begin("open", relative, false)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := v.requirePlainParents(full); err != nil {
		return nil, err
	}
	before, beforeExists, err := v.optionalLstat(full)
	if err != nil {
		return nil, err
	}
	if beforeExists && !isPlainRegular(before) {
		return nil, fail(ErrUnsafeEntry, "open", full, "file target must be a plain regular file")
	}
	file, err := v.root.fs.Open(nativePath(full))
	if err != nil {
		return nil, wrap(ErrIO, "open", full, "open root-relative file", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return nil, wrap(ErrIO, "open", full, "stat opened file handle", err)
	}
	if !isPlainRegular(opened) {
		return nil, fail(ErrUnsafeEntry, "open", full, "opened handle is not a plain regular file")
	}
	if beforeExists && !os.SameFile(before, opened) {
		return nil, fail(ErrRootChanged, "open", full, "file changed between path inspection and open")
	}
	after, err := v.root.fs.Lstat(nativePath(full))
	if err != nil {
		return nil, wrap(ErrIO, "open", full, "reinspect opened file path", err)
	}
	if !isPlainRegular(after) || !os.SameFile(opened, after) {
		return nil, fail(ErrRootChanged, "open", full, "file path changed while it was opened")
	}
	if err := v.requirePlainParents(full); err != nil {
		return nil, err
	}
	if err := v.root.verifyPathIdentityLocked(); err != nil {
		return nil, err
	}
	keep = true
	return file, nil
}

// Identity returns a diagnostic, short-lived platform identity for a plain
// directory or regular file. The target handle is closed before return, so the
// value must not become a journal, CAS, or authorization identity.
func (v View) Identity(relative string) (Identity, error) {
	full, release, err := v.begin("identity", relative, true)
	if err != nil {
		return Identity{}, err
	}
	defer release()
	identity, err := v.root.identityWithoutBoundary(full)
	if err != nil {
		return Identity{}, err
	}
	if err := v.requirePlainParents(full); err != nil {
		return Identity{}, err
	}
	if err := v.root.verifyPathIdentityLocked(); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func (v View) begin(op, relative string, allowDot bool) (string, func(), error) {
	if v.root == nil {
		return "", nil, fail(ErrClosed, op, "root", "view has no held filesystem root")
	}
	release, err := v.root.acquireRead(op)
	if err != nil {
		return "", nil, err
	}
	if err := v.root.verifyPathIdentityLocked(); err != nil {
		release()
		return "", nil, err
	}
	full, err := v.resolve(relative, allowDot)
	if err != nil {
		release()
		return "", nil, err
	}
	return full, release, nil
}

func (v View) resolve(relative string, allowDot bool) (string, error) {
	if v.root == nil {
		return "", fail(ErrClosed, "resolve", "root", "view has no held filesystem root")
	}
	canonical, err := validatePortablePath(relative, allowDot)
	if err != nil {
		return "", wrap(ErrInvalidPath, "resolve", relative, "invalid portable root-relative path", err)
	}
	full := canonical
	if v.prefix != "." {
		full = path.Join(v.prefix, canonical)
	}
	full, err = validatePortablePath(full, true)
	if err != nil {
		return "", wrap(ErrInvalidPath, "resolve", relative, "resolved path escaped its View", err)
	}
	return full, nil
}

func (v View) requirePlainParents(full string) error {
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
		info, err := v.root.fs.Lstat(nativePath(current))
		if err != nil {
			return wrap(ErrIO, "inspect-parent", current, "inspect root-relative parent", err)
		}
		if !isPlainDirectory(info) {
			return fail(ErrUnsafeEntry, "inspect-parent", current, "parent component must be a plain directory")
		}
	}
	return nil
}

func (v View) optionalLstat(full string) (os.FileInfo, bool, error) {
	info, err := v.root.fs.Lstat(nativePath(full))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, wrap(ErrIO, "lstat", full, "inspect optional root-relative entry", err)
	}
	return info, true, nil
}

func (r *Root) identityWithoutBoundary(full string) (Identity, error) {
	info, err := r.fs.Lstat(nativePath(full))
	if err != nil {
		return Identity{}, wrap(ErrIO, "identity", full, "inspect identity target", err)
	}
	if !isPlainDirectory(info) && !isPlainRegular(info) {
		return Identity{}, fail(ErrUnsafeEntry, "identity", full, "identity target must be a plain directory or regular file")
	}
	file, err := r.fs.Open(nativePath(full))
	if err != nil {
		return Identity{}, wrap(ErrIO, "identity", full, "open identity target", err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return Identity{}, wrap(ErrIO, "identity", full, "stat identity handle", err)
	}
	if (!isPlainDirectory(opened) && !isPlainRegular(opened)) || !os.SameFile(info, opened) {
		return Identity{}, fail(ErrRootChanged, "identity", full, "identity target changed while it was opened")
	}
	identity, err := identityForOpenFile(file)
	if err != nil {
		return Identity{}, err
	}
	after, err := r.fs.Lstat(nativePath(full))
	if err != nil {
		return Identity{}, wrap(ErrIO, "identity", full, "reinspect identity target", err)
	}
	if !os.SameFile(opened, after) {
		return Identity{}, fail(ErrRootChanged, "identity", full, "identity target path changed while inspected")
	}
	return identity, nil
}

func splitPortable(value string) []string {
	if value == "." || value == "" {
		return nil
	}
	return pathSegments(value)
}

func pathSegments(value string) []string {
	return strings.Split(value, "/")
}

func nativePath(portable string) string { return filepath.FromSlash(portable) }
