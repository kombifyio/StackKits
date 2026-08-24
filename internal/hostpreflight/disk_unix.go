//go:build !windows

package hostpreflight

import "syscall"

// freeBytes returns the free space a non-privileged writer can still use at
// path. The unprivileged figure (Bavail) is the honest one: reserved blocks are
// not available to Apply.
func freeBytes(path string) (uint64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, false
	}
	return stat.Bavail * uint64(stat.Bsize), true
}
