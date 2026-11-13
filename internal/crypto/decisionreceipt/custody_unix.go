//go:build unix

package decisionreceipt

import (
	"os"
	"syscall"
)

// fileOwner returns the owning uid and the link count of one file.
func fileOwner(info os.FileInfo) (uint32, uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, 0, false
	}
	return stat.Uid, uint64(stat.Nlink), true
}

// openNoFollow opens one file for reading without following a symbolic link
// at its last component.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// createExclusive creates one new file with mode 0600 and fails when the name
// already exists, without following a symbolic link.
func createExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
}

// effectiveUser is the uid this process acts as.
func effectiveUser() (uint32, bool) {
	uid := os.Geteuid()
	if uid < 0 {
		return 0, false
	}
	return uint32(uid), true
}
