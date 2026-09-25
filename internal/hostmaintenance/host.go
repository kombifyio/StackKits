package hostmaintenance

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Command is one closed argv. No shell interprets it.
type Command struct {
	Name string
	Args []string
}

// Process is one running process: its executable path (as seen from its own
// mount namespace for a container) and its argv.
type Process struct {
	PID  int
	Exe  string
	Args []string
}

// Output is what a command printed and how it exited.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Host is every interaction host maintenance has with the node.
type Host interface {
	// LookPath resolves an executable like exec.LookPath.
	LookPath(name string) (string, error)
	// ReadFile reads a file like os.ReadFile.
	ReadFile(path string) ([]byte, error)
	// Exists reports whether a path exists.
	Exists(path string) bool
	// LockHolder reports whether a process holds a POSIX write lock on path,
	// and its PID when known. A missing file is not locked.
	LockHolder(path string) (held bool, pid int, err error)
	// TryLock takes a POSIX write lock on path without waiting. A missing
	// file is created with createMode, or is an error when createMode is 0.
	// acquired is false when another process holds the lock. release drops
	// it; the kernel drops it when the process exits.
	TryLock(path string, createMode os.FileMode) (release func(), acquired bool, err error)
	// Processes lists every running process with its executable and argv.
	// A process that cannot be read for lack of permission is an error: an
	// incomplete list cannot rule a process out. Processes that exit while
	// they are read are skipped.
	Processes() ([]Process, error)
	// Executable is the absolute path of the running stackkit binary.
	Executable() (string, error)
	// Run executes a command. A nonzero exit is reported in Output.ExitCode
	// with a nil error; the error is for a command that could not run or a
	// context that ended.
	Run(ctx context.Context, command Command) (Output, error)
	// Now is the host clock.
	Now() time.Time
	// Sleep waits for d or until ctx ends.
	Sleep(ctx context.Context, d time.Duration) error
}

// LocalHost is the real node.
type LocalHost struct{}

func (LocalHost) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (LocalHost) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (LocalHost) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (LocalHost) LockHolder(path string) (bool, int, error) { return posixLockHolder(path) }

func (LocalHost) TryLock(path string, createMode os.FileMode) (func(), bool, error) {
	return tryPosixLock(path, createMode)
}

func (LocalHost) Processes() ([]Process, error) { return listProcesses() }

func (LocalHost) Executable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func (LocalHost) Run(ctx context.Context, command Command) (Output, error) {
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	// A predictable, English message catalog keeps lock and error detection
	// independent of the node's locale.
	process.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	err := process.Run()
	output := Output{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return output, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		output.ExitCode = exitErr.ExitCode()
		return output, nil
	}
	return output, err
}

func (LocalHost) Now() time.Time { return time.Now().UTC() }

func (LocalHost) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
