package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
	"gopkg.in/yaml.v3"
)

type fullLocalComposeDocument struct {
	Name     string `yaml:"name"`
	Networks map[string]struct {
		architecturev2renderer.WorkloadComposeNetwork `yaml:",inline"`
		Driver                                        string `yaml:"driver"`
	} `yaml:"networks"`
	Services map[string]struct {
		DNS         []string  `yaml:"dns"`
		Restart     string    `yaml:"restart"`
		NetworkMode yaml.Node `yaml:"network_mode"`
		Networks    yaml.Node `yaml:"networks"`
	} `yaml:"services"`
	path string
}

// admitFullLocalCompose is the pre-start gate shared by both admitted native
// runners, including service recreation. Keep the admitted definition for
// readback and containment, which must never reread a mutable Compose path.
func admitFullLocalCompose(ctx context.Context, path, project string) (*fullLocalComposeDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document fullLocalComposeDocument
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	stackID := ""
	for _, network := range document.Networks {
		if id := network.Labels[architecturev2renderer.FullLocalNetworkOwnerLabel]; id != "" {
			if stackID != "" && stackID != id {
				return nil, errors.New("Full Local networks disagree about stack ownership")
			}
			stackID = id
		}
	}
	profile, err := runtimeprofile.FromBootEnv()
	if err != nil {
		return nil, err
	}
	if !profile.FullLocal() {
		if stackID != "" {
			return nil, errors.New("Full Local network artifact disagrees with Standard boot")
		}
		return nil, nil
	}
	if document.Name == "" || document.Name != project || !filepath.IsAbs(path) {
		return nil, errors.New("Full Local requires its exact Compose project identity and absolute definition path")
	}
	document.path = filepath.Clean(path)
	if stackID == "" {
		return nil, errors.New("Full Local container start requires plan-bound destination admission")
	}
	// Admit the whole definition before Docker observation or firewall effects.
	// Generated services always declare their networks; an implicit default or
	// any network_mode could escape the owned bridge destination rules.
	for name, service := range document.Services {
		if service.NetworkMode.Kind != 0 {
			return nil, fmt.Errorf("Full Local service %s may not declare network_mode", name)
		}
		if _, err := document.serviceNetworks(name); err != nil {
			return nil, err
		}
		if service.Restart != "no" {
			return nil, fmt.Errorf("Full Local service %s may start only through guarded lifecycle admission", name)
		}
		if len(service.DNS) == 0 {
			return nil, fmt.Errorf("Full Local service %s has no admitted DNS transport", name)
		}
		if name == "lan-dns" && len(service.DNS) == 1 && service.DNS[0] == "127.0.0.1" {
			continue
		}
		if err := runtimeprofile.ValidateLocalDNS(service.DNS); err != nil {
			return nil, err
		}
	}
	scope := hostsecurity.ContainerDestinationPolicy{StackID: stackID}
	for _, network := range document.Networks {
		if network.Name == "" || (network.Driver != "" && network.Driver != "bridge") {
			return nil, errors.New("Full Local networks require explicit stable bridge names")
		}
		bridge := hostsecurity.StableContainerBridge(stackID, network.Name)
		if !network.External && (network.Labels[architecturev2renderer.FullLocalNetworkOwnerLabel] != stackID || network.DriverOpts["com.docker.network.bridge.name"] != bridge) {
			return nil, errors.New("Full Local network lost its owned bridge binding")
		}
		command := exec.CommandContext(ctx, "docker", "network", "inspect", network.Name)
		command.Env = []string{"LANG=C", "LC_ALL=C"}
		out, inspectErr := command.CombinedOutput()
		if inspectErr == nil {
			var existing []struct {
				Driver  string            `json:"Driver"`
				Labels  map[string]string `json:"Labels"`
				Options map[string]string `json:"Options"`
			}
			if json.Unmarshal(out, &existing) != nil || len(existing) != 1 || existing[0].Driver != "bridge" || existing[0].Labels[architecturev2renderer.FullLocalNetworkOwnerLabel] != stackID || existing[0].Options["com.docker.network.bridge.name"] != bridge {
				return nil, errors.New("existing Docker network is not owned by the Full Local cohort")
			}
			if _, err := os.Stat(filepath.Join("/sys/class/net", bridge, "bridge")); err != nil {
				return nil, errors.New("owned Docker network has no corresponding Linux bridge")
			}
		} else {
			if !strings.Contains(string(out), "not found") && !strings.Contains(string(out), "No such network") {
				return nil, fmt.Errorf("cannot establish Full Local network ownership: %s", strings.TrimSpace(string(out)))
			}
			if network.External {
				return nil, errors.New("Full Local external routing network is missing")
			}
			if _, err := net.InterfaceByName(bridge); err == nil {
				return nil, errors.New("stable Full Local interface exists without its owned Docker network")
			}
		}
		scope.Bridges = append(scope.Bridges, hostsecurity.ContainerBridge{Network: network.Name, Interface: bridge})
	}
	if err := (hostsecurity.Engine{Host: hostsecurity.LocalHost{}}).EnforceContainerDestinations(ctx, scope); err != nil {
		return nil, err
	}
	if err := verifyFullLocalComposeContainers(ctx, &document, false); err != nil {
		return nil, err
	}
	return &document, nil
}

