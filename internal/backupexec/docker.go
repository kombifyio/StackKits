package backupexec

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

const (
	// DefaultContainer is the name of the local Kopia agent container. It
	// matches the StackKit local backup runtime contract.
	DefaultContainer = "kopia-agent"

	v2ComposeProject = "stackkit-basement-core"
	v2ComposeService = localbackuppolicy.ServiceRef
	v2NetworkName    = "stackkit-" + localbackuppolicy.NetworkRef

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
// StackAction endpoints share this adapter so both speak identical argv
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

// DockerV2Executor binds the native-v2 secret executor to docker exec -i.
// Kopia 0.18.2 reads KOPIA_PASSWORD before attempting its terminal-only
// password prompt. A fixed shell adapter reads exactly one secret line from
// stdin into that child-process environment and then execs the closed Kopia
// argv. The secret is never present in Docker argv, the container definition,
// or a persistent environment. No caller can supply a free-form shell command.
func DockerV2Executor() SecretExecutor {
	return dockerV2Executor(func(timeout time.Duration) dockerV2Client {
		return docker.NewLocalClient(docker.WithTimeout(timeout))
	})
}

type dockerV2Client interface {
	IsInstalled() bool
	IsRunning(context.Context) bool
	ResolveComposeServiceContainer(context.Context, string, string) (*docker.ContainerInfo, error)
	InspectNetwork(context.Context, string) (*docker.NetworkInfo, error)
	ExecWithStdin(context.Context, string, []string, []byte) (string, error)
}

type dockerV2ClientFactory func(time.Duration) dockerV2Client

func dockerV2Executor(newClient dockerV2ClientFactory) SecretExecutor {
	return func(ctx context.Context, command []string, sensitiveInput []byte) (string, error) {
		if newClient == nil {
			return "", fmt.Errorf("native-v2 Docker client factory is required")
		}
		client := newClient(dockerTimeout(ctx, LongOperationTimeout))
		if !client.IsInstalled() {
			return "", fmt.Errorf("docker is not installed on this host — the backup engine requires the local %s service", v2ComposeService)
		}
		if !client.IsRunning(ctx) {
			return "", fmt.Errorf("docker daemon is not running")
		}
		container, err := client.ResolveComposeServiceContainer(ctx, v2ComposeProject, v2ComposeService)
		if err != nil {
			return "", fmt.Errorf("resolve local kopia runtime: %w (provision the local backup runtime and re-apply the stack)", err)
		}
		network, err := client.InspectNetwork(ctx, v2NetworkName)
		if err != nil {
			return "", fmt.Errorf("inspect local kopia runtime network: %w", err)
		}
		if err := validateDockerV2Runtime(container, network); err != nil {
			return "", fmt.Errorf("local kopia runtime differs from the governed policy: %w", err)
		}
		passwordCommand, err := kopiaPasswordCommand(command)
		if err != nil {
			return "", err
		}
		return client.ExecWithStdin(ctx, container.ID, passwordCommand, sensitiveInput)
	}
}

func validateDockerV2Runtime(container *docker.ContainerInfo, network *docker.NetworkInfo) error {
	if container == nil || network == nil {
		return fmt.Errorf("container and network inspection are required")
	}
	runtime := localbackuppolicy.GovernedRuntime()
	source := localbackuppolicy.GovernedSource()
	config := container.Config
	if config.Image != runtime.Image {
		return fmt.Errorf("container image is not the exact pinned runtime")
	}
	if config.Hostname != runtime.Hostname {
		return fmt.Errorf("container hostname is not the deterministic Kopia source identity")
	}
	if config.User != "" || config.WorkingDir != "/app" {
		return fmt.Errorf("container process identity differs from the pinned runtime")
	}
	wantEnvironment := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"DEBIAN_FRONTEND=noninteractive",
		"TERM=xterm-256color",
		"LC_ALL=C.UTF-8",
		"KOPIA_CONFIG_PATH=/app/config/repository.config",
		"KOPIA_LOG_DIR=/app/logs",
		"KOPIA_CACHE_DIRECTORY=/app/cache",
		"RCLONE_CONFIG=/app/rclone/rclone.conf",
		"KOPIA_PERSIST_CREDENTIALS_ON_CONNECT=false",
		"KOPIA_CHECK_FOR_UPDATES=false",
	}
	if !slices.Equal(config.Env, wantEnvironment) {
		return fmt.Errorf("container environment differs from the credential-free pinned runtime")
	}
	if len(config.ExposedPorts) != 0 {
		return fmt.Errorf("container exposes ports outside the governed runtime")
	}
	if err := validateDockerV2HostConfig(container.HostConfig, source); err != nil {
		return err
	}
	if err := validateDockerV2Labels(container); err != nil {
		return err
	}
	if !slices.Equal(config.Entrypoint, []string{"/bin/sh", "-c"}) ||
		!slices.Equal(config.Cmd, []string{"trap : TERM INT; sleep infinity & wait"}) {
		return fmt.Errorf("container idle command differs from the governed owner-command runtime")
	}
	health := config.Healthcheck
	if health == nil ||
		!slices.Equal(health.Test, []string{"CMD", "kopia", "--version"}) ||
		health.Interval != int64(10*time.Second) ||
		health.Timeout != int64(5*time.Second) ||
		health.Retries != 12 ||
		health.StartPeriod != int64(5*time.Second) {
		return fmt.Errorf("container healthcheck differs from the governed runtime")
	}
	if container.HostConfig.NetworkMode != v2NetworkName ||
		len(container.NetworkSettings.Networks) != 1 {
		return fmt.Errorf("container network mode differs from the isolated backup network")
	}
	if _, ok := container.NetworkSettings.Networks[v2NetworkName]; !ok {
		return fmt.Errorf("container is not attached solely to the isolated backup network")
	}

	wantMounts := map[string]docker.ContainerMount{
		source.RepositoryPath: {
			Type: "volume", Name: v2ComposeProject + "_kopia-repository",
			Destination: source.RepositoryPath, RW: true,
		},
		source.ConfigPath: {
			Type: "volume", Name: v2ComposeProject + "_kopia-config",
			Destination: source.ConfigPath, RW: true,
		},
		source.CachePath: {
			Type: "volume", Name: v2ComposeProject + "_kopia-cache",
			Destination: source.CachePath, RW: true,
		},
		localbackuppolicy.RestoreStagingPath: {
			Type: "volume", Name: v2ComposeProject + "_kopia-restore-staging",
			Destination: localbackuppolicy.RestoreStagingPath, RW: true,
		},
	}
	for _, volumeName := range source.ManagedVolumeNames {
		hostPath := source.HostPath + "/" + volumeName + "/_data"
		containerPath := source.ContainerPath + "/" + volumeName + "/_data"
		wantMounts[containerPath] = docker.ContainerMount{
			Type: "bind", Source: hostPath, Destination: containerPath, RW: false,
		}
	}
	if len(container.Mounts) != len(wantMounts) {
		return fmt.Errorf("container mount count differs from the governed runtime")
	}
	for _, actual := range container.Mounts {
		want, ok := wantMounts[actual.Destination]
		if !ok ||
			actual.Type != want.Type ||
			actual.Name != want.Name ||
			actual.RW != want.RW {
			return fmt.Errorf("container mount differs from the governed runtime")
		}
		if actual.Type == "bind" && actual.Source != want.Source {
			return fmt.Errorf("container bind source differs from the governed runtime")
		}
		if actual.Type == "bind" && actual.Propagation != "rprivate" {
			return fmt.Errorf("container bind propagation differs from the governed runtime")
		}
	}

	if network.Name != v2NetworkName || !network.Internal ||
		len(network.Containers) != 1 {
		return fmt.Errorf("backup network is not internal with exactly one peer")
	}
	peer, ok := network.Containers[container.ID]
	if !ok || strings.TrimPrefix(container.Name, "/") != peer.Name {
		return fmt.Errorf("backup network peer differs from the resolved Kopia container")
	}
	return nil
}

