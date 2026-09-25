//go:build !linux

package hostmaintenance

import (
	"errors"
	"os"
)

// Host maintenance refuses non-Linux systems before it touches locks or
// processes; these exist so the package builds everywhere.

var errLinuxOnly = errors.New("host maintenance runs on Linux only")

func posixLockHolder(string) (bool, int, error) { return false, 0, nil }

func tryPosixLock(string, os.FileMode) (func(), bool, error) { return nil, false, errLinuxOnly }

func listProcesses() ([]Process, error) { return nil, errLinuxOnly }
