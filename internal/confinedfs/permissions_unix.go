//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package confinedfs

import (
	"fmt"
	"os"
)

func verifyMode(info os.FileInfo, want os.FileMode) (bool, error) {
	if info.Mode().Perm() != want {
		return false, fmt.Errorf("mode is %04o, want %04o", info.Mode().Perm(), want)
	}
	return true, nil
}
