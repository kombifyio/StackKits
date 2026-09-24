//go:build !windows

package localevidence

import (
	"fmt"
	"os"
	"syscall"
)

// stackKitServerCredentialOwner returns the uid:gid that owns the credential
// directory, which is the account that ran Apply.
func stackKitServerCredentialOwner(directory string) (string, bool) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return "", false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d:%d", stat.Uid, stat.Gid), true
}
