//go:build windows

package hostconformance

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

func platformCPUCores() (int, error) {
	count := runtime.NumCPU()
	if count < 1 {
		return 0, fmt.Errorf("host CPU core count is unobserved")
	}
	return count, nil
}

func platformMemoryBytes() (uint64, error) {
	var status memoryStatusEx
	status.length = uint32(unsafe.Sizeof(status))
	r1, _, callErr := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if r1 == 0 {
		return 0, fmt.Errorf("read host memory: %w", callErr)
	}
	if status.totalPhys == 0 {
		return 0, fmt.Errorf("host RAM is unobserved")
	}
	return status.totalPhys, nil
}

func platformStorageBytes() (uint64, error) {
	root := `C:\`
	if wd, err := os.Getwd(); err == nil && wd != "" {
		root = wd
	}
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, fmt.Errorf("encode storage root: %w", err)
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(path, &free, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("read host storage: %w", err)
	}
	if total == 0 {
		return 0, fmt.Errorf("host storage is unobserved")
	}
	return total, nil
}
