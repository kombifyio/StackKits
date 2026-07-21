//go:build windows

package confinedfs

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func platformTryLockOutputFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errPlatformOutputLockBusy
	}
	return err
}

func platformUnlockOutputFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
}

// Windows path lookup is case-insensitive for the supported workspace model,
// so case aliases must map to one lock object. Go's invariant Unicode upper
// mapping collapses ASCII and ordinary Unicode case aliases deterministically.
// It cannot exactly reproduce every filesystem- or Windows-version-specific
// Unicode upcase table; portable ASCII output-root segments remain the only
// fully cross-volume spelling contract.
func platformOutputLockKey(canonical string) string { return strings.ToUpper(canonical) }

// Windows FileMode bits do not prove ACL equivalence to POSIX 0700/0600. The
// held-root checks still require plain directories/files and reject links.
func platformVerifyPrivateOutputLockDirectory(info os.FileInfo) error {
	if !isPlainDirectory(info) {
		return fmt.Errorf("entry is not a plain directory")
	}
	return nil
}

func platformVerifyPrivateOutputLockFile(info os.FileInfo) error {
	if !isPlainRegular(info) {
		return fmt.Errorf("entry is not a plain regular file")
	}
	return nil
}
