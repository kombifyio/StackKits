// Package docker provides Docker operations for StackKits.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
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
	binary  string
	timeout time.Duration
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

// ContainerInfo represents container information
type ContainerInfo struct {
	ID      string            `json:"Id"`
	Name    string            `json:"Name"`
	Image   string            `json:"Image"`
	State   ContainerState    `json:"State"`
	Ports   []ContainerPort   `json:"Ports"`
	Labels  map[string]string `json:"Labels"`
	Created string            `json:"Created"`
}

// ContainerState represents container state
type ContainerState struct {
	Status     string       `json:"Status"`
	Running    bool         `json:"Running"`
	Paused     bool         `json:"Paused"`
	Restarting bool         `json:"Restarting"`
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

	cmd := exec.CommandContext(ctx, c.binary, "version", "--format", "{{.Server.Version}}") // #nosec G204 -- binary path is set at construction, not from user input
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

	cmd := exec.CommandContext(ctx, c.binary, "info") // #nosec G204 -- binary path is set at construction, not from user input
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

	cmd := exec.CommandContext(ctx, c.binary, args...) // #nosec G204 -- binary path is set at construction, not from user input
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

	cmd := exec.CommandContext(ctx, c.binary, "inspect", nameOrID) // #nosec G204 -- binary path is set at construction, not from user input
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

// ContainerIPAddress returns the first Docker network IP assigned to a
// container. Host-network containers legitimately return an empty string.
func (c *Client) ContainerIPAddress(ctx context.Context, nameOrID string) (string, error) {
	if err := validateNameOrID(nameOrID); err != nil {
		return "", fmt.Errorf("invalid container name/ID: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binary, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", nameOrID) // #nosec G204 -- binary path is set at construction, not from user input
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
	cmd := exec.CommandContext(ctx, c.binary, "ps", "-a",
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

	cmd := exec.CommandContext(ctx, c.binary, "network", "inspect", name) // #nosec G204 -- binary path is set at construction, not from user input
	return cmd.Run() == nil
}

// VolumeExists checks if a volume exists
func (c *Client) VolumeExists(ctx context.Context, name string) bool {
	// Validate volume name
	if err := validateName(name); err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binary, "volume", "inspect", name) // #nosec G204 -- binary path is set at construction, not from user input
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
	cmd := exec.CommandContext(ctx, c.binary, args...) // #nosec G204 -- binary path is set at construction, not from user input

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("exec failed: %w: %s", err, stderr.String())
	}

	return stdout.String(), nil
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

	cmd := exec.CommandContext(ctx, c.binary, args...) // #nosec G204 -- binary path is set at construction, not from user input
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

	cmd := exec.CommandContext(ctx, c.binary, "pull", image) // #nosec G204 -- binary path is set at construction, not from user input
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

	cmd := exec.CommandContext(ctx, c.binary, "run", "--rm", "busybox", "true") // #nosec G204 -- binary path is set at construction
	return cmd.Run() == nil
}

// removeResource executes a docker remove command and treats "not found" as success.
func (c *Client) removeResource(ctx context.Context, args []string, resourceType, name string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binary, args...) // #nosec G204 -- binary path is set at construction, not from user input
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

	cmd := exec.CommandContext(ctx, c.binary, "rmi", image) // #nosec G204 -- binary path is set at construction, not from user input
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

	cmd := exec.CommandContext(ctx, c.binary, resourceType, "ls", // #nosec G204 -- binary path is set at construction, not from user input
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
	cmd := exec.CommandContext(ctx, c.binary, "image", "prune", "-f") // #nosec G204 -- binary path is set at construction
	if output, err := cmd.Output(); err == nil {
		reclaimed += parseReclaimedSpace(string(output))
	}

	// Prune build cache
	cmd = exec.CommandContext(ctx, c.binary, "builder", "prune", "-f") // #nosec G204 -- binary path is set at construction
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
