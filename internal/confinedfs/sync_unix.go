//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package confinedfs

import "os"

func syncDirectoryHandle(directory *os.File) (bool, error) {
	if err := directory.Sync(); err != nil {
		return false, err
	}
	return true, nil
}
