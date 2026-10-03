//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package confinedfs

import "os"

func verifyMode(os.FileInfo, os.FileMode) (bool, error) { return false, nil }
