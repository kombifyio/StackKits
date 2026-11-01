//go:build linux

package lifecyclemutation

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// initialPIDNamespace is the kernel's fixed identity of the initial PID
// namespace (PROC_PID_INIT_INO). Only from there does /proc list every
// process on the host.
const initialPIDNamespace = "pid:[4026531836]"

const joinOperationFlag = "--internal-lifecycle-operation"

// requireLegacyClaimantGone proves that no process still carries the
// operation's lifecycle join flags. An older child released its claim lock
// right after claiming, so the lock cannot tell a dead claimant from a live
// one. Every child is started with --internal-lifecycle-operation <id>, and
// recovery holds the shared lifecycle lock, so no new child of the operation
// can start while it reads the host's complete process table. It fails
// closed when that table is not provably complete.
func requireLegacyClaimantGone(operationID string) error {
	namespace, err := os.Readlink("/proc/self/ns/pid")
	if err != nil || namespace != initialPIDNamespace {
		return errors.New("an older StackKits child claimed this phase; prove it ended by running recovery as root in the host's initial PID namespace")
	}
	var proc unix.Statfs_t
	if err := unix.Statfs("/proc", &proc); err != nil || proc.Type != unix.PROC_SUPER_MAGIC {
		return errors.New("an older StackKits child claimed this phase and /proc is not the kernel process table")
	}
	// /proc mounted with hidepid hides other users' processes from anyone
	// without CAP_SYS_PTRACE, root included.
	if os.Geteuid() != 0 || !effectiveCapability(unix.CAP_SYS_PTRACE) {
		return errors.New("an older StackKits child claimed this phase; only root with CAP_SYS_PTRACE sees every process that could still carry it")
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return fmt.Errorf("list the host process table: %w", err)
	}
	self := os.Getpid()
	operation := []byte(operationID)
	inline := []byte(joinOperationFlag + "=" + operationID)
	for _, entry := range entries {
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid == self {
			continue
		}
		raw, readErr := os.ReadFile("/proc/" + entry.Name() + "/cmdline")
		if errors.Is(readErr, fs.ErrNotExist) || errors.Is(readErr, syscall.ESRCH) {
			continue
		}
		if readErr != nil {
			return fmt.Errorf("read process %d to prove the claimed child ended: %w", pid, readErr)
		}
		arguments := bytes.Split(raw, []byte{0})
		for index, argument := range arguments {
			if bytes.Equal(argument, inline) ||
				(string(argument) == joinOperationFlag && index+1 < len(arguments) &&
					bytes.Equal(arguments[index+1], operation)) {
				return fmt.Errorf("process %d still runs as a child of lifecycle operation %s", pid, operationID)
			}
		}
	}
	return nil
}

// effectiveCapability reads this process's effective capability set; any
// read or parse failure counts as absent.
func effectiveCapability(capability int) bool {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		value, found := strings.CutPrefix(line, "CapEff:")
		if !found {
			continue
		}
		set, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		return err == nil && set&(1<<uint(capability)) != 0
	}
	return false
}
