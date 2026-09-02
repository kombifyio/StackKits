//go:build windows

package hostconformance

import (
	"fmt"
	"os"
	"runtime"
	"strings"
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
	return platformStorageBytesForPath(`C:\`)
}

func platformStorageBytesForPath(root string) (uint64, error) {
	if wd, err := os.Getwd(); err == nil && wd != "" {
		if root == `C:\` {
			root = wd
		}
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

func platformStorageFreeBytes(path string) (uint64, error) {
	root := strings.TrimSpace(path)
	if root == "" {
		root = `C:\`
	}
	pathPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, fmt.Errorf("encode storage path: %w", err)
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("read storage free space for %q: %w", root, err)
	}
	if total == 0 {
		return 0, fmt.Errorf("storage free space for %q is unobserved", root)
	}
	return free, nil
}
