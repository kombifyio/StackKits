package confinedfs

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
)

const atomicNameAttempts = 8

// AtomicWriteResult distinguishes a pre-install failure from a failure after
// rename. DirectorySynced is false without error on platforms/filesystems that
// cannot durably flush directory metadata.
type AtomicWriteResult struct {
	Installed           bool
	FileSynced          bool
	DirectorySynced     bool
	PermissionsVerified bool
}

// WriteAtomic0600 writes bytes through an exclusive temporary file, syncs and
// closes its handle, and replaces the target relative to one held parent. The
// parent must already exist. Until the transaction layer adds locking and CAS
// recovery, callers may use this only in a private staging tree without
// concurrent writers.
func (v View) WriteAtomic0600(relative string, data []byte) (result AtomicWriteResult, returnErr error) {
	return v.writeAtomic0600(relative, data, true)
}

// WriteAtomic0600NoReplace publishes a fully written 0600 file through a
// same-parent hard link. The link is the atomic commit point and fails if the
// destination appeared concurrently; an existing file is never replaced.
func (v View) WriteAtomic0600NoReplace(relative string, data []byte) (result AtomicWriteResult, returnErr error) {
	return v.writeAtomic0600(relative, data, false)
}

func (v View) writeAtomic0600(relative string, data []byte, replace bool) (result AtomicWriteResult, returnErr error) {
	full, release, err := v.begin("atomic-write", relative, false)
	if err != nil {
		return result, err
	}
	defer release()
	parent := path.Dir(full)
	if err := v.prepareAtomicParent(full, parent); err != nil {
		return result, err
	}
	parentRoot, parentHandle, parentInfo, err := v.openAtomicParent(parent)
	if err != nil {
		return result, err
	}
	parentClosed := false
	defer func() {
		if !parentClosed {
			_ = parentHandle.Close()
			_ = parentRoot.Close()
		}
	}()
	targetName := path.Base(full)
	if err := requirePlainAtomicTarget(parentRoot, targetName, full); err != nil {
		return result, err
	}
	temporaryName, temporary, err := createAtomicTemporary(parentRoot, parent)
	if err != nil {
		return result, err
	}
	// A failed write deliberately preserves the unpredictable 0600 temporary
	// name. Portable deletion by identity is unavailable; recovery owns it.
	defer func() { _ = temporary.Close() }()
	openedInfo, permissionsVerified, err := writeSyncCloseTemporary(path.Join(parent, temporaryName), temporary, data)
	if err != nil {
		return result, err
	}
	result.FileSynced = true
	result.PermissionsVerified = permissionsVerified
	if err := validateAtomicInstallPaths(parentRoot, full, targetName, temporaryName, openedInfo); err != nil {
		return result, err
	}
	if replace {
		if err := parentRoot.Rename(nativePath(temporaryName), nativePath(targetName)); err != nil {
			return result, wrap(ErrIO, "atomic-write", full, "install temporary file with confined rename", err)
		}
		result.Installed = true
	} else {
		if err := parentRoot.Link(nativePath(temporaryName), nativePath(targetName)); err != nil {
			installErr := wrap(ErrIO, "atomic-write", full, "install temporary file with confined no-replace link", err)
			if cleanupErr := parentRoot.Remove(nativePath(temporaryName)); cleanupErr != nil {
				return result, errors.Join(installErr, wrap(ErrIO, "atomic-write", full, "remove rejected no-replace temporary", cleanupErr))
			}
			return result, installErr
		}
		result.Installed = true
		if err := parentRoot.Remove(nativePath(temporaryName)); err != nil {
			return result, markInstalled(wrap(ErrIO, "atomic-write", full, "remove temporary no-replace link after install", err))
		}
	}
	installedInfo, err := parentRoot.Lstat(nativePath(targetName))
	if err != nil {
		return result, markInstalled(wrap(ErrIO, "atomic-write", full, "inspect installed file", err))
	}
	if !isPlainRegular(installedInfo) || !os.SameFile(openedInfo, installedInfo) {
		return result, markInstalled(fail(ErrRootChanged, "atomic-write", full, "installed path does not identify the temporary file"))
	}
	supported, err := syncDirectoryHandle(parentHandle)
	if err != nil {
		return result, markInstalled(wrap(ErrIO, "atomic-write", parent, "sync containing directory", err))
	}
	result.DirectorySynced = supported
	if err := v.verifyAtomicParent(parent, parentRoot, parentHandle, parentInfo); err != nil {
		return result, markInstalled(err)
	}
	if err := v.root.verifyPathIdentityLocked(); err != nil {
		return result, markInstalled(err)
	}
	if err := closeAtomicParent(parentRoot, parentHandle); err != nil {
		return result, markInstalled(wrap(ErrIO, "atomic-write", parent, "close held atomic-write parent", err))
	}
	parentClosed = true
	return result, nil
}

