package confinedfs

import (
	"errors"
	"os"
)

// SyncDirectory flushes one held root-relative directory after a metadata
// mutation. Supported is false, without weakening confinement, when the
// platform/filesystem cannot provide a directory durability primitive.
func (t *Transaction) SyncDirectory(relative string) (supported bool, returnErr error) {
	full, release, err := t.beginPath("transaction-sync-directory", relative, true)
	if err != nil {
		return false, err
	}
	defer release()
	if err := t.requirePlainParents(full); err != nil {
		return false, err
	}
	before, err := t.root.fs.Lstat(nativePath(full))
	if err != nil {
		return false, wrap(ErrIO, "transaction-sync-directory", full, "inspect directory before opening", err)
	}
	if !isPlainDirectory(before) {
		return false, fail(ErrUnsafeEntry, "transaction-sync-directory", full, "entry must be a plain directory")
	}
	directory, err := t.root.fs.Open(nativePath(full))
	if err != nil {
		return false, wrap(ErrIO, "transaction-sync-directory", full, "open held directory", err)
	}
	closed := false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, directory.Close())
		}
	}()
	opened, err := directory.Stat()
	if err != nil {
		return false, wrap(ErrIO, "transaction-sync-directory", full, "stat opened directory", err)
	}
	if !isPlainDirectory(opened) || !os.SameFile(before, opened) {
		return false, fail(ErrRootChanged, "transaction-sync-directory", full, "directory changed while it was opened")
	}
	supported, err = syncDirectoryHandle(directory)
	if err != nil {
		return false, wrap(ErrIO, "transaction-sync-directory", full, "flush directory metadata", err)
	}
	current, err := t.root.fs.Lstat(nativePath(full))
	if err != nil {
		return false, wrap(ErrIO, "transaction-sync-directory", full, "reinspect synced directory", err)
	}
	if !isPlainDirectory(current) || !os.SameFile(opened, current) {
		return false, fail(ErrRootChanged, "transaction-sync-directory", full, "directory path changed while metadata was flushed")
	}
	if err := directory.Close(); err != nil {
		return false, wrap(ErrIO, "transaction-sync-directory", full, "close synced directory", err)
	}
	closed = true
	return supported, nil
}
