package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
)

func normalizeStackActionPlatformNodes(nodes []stackaction.PlatformNode) []stackaction.PlatformNode {
	out := make([]stackaction.PlatformNode, 0, len(nodes))
	for _, node := range nodes {
		node.Name = strings.TrimSpace(node.Name)
		node.Role = normalizeStackActionNodeRole(node.Role)
		node.IP = strings.TrimSpace(node.IP)
		node.Host = strings.TrimSpace(node.Host)
		node.Services = normalizeStackActionNodeServices(node.Services)
		node.Platform.ServerID = strings.TrimSpace(node.Platform.ServerID)
		node.Platform.DestinationUUID = strings.TrimSpace(node.Platform.DestinationUUID)
		node.Platform.EnvironmentID = strings.TrimSpace(node.Platform.EnvironmentID)
		node.Platform.ProjectUUID = strings.TrimSpace(node.Platform.ProjectUUID)
		node.Platform.EnvironmentUUID = strings.TrimSpace(node.Platform.EnvironmentUUID)
		if node.Bootstrap != nil {
			node.Bootstrap.KomodoCoreAddress = strings.TrimSpace(node.Bootstrap.KomodoCoreAddress)
			if node.Bootstrap.SSH != nil {
				node.Bootstrap.SSH.Host = strings.TrimSpace(node.Bootstrap.SSH.Host)
				node.Bootstrap.SSH.User = strings.TrimSpace(node.Bootstrap.SSH.User)
				node.Bootstrap.SSH.ProxyJump = strings.TrimSpace(node.Bootstrap.SSH.ProxyJump)
			}
		}
		if node.Name == "" {
			node.Name = runtimeFirstNonEmptyStackAction(node.Host, node.IP, node.Role)
		}
		if node.Name == "" || isStackActionMainNodeRole(node.Role) {
			continue
		}
		out = append(out, node)
	}
	return out
}

func normalizeStackActionPlatformNodeReferences(nodes []stackaction.PlatformNode, now time.Time) ([]stackaction.PlatformNode, error) {
	nodes = normalizeStackActionPlatformNodes(nodes)
	for index := range nodes {
		node := &nodes[index]
		if runtimePlatformNodeHasObservedTarget(node.Platform) {
			continue
		}
		if node.Bootstrap == nil || node.Bootstrap.SSH == nil {
			return nil, fmt.Errorf("platform node %q has neither an observed platform target nor bootstrap references", node.Name)
		}
		onboardingRef, err := normalizeStackActionReference(node.Bootstrap.OnboardingRef, stackActionScopeNodeOnboard, now)
		if err != nil {
			return nil, fmt.Errorf("platform node %q onboarding_ref: %w", node.Name, err)
		}
		accessRef, err := normalizeStackActionReference(node.Bootstrap.SSH.AccessProfileRef, stackActionScopeRuntimeSSH, now)
		if err != nil {
			return nil, fmt.Errorf("platform node %q access_profile_ref: %w", node.Name, err)
		}
		node.Bootstrap.OnboardingRef = onboardingRef
		node.Bootstrap.SSH.AccessProfileRef = accessRef
	}
	return nodes, nil
}

func runtimePlatformNodeHasObservedTarget(target stackaction.NodePlatformTarget) bool {
	return runtimeFirstNonEmptyStackAction(target.ServerID, target.DestinationUUID, target.EnvironmentID, target.ProjectUUID, target.EnvironmentUUID) != ""
}

func prepareRuntimePlatformNodesStackAction(ctx context.Context, deployDir string, nodes []stackaction.PlatformNode, resolver StackActionReferenceResolver) ([]stackActionCheck, error) {
	var err error
	nodes, err = normalizeStackActionPlatformNodeReferences(nodes, time.Now())
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	platform, cfg, err := runtimePlatformNodeConfigStackAction(deployDir)
	if err != nil {
		return nil, err
	}
	if platform == "" {
		return nil, fmt.Errorf("supplemental nodes require a generated platform manifest or .stackkit/platform.json")
	}
	resolvedNodes, resolveErr := runtimePlatformDeployNodesStackAction(ctx, nodes, resolver)
	if resolveErr != nil {
		return nil, resolveErr
	}
	defer clearRuntimePlatformDeployNodeSecrets(resolvedNodes)
	results, err := platformdeploy.PrepareSupplementalNodeTargets(ctx, platform, resolvedNodes, cfg, nil)
	checks := runtimePlatformNodeChecksStackAction(results)
	if err != nil {
		return checks, err
	}
	if len(checks) == 0 {
		checks = append(checks, stackActionCheck{Name: "platform_nodes_prepare", Status: stackaction.CheckStatusSkipped, Detail: "no supplemental platform nodes"})
	}
	return checks, nil
}

