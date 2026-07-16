//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package confinedfs

import "os"

func syncDirectoryHandle(_ *os.File) (bool, error) { return false, nil }
