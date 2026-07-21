//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package confinedfs

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func platformTryLockOutputFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return errPlatformOutputLockBusy
	}
	return err
}

func platformUnlockOutputFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

// Unix output paths retain byte-for-byte case distinctions in the lock key.
func platformOutputLockKey(canonical string) string { return canonical }

func platformVerifyPrivateOutputLockDirectory(info os.FileInfo) error {
	if !isPlainDirectory(info) {
		return fmt.Errorf("entry is not a plain directory")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("mode is %04o, want 0700", info.Mode().Perm())
	}
	return nil
}

func platformVerifyPrivateOutputLockFile(info os.FileInfo) error {
	if !isPlainRegular(info) {
		return fmt.Errorf("entry is not a plain regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("mode is %04o, want 0600", info.Mode().Perm())
	}
	return nil
}
