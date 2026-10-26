//go:build linux

package opentofu

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func requireKomodoProcessPlatform() error {
	// Cancellation must be able to enumerate this process's owned children.
	// Refuse a kernel/procfs configuration that cannot support that invariant.
	if _, err := os.ReadFile("/proc/self/task/" + strconv.Itoa(os.Getpid()) + "/children"); err != nil {
		return errors.New("Komodo process custody requires readable Linux process children")
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return errors.New("Komodo process custody requires Linux pidfd support")
	}
	unix.Close(fd)
	return nil
}
func configureKomodoOwnerCancellation(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
}

func runKomodoOwnedProcess(ctx context.Context, request komodoProcessRequest) (int, error) {
	// This dedicated process, not the CLI application, adopts orphaned providers
	// and local-exec descendants. Its received project lease closes only after
	// wait4 proves ECHILD, including when OpenTofu exits before a provider.
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return 1, errors.New("Komodo process custody cannot establish a child subreaper")
	}
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	ctx, deadline := context.WithTimeout(ctx, 14*time.Minute)
	defer deadline()
	cmd := exec.Command(request.Binary, request.Args...)
	cmd.Dir = request.root()
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 1, errors.New("Komodo process owner could not start pinned OpenTofu")
	}
	defer cmd.Process.Release()
	leader, exitCode := cmd.Process.Pid, 1
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	cancelled := false
	finishing := false
	for {
		if ctx.Err() != nil {
			cancelled = true
		}
		if cancelled || finishing {
			killKomodoOwnedChildren()
		}
		var status unix.WaitStatus
		pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
		if err == unix.EINTR {
			continue
		}
		if err == unix.ECHILD {
			if cancelled {
				return 1, errors.New("Komodo OpenTofu cancelled after reaping its process tree")
			}
			return exitCode, nil
		}
		if err != nil {
			cancel()
			continue
		} // retain custody while forcing owned children down
		if pid > 0 {
			if pid == leader {
				finishing = true
				exitCode = status.ExitStatus()
				if exitCode < 0 {
					exitCode = 1
				}
			}
			continue
		}
		<-ticker.C
	}
}

// Enumerate only this owner's direct children. Never traverse a child PID's
// procfs tree: its number could be recycled after its parent reaps it. Killing
// direct children makes surviving descendants adopted direct children of this
// subreaper; the wait4 loop repeats until ECHILD before releasing the lease.
func killKomodoOwnedChildren() {
	parent := os.Getpid()
	seen := map[int]bool{}
	tasks, _ := filepath.Glob("/proc/self/task/*/children")
	for _, task := range tasks {
		raw, err := os.ReadFile(task)
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(raw)) {
			pid, err := strconv.Atoi(field)
			if err != nil || seen[pid] {
				continue
			}
			seen[pid] = true
			fd, err := unix.PidfdOpen(pid, 0)
			if err != nil {
				continue
			}
			stat, err := os.ReadFile("/proc/" + field + "/stat")
			valid := false
			if err == nil {
				end := strings.LastIndex(string(stat), ")")
				if end >= 0 {
					fields := strings.Fields(string(stat)[end+1:])
					if len(fields) > 1 {
						ppid, _ := strconv.Atoi(fields[1])
						valid = ppid == parent
					}
				}
			}
			if valid {
				unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
			}
			unix.Close(fd)
		}
	}
}
