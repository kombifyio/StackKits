//go:build windows

package confinedfs

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func syncDirectoryHandle(directory *os.File) (bool, error) {
	err := directory.Sync()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// os.Root.Open gives the parent a read handle. Windows directory flush
		// needs write access; reopen that held handle, never by a
		// mutable path, and verify the directory identity before flushing it.
		writable, openErr := openDirectorySyncHandle(directory)
		if openErr != nil {
			err = openErr
		} else {
			err = writable.Sync()
			if closeErr := writable.Close(); closeErr != nil {
				return false, errors.Join(err, closeErr)
			}
		}
	}
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) ||
			errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
			errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func openDirectorySyncHandle(directory *os.File) (*os.File, error) {
	expected, err := directory.Stat()
	if err != nil {
		return nil, err
	}
	// An empty NT object name addresses RootDirectory itself. NT paths do not
	// normalize a Win32 '.' segment.
	objectName, err := windows.NewNTUnicodeString("")
	if err != nil {
		return nil, err
	}
	raw, err := directory.SyscallConn()
	if err != nil {
		return nil, err
	}
	var handle windows.Handle
	var openErr error
	if err := raw.Control(func(held uintptr) {
		attributes := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: windows.Handle(held), ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE}
		openErr = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE,
			&attributes, &windows.IO_STATUS_BLOCK{}, nil, 0,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN,
			windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	}); err != nil {
		return nil, err
	}
	if openErr != nil {
		return nil, windowsRenameError(openErr)
	}
	writable := os.NewFile(uintptr(handle), directory.Name())
	actual, err := writable.Stat()
	if err != nil || !isPlainDirectory(actual) || !os.SameFile(expected, actual) {
		_ = writable.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("directory identity changed before metadata flush")
	}
	return writable, nil
}