func (v View) prepareAtomicParent(full, parent string) error {
	if err := v.requirePlainParents(full); err != nil {
		return err
	}
	parentInfo, err := v.root.fs.Lstat(nativePath(parent))
	if err != nil {
		return wrap(ErrIO, "atomic-write", parent, "inspect existing atomic-write parent", err)
	}
	if !isPlainDirectory(parentInfo) {
		return fail(ErrUnsafeEntry, "atomic-write", parent, "atomic-write parent must be an existing plain directory")
	}
	return nil
}

func requirePlainAtomicTarget(parentRoot *os.Root, targetName, displayPath string) error {
	target, err := parentRoot.Lstat(nativePath(targetName))
	exists := err == nil
	if os.IsNotExist(err) {
		exists = false
		err = nil
	}
	if err != nil {
		return wrap(ErrIO, "atomic-write", displayPath, "inspect atomic-write target", err)
	}
	if exists && !isPlainRegular(target) {
		return fail(ErrUnsafeEntry, "atomic-write", displayPath, "atomic-write target must be absent or a plain regular file")
	}
	return nil
}

func writeSyncCloseTemporary(temporaryFull string, temporary *os.File, data []byte) (os.FileInfo, bool, error) {
	if err := temporary.Chmod(0o600); err != nil {
		return nil, false, wrap(ErrIO, "atomic-write", temporaryFull, "set temporary file permissions through its handle", err)
	}
	if _, err := io.Copy(temporary, bytes.NewReader(append([]byte(nil), data...))); err != nil {
		return nil, false, wrap(ErrIO, "atomic-write", temporaryFull, "write temporary file", err)
	}
	if err := temporary.Sync(); err != nil {
		return nil, false, wrap(ErrIO, "atomic-write", temporaryFull, "sync temporary file", err)
	}
	openedInfo, err := temporary.Stat()
	if err != nil {
		return nil, false, wrap(ErrIO, "atomic-write", temporaryFull, "stat temporary file", err)
	}
	if !isPlainRegular(openedInfo) {
		return nil, false, fail(ErrUnsafeEntry, "atomic-write", temporaryFull, "temporary handle is not a plain regular file")
	}
	permissionsVerified, err := verifyMode0600(openedInfo)
	if err != nil {
		return nil, false, wrap(ErrUnsafeEntry, "atomic-write", temporaryFull, "verify temporary file permissions through its handle", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, false, wrap(ErrIO, "atomic-write", temporaryFull, "close temporary file", err)
	}
	return openedInfo, permissionsVerified, nil
}

func validateAtomicInstallPaths(parentRoot *os.Root, full, targetName, temporaryName string, openedInfo os.FileInfo) error {
	temporaryFull := path.Join(path.Dir(full), temporaryName)
	currentTemporary, err := parentRoot.Lstat(nativePath(temporaryName))
	if err != nil {
		return wrap(ErrIO, "atomic-write", temporaryFull, "reinspect temporary file", err)
	}
	if !isPlainRegular(currentTemporary) || !os.SameFile(openedInfo, currentTemporary) {
		return fail(ErrRootChanged, "atomic-write", temporaryFull, "temporary path changed before install")
	}
	return requirePlainAtomicTarget(parentRoot, targetName, full)
}

func createAtomicTemporary(parentRoot *os.Root, parent string) (string, *os.File, error) {
	for attempt := 0; attempt < atomicNameAttempts; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, wrap(ErrIO, "atomic-write", parent, "generate temporary file identity", err)
		}
		name := ".stackkit-tmp-" + hex.EncodeToString(random[:])
		file, err := parentRoot.OpenFile(nativePath(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, file, nil
		}
		if os.IsExist(err) {
			continue
		}
		return "", nil, wrap(ErrIO, "atomic-write", path.Join(parent, name), "create exclusive temporary file", err)
	}
	return "", nil, fail(ErrIO, "atomic-write", parent, "could not allocate a unique temporary file after %d attempts", atomicNameAttempts)
}

