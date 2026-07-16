//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package confinedfs

import (
	"fmt"
	"os"
	"runtime"
)

func platformFileIdentity(_ *os.File) (Identity, error) {
	return Identity{}, fmt.Errorf("stable file identity is unsupported on %s", runtime.GOOS)
}
