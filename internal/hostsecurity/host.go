// Package hostsecurity observes and repairs the StackKits host security
// baseline on one node and produces versioned, expiring evidence of it.
//
// It is the node-side half of continuous host-security evidence:
// Techstack dispatches the operation through the pinned StackKits CLI, and a
// standalone (Standard Mode) operator runs the same commands without any
// account. Every host interaction goes through the Host interface so the
// decisions (what is compliant, what may be repaired without cutting the
// management path) are testable without root and on any operating system.
package hostsecurity

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Output is what a command printed and how it exited.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Host is every interaction the baseline has with the node.
type Host interface {
	// LookPath resolves an executable like exec.LookPath.
	LookPath(name string) (string, error)
	// ReadFile reads a file like os.ReadFile.
	ReadFile(path string) ([]byte, error)
	// Stat reports file metadata like os.Stat.
	Stat(path string) (fs.FileInfo, error)
	// Run executes a command by name or absolute path. A nonzero exit is
	// reported in Output.ExitCode with a nil error; the error is for a command
	// that could not run or a context that ended.
	Run(ctx context.Context, name string, args ...string) (Output, error)
	// WriteFile atomically replaces a file with the given mode.
	WriteFile(path string, data []byte, mode os.FileMode) error
	// Remove deletes a file; a missing file is not an error.
	Remove(path string) error
	// MkdirAll creates a directory and its parents like os.MkdirAll.
	MkdirAll(path string, mode os.FileMode) error
	// Getenv reads one environment variable of the running process.
	Getenv(key string) string
	// Geteuid is the effective user ID of the running process.
	Geteuid() int
	// Now is the host clock.
	Now() time.Time
	// Sleep waits for d or until ctx ends.
	Sleep(ctx context.Context, d time.Duration) error
}

const (
	maxCommandOutput = 4 << 20
	commandTimeout   = 10 * time.Minute
)

// LocalHost is the real node.
type LocalHost struct{}

func (LocalHost) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (LocalHost) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (LocalHost) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

func (LocalHost) Run(ctx context.Context, name string, args ...string) (Output, error) {
	bounded, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	process := exec.CommandContext(bounded, name, args...)
	// A predictable message catalog keeps parsing independent of the node's
	// locale, and apt must never wait on an interactive prompt.
	process.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "DEBIAN_FRONTEND=noninteractive")
	stdout := &boundedBuffer{remaining: maxCommandOutput}
	stderr := &boundedBuffer{remaining: maxCommandOutput}
	process.Stdout, process.Stderr = stdout, stderr
	err := process.Run()
	output := Output{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return output, ctxErr
	}
	if bounded.Err() != nil {
		return output, bounded.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		output.ExitCode = exitErr.ExitCode()
		return output, nil
	}
	if stdout.exceeded || stderr.exceeded {
		return output, errors.New("command output exceeded the evidence bound")
	}
	return output, err
}

func (LocalHost) WriteFile(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if existing, err := os.Lstat(path); err == nil && existing.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to replace a symlink: " + path)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (LocalHost) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (LocalHost) MkdirAll(path string, mode os.FileMode) error { return os.MkdirAll(path, mode) }

func (LocalHost) Getenv(key string) string { return os.Getenv(key) }

func (LocalHost) Geteuid() int { return os.Geteuid() }

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

type boundedBuffer struct {
	bytes.Buffer
	remaining int
	exceeded  bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if len(value) > b.remaining {
		value = value[:b.remaining]
		b.exceeded = true
	}
	b.remaining -= len(value)
	_, _ = b.Buffer.Write(value)
	return original, nil
}
