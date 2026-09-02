// Package docker provides Docker operations for StackKits.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
)

// containerNameRegex validates Docker container/network/volume names
var containerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

const dockerPSFormat = "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.State}}\t{{.Status}}\t{{.Ports}}"

// validateName validates a Docker resource name (container, network, volume)
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if len(name) > 255 {
		return fmt.Errorf("name too long (max 255 characters)")
	}
	if !containerNameRegex.MatchString(name) {
		return fmt.Errorf("invalid name: must match [a-zA-Z0-9][a-zA-Z0-9_.-]*")
	}
	return nil
}

// validateNameOrID validates a container name or ID (allows hex IDs)
func validateNameOrID(nameOrID string) error {
	if nameOrID == "" {
		return fmt.Errorf("name or ID cannot be empty")
	}
	// Allow hex container IDs (64 char) or short IDs (12 char)
	if regexp.MustCompile(`^[a-f0-9]{12,64}$`).MatchString(nameOrID) {
		return nil
	}
	return validateName(nameOrID)
}

// Client handles Docker operations
type Client struct {
	binary    string
	timeout   time.Duration
	env       []string
	localOnly bool
}

// ClientOption configures the Docker client
type ClientOption func(*Client)

// WithBinary sets the Docker binary path
func WithBinary(binary string) ClientOption {
	return func(c *Client) {
		c.binary = binary
	}
}

// WithTimeout sets the operation timeout
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.timeout = timeout
	}
}

// WithEnv adds Docker transport environment entries (for example DOCKER_HOST
// and DOCKER_SSH_COMMAND) without mutating the process environment. This is
// used by managed-runtime verification to inspect the same remote host that a
// rollout targeted.
func WithEnv(values ...string) ClientOption {
	return func(c *Client) {
		c.env = append([]string(nil), values...)
	}
}

// NewClient creates a new Docker client
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		binary:  "docker",
		timeout: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewLocalClient creates a Docker CLI client that cannot follow process or
// option-provided remote daemon configuration. Every command binds the
// platform's canonical local socket/named pipe explicitly.
func NewLocalClient(opts ...ClientOption) *Client {
	c := NewClient(opts...)
	c.localOnly = true
	return c
}

func (c *Client) command(ctx context.Context, args ...string) *exec.Cmd {
	if c.localOnly {
		args = append([]string{"--host", localDockerEndpoint()}, args...)
	}
	cmd := exec.CommandContext(ctx, c.binary, args...) // #nosec G204 -- binary path is set at construction, not from user input
	environment := append(os.Environ(), c.env...)
	if c.localOnly {
		environment = environmentWithoutDockerTransport(environment)
	}
	if len(c.env) > 0 || c.localOnly {
		cmd.Env = environment
	}
	return cmd
}

func localDockerEndpoint() string {
	if runtime.GOOS == "windows" {
		return "npipe:////./pipe/docker_engine"
	}
	return "unix:///var/run/docker.sock"
}

var dockerTransportEnvironment = []string{
	"DOCKER_HOST",
	"DOCKER_CONTEXT",
	"DOCKER_TLS",
	"DOCKER_TLS_VERIFY",
	"DOCKER_CERT_PATH",
	"DOCKER_SSH_COMMAND",
	"DOCKER_SSH_CONFIG",
}