func (v View) openAtomicParent(parent string) (*os.Root, *os.File, os.FileInfo, error) {
	before, err := v.root.fs.Lstat(nativePath(parent))
	if err != nil {
		return nil, nil, nil, wrap(ErrIO, "atomic-write", parent, "inspect atomic-write parent before opening", err)
	}
	if !isPlainDirectory(before) {
		return nil, nil, nil, fail(ErrUnsafeEntry, "atomic-write", parent, "atomic-write parent must be a plain directory")
	}
	parentRoot, err := v.root.fs.OpenRoot(nativePath(parent))
	if err != nil {
		return nil, nil, nil, wrap(ErrIO, "atomic-write", parent, "open held atomic-write parent", err)
	}
	keepRoot := false
	defer func() {
		if !keepRoot {
			_ = parentRoot.Close()
		}
	}()
	opened, err := parentRoot.Stat(".")
	if err != nil {
		return nil, nil, nil, wrap(ErrIO, "atomic-write", parent, "stat held atomic-write parent", err)
	}
	if !isPlainDirectory(opened) || !os.SameFile(before, opened) {
		return nil, nil, nil, fail(ErrRootChanged, "atomic-write", parent, "atomic-write parent changed while it was opened")
	}
	parentHandle, err := parentRoot.Open(".")
	if err != nil {
		return nil, nil, nil, wrap(ErrIO, "atomic-write", parent, "open atomic-write parent sync handle", err)
	}
	keepHandle := false
	defer func() {
		if !keepHandle {
			_ = parentHandle.Close()
		}
	}()
	handleInfo, err := parentHandle.Stat()
	if err != nil {
		return nil, nil, nil, wrap(ErrIO, "atomic-write", parent, "stat atomic-write parent sync handle", err)
	}
	after, err := v.root.fs.Lstat(nativePath(parent))
	if err != nil {
		return nil, nil, nil, wrap(ErrIO, "atomic-write", parent, "reinspect atomic-write parent", err)
	}
	if !isPlainDirectory(handleInfo) || !isPlainDirectory(after) || !os.SameFile(opened, handleInfo) || !os.SameFile(opened, after) {
		return nil, nil, nil, fail(ErrRootChanged, "atomic-write", parent, "atomic-write parent path changed while handles were opened")
	}
	keepRoot = true
	keepHandle = true
	return parentRoot, parentHandle, opened, nil
}

func (v View) verifyAtomicParent(parent string, parentRoot *os.Root, parentHandle *os.File, expected os.FileInfo) error {
	held, err := parentRoot.Stat(".")
	if err != nil {
		return wrap(ErrIO, "atomic-write", parent, "reinspect held atomic-write parent", err)
	}
	handleInfo, err := parentHandle.Stat()
	if err != nil {
		return wrap(ErrIO, "atomic-write", parent, "reinspect atomic-write parent sync handle", err)
	}
	named, err := v.root.fs.Lstat(nativePath(parent))
	if err != nil {
		return wrap(ErrIO, "atomic-write", parent, "reinspect named atomic-write parent", err)
	}
	if !isPlainDirectory(held) || !isPlainDirectory(handleInfo) || !isPlainDirectory(named) ||
		!os.SameFile(expected, held) || !os.SameFile(held, handleInfo) || !os.SameFile(held, named) {
		return fail(ErrRootChanged, "atomic-write", parent, "atomic-write parent identity changed during install")
	}
	return nil
}

func closeAtomicParent(parentRoot *os.Root, parentHandle *os.File) error {
	var closeErrors []error
	if err := parentHandle.Close(); err != nil {
		closeErrors = append(closeErrors, err)
	}
	if err := parentRoot.Close(); err != nil {
		closeErrors = append(closeErrors, err)
	}
	return errors.Join(closeErrors...)
}
