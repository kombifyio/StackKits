//go:build linux

package hostmaintenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// posixLockHolder asks the kernel whether a write lock on path would conflict
// (F_GETLK). It never takes the lock, so probing cannot itself block apt or
// dpkg. dpkg and apt lock with fcntl; an open-file-description lock reports
// PID -1.
func posixLockHolder(path string) (bool, int, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, 0, nil
		}
		return false, 0, err
	}
	defer func() { _ = file.Close() }()
	probe := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err := syscall.FcntlFlock(file.Fd(), syscall.F_GETLK, &probe); err != nil {
		return false, 0, err
	}
	if probe.Type == syscall.F_UNLCK {
		return false, 0, nil
	}
	return true, int(probe.Pid), nil
}

// tryPosixLock takes the same kind of lock apt and dpkg take (F_SETLK), so a
// held dpkg lock keeps every apt and dpkg run out until it is released.
func tryPosixLock(path string, createMode os.FileMode) (func(), bool, error) {
	flags := os.O_RDWR
	if createMode != 0 {
		flags |= os.O_CREATE
	}
	file, err := os.OpenFile(path, flags, createMode)
	if err != nil {
		return nil, false, err
	}
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err := syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &lock); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { _ = file.Close() }, true, nil
}

// listProcesses reads /proc/<pid>/exe and /proc/<pid>/cmdline of every
// process. Processes in containers are visible here too, with their
// in-container path. Kernel threads have no executable, and a process that
// exits while it is read is skipped.
func listProcesses() ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var processes []Process
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read executable of process %d: %w", pid, err)
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read command line of process %d: %w", pid, err)
		}
		processes = append(processes, Process{
			PID:  pid,
			Exe:  strings.TrimSuffix(target, " (deleted)"),
			Args: strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"),
		})
	}
	return processes, nil
}

func processGone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}
