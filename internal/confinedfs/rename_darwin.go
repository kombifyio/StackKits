//go:build darwin

package confinedfs

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameNoReplace(oldParent, newParent *os.File, oldName, newName string) error {
	return unix.RenameatxNp(int(oldParent.Fd()), oldName, int(newParent.Fd()), newName, unix.RENAME_EXCL)
}
