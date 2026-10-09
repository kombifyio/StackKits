package stackkitmcp

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureMCPCLIProcess(cmd *exec.Cmd) {
	// A CLI may delegate recovery to its original attested release. Keep the
	// whole invocation in a private group so a timed-out MCP call cannot leave
	// that release or its ordinary children mutating after the caller returns.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	// Bound pipe completion if a descendant deliberately leaves the group.
	cmd.WaitDelay = time.Second
}
