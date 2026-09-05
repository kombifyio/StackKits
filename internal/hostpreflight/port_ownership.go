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

var (
	dockerObjectID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)
	composeProject = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	composeService = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)
)

type inspectedRuntimeContainer struct {
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

// coreRuntimeDirectories lists the private per-kit core runtime directories this
// workspace has rendered — `.stackkit/runtime/cloud-core` for the Cloud kit,
// `.stackkit/runtime/basement-core` for Basement, and whatever a later kit core
// renders under the same root. The set is read from disk rather than named, so a
// new kit core is admitted without editing this file; that is deliberate, because
// naming one core here is what previously blocked every other kit's second Apply.
// Application bundles under `.stackkit/runtime/applications/<name>` are not core
// runtimes and are deliberately excluded: `applications` itself holds no
// compose.yaml, so it never matches.
func coreRuntimeDirectories(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, ".stackkit", "runtime"))
	if err != nil {
		return nil
	}
	directories := make([]string, 0, len(entries))
	for _, entry := range entries {
		// ReadDir reports symlinks as links rather than directories, so a
		// symlinked runtime directory is skipped instead of followed.
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(root, ".stackkit", "runtime", entry.Name())
		info, err := os.Lstat(filepath.Join(directory, "compose.yaml"))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		directories = append(directories, directory)
	}
	return directories
}

// currentWorkspaceOwnsPort admits an occupied listener only when Docker proves
// that a Compose definition rendered into this workspace created the running
// container and published that port. Ownership is derived from the container's
// own Compose labels and then re-verified against the workspace's Compose file,
// so no kit, project or service name is hard-coded. Any missing, foreign, stale,
// or unparseable evidence remains false and therefore fail-closed.
func currentWorkspaceOwnsPort(ctx context.Context, workspace string, port int) bool {
	root, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil || root == "" || port < 1 || port > 65535 {
		return false
	}
	directories := coreRuntimeDirectories(root)
	if len(directories) == 0 {
		return false
	}
	ids, ok := boundedDockerOutput(ctx, root, nil, "ps", "--filter", "publish="+strconv.Itoa(port), "--format", "{{.ID}}")
	if !ok {
		return false
	}
	fields := strings.Fields(string(ids))
	if len(fields) != 1 || !dockerObjectID.MatchString(fields[0]) {
		return false
	}
	raw, ok := boundedDockerOutput(ctx, root, nil, "inspect", fields[0])
	if !ok {
		return false
	}
	var containers []inspectedRuntimeContainer
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 {
		return false
	}
	container := containers[0]
	if !container.State.Running {
		return false
	}
	labels := container.Config.Labels
	project := labels["com.docker.compose.project"]
	service := labels["com.docker.compose.service"]
	if !composeProject.MatchString(project) || !composeService.MatchString(service) {
		return false
	}
	runtimeDir, ok := claimedRuntimeDirectory(labels, directories)
	if !ok {
		return false
	}
	// The labels above are the container's own claim. Recomputing the hash from
	// this workspace's Compose file for the claimed service is what turns that
	// claim into proof: a container that did not come from this definition
	// cannot reproduce its config hash.
	expectedHash, ok := runtimeConfigHash(ctx, root, runtimeDir, project, service)
	if !ok || labels["com.docker.compose.config-hash"] != expectedHash {
		return false
	}
	return publishesHostPort(container, port)
}

// publishesHostPort proves the container publishes exactly this host port on a
// host-wide address. PortBindings is keyed by CONTAINER port, so the host port
// has to be read out of the binding rather than used as the lookup key —
// matching on the key only happens to work while a service maps a port onto
// itself, and fails closed the moment one does not.
func publishesHostPort(container inspectedRuntimeContainer, port int) bool {
	wanted := strconv.Itoa(port)
	matches := 0
	for containerPort, bindings := range container.HostConfig.PortBindings {
		if !strings.HasSuffix(containerPort, "/tcp") {
			continue
		}
		for _, binding := range bindings {
			if binding.HostPort != wanted {
				continue
			}
			if binding.HostIP != "" && binding.HostIP != "0.0.0.0" {
				return false
			}
			matches++
		}
	}
	return matches == 1
}

// claimedRuntimeDirectory binds the container to one of this workspace's own
// core runtime directories. Both the working directory and the Compose file path
// must match, so a container started from an identically named project outside
// this workspace is rejected.
func claimedRuntimeDirectory(labels map[string]string, directories []string) (string, bool) {
	workingDir := filepath.Clean(labels["com.docker.compose.project.working_dir"])
	configFiles := filepath.Clean(labels["com.docker.compose.project.config_files"])
	for _, directory := range directories {
		if workingDir == directory && configFiles == filepath.Join(directory, "compose.yaml") {
			return directory, true
		}
	}
	return "", false
}

func runtimeConfigHash(ctx context.Context, root, runtimeDir, project, service string) (string, bool) {
	composePath := filepath.Join(runtimeDir, "compose.yaml")
	environment := append(os.Environ(), "STACKKIT_CUSTODY_DIR="+filepath.Join(root, ".stackkit", "custody"))
	raw, ok := boundedDockerOutput(ctx, runtimeDir, environment,
		"compose", "--project-name", project, "-f", composePath, "config", "--hash", service)
	fields := strings.Fields(string(raw))
	if !ok || len(fields) != 2 || fields[0] != service || len(fields[1]) != 64 {
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