func validateDockerV2HostConfig(config docker.ContainerHostConfig, source localbackuppolicy.Source) error {
	if len(config.UnknownInspectionFields) != 0 {
		return fmt.Errorf("container inspection contains unsupported host controls")
	}
	wantBinds := make([]string, 0, len(source.ManagedVolumeNames)+4)
	for _, volumeName := range source.ManagedVolumeNames {
		wantBinds = append(
			wantBinds,
			source.HostPath+"/"+volumeName+"/_data:"+
				source.ContainerPath+"/"+volumeName+"/_data:ro",
		)
	}
	wantBinds = append(wantBinds,
		v2ComposeProject+"_kopia-repository:"+source.RepositoryPath+":rw",
		v2ComposeProject+"_kopia-config:"+source.ConfigPath+":rw",
		v2ComposeProject+"_kopia-cache:"+source.CachePath+":rw",
		v2ComposeProject+"_kopia-restore-staging:"+localbackuppolicy.RestoreStagingPath+":rw",
	)
	actualBinds := slices.Clone(config.Binds)
	slices.Sort(actualBinds)
	slices.Sort(wantBinds)
	if !slices.Equal(actualBinds, wantBinds) ||
		config.ContainerIDFile != "" ||
		len(config.PortBindings) != 0 ||
		config.AutoRemove ||
		config.VolumeDriver != "" ||
		len(config.VolumesFrom) != 0 ||
		config.ConsoleSize != [2]uint{} {
		return fmt.Errorf("container host attachment differs from the governed runtime")
	}
	if config.RestartPolicy.Name != "unless-stopped" ||
		config.RestartPolicy.MaximumRetryCount != 0 {
		return fmt.Errorf("container restart policy differs from the governed runtime")
	}
	if config.PidMode != "" ||
		config.IpcMode != "private" ||
		config.UTSMode != "" ||
		config.UsernsMode != "" ||
		config.CgroupnsMode != "private" ||
		config.Cgroup != "" {
		return fmt.Errorf("container namespace isolation differs from the governed runtime")
	}
	if len(config.CapAdd) != 0 ||
		len(config.CapDrop) != 0 ||
		len(config.Devices) != 0 ||
		len(config.DeviceCgroupRules) != 0 ||
		len(config.DeviceRequests) != 0 ||
		len(config.SecurityOpt) != 0 ||
		len(config.GroupAdd) != 0 ||
		len(config.Links) != 0 ||
		config.Privileged ||
		config.PublishAllPorts ||
		config.ReadonlyRootfs ||
		config.Runtime != "runc" ||
		config.Isolation != "" {
		return fmt.Errorf("container privilege boundary differs from the governed runtime")
	}
	if len(config.DNS) != 0 ||
		len(config.DNSOptions) != 0 ||
		len(config.DNSSearch) != 0 ||
		len(config.ExtraHosts) != 0 ||
		len(config.Sysctls) != 0 ||
		config.Init != nil {
		return fmt.Errorf("container host policy differs from the governed runtime")
	}
	if config.ShmSize != 64*1024*1024 ||
		config.CPUShares != 0 ||
		config.Memory != 0 ||
		config.NanoCPUs != 0 ||
		config.CgroupParent != "" ||
		config.BlkioWeight != 0 ||
		len(config.BlkioWeightDevice) != 0 ||
		len(config.BlkioDeviceReadBps) != 0 ||
		len(config.BlkioDeviceWriteBps) != 0 ||
		len(config.BlkioDeviceReadIOps) != 0 ||
		len(config.BlkioDeviceWriteIOps) != 0 ||
		config.CPUPeriod != 0 ||
		config.CPUQuota != 0 ||
		config.CPURealtimePeriod != 0 ||
		config.CPURealtimeRuntime != 0 ||
		config.CpusetCPUs != "" ||
		config.CpusetMems != "" ||
		config.MemoryReservation != 0 ||
		config.MemorySwap != 0 ||
		config.MemorySwappiness != nil ||
		config.CPUCount != 0 ||
		config.CPUPercent != 0 ||
		config.IOMaximumIOps != 0 ||
		config.IOMaximumBandwidth != 0 ||
		len(config.Ulimits) != 0 {
		return fmt.Errorf("container cgroup resource policy differs from the governed runtime")
	}
	if config.OomScoreAdj != 0 ||
		(config.OomKillDisable != nil && *config.OomKillDisable) ||
		config.PidsLimit != nil {
		return fmt.Errorf("container process availability policy differs from the governed runtime")
	}
	wantMaskedPaths := []string{
		"/proc/acpi", "/proc/asound", "/proc/interrupts", "/proc/kcore",
		"/proc/keys", "/proc/latency_stats", "/proc/sched_debug", "/proc/scsi",
		"/proc/timer_list", "/proc/timer_stats", "/sys/devices/virtual/powercap", "/sys/firmware",
	}
	wantReadonlyPaths := []string{
		"/proc/bus", "/proc/fs", "/proc/irq", "/proc/sys", "/proc/sysrq-trigger",
	}
	// Docker owns these default hardening lists and may reorder them or add
	// further restrictions between engine releases. Require every governed
	// restriction, while accepting engine-added masking/readonly paths: those
	// additions can only further constrain the Kopia process and the pinned
	// command/health checks still prove it is usable.
	if !containsAllPaths(config.MaskedPaths, wantMaskedPaths) ||
		!containsAllPaths(config.ReadonlyPaths, wantReadonlyPaths) {
		return fmt.Errorf("container proc/sys isolation differs from the governed runtime")
	}
	return nil
}

