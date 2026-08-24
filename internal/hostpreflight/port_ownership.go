package hostpreflight

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	cloudCoreProject = "stackkit-cloud-core"
	cloudCoreService = "router"
)

var dockerObjectID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

type inspectedCloudCoreContainer struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
	} `json:"HostConfig"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

// currentCloudCoreOwnsPort admits an occupied listener only when Docker proves
// that the exact Compose definition in this workspace created the running
// Cloud Core router and published that port. Any missing, foreign, stale, or
// unparseable evidence remains false and therefore fail-closed.
func currentCloudCoreOwnsPort(ctx context.Context, workspace string, port int) bool {
	root, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil || root == "" || port < 1 || port > 65535 {
		return false
	}
	runtimeDir := filepath.Join(root, ".stackkit", "runtime", "cloud-core")
	composePath := filepath.Join(runtimeDir, "compose.yaml")
	info, err := os.Lstat(composePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	expectedHash, ok := currentCloudCoreConfigHash(ctx, root, runtimeDir, composePath)
	if !ok {
		return false
	}
	ids, ok := boundedDockerOutput(ctx, runtimeDir, nil, "ps", "--filter", "publish="+strconv.Itoa(port), "--format", "{{.ID}}")
	if !ok {
		return false
	}
	fields := strings.Fields(string(ids))
	if len(fields) != 1 || !dockerObjectID.MatchString(fields[0]) {
		return false
	}
	raw, ok := boundedDockerOutput(ctx, runtimeDir, nil, "inspect", fields[0])
	if !ok {
		return false
	}
	var containers []inspectedCloudCoreContainer
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 {
		return false
	}
	container := containers[0]
	labels := container.Config.Labels
	if !container.State.Running || labels["com.docker.compose.project"] != cloudCoreProject ||
		labels["com.docker.compose.service"] != cloudCoreService ||
		labels["com.docker.compose.config-hash"] != expectedHash ||
		filepath.Clean(labels["com.docker.compose.project.config_files"]) != composePath ||
		filepath.Clean(labels["com.docker.compose.project.working_dir"]) != runtimeDir {
		return false
	}
	bindings := container.HostConfig.PortBindings[strconv.Itoa(port)+"/tcp"]
	return len(bindings) == 1 && bindings[0].HostPort == strconv.Itoa(port) &&
		(bindings[0].HostIP == "" || bindings[0].HostIP == "0.0.0.0")
}

func currentCloudCoreConfigHash(ctx context.Context, root, runtimeDir, composePath string) (string, bool) {
	environment := append(os.Environ(), "STACKKIT_CUSTODY_DIR="+filepath.Join(root, ".stackkit", "custody"))
	raw, ok := boundedDockerOutput(ctx, runtimeDir, environment,
		"compose", "--project-name", cloudCoreProject, "-f", composePath, "config", "--hash", cloudCoreService)
	fields := strings.Fields(string(raw))
	if !ok || len(fields) != 2 || fields[0] != cloudCoreService || len(fields[1]) != 64 {
		return "", false
	}
	for _, value := range fields[1] {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return "", false
		}
	}
	return fields[1], true
}

func boundedDockerOutput(ctx context.Context, directory string, environment []string, args ...string) ([]byte, bool) {
	bounded, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	command := exec.CommandContext(bounded, "docker", args...) //nolint:gosec // fixed command plus validated Docker object/port data
	command.Dir = directory
	if environment != nil {
		command.Env = environment
	}
	output, err := command.Output()
	return output, err == nil && len(output) <= 1<<20 && bounded.Err() == nil
}
