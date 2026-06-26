package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeaction"
)

func normalizeRuntimeActionPlatformNodes(nodes []runtimeaction.PlatformNode) []runtimeaction.PlatformNode {
	out := make([]runtimeaction.PlatformNode, 0, len(nodes))
	for _, node := range nodes {
		node.Name = strings.TrimSpace(node.Name)
		node.Role = normalizeRuntimeActionNodeRole(node.Role)
		node.IP = strings.TrimSpace(node.IP)
		node.Host = strings.TrimSpace(node.Host)
		node.Services = normalizeRuntimeActionNodeServices(node.Services)
		node.Platform.ServerID = strings.TrimSpace(node.Platform.ServerID)
		node.Platform.DestinationUUID = strings.TrimSpace(node.Platform.DestinationUUID)
		node.Platform.EnvironmentID = strings.TrimSpace(node.Platform.EnvironmentID)
		node.Platform.ProjectUUID = strings.TrimSpace(node.Platform.ProjectUUID)
		node.Platform.EnvironmentUUID = strings.TrimSpace(node.Platform.EnvironmentUUID)
		if node.Bootstrap != nil {
			node.Bootstrap.KomodoCoreAddress = strings.TrimSpace(node.Bootstrap.KomodoCoreAddress)
			node.Bootstrap.KomodoOnboardingKey = strings.TrimSpace(node.Bootstrap.KomodoOnboardingKey)
			if node.Bootstrap.SSH != nil {
				node.Bootstrap.SSH.Host = strings.TrimSpace(node.Bootstrap.SSH.Host)
				node.Bootstrap.SSH.User = strings.TrimSpace(node.Bootstrap.SSH.User)
				node.Bootstrap.SSH.KeyPath = strings.TrimSpace(node.Bootstrap.SSH.KeyPath)
				node.Bootstrap.SSH.KeyPEM = strings.TrimSpace(node.Bootstrap.SSH.KeyPEM)
				node.Bootstrap.SSH.PrivateKey = strings.TrimSpace(node.Bootstrap.SSH.PrivateKey)
				node.Bootstrap.SSH.ClientPrivateKey = strings.TrimSpace(node.Bootstrap.SSH.ClientPrivateKey)
				node.Bootstrap.SSH.ProxyJump = strings.TrimSpace(node.Bootstrap.SSH.ProxyJump)
			}
		}
		if node.Name == "" {
			node.Name = runtimeFirstNonEmpty(node.Host, node.IP, node.Role)
		}
		if node.Name == "" || isRuntimeActionMainNodeRole(node.Role) {
			continue
		}
		out = append(out, node)
	}
	return out
}

func prepareRuntimePlatformNodes(ctx context.Context, deployDir string, nodes []runtimeaction.PlatformNode) ([]runtimeActionCheck, error) {
	nodes = normalizeRuntimeActionPlatformNodes(nodes)
	if len(nodes) == 0 {
		return nil, nil
	}
	platform, cfg, err := runtimePlatformNodeConfig(deployDir)
	if err != nil {
		return nil, err
	}
	if platform == "" {
		return nil, fmt.Errorf("supplemental nodes require a generated platform manifest or .stackkit/platform.json")
	}
	results, err := platformdeploy.PrepareSupplementalNodeTargets(ctx, platform, runtimePlatformDeployNodes(nodes), cfg, nil)
	checks := runtimePlatformNodeChecks(results)
	if err != nil {
		return checks, err
	}
	if len(checks) == 0 {
		checks = append(checks, runtimeActionCheck{Name: "platform_nodes_prepare", Status: runtimeaction.CheckStatusSkipped, Detail: "no supplemental platform nodes"})
	}
	return checks, nil
}

func runtimePlatformNodeConfig(deployDir string) (string, platformdeploy.HTTPConfig, error) {
	persisted := runtimeLoadPlatformConfigFile(deployDir)
	platforms := map[string]bool{}
	if platform := normalizeRuntimePlatformName(persisted.Platform); platform != "" {
		platforms[platform] = true
	}
	for _, platform := range runtimePlatformNamesFromManifests(deployDir) {
		if platform != "" {
			platforms[platform] = true
		}
	}
	if len(platforms) > 1 {
		names := make([]string, 0, len(platforms))
		for name := range platforms {
			names = append(names, name)
		}
		return "", platformdeploy.HTTPConfig{}, fmt.Errorf("supplemental node handoff currently requires exactly one selected platform, got %s", strings.Join(names, ","))
	}
	var platform string
	for name := range platforms {
		platform = name
	}
	cfg := platformdeploy.HTTPConfig{
		BaseURL:         runtimeFirstNonEmpty(runtimeFirstPlatformEnv(platform, "endpoint"), persisted.endpoint()),
		Token:           runtimeFirstNonEmpty(runtimeFirstPlatformEnv(platform, "token"), persisted.Token),
		APIKey:          runtimeFirstNonEmpty(runtimeFirstPlatformEnv(platform, "api_key"), persisted.APIKey, persisted.Token),
		Secret:          runtimeFirstNonEmpty(runtimeFirstPlatformEnv(platform, "api_secret"), persisted.APISecret),
		EnvironmentID:   runtimeFirstNonEmpty(runtimeFirstPlatformEnv(platform, "environment_id"), persisted.EnvironmentID),
		ServerID:        runtimeFirstNonEmpty(runtimeFirstPlatformEnv(platform, "server_id"), persisted.ServerID),
		ProjectUUID:     runtimeFirstNonEmpty(runtimeFirstPlatformEnv(platform, "project_uuid"), persisted.ProjectUUID),
		EnvironmentUUID: runtimeFirstNonEmpty(runtimeFirstPlatformEnv(platform, "environment_uuid"), persisted.EnvironmentUUID),
		DestinationUUID: runtimeFirstNonEmpty(runtimeFirstPlatformEnv(platform, "destination_uuid"), persisted.DestinationUUID),
	}
	return platform, cfg, nil
}

