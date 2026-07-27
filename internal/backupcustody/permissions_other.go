//go:build !windows

package backupcustody

import (
	"fmt"
	"os"
)

func restrictPathToCurrentUser(string, bool) error {
	return nil
}

func requirePrivatePath(string, bool) error {
	return nil
}

func ProtectPrivatePath(path string, directory bool) error {
	mode := os.FileMode(0o600)
	if directory {
		mode = 0o700
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	return RequirePrivatePath(path, directory)
}

func RequirePrivatePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	want := os.FileMode(0o600)
	if directory {
		want = 0o700
	}
	validType := info.Mode().IsRegular()
	if directory {
		validType = info.IsDir()
	}
	special := info.Mode() & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	if !validType || info.Mode().Perm() != want || special != 0 {
		return fmt.Errorf("private lifecycle path mode is %s, want %04o", info.Mode(), want)
	}
	return nil
}
