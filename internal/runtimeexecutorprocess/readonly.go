package runtimeexecutorprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// InvokeReadOnly uses the existing digest-pinned process custody for the two
// fixed native CLI read operations. Remote callers cannot choose flags, paths,
// executables or an environment. The caller must first admit the current plan.
func InvokeReadOnly(ctx context.Context, binding Binding, workspace, planPath, planHash, action string) ([]byte, error) {
	if action != "plan" && action != "verify" {
		return nil, errors.New("standard read-only process action unavailable")
	}
	if !validDigest(planHash) {
		return nil, errors.New("standard read-only process requires exact plan hash")
	}
	if err := ValidateBinding(binding); err != nil {
		return nil, err
	}
	executable, err := loadExecutable(binding)
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "stackkit-readonly-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	private := filepath.Join(directory, filepath.Base(binding.Executable))
	if err := os.WriteFile(private, executable, 0700); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, private, "--chdir", workspace, "federation", "control", "run-readonly", "--action", action, "--resolved-plan", planPath, "--expected-plan-hash", planHash)
	command.Dir = workspace
	// Existing local owners resolve their fixed OS runtimes using PATH. No
	// ambient API tokens, provider credentials or remote flags are inherited.
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "HOME=" + os.Getenv("HOME"), "USERPROFILE=" + os.Getenv("USERPROFILE")}
	var stdout, stderr boundedBuffer
	stdout.limit, stderr.limit = 1<<20, 16<<10
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("standard read-only process output exceeds bound")
	}
	return stdout.Bytes(), err
}
