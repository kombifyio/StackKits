//go:build linux

package confinedfs

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameEntry(oldParent, newParent *os.File, oldName, newName string, replace bool) error {
	flags := uint(unix.RENAME_NOREPLACE)
	if replace {
		flags = 0
	}
	return unix.Renameat2(int(oldParent.Fd()), oldName, int(newParent.Fd()), newName, flags)
}

func renameDomainForDirectory(directory *os.File) (Identity, error) {
	var stat unix.Statx_t
	if err := unix.Statx(int(directory.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &stat); err != nil {
		return Identity{}, err
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 {
		return Identity{}, fail(ErrIdentityUnsupported, "staging-admission", "directory", "kernel does not identify the rename mount")
	}
	return Identity{Scheme: "linux-mount-id", Volume: stat.Mnt_id}, nil
}
