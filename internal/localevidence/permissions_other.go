//go:build !windows

package localevidence

import (
	"fmt"
	"os"
)

func restrictFileToCurrentUser(path string) error {
	return os.Chmod(path, ownerKeyFileMode)
}

func requireFilePrivateToCurrentUser(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != ownerKeyFileMode {
		return fmt.Errorf("localevidence: private custody file mode is %s, want 0600", info.Mode())
	}
	return nil
}
