package localbackupschedule

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

const systemctlCommandTimeout = 15 * time.Second

// ExecRunner invokes one fixed systemctl argv directly and bounds the call.
// It never runs a shell and is intentionally separate from the backup CLI
// process that the rendered service will execute later.
type ExecRunner struct{}

func (ExecRunner) Run(parent context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("systemd command is empty")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, systemctlCommandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}