func runtimePlatformNodeConfigStackAction(deployDir string) (string, platformdeploy.HTTPConfig, error) {
	persisted := runtimeLoadPlatformConfigFileStackAction(deployDir)
	platforms := map[string]bool{}
	if platform := normalizeRuntimePlatformNameStackAction(persisted.Platform); platform != "" {
		platforms[platform] = true
	}
	for _, platform := range runtimePlatformNamesFromManifestsStackAction(deployDir) {
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
		BaseURL:         runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(platform, "endpoint"), persisted.endpointStackAction()),
		Token:           runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(platform, "token"), persisted.Token),
		APIKey:          runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(platform, "api_key"), persisted.APIKey, persisted.Token),
		Secret:          runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(platform, "api_secret"), persisted.APISecret),
		EnvironmentID:   runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(platform, "environment_id"), persisted.EnvironmentID),
		ServerID:        runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(platform, "server_id"), persisted.ServerID),
		ProjectUUID:     runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(platform, "project_uuid"), persisted.ProjectUUID),
		EnvironmentUUID: runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(platform, "environment_uuid"), persisted.EnvironmentUUID),
		DestinationUUID: runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(platform, "destination_uuid"), persisted.DestinationUUID),
	}
	return platform, cfg, nil
}

func runtimePlatformNamesFromManifestsStackAction(deployDir string) []string {
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
		for _, deployBundle := range runtimePlatformDeploymentBundlesStackAction(bundle) {
			platform := normalizeRuntimePlatformNameStackAction(deployBundle.Platform)
			if platform == "" || seen[platform] {
				continue
			}
			seen[platform] = true
			out = append(out, platform)
		}
	}
	return out
}

func runtimePlatformDeployNodesStackAction(ctx context.Context, nodes []stackaction.PlatformNode, resolver StackActionReferenceResolver) ([]platformdeploy.SupplementalNodeTarget, error) {
	out := make([]platformdeploy.SupplementalNodeTarget, 0, len(nodes))
	for _, node := range normalizeStackActionPlatformNodes(nodes) {
		var bootstrap *platformdeploy.NodeBootstrap
		if node.Bootstrap != nil {
			if resolver == nil {
				return nil, fmt.Errorf("StackAction reference resolver is not configured")
			}
			onboardingRef, err := normalizeStackActionReference(node.Bootstrap.OnboardingRef, stackActionScopeNodeOnboard, time.Now())
			if err != nil {
				return nil, fmt.Errorf("invalid platform node %q onboarding_ref: %w", node.Name, err)
			}
			onboarding, err := resolver.ResolveNodeOnboarding(ctx, *onboardingRef)
			if err != nil {
				return nil, fmt.Errorf("resolve platform node %q onboarding ref: %w", node.Name, err)
			}
			bootstrap = &platformdeploy.NodeBootstrap{
				KomodoCoreAddress:   node.Bootstrap.KomodoCoreAddress,
				KomodoOnboardingKey: strings.TrimSpace(onboarding.OnboardingKey),
			}
			if node.Bootstrap.SSH != nil {
				accessRef, err := normalizeStackActionReference(node.Bootstrap.SSH.AccessProfileRef, stackActionScopeRuntimeSSH, time.Now())
				if err != nil {
					return nil, fmt.Errorf("invalid platform node %q access_profile_ref: %w", node.Name, err)
				}
				access, err := resolver.ResolveAccessProfile(ctx, *accessRef)
				if err != nil {
					return nil, fmt.Errorf("resolve platform node %q access profile: %w", node.Name, err)
				}
				privateKey := append([]byte(nil), access.PrivateKey...)
				bootstrap.SSH = &platformdeploy.SSHBootstrap{
					Host:       node.Bootstrap.SSH.Host,
					User:       node.Bootstrap.SSH.User,
					Port:       node.Bootstrap.SSH.Port,
					PrivateKey: string(privateKey),
					ProxyJump:  node.Bootstrap.SSH.ProxyJump,
				}
				clear(privateKey)
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
	return out, nil
}

func clearRuntimePlatformDeployNodeSecrets(nodes []platformdeploy.SupplementalNodeTarget) {
	for index := range nodes {
		if nodes[index].Bootstrap == nil {
			continue
		}
		nodes[index].Bootstrap.KomodoOnboardingKey = ""
		if nodes[index].Bootstrap.SSH != nil {
			nodes[index].Bootstrap.SSH.PrivateKey = ""
		}
	}
}

func runtimePlatformNodeChecksStackAction(results []platformdeploy.NodePrepareResult) []stackActionCheck {
	checks := make([]stackActionCheck, 0, len(results))
	for _, result := range results {
		status := stackaction.CheckStatusOK
		if strings.EqualFold(result.Status, "skipped") {
			status = stackaction.CheckStatusSkipped
		}
		detail := strings.TrimSpace(result.Detail)
		if result.NodeName != "" {
			detail = strings.TrimSpace(result.NodeName + ": " + detail)
		}
		checks = append(checks, stackActionCheck{Name: "platform_nodes_prepare", Status: status, Detail: detail})
	}
	return checks
}

func normalizeRuntimePlatformNameStackAction(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "none" {
		return ""
	}
	return platform
}

func normalizeStackActionNodeRole(role string) string {
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

func isStackActionMainNodeRole(role string) bool {
	return normalizeStackActionNodeRole(role) == "main"
}

func normalizeStackActionNodeServices(values []string) []string {
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
