//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package confinedfs

import (
	"fmt"
	"os"
)

func platformTryLockOutputFile(_ *os.File) error {
	return fmt.Errorf("advisory output locks are unsupported on this platform")
}

func platformUnlockOutputFile(_ *os.File) error { return nil }

func platformOutputLockKey(canonical string) string { return canonical }

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