func containsAllPaths(actual, required []string) bool {
	seen := make(map[string]struct{}, len(actual))
	for _, path := range actual {
		seen[path] = struct{}{}
	}
	for _, path := range required {
		if _, ok := seen[path]; !ok {
			return false
		}
	}
	return true
}

func validateDockerV2Labels(container *docker.ContainerInfo) error {
	labels := container.Config.Labels
	if len(labels) != 12 ||
		labels["com.docker.compose.container-number"] != "1" ||
		labels["com.docker.compose.depends_on"] != "" ||
		labels["com.docker.compose.image"] != container.Image ||
		labels["com.docker.compose.oneoff"] != "False" ||
		labels["com.docker.compose.project"] != v2ComposeProject ||
		labels["com.docker.compose.project.config_files"] == "" ||
		labels["com.docker.compose.project.working_dir"] == "" ||
		labels["com.docker.compose.service"] != v2ComposeService ||
		labels["com.docker.compose.version"] == "" ||
		labels["org.opencontainers.image.ref.name"] != "ubuntu" ||
		labels["org.opencontainers.image.version"] != "22.04" {
		return fmt.Errorf("container Compose labels differ from the governed runtime")
	}
	configHash, err := hex.DecodeString(labels["com.docker.compose.config-hash"])
	if err != nil || len(configHash) != 32 {
		return fmt.Errorf("container Compose config hash label is invalid")
	}
	return nil
}

func kopiaPasswordCommand(command []string) ([]string, error) {
	if len(command) == 0 || command[0] != "kopia" {
		return nil, fmt.Errorf("native-v2 password adapter accepts only kopia argv")
	}
	for _, arg := range command {
		if strings.ContainsRune(arg, '\x00') {
			return nil, fmt.Errorf("kopia argv contains NUL")
		}
	}
	argv := []string{
		"/bin/sh", "-ceu",
		`IFS= read -r KOPIA_PASSWORD; export KOPIA_PASSWORD; exec "$@"`,
		"--",
	}
	return append(argv, command...), nil
}

// NewDockerV2Engine wires the native-v2 engine to its fixed Docker adapter.
func NewDockerV2Engine() V2Engine {
	return NewV2Engine(DockerV2Executor())
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