func (document *fullLocalComposeDocument) serviceNetworks(name string) ([]string, error) {
	var keys []string
	node := document.Services[name].Networks
	switch node.Kind {
	case yaml.SequenceNode:
		for _, key := range node.Content {
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return nil, fmt.Errorf("Full Local service %s has an invalid network attachment", name)
			}
			keys = append(keys, key.Value)
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			keys = append(keys, node.Content[i].Value)
		}
	default:
		return nil, fmt.Errorf("Full Local service %s requires explicit owned network attachments", name)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("Full Local service %s has no owned network attachment", name)
	}
	var networks []string
	for _, key := range keys {
		network, ok := document.Networks[key]
		if !ok || network.Name == "" {
			return nil, fmt.Errorf("Full Local service %s references an undeclared network", name)
		}
		networks = append(networks, network.Name)
	}
	return networks, nil
}

type fullLocalComposeContainer struct {
	ID, Name   string
	Config     struct{ Labels map[string]string }
	HostConfig struct {
		DNS           []string `json:"Dns"`
		NetworkMode   string
		RestartPolicy struct{ Name string }
	}
	NetworkSettings struct{ Networks map[string]json.RawMessage }
	State           struct{ Running, Restarting bool }
}

func observeFullLocalComposeContainers(ctx context.Context, document *fullLocalComposeDocument) ([]fullLocalComposeContainer, error) {
	query, err := fullLocalDockerQuery(ctx, "ps", "--all", "--quiet", "--no-trunc", "--filter", "label=com.docker.compose.project="+document.Name)
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(query))
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 256 {
		return nil, errors.New("Full Local container observation exceeds bound")
	}
	observed := map[string]bool{}
	for _, id := range ids {
		if len(id) != 64 || observed[id] {
			return nil, errors.New("invalid observed Docker container identity")
		}
		for _, ch := range id {
			if !strings.ContainsRune("0123456789abcdef", ch) {
				return nil, errors.New("invalid observed Docker container identity")
			}
		}
		observed[id] = true
	}
	query, err = fullLocalDockerQuery(ctx, append([]string{"container", "inspect"}, ids...)...)
	if err != nil {
		return nil, err
	}
	var containers []fullLocalComposeContainer
	if err := json.Unmarshal(query, &containers); err != nil {
		return nil, err
	}
	if len(containers) != len(ids) {
		return nil, errors.New("Docker container observation is incomplete")
	}
	for _, container := range containers {
		if !observed[container.ID] {
			return nil, errors.New("Docker container inspection differs from the observed identities")
		}
		delete(observed, container.ID)
	}
	return containers, nil
}

// Project/service labels alone also match foreign extras. Require the exact
// generated Compose slot and its source labels before any containment effect.
func (document *fullLocalComposeDocument) ownsContainer(container fullLocalComposeContainer) bool {
	labels := container.Config.Labels
	service := labels["com.docker.compose.service"]
	_, known := document.Services[service]
	return known && labels["com.docker.compose.project"] == document.Name &&
		labels["com.docker.compose.project.working_dir"] == filepath.Dir(document.path) &&
		labels["com.docker.compose.project.config_files"] == document.path &&
		labels["com.docker.compose.container-number"] == "1" &&
		labels["com.docker.compose.oneoff"] == "False" &&
		container.Name == "/"+document.Name+"-"+service+"-1"
}

