//go:build windows

package confinedfs

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileRenameInformation is the variable-length FILE_RENAME_INFORMATION
// payload used by NtSetInformationFile. ReplaceIfExists remains false so the
// kernel performs an atomic no-replace rename relative to RootDirectory.
type fileRenameInformation struct {
	ReplaceIfExists bool
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        uint16
}

func renameNoReplace(oldParent, newParent *os.File, oldName, newName string) error {
	source, err := openRenameSource(oldParent, oldName)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(source) }()

	name, err := windows.UTF16FromString(newName)
	if err != nil {
		return err
	}
	name = name[:len(name)-1]
	fileNameLength := uint32(len(name) * 2)
	fileNameOffset := unsafe.Offsetof(fileRenameInformation{}.FileName)
	buffer := make([]byte, int(fileNameOffset)+int(fileNameLength))
	info := (*fileRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.RootDirectory = windows.Handle(newParent.Fd())
	info.FileNameLength = fileNameLength
	copy(unsafe.Slice(&info.FileName, len(name)), name)

	if err := windows.NtSetInformationFile(
		source,
		&windows.IO_STATUS_BLOCK{},
		&buffer[0],
		uint32(len(buffer)),
		windows.FileRenameInformation,
	); err != nil {
		return windowsRenameError(err)
	}
	return nil
}

func openRenameSource(parent *os.File, name string) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: windows.Handle(parent.Fd()),
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	var source windows.Handle
	if err := windows.NtCreateFile(
		&source,
		windows.SYNCHRONIZE|windows.DELETE,
		&attributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		0,
		windows.FILE_SHARE_DELETE|windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	); err != nil {
		return windows.InvalidHandle, windowsRenameError(err)
	}
	return source, nil
}

func windowsRenameError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}
