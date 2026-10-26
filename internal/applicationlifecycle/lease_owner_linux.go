//go:build linux

package applicationlifecycle

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// leaseOwnerEnv tags this process and every lifecycle child that inherits
// its environment. The reaper stops only processes that carry the dead
// owner's exact token, which is positive proof of descent; process-group,
// session and PID values are never used to choose a target.
const leaseOwnerEnv = "STACKKIT_LEASE_OWNER"

var (
	ownerTokenOnce sync.Once
	ownerToken     string
)

// processOwnerToken returns this process's random owner token and exports it
// to the environment children inherit. An inherited token (a lifecycle
// command started by another lifecycle command) is replaced: each owner
// reaps only what it started.
func processOwnerToken() string {
	ownerTokenOnce.Do(func() {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return
		}
		token := hex.EncodeToString(raw)
		if os.Setenv(leaseOwnerEnv, token) == nil {
			ownerToken = token
		}
	})
	return ownerToken
}

func currentLeaseOwner() (leaseOwner, bool) {
	stat, ok := readProcStat(os.Getpid())
	token := processOwnerToken()
	if !ok || token == "" {
		return leaseOwner{}, false
	}
	return leaseOwner{PID: os.Getpid(), PGID: stat.pgrp, SID: stat.session, StartTicks: stat.start, BootID: currentBootID(), Token: token}, true
}

func currentBootID() string {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

type procStat struct {
	ppid, pgrp, session int
	start               uint64
}

// readProcStat parses /proc/<pid>/stat after the parenthesised command name,
// which may itself contain spaces or parentheses.
func readProcStat(pid int) (procStat, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, false
	}
	text := string(raw)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return procStat{}, false
	}
	fields := strings.Fields(text[end+1:])
	// fields[0] is state (field 3); ppid 4, pgrp 5, session 6, starttime 22.
	if len(fields) < 20 {
		return procStat{}, false
	}
	ppid, err0 := strconv.Atoi(fields[1])
	pgrp, err1 := strconv.Atoi(fields[2])
	session, err2 := strconv.Atoi(fields[3])
	start, err3 := strconv.ParseUint(fields[19], 10, 64)
	if err0 != nil || err1 != nil || err2 != nil || err3 != nil {
		return procStat{}, false
	}
	return procStat{ppid: ppid, pgrp: pgrp, session: session, start: start}, true
}

// carriesOwnerToken reports whether the process environment holds exactly
// this owner token.
func carriesOwnerToken(pid int, token string) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return false
	}
	want := []byte(leaseOwnerEnv + "=" + token)
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if bytes.Equal(entry, want) {
			return true
		}
	}
	return false
}

// protectedPIDs are this process, its ancestors and the members of its own
// process group, which the reaper never signals.
func protectedPIDs() (map[int]bool, int) {
	protected := map[int]bool{}
	self, _ := readProcStat(os.Getpid())
	for pid := os.Getpid(); pid > 1 && !protected[pid]; {
		protected[pid] = true
		stat, ok := readProcStat(pid)
		if !ok {
			break
		}
		pid = stat.ppid
	}
	return protected, self.pgrp
}

// reapLeaseOwnerOrphans stops leftover processes of the dead owner. A target
// must carry the owner's exact token in its environment, run in the same
// boot and be neither this process, an ancestor nor a member of our group.
// Each target is pinned with pidfd_open, re-verified through the pinned
// identity, and only then signalled with pidfd_send_signal, so a PID reused
// between the check and the signal is never hit. Without that evidence
// nothing is stopped.
func reapLeaseOwnerOrphans(owner leaseOwner) string {
	if owner.BootID == "" || owner.BootID != currentBootID() {
		return "the owner ran before the current boot, so none of its child processes survive"
	}
	if len(owner.Token) != 32 {
		return "the owner recorded no process token, so no child process was stopped; effects of the old operation may still be in progress"
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "the process table is unreadable, so no child process was stopped; effects of the old operation may still be in progress"
	}
	protected, ownGroup := protectedPIDs()
	stopped, failed := 0, 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || protected[pid] {
			continue
		}
		stat, ok := readProcStat(pid)
		if !ok || stat.pgrp == ownGroup || !carriesOwnerToken(pid, owner.Token) {
			continue
		}
		fd, err := unix.PidfdOpen(pid, 0)
		if errors.Is(err, unix.ESRCH) {
			continue // exited meanwhile
		}
		if err != nil {
			failed++ // matched but cannot be pinned (kernel before 5.3 or blocked syscall)
			continue
		}
		again, ok := readProcStat(pid)
		if ok && again.start == stat.start && again.pgrp != ownGroup && carriesOwnerToken(pid, owner.Token) {
			if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil {
				failed++
			} else {
				stopped++
			}
		}
		unix.Close(fd)
	}
	switch {
	case failed > 0:
		return fmt.Sprintf("stopped %d and could not stop %d leftover child processes of the owner; effects of the old operation may still be in progress", stopped, failed)
	case stopped == 0:
		return "found no leftover child process carrying the owner's token; processes started without the inherited environment were not stopped, so effects of the old operation may still be in progress"
	default:
		return fmt.Sprintf("stopped %d leftover child processes carrying the owner's token", stopped)
	}
}
