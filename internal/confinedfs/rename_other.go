//go:build !linux && !darwin && !windows

package confinedfs

import (
	"fmt"
	"os"
	"runtime"
)

func renameNoReplace(_, _ *os.File, _, _ string) error {
	return fmt.Errorf("atomic no-replace rename is unsupported on %s", runtime.GOOS)
}
