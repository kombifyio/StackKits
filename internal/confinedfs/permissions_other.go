//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package confinedfs

import "os"

func verifyMode0600(_ os.FileInfo) (bool, error) { return false, nil }
