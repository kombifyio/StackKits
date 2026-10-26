//go:build darwin

package confinedfs

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameEntry(oldParent, newParent *os.File, oldName, newName string, replace bool) error {
	flags := uint32(unix.RENAME_EXCL)
	if replace {
		flags = 0
	}
	return unix.RenameatxNp(int(oldParent.Fd()), oldName, int(newParent.Fd()), newName, flags)
}

func renameDomainForDirectory(directory *os.File) (Identity, error) {
	return identityForOpenFile(directory)
}
