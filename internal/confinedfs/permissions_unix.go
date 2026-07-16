//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package confinedfs

import (
	"fmt"
	"os"
)

func verifyMode0600(info os.FileInfo) (bool, error) {
	if info.Mode().Perm() != 0o600 {
		return false, fmt.Errorf("mode is %04o, want 0600", info.Mode().Perm())
	}
	return true, nil
}
