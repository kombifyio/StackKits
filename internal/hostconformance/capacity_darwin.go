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
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err != nil {
		return 0, fmt.Errorf("read root filesystem size: %w", err)
	}
	if stat.Bsize <= 0 || stat.Blocks == 0 {
		return 0, fmt.Errorf("root filesystem size is unobserved")
	}
	return uint64(stat.Blocks) * uint64(stat.Bsize), nil
}
