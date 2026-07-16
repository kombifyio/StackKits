package backupexec

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
)

const (
	// DefaultContainer is the name of the local Kopia agent container. It
	// matches the StackKit local backup runtime contract.
	DefaultContainer = "kopia-agent"

	// LongOperationTimeout is the global StackKits phase policy: no workflow,
	// release gate, restore, or backup wait may run longer than 15 minutes.
	// Longer work must be split into visible phases.
	LongOperationTimeout = 15 * time.Minute

	// QuickOperationTimeout bounds engine calls issued without a context
	// deadline of their own.
	QuickOperationTimeout = 30 * time.Second
)

// DockerExecutor returns an Executor that runs commands inside the named
// kopia-agent container via the local docker daemon. The CLI and the
// runtime-action endpoints share this adapter so both speak identical argv
// against the same container. Per-call client timeouts derive from the
// context deadline, capped at LongOperationTimeout.
func DockerExecutor(container string) Executor {
	return dockerExecutorWithCap(container, LongOperationTimeout)
}

// DockerExecutorUncapped derives the per-call client timeout solely from the
// context deadline. It exists for detached node-side runs (first content
// snapshots can legitimately exceed the 15-minute wait policy — the wait is
// split into backup_status polls, the underlying snapshot is not).
func DockerExecutorUncapped(container string) Executor {
	return dockerExecutorWithCap(container, 0)
}

// NewDockerEngine wires Engine to the shared docker exec adapter.
func NewDockerEngine(container string) Engine {
	return Engine{Exec: DockerExecutor(container)}
}

// ErrContainerNotPresent marks a hook target container that does not exist
// on this node. Hook execution classifies it as skipped — as opposed to a
// command failing INSIDE a running container, which must fail the run.
var ErrContainerNotPresent = errors.New("container not present on this node")

// DockerContainerExecutor runs pre-snapshot hook commands against arbitrary
// containers (the database containers themselves, not the kopia-agent). A
// missing container surfaces as ErrContainerNotPresent so hook execution can
// classify it as skipped rather than failed.
func DockerContainerExecutor() ContainerExecutor {
	return func(ctx context.Context, container string, command []string) (string, error) {
		client := docker.NewClient(docker.WithTimeout(dockerTimeout(ctx, LongOperationTimeout)))
		if !client.IsInstalled() {
			return "", fmt.Errorf("docker is not installed on this host")
		}
		if !client.IsRunning(ctx) {
			return "", fmt.Errorf("docker daemon is not running")
		}
		if _, err := client.InspectContainer(ctx, container); err != nil {
			return "", fmt.Errorf("container %q: %w: %s", container, ErrContainerNotPresent, err.Error())
		}
		out, err := client.Exec(ctx, container, command)
		if err != nil {
			if out == "" {
				out = err.Error()
			}
			return out, err
		}
		return out, nil
	}
}

func dockerExecutorWithCap(container string, cap time.Duration) Executor {
	return func(ctx context.Context, command []string) (string, error) {
		client := docker.NewClient(docker.WithTimeout(dockerTimeout(ctx, cap)))
		if !client.IsInstalled() {
			return "", fmt.Errorf("docker is not installed on this host — the backup engine requires the local %s container", container)
		}
		if !client.IsRunning(ctx) {
			return "", fmt.Errorf("docker daemon is not running")
		}
		if _, err := client.InspectContainer(ctx, container); err != nil {
			return "", fmt.Errorf("kopia-agent container %q not found: %w (provision the local backup runtime and re-apply the stack)", container, err)
		}
		out, err := client.Exec(ctx, container, command)
		if err != nil {
			if out == "" {
				out = err.Error()
			}
			return out, err
		}
		return out, nil
	}
}

// dockerTimeout derives the docker client timeout from the context deadline.
// cap <= 0 disables the upper bound (context deadline still applies).
func dockerTimeout(ctx context.Context, cap time.Duration) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return QuickOperationTimeout
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return time.Nanosecond
	}
	if cap > 0 && remaining > cap {
		return cap
	}
	return remaining
}
