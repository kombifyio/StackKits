//go:build !linux && !darwin && !windows

package confinedfs

import (
	"fmt"
	"os"
	"runtime"
)

func renameEntry(_, _ *os.File, _, _ string, _ bool) error {
	return fmt.Errorf("atomic held-parent rename is unsupported on %s", runtime.GOOS)
}

func renameDomainForDirectory(directory *os.File) (Identity, error) {
	return identityForOpenFile(directory)
}
