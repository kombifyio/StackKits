//go:build darwin

package hostconformance

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

func platformCPUCores() (int, error) {
	count := runtime.NumCPU()
	if count < 1 {
		return 0, fmt.Errorf("host CPU core count is unobserved")
	}
	return count, nil
}

func platformMemoryBytes() (uint64, error) {
	size, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, fmt.Errorf("read host memory: %w", err)
	}
	if size == 0 {
		return 0, fmt.Errorf("host RAM is unobserved")
	}
	return size, nil
}

func platformStorageBytes() (uint64, error) {
	return platformStorageBytesForPath("/")
}

func platformStorageBytesForPath(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("read filesystem size for %q: %w", path, err)
	}
	if stat.Bsize <= 0 || stat.Blocks == 0 {
		return 0, fmt.Errorf("filesystem size for %q is unobserved", path)
	}
	return uint64(stat.Blocks) * uint64(stat.Bsize), nil
}

func platformStorageFreeBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("read filesystem free space for %q: %w", path, err)
	}
	if stat.Bsize <= 0 || stat.Bavail < 0 {
		return 0, fmt.Errorf("filesystem free space for %q is unobserved", path)
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
