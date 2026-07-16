//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package confinedfs

import (
	"fmt"
	"os"
	"syscall"
)

func platformFileIdentity(file *os.File) (Identity, error) {
	info, err := file.Stat()
	if err != nil {
		return Identity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return Identity{}, fmt.Errorf("unexpected stat payload %T", info.Sys())
	}
	return Identity{Scheme: "unix-dev-inode", Volume: uint64(stat.Dev), File: uint64(stat.Ino)}, nil
}
