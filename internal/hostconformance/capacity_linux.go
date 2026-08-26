//go:build linux

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
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0, fmt.Errorf("read host memory: %w", err)
	}
	unit := uint64(info.Unit)
	if unit == 0 {
		unit = 1
	}
	return uint64(info.Totalram) * unit, nil
}

func platformStorageBytes() (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err != nil {
		return 0, fmt.Errorf("read root filesystem size: %w", err)
	}
	if stat.Bsize <= 0 || stat.Blocks == 0 {
		return 0, fmt.Errorf("root filesystem size is unobserved")
	}
	return stat.Blocks * uint64(stat.Bsize), nil
}