func environmentWithoutDockerTransport(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if slices.Contains(dockerTransportEnvironment, strings.ToUpper(key)) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// ContainerInfo represents container information
type ContainerInfo struct {
	ID              string                   `json:"Id"`
	Name            string                   `json:"Name"`
	Image           string                   `json:"Image"`
	State           ContainerState           `json:"State"`
	Ports           []ContainerPort          `json:"Ports"`
	Labels          map[string]string        `json:"Labels"`
	Created         string                   `json:"Created"`
	Config          ContainerConfig          `json:"Config"`
	HostConfig      ContainerHostConfig      `json:"HostConfig"`
	Mounts          []ContainerMount         `json:"Mounts"`
	NetworkSettings ContainerNetworkSettings `json:"NetworkSettings"`
}

// ContainerConfig is the security-relevant runtime projection returned by
// docker inspect. Environment values are retained so the native-v2 adapter can
// require the exact credential-free environment inherited from its pinned image.
type ContainerConfig struct {
	Hostname     string                `json:"Hostname"`
	Image        string                `json:"Image"`
	StopSignal   string                `json:"StopSignal"`
	User         string                `json:"User"`
	WorkingDir   string                `json:"WorkingDir"`
	Env          []string              `json:"Env"`
	ExposedPorts map[string]any        `json:"ExposedPorts"`
	Entrypoint   []string              `json:"Entrypoint"`
	Cmd          []string              `json:"Cmd"`
	Labels       map[string]string     `json:"Labels"`
	Healthcheck  *ContainerHealthcheck `json:"Healthcheck"`
}

type ContainerHealthcheck struct {
	Test        []string `json:"Test"`
	Interval    int64    `json:"Interval"`
	Timeout     int64    `json:"Timeout"`
	Retries     int      `json:"Retries"`
	StartPeriod int64    `json:"StartPeriod"`
}

type ContainerHostConfig struct {
	Binds                   []string                          `json:"Binds"`
	ContainerIDFile         string                            `json:"ContainerIDFile"`
	LogConfig               ContainerLogConfig                `json:"LogConfig"`
	NetworkMode             string                            `json:"NetworkMode"`
	PortBindings            map[string][]ContainerPortBinding `json:"PortBindings"`
	RestartPolicy           ContainerRestartPolicy            `json:"RestartPolicy"`
	AutoRemove              bool                              `json:"AutoRemove"`
	VolumeDriver            string                            `json:"VolumeDriver"`
	VolumesFrom             []string                          `json:"VolumesFrom"`
	ConsoleSize             [2]uint                           `json:"ConsoleSize"`
	CapAdd                  []string                          `json:"CapAdd"`
	CapDrop                 []string                          `json:"CapDrop"`
	CgroupnsMode            string                            `json:"CgroupnsMode"`
	DNS                     []string                          `json:"Dns"`
	DNSOptions              []string                          `json:"DnsOptions"`
	DNSSearch               []string                          `json:"DnsSearch"`
	ExtraHosts              []string                          `json:"ExtraHosts"`
	GroupAdd                []string                          `json:"GroupAdd"`
	IpcMode                 string                            `json:"IpcMode"`
	Cgroup                  string                            `json:"Cgroup"`
	Links                   []string                          `json:"Links"`
	OomScoreAdj             int                               `json:"OomScoreAdj"`
	PidMode                 string                            `json:"PidMode"`
	Privileged              bool                              `json:"Privileged"`
	PublishAllPorts         bool                              `json:"PublishAllPorts"`
	ReadonlyRootfs          bool                              `json:"ReadonlyRootfs"`
	SecurityOpt             []string                          `json:"SecurityOpt"`
	UTSMode                 string                            `json:"UTSMode"`
	UsernsMode              string                            `json:"UsernsMode"`
	ShmSize                 int64                             `json:"ShmSize"`
	Runtime                 string                            `json:"Runtime"`
	Isolation               string                            `json:"Isolation"`
	CPUShares               int64                             `json:"CpuShares"`
	Memory                  int64                             `json:"Memory"`
	NanoCPUs                int64                             `json:"NanoCpus"`
	CgroupParent            string                            `json:"CgroupParent"`
	BlkioWeight             uint16                            `json:"BlkioWeight"`
	BlkioWeightDevice       []ContainerWeightDevice           `json:"BlkioWeightDevice"`
	BlkioDeviceReadBps      []ContainerThrottleDevice         `json:"BlkioDeviceReadBps"`
	BlkioDeviceWriteBps     []ContainerThrottleDevice         `json:"BlkioDeviceWriteBps"`
	BlkioDeviceReadIOps     []ContainerThrottleDevice         `json:"BlkioDeviceReadIOps"`
	BlkioDeviceWriteIOps    []ContainerThrottleDevice         `json:"BlkioDeviceWriteIOps"`
	CPUPeriod               int64                             `json:"CpuPeriod"`
	CPUQuota                int64                             `json:"CpuQuota"`
	CPURealtimePeriod       int64                             `json:"CpuRealtimePeriod"`
	CPURealtimeRuntime      int64                             `json:"CpuRealtimeRuntime"`
	CpusetCPUs              string                            `json:"CpusetCpus"`
	CpusetMems              string                            `json:"CpusetMems"`
	Devices                 []ContainerDeviceMapping          `json:"Devices"`
	DeviceCgroupRules       []string                          `json:"DeviceCgroupRules"`
	DeviceRequests          []ContainerDeviceRequest          `json:"DeviceRequests"`
	MemoryReservation       int64                             `json:"MemoryReservation"`
	MemorySwap              int64                             `json:"MemorySwap"`
	MemorySwappiness        *int64                            `json:"MemorySwappiness"`
	OomKillDisable          *bool                             `json:"OomKillDisable"`
	PidsLimit               *int64                            `json:"PidsLimit"`
	Ulimits                 []ContainerUlimit                 `json:"Ulimits"`
	CPUCount                int64                             `json:"CpuCount"`
	CPUPercent              int64                             `json:"CpuPercent"`
	IOMaximumIOps           uint64                            `json:"IOMaximumIOps"`
	IOMaximumBandwidth      uint64                            `json:"IOMaximumBandwidth"`
	MaskedPaths             []string                          `json:"MaskedPaths"`
	ReadonlyPaths           []string                          `json:"ReadonlyPaths"`
	Sysctls                 map[string]string                 `json:"Sysctls"`
	Init                    *bool                             `json:"Init"`
	UnknownInspectionFields []string                          `json:"-"`
}

// UnmarshalJSON retains fields unknown to this typed projection. Native
// lifecycle validators can then reject a daemon inspection surface they do
// not understand instead of silently ignoring a future privilege control.
func (config *ContainerHostConfig) UnmarshalJSON(data []byte) error {
	type hostConfigAlias ContainerHostConfig
	var decoded hostConfigAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	known := make(map[string]struct{})
	projection := reflect.TypeOf(decoded)
	for index := 0; index < projection.NumField(); index++ {
		name := strings.Split(projection.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			known[name] = struct{}{}
		}
	}
	var unknown []string
	for name := range raw {
		if _, ok := known[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	*config = ContainerHostConfig(decoded)
	config.UnknownInspectionFields = unknown
	return nil
}

type ContainerLogConfig struct {
	Type   string            `json:"Type"`
	Config map[string]string `json:"Config"`
}

type ContainerWeightDevice struct {
	Path   string `json:"Path"`
	Weight uint16 `json:"Weight"`
}

type ContainerThrottleDevice struct {
	Path string `json:"Path"`
	Rate uint64 `json:"Rate"`
}

type ContainerDeviceMapping struct {
	PathOnHost        string `json:"PathOnHost"`
	PathInContainer   string `json:"PathInContainer"`
	CgroupPermissions string `json:"CgroupPermissions"`
}

type ContainerDeviceRequest struct {
	Driver       string            `json:"Driver"`
	Count        int               `json:"Count"`
	DeviceIDs    []string          `json:"DeviceIDs"`
	Capabilities [][]string        `json:"Capabilities"`
	Options      map[string]string `json:"Options"`
}

type ContainerUlimit struct {
	Name string `json:"Name"`
	Hard int64  `json:"Hard"`
	Soft int64  `json:"Soft"`
}

type ContainerPortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type ContainerRestartPolicy struct {
	Name              string `json:"Name"`
	MaximumRetryCount int    `json:"MaximumRetryCount"`
}

type ContainerMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
	Propagation string `json:"Propagation"`
}

type ContainerNetworkSettings struct {
	Networks map[string]ContainerNetwork `json:"Networks"`
}

type ContainerNetwork struct {
	NetworkID string `json:"NetworkID"`
}

// NetworkInfo is the exact isolation projection returned by docker network
// inspect for a rendered local-only network.
type NetworkInfo struct {
	ID         string                      `json:"Id"`
	Name       string                      `json:"Name"`
	Internal   bool                        `json:"Internal"`
	Containers map[string]NetworkContainer `json:"Containers"`
}

type NetworkContainer struct {
	Name string `json:"Name"`
}

// ContainerState represents container state
type ContainerState struct {
	Status     string       `json:"Status"`
	Running    bool         `json:"Running"`
	Paused     bool         `json:"Paused"`
	Restarting bool         `json:"Restarting"`
	ExitCode   int          `json:"ExitCode"`
	OOMKilled  bool         `json:"OOMKilled"`
	Error      string       `json:"Error"`
	Health     *HealthState `json:"Health,omitempty"`
}

// HealthState represents container health
type HealthState struct {
	Status string `json:"Status"`
}

// ContainerPort represents a container port
type ContainerPort struct {
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

// IsInstalled checks if Docker is installed
func (c *Client) IsInstalled() bool {
	_, err := exec.LookPath(c.binary)
	return err == nil
}

// Version returns the Docker version
func (c *Client) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, "version", "--format", "{{.Server.Version}}")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get Docker version: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// IsRunning checks if Docker daemon is running
func (c *Client) IsRunning(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, "info")
	return cmd.Run() == nil
}

// ListContainers lists all containers
func (c *Client) ListContainers(ctx context.Context, all bool) ([]ContainerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := []string{"ps", "--format", dockerPSFormat}
	if all {
		args = []string{"ps", "-a", "--format", dockerPSFormat}
	}

	cmd := c.command(ctx, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	return parseDockerPSOutput(output), nil
}

// InspectContainer inspects a container
func (c *Client) InspectContainer(ctx context.Context, nameOrID string) (*ContainerInfo, error) {
	// Validate input to prevent command injection
	if err := validateNameOrID(nameOrID); err != nil {
		return nil, fmt.Errorf("invalid container name/ID: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, "inspect", nameOrID)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	var containers []ContainerInfo
	if err := json.Unmarshal(output, &containers); err != nil {
		return nil, fmt.Errorf("failed to parse container info: %w", err)
	}

	if len(containers) == 0 {
		return nil, fmt.Errorf("container not found: %s", nameOrID)
	}

	return &containers[0], nil
}

// StopContainer stops one exact container identity. Callers that need
// idempotent quiescence inspect first and only invoke this method while the
// container is still running.
func (c *Client) StopContainer(ctx context.Context, nameOrID string) error {
	if err := validateNameOrID(nameOrID); err != nil {
		return fmt.Errorf("invalid container name/ID: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := c.command(ctx, "stop", nameOrID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w (%s)", nameOrID, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// StopContainerWithTimeout stops one exact container identity with an
// explicit Docker grace period. StopContainer intentionally retains its
// historical daemon-default behavior for consumers that do not own a cold
// snapshot boundary.
func (c *Client) StopContainerWithTimeout(ctx context.Context, nameOrID string, grace time.Duration) error {
	if err := validateNameOrID(nameOrID); err != nil {
		return fmt.Errorf("invalid container name/ID: %w", err)
	}
	if grace <= 0 {
		return fmt.Errorf("container stop grace must be positive")
	}
	seconds := (grace + time.Second - 1) / time.Second
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := c.command(ctx, "stop", "--time", strconv.FormatInt(int64(seconds), 10), nameOrID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w (%s)", nameOrID, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// StartContainer starts one exact container identity. Callers that need
// idempotent restoration inspect first and only invoke this method while the
// container is stopped and was recorded as running before quiescence.
func (c *Client) StartContainer(ctx context.Context, nameOrID string) error {
	if err := validateNameOrID(nameOrID); err != nil {
		return fmt.Errorf("invalid container name/ID: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := c.command(ctx, "start", nameOrID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start container %s: %w (%s)", nameOrID, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ContainerIPAddress returns the first Docker network IP assigned to a
// container. Host-network containers legitimately return an empty string.
func (c *Client) ContainerIPAddress(ctx context.Context, nameOrID string) (string, error) {
	if err := validateNameOrID(nameOrID); err != nil {
		return "", fmt.Errorf("invalid container name/ID: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", nameOrID)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect container network: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// GetContainerHealth gets the health status of a container
func (c *Client) GetContainerHealth(ctx context.Context, nameOrID string) (models.HealthStatus, error) {
	// Validation happens in InspectContainer
	info, err := c.InspectContainer(ctx, nameOrID)
	if err != nil {
		return models.HealthStatusUnknown, err
	}

	if !info.State.Running {
		return models.HealthStatusUnhealthy, nil
	}

	if info.State.Health == nil {
		return models.HealthStatusNone, nil
	}

	switch info.State.Health.Status {
	case "healthy":
		return models.HealthStatusHealthy, nil
	case "unhealthy":
		return models.HealthStatusUnhealthy, nil
	case "starting":
		return models.HealthStatusStarting, nil
	default:
		return models.HealthStatusNone, nil
	}
}

// GetStackKitContainers returns containers managed by StackKit
func (c *Client) GetStackKitContainers(ctx context.Context) ([]ContainerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Filter by StackKit label — all resources created by main.tf carry stackkit.layer
	// #nosec G204 -- binary path is set at construction, not from user input
	cmd := c.command(ctx, "ps", "-a",
		"--filter", "label=stackkit.layer",
		"--format", dockerPSFormat)

	output, err := cmd.Output()
	if err != nil {
		// Return empty list — do NOT fall back to listing all containers,
		// which would show non-StackKit containers and confuse users.
		return []ContainerInfo{}, nil
	}

	return parseDockerPSOutput(output), nil
}

// GetContainersByLabel returns all containers carrying an exact Docker label
// filter. Managed platform deployments use compose-project labels, while base
// StackKit services use the stackkit.layer filter above.
func (c *Client) GetContainersByLabel(ctx context.Context, label string) ([]ContainerInfo, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, fmt.Errorf("container label filter cannot be empty")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, "ps", "-a", "--filter", "label="+label, "--format", dockerPSFormat)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list containers by label: %w", err)
	}
	return parseDockerPSOutput(output), nil
}

// ResolveComposeServiceContainer resolves one running, healthy container by
// the two exact labels Compose owns. Native-v2 lifecycle code uses this instead
// of assuming that a service name is also the runtime container name.
func (c *Client) ResolveComposeServiceContainer(ctx context.Context, project, service string) (*ContainerInfo, error) {
	if err := validateName(project); err != nil {
		return nil, fmt.Errorf("invalid compose project: %w", err)
	}
	if err := validateName(service); err != nil {
		return nil, fmt.Errorf("invalid compose service: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx,
		"ps",
		"--filter", "label=com.docker.compose.project="+project,
		"--filter", "label=com.docker.compose.service="+service,
		"--filter", "status=running",
		"--format", "{{.ID}}\t{{.Names}}",
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("resolve compose service container: %w", err)
	}

	var candidates []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(fields) == 2 && fields[0] != "" && fields[1] != "" {
			candidates = append(candidates, fields[0])
		}
	}
	if len(candidates) != 1 {
		return nil, fmt.Errorf(
			"compose service %s/%s resolved %d running containers; exactly one is required",
			project,
			service,
			len(candidates),
		)
	}

	container, err := c.InspectContainer(ctx, candidates[0])
	if err != nil {
		return nil, fmt.Errorf("inspect compose service %s/%s: %w", project, service, err)
	}
	if !container.State.Running ||
		container.State.Health == nil ||
		container.State.Health.Status != "healthy" {
		return nil, fmt.Errorf("compose service %s/%s is not healthy", project, service)
	}
	return container, nil
}

func parseDockerPSOutput(output []byte) []ContainerInfo {
	var containers []ContainerInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 6)
		if len(fields) < 5 {
			continue
		}
		containers = append(containers, ContainerInfo{
			ID:    fields[0],
			Name:  fields[1],
			Image: fields[2],
			State: ContainerState{
				Status:  fields[4],
				Running: strings.EqualFold(fields[3], "running"),
			},
		})
	}
	return containers
}

// NetworkExists checks if a network exists
func (c *Client) NetworkExists(ctx context.Context, name string) bool {
	// Validate network name
	if err := validateName(name); err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, "network", "inspect", name)
	return cmd.Run() == nil
}

// InspectNetwork returns the typed isolation and peer projection for one
// Docker network.
func (c *Client) InspectNetwork(ctx context.Context, name string) (*NetworkInfo, error) {
	if err := validateName(name); err != nil {
		return nil, fmt.Errorf("invalid network name: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, "network", "inspect", name)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect network: %w", err)
	}
	var networks []NetworkInfo
	if err := json.Unmarshal(output, &networks); err != nil {
		return nil, fmt.Errorf("failed to parse network info: %w", err)
	}
	if len(networks) != 1 {
		return nil, fmt.Errorf("network inspect returned %d records for %s", len(networks), name)
	}
	return &networks[0], nil
}

// VolumeExists checks if a volume exists
func (c *Client) VolumeExists(ctx context.Context, name string) bool {
	// Validate volume name
	if err := validateName(name); err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, "volume", "inspect", name)
	return cmd.Run() == nil
}

// Exec runs a command in a container
func (c *Client) Exec(ctx context.Context, container string, command []string) (string, error) {
	// Validate container name/ID
	if err := validateNameOrID(container); err != nil {
		return "", fmt.Errorf("invalid container name/ID: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := append([]string{"exec", container}, command...)
	cmd := c.command(ctx, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("exec failed: %w: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// ExecWithStdin runs a command in a container with docker exec's interactive
// stdin enabled. The input is treated as sensitive material: it is never
// copied into argv, inherited environment entries containing it are removed,
// and any command output echoing it is redacted before returning.
func (c *Client) ExecWithStdin(ctx context.Context, container string, command []string, sensitiveInput []byte) (string, error) {
	if err := validateNameOrID(container); err != nil {
		return "", fmt.Errorf("invalid container name/ID: %w", err)
	}

	secrets := sensitiveValues(sensitiveInput)
	for _, secret := range secrets {
		for _, arg := range command {
			if bytes.Contains([]byte(arg), secret) {
				return "", fmt.Errorf("sensitive stdin must not be present in docker exec argv")
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := append([]string{"exec", "-i", container}, command...)
	cmd := c.command(ctx, args...)
	cmd.Env = environmentWithoutSecrets(cmd.Environ(), secrets)

	inputCopy := append([]byte(nil), sensitiveInput...)
	defer clear(inputCopy)
	cmd.Stdin = bytes.NewReader(inputCopy)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		message := redactSensitive(stderr.String(), secrets)
		return redactSensitive(stdout.String(), secrets), fmt.Errorf("exec failed: %w: %s", err, message)
	}

	return redactSensitive(stdout.String(), secrets), nil
}

func sensitiveValues(sensitiveInput []byte) [][]byte {
	whole := bytes.TrimRight(sensitiveInput, "\r\n")
	if len(whole) == 0 {
		return nil
	}
	values := [][]byte{whole}
	for _, line := range bytes.FieldsFunc(whole, func(char rune) bool {
		return char == '\r' || char == '\n'
	}) {
		if len(line) > 0 {
			values = append(values, line)
		}
	}
	return values
}

func environmentWithoutSecrets(environment []string, secrets [][]byte) []string {
	if len(secrets) == 0 {
		return environment
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		containsSecret := false
		for _, secret := range secrets {
			if bytes.Contains([]byte(entry), secret) {
				containsSecret = true
				break
			}
		}
		if !containsSecret {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func redactSensitive(value string, secrets [][]byte) string {
	redacted := []byte(value)
	for _, secret := range secrets {
		redacted = bytes.ReplaceAll(redacted, secret, []byte("[REDACTED]"))
	}
	return string(redacted)
}

// Logs returns container logs
func (c *Client) Logs(ctx context.Context, container string, tail int) (string, error) {
	// Validate container name/ID
	if err := validateNameOrID(container); err != nil {
		return "", fmt.Errorf("invalid container name/ID: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := []string{"logs", container}
	if tail > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", tail))
	}

	cmd := c.command(ctx, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}

	return string(output), nil
}

// validateImageName validates a Docker image name
// Supports formats: name, name:tag, registry/name, registry/name:tag, name@sha256:digest
var imageNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./@:-]*$`)

func validateImageName(image string) error {
	if image == "" {
		return fmt.Errorf("image name cannot be empty")
	}
	if len(image) > 512 {
		return fmt.Errorf("image name too long (max 512 characters)")
	}
	if !imageNameRegex.MatchString(image) {
		return fmt.Errorf("invalid image name format")
	}
	return nil
}

// Pull pulls a Docker image with a 10-minute timeout.
func (c *Client) Pull(ctx context.Context, image string) error {
	// Validate image name
	if err := validateImageName(image); err != nil {
		return fmt.Errorf("invalid image name: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := c.command(ctx, "pull", image)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("failed to pull image %s: %w (%s)", image, err, errMsg)
		}
		return fmt.Errorf("failed to pull image %s: %w", image, err)
	}

	return nil
}

// CanRunContainers tests whether the Docker daemon can actually create containers.
// On some VPS (OpenVZ/LXC), Docker installs and the daemon starts, but the kernel
// blocks container creation (unshare/namespace errors).
func (c *Client) CanRunContainers(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := c.command(ctx, "run", "--rm", "busybox", "true")
	return cmd.Run() == nil
}

// removeResource executes a docker remove command and treats "not found" as success.
func (c *Client) removeResource(ctx context.Context, args []string, resourceType, name string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		out := strings.TrimSpace(string(output))
		if strings.Contains(out, "not found") || strings.Contains(out, "No such") {
			return nil // already gone
		}
		return fmt.Errorf("failed to remove %s %s: %w", resourceType, name, err)
	}
	return nil
}

// RemoveContainer force-removes a container (stopped or running).
func (c *Client) RemoveContainer(ctx context.Context, nameOrID string) error {
	if err := validateNameOrID(nameOrID); err != nil {
		return fmt.Errorf("invalid container name/ID: %w", err)
	}
	return c.removeResource(ctx, []string{"rm", "-f", nameOrID}, "container", nameOrID)
}

// RemoveNetwork removes a Docker network by name.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	if err := validateName(name); err != nil {
		return fmt.Errorf("invalid network name: %w", err)
	}
	return c.removeResource(ctx, []string{"network", "rm", name}, "network", name)
}

// RemoveVolume removes a Docker volume by name.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	if err := validateName(name); err != nil {
		return fmt.Errorf("invalid volume name: %w", err)
	}
	return c.removeResource(ctx, []string{"volume", "rm", name}, "volume", name)
}

// RemoveImage removes a Docker image by name/tag.
func (c *Client) RemoveImage(ctx context.Context, image string) error {
	if err := validateImageName(image); err != nil {
		return fmt.Errorf("invalid image name: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, "rmi", image)
	output, err := cmd.CombinedOutput()
	if err != nil {
		out := strings.TrimSpace(string(output))
		if strings.Contains(out, "No such image") || strings.Contains(out, "not found") {
			return nil // already gone
		}
		return fmt.Errorf("failed to remove image %s: %w", image, err)
	}
	return nil
}

// listByLabel lists Docker resources of the given type matching a label filter.
func (c *Client) listByLabel(ctx context.Context, resourceType, label string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.command(ctx, resourceType, "ls",
		"--filter", "label="+label,
		"--format", "{{.Name}}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list %ss: %w", resourceType, err)
	}

	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// ListNetworksByLabel lists Docker networks matching a label filter.
func (c *Client) ListNetworksByLabel(ctx context.Context, label string) ([]string, error) {
	return c.listByLabel(ctx, "network", label)
}

// ListVolumesByLabel lists Docker volumes matching a label filter.
func (c *Client) ListVolumesByLabel(ctx context.Context, label string) ([]string, error) {
	return c.listByLabel(ctx, "volume", label)
}

// Prune removes dangling images and build cache to reclaim disk space.
// Returns the number of bytes reclaimed.
func (c *Client) Prune(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var reclaimed int64

	// Prune dangling images
	cmd := c.command(ctx, "image", "prune", "-f")
	if output, err := cmd.Output(); err == nil {
		reclaimed += parseReclaimedSpace(string(output))
	}

	// Prune build cache
	cmd = c.command(ctx, "builder", "prune", "-f")
	if output, err := cmd.Output(); err == nil {
		reclaimed += parseReclaimedSpace(string(output))
	}

	return reclaimed, nil
}

// parseReclaimedSpace extracts "Total reclaimed space: X.YMB" from docker prune output.
func parseReclaimedSpace(output string) int64 {
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "reclaimed space") {
			continue
		}
		// Format: "Total reclaimed space: 123.4MB" or "... 1.2GB" or "... 456kB"
		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}
		sizeStr := strings.TrimSpace(parts[len(parts)-1])
		var value float64
		var unit string
		if _, err := fmt.Sscanf(sizeStr, "%f%s", &value, &unit); err != nil {
			continue
		}
		unit = strings.ToUpper(unit)
		switch {
		case strings.HasPrefix(unit, "G"):
			return int64(value * 1024 * 1024 * 1024)
		case strings.HasPrefix(unit, "M"):
			return int64(value * 1024 * 1024)
		case strings.HasPrefix(unit, "K"):
			return int64(value * 1024)
		case strings.HasPrefix(unit, "B"):
			return int64(value)
		}
	}
	return 0
}

// GetServiceStatus converts container info to service status
func GetServiceStatus(container *ContainerInfo) models.ServiceStatus {
	if container == nil {
		return models.ServiceStatusUnknown
	}

	if container.State.Running {
		return models.ServiceStatusRunning
	}
	if container.State.Restarting {
		return models.ServiceStatusStarting
	}

	return models.ServiceStatusStopped
}
