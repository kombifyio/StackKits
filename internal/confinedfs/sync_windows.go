//go:build windows

package confinedfs

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func syncDirectoryHandle(directory *os.File) (bool, error) {
	if err := directory.Sync(); err != nil {
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) ||
			errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
			errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
