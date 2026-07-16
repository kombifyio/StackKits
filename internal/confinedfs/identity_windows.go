//go:build windows

package confinedfs

import (
	"os"
	"syscall"
)

func platformFileIdentity(file *os.File) (Identity, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return Identity{}, err
	}
	var (
		information syscall.ByHandleFileInformation
		callErr     error
	)
	if err := raw.Control(func(handle uintptr) {
		callErr = syscall.GetFileInformationByHandle(syscall.Handle(handle), &information)
	}); err != nil {
		return Identity{}, err
	}
	if callErr != nil {
		return Identity{}, callErr
	}
	fileID := uint64(information.FileIndexHigh)<<32 | uint64(information.FileIndexLow)
	return Identity{Scheme: "windows-volume-fileid", Volume: uint64(information.VolumeSerialNumber), File: fileID}, nil
}