// Compare the daemon's observed policy before an update and after a start.
// Never repair an unowned container or silently adopt an extra network.
func verifyFullLocalComposeContainers(ctx context.Context, document *fullLocalComposeDocument, started bool) error {
	containers, err := observeFullLocalComposeContainers(ctx, document)
	if err != nil {
		return err
	}
	if len(containers) == 0 && started {
		return errors.New("Docker did not observe the started Full Local cohort")
	}
	for _, container := range containers {
		if !document.ownsContainer(container) {
			return errors.New("existing container differs from the exact Full Local project")
		}
		name := container.Config.Labels["com.docker.compose.service"]
		service := document.Services[name]
		if container.HostConfig.RestartPolicy.Name != "no" {
			return errors.New("existing Full Local container has an unguarded restart policy; recover it through the supported lifecycle")
		}
		if !slices.Equal(service.DNS, container.HostConfig.DNS) {
			return errors.New("existing Full Local container DNS differs from the verified artifact")
		}
		if container.HostConfig.NetworkMode == "host" || strings.HasPrefix(container.HostConfig.NetworkMode, "container:") {
			return errors.New("Full Local container bypasses its owned bridges")
		}
		allowedNetworks, err := document.serviceNetworks(name)
		if err != nil {
			return err
		}
		for network := range container.NetworkSettings.Networks {
			if !slices.Contains(allowedNetworks, network) {
				return errors.New("existing Full Local container is attached to an unplanned network")
			}
		}
	}
	return nil
}

// A failed up may already have started containers. Cleanup survives caller
// cancellation, but is bounded and acts only on immutable observed IDs, never
// through Compose, a mutable definition, a volume or a network.
func finishFullLocalCompose(ctx context.Context, document *fullLocalComposeDocument, runErr error) error {
	if document == nil {
		return runErr
	}
	if runErr == nil {
		runErr = verifyFullLocalComposeContainers(ctx, document, true)
	}
	if runErr == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	containers, err := observeFullLocalComposeContainers(cleanupCtx, document)
	if err != nil {
		return errors.Join(runErr, fmt.Errorf("Full Local containment unverified; Docker observation unavailable: %w", err))
	}
	var ids []string
	foreign := 0
	for _, container := range containers {
		if document.ownsContainer(container) {
			ids = append(ids, container.ID)
		} else {
			foreign++
		}
	}
	var failures []error
	if len(ids) != 0 {
		if _, err := fullLocalDockerQuery(cleanupCtx, append([]string{"container", "update", "--restart=no"}, ids...)...); err != nil {
			failures = append(failures, err)
		}
		if _, err := fullLocalDockerQuery(cleanupCtx, append([]string{"container", "stop", "--time", "0"}, ids...)...); err != nil {
			failures = append(failures, err)
		}
		query, err := fullLocalDockerQuery(cleanupCtx, append([]string{"container", "inspect"}, ids...)...)
		var stopped []fullLocalComposeContainer
		if err == nil {
			err = json.Unmarshal(query, &stopped)
		}
		if err == nil && len(stopped) != len(ids) {
			err = errors.New("containment readback is incomplete")
		}
		remaining := map[string]bool{}
		for _, id := range ids {
			remaining[id] = true
		}
		if err == nil {
			for _, container := range stopped {
				if !remaining[container.ID] || !document.ownsContainer(container) || container.State.Running || container.State.Restarting || container.HostConfig.RestartPolicy.Name != "no" {
					err = errors.New("containment did not prove the exact owned containers stopped with restart disabled")
					break
				}
				delete(remaining, container.ID)
			}
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) != 0 {
		return errors.Join(runErr, fmt.Errorf("Full Local containment incomplete; foreign observations left untouched (%d): %w", foreign, errors.Join(failures...)))
	}
	return fmt.Errorf("%w; Full Local containment verified: %d exact owned observed containers stopped with restart disabled, %d foreign observations left untouched", runErr, len(ids), foreign)
}

func fullLocalDockerQuery(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	output := &standaloneComposeBoundedBuffer{remaining: 4 << 20}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("observe Full Local Docker policy: %w: %s", err, boundedBasementCoreProcessDiagnostic(output.Bytes()))
	}
	if output.exceeded {
		return nil, errors.New("Full Local Docker observation exceeds bound")
	}
	return output.Bytes(), nil
}

func verifyFullLocalCoreDNS(workspace string, definition []byte) error {
	var document struct {
		Networks map[string]architecturev2renderer.WorkloadComposeNetwork `yaml:"networks"`
	}
	if err := yaml.Unmarshal(definition, &document); err != nil {
		return err
	}
	network := document.Networks["basement-core"]
	if network.Labels[architecturev2renderer.FullLocalNetworkOwnerLabel] == "" {
		return nil
	}
	var upstream []string
	if value := network.Labels[architecturev2renderer.FullLocalUpstreamLabel]; value != "" {
		upstream = strings.Split(value, ",")
	}
	return localevidence.VerifyBasementLANDNSPolicy(filepath.Clean(workspace), localevidence.LANDNSOptions{FullLocal: true, Upstream: upstream})
}