func runtimePlatformNamesFromManifests(deployDir string) []string {
	manifestPaths := []string{
		filepath.Join(deployDir, "platform-apps", "manifest.json"),
		filepath.Join(deployDir, ".platform-apps-manifest.json"),
	}
	seen := map[string]bool{}
	var out []string
	for _, manifestPath := range manifestPaths {
		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}
		bundle, err := platformdeploy.LoadBundleManifest(manifestPath)
		if err != nil {
			continue
		}
		for _, deployBundle := range runtimePlatformDeploymentBundles(bundle) {
			platform := normalizeRuntimePlatformName(deployBundle.Platform)
			if platform == "" || seen[platform] {
				continue
			}
			seen[platform] = true
			out = append(out, platform)
		}
	}
	return out
}

func runtimePlatformDeployNodes(nodes []runtimeaction.PlatformNode) []platformdeploy.SupplementalNodeTarget {
	out := make([]platformdeploy.SupplementalNodeTarget, 0, len(nodes))
	for _, node := range normalizeRuntimeActionPlatformNodes(nodes) {
		var bootstrap *platformdeploy.NodeBootstrap
		if node.Bootstrap != nil {
			bootstrap = &platformdeploy.NodeBootstrap{
				KomodoCoreAddress:   node.Bootstrap.KomodoCoreAddress,
				KomodoOnboardingKey: node.Bootstrap.KomodoOnboardingKey,
			}
			if node.Bootstrap.SSH != nil {
				bootstrap.SSH = &platformdeploy.SSHBootstrap{
					Host:             node.Bootstrap.SSH.Host,
					User:             node.Bootstrap.SSH.User,
					Port:             node.Bootstrap.SSH.Port,
					KeyPath:          node.Bootstrap.SSH.KeyPath,
					KeyPEM:           node.Bootstrap.SSH.KeyPEM,
					PrivateKey:       node.Bootstrap.SSH.PrivateKey,
					ClientPrivateKey: node.Bootstrap.SSH.ClientPrivateKey,
					ProxyJump:        node.Bootstrap.SSH.ProxyJump,
				}
			}
		}
		out = append(out, platformdeploy.SupplementalNodeTarget{
			Name:     node.Name,
			Role:     node.Role,
			IP:       node.IP,
			Host:     node.Host,
			Services: append([]string(nil), node.Services...),
			Platform: platformdeploy.NodePlatformTarget{
				ServerID:        node.Platform.ServerID,
				DestinationUUID: node.Platform.DestinationUUID,
				EnvironmentID:   node.Platform.EnvironmentID,
				ProjectUUID:     node.Platform.ProjectUUID,
				EnvironmentUUID: node.Platform.EnvironmentUUID,
			},
			Bootstrap: bootstrap,
		})
	}
	return out
}

func runtimePlatformNodeChecks(results []platformdeploy.NodePrepareResult) []runtimeActionCheck {
	checks := make([]runtimeActionCheck, 0, len(results))
	for _, result := range results {
		status := runtimeaction.CheckStatusOK
		if strings.EqualFold(result.Status, "skipped") {
			status = runtimeaction.CheckStatusSkipped
		}
		detail := strings.TrimSpace(result.Detail)
		if result.NodeName != "" {
			detail = strings.TrimSpace(result.NodeName + ": " + detail)
		}
		checks = append(checks, runtimeActionCheck{Name: "platform_nodes_prepare", Status: status, Detail: detail})
	}
	return checks
}

func normalizeRuntimePlatformName(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "none" {
		return ""
	}
	return platform
}

func normalizeRuntimeActionNodeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "foundation", "standalone", "main", "control-plane", "control_plane":
		return "main"
	case "storage":
		return "storage"
	case "":
		return "worker"
	default:
		return strings.ToLower(strings.TrimSpace(role))
	}
}

func isRuntimeActionMainNodeRole(role string) bool {
	return normalizeRuntimeActionNodeRole(role) == "main"
}

func normalizeRuntimeActionNodeServices(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}
