//go:build !windows

package runtimeexecutorlocal

import (
	"errors"
	"os"
)

func restrictBasementRuntimeFile(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("Basement runtime file permissions are not owner-only")
	}
	return nil
}
