//go:build linux && (amd64 || arm64)

package hostsecurity

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

func runObserveSandbox(command string) int {
	filter, writer, err := os.Pipe()
	if err != nil {
		return 1
	}
	defer func() { _ = filter.Close() }()
	writeErr := binary.Write(writer, binary.NativeEndian, observeSocketFilter())
	if closeErr := writer.Close(); writeErr != nil || closeErr != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, "/usr/bin/setpriv", observeSandboxArgs(command)...)
	process.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	process.Dir = "/"
	process.Stdin, process.Stdout, process.Stderr = os.Stdin, os.Stdout, os.Stderr
	process.ExtraFiles = []*os.File{filter}
	if err := process.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}

func observeSandboxArgs(command string) []string {
	args := []string{
		"--no-new-privs", "--", "/usr/bin/prlimit", "--nproc=16", "--nofile=64", "--core=0", "--cpu=30", "--as=67108864", "--",
		"/usr/bin/bwrap", "--unshare-all", "--unshare-user", "--disable-userns", "--die-with-parent", "--new-session", "--cap-drop", "ALL",
		"--clearenv", "--setenv", "PATH", "/bin", "--setenv", "HOME", "/scratch", "--setenv", "TMPDIR", "/scratch",
		"--setenv", "LC_ALL", "C", "--seccomp", "3", "--tmpfs", "/",
		"--ro-bind", "/bin/busybox", "/bin/busybox",
	}
	for _, applet := range []string{"awk", "cat", "cut", "date", "grep", "head", "ls", "sed", "sort", "tail", "tr", "uniq", "wc"} {
		args = append(args, "--symlink", "busybox", "/bin/"+applet)
	}
	for _, fact := range []string{"cpuinfo", "diskstats", "loadavg", "meminfo", "partitions", "stat", "uptime", "version"} {
		args = append(args, "--ro-bind", "/proc/"+fact, "/host/"+fact)
	}
	return append(args, "--size", "16777216", "--tmpfs", "/scratch", "--remount-ro", "/", "--chdir", "/scratch",
		"--", "/bin/busybox", "sh", "-c", command)
}

func observeSocketFilter() []unix.SockFilter {
	architecture := uint32(unix.AUDIT_ARCH_X86_64)
	if runtime.GOARCH == "arm64" {
		architecture = unix.AUDIT_ARCH_AARCH64
	}
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: architecture},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K, Jf: 1, K: 0x40000000},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
	}
	for _, number := range []uint32{
		unix.SYS_SOCKET, unix.SYS_SOCKETPAIR, unix.SYS_CONNECT, unix.SYS_SENDTO, unix.SYS_SENDMSG,
		unix.SYS_IO_URING_SETUP, unix.SYS_BPF, unix.SYS_PERF_EVENT_OPEN, unix.SYS_USERFAULTFD,
		unix.SYS_ADD_KEY, unix.SYS_REQUEST_KEY, unix.SYS_KEYCTL, unix.SYS_PTRACE, unix.SYS_PIDFD_GETFD,
	} {
		filter = append(filter,
			unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: number},
			unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		)
	}
	return append(filter, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
}
