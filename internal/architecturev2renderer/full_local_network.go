package architecturev2renderer

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
	"gopkg.in/yaml.v3"
)

const fullLocalNetworkContractSalt = "|full-local-network:stable-owned-bridge,destination-admission,explicit-local-dns,guarded-restarts-v1"
const FullLocalNetworkOwnerLabel = hostsecurity.FullLocalContainerOwnerLabel
const FullLocalResolverLabel = "io.stackkit.full-local-resolver"
const FullLocalUpstreamLabel = "io.stackkit.full-local-upstream"

func FullLocalNetworkOptions(stackID, network string) (map[string]string, map[string]string) {
	return map[string]string{"com.docker.network.bridge.name": hostsecurity.StableContainerBridge(stackID, network)}, map[string]string{FullLocalNetworkOwnerLabel: stackID}
}

func coreFullLocalNetworkFragment(policy runtimeprofile.LocalNetwork, network string) string {
	return fmt.Sprintf("    driver_opts:\n      com.docker.network.bridge.name: %s\n    labels:\n      %s: %q\n      %s: %q\n      %s: %q\n", hostsecurity.StableContainerBridge(policy.StackID, network), FullLocalNetworkOwnerLabel, policy.StackID, FullLocalResolverLabel, policy.Resolver, FullLocalUpstreamLabel, strings.Join(policy.Upstream, ","))
}

var fullLocalCoreNetworks = hostsecurity.CoreContainerNetworks()

func coreDNSServices(compose []byte) ([]string, error) {
	var document struct {
		Services map[string]struct{} `yaml:"services"`
	}
	if err := yaml.Unmarshal(compose, &document); err != nil {
		return nil, err
	}
	services := make([]string, 0, len(document.Services))
	for service := range document.Services {
		// Its DNS binding already belongs to the existing listener renderer.
		if service != "step-ca" {
			services = append(services, service)
		}
	}
	sort.Strings(services)
	return services, nil
}

func renderFullLocalCoreNetwork(unit RenderUnit, compose []byte) ([]byte, error) {
	policy := unit.LocalNetwork()
	if policy == nil {
		return nil, fail(ErrInvalidPlan, "core.network", "Full Local requires the plan-bound local network")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	// Daemon autostart cannot run the plan-bound pre-start gate. A stopped
	// cohort must return through the supported lifecycle, including at boot.
	compose = bytes.ReplaceAll(compose, []byte("    restart: unless-stopped\n"), []byte("    restart: \"no\"\n"))
	for _, network := range fullLocalCoreNetworks {
		anchor := "    name: " + network + "\n"
		if bytes.Count(compose, []byte(anchor)) != 1 {
			return nil, fail(ErrOutputChanged, "core.network", "missing unique core network")
		}
		compose = bytes.Replace(compose, []byte(anchor), []byte(anchor+coreFullLocalNetworkFragment(*policy, network)), 1)
	}
	services, err := coreDNSServices(compose)
	if err != nil {
		return nil, err
	}
	for _, service := range services {
		anchor := "  " + service + ":\n"
		resolver := policy.Resolver
		if service == "lan-dns" {
			resolver = "127.0.0.1"
		}
		if bytes.Count(compose, []byte(anchor)) != 1 {
			return nil, fail(ErrOutputChanged, "core.network", "missing governed service")
		}
		compose = bytes.Replace(compose, []byte(anchor), []byte(anchor+fmt.Sprintf("    dns: [%q]\n", resolver)), 1)
	}
	return compose, nil
}

func stripFullLocalCoreNetwork(compose []byte) ([]byte, bool) {
	var document struct {
		Networks map[string]struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"networks"`
	}
	if yaml.Unmarshal(compose, &document) != nil {
		return nil, false
	}
	labels := document.Networks["basement-core"].Labels
	stackID, present := labels[FullLocalNetworkOwnerLabel]
	if !present {
		return compose, true
	}
	policy := runtimeprofile.LocalNetwork{StackID: stackID, Resolver: labels[FullLocalResolverLabel]}
	if labels[FullLocalUpstreamLabel] != "" {
		policy.Upstream = strings.Split(labels[FullLocalUpstreamLabel], ",")
	}
	if policy.Validate() != nil {
		return nil, false
	}
	compose = bytes.ReplaceAll(compose, []byte("    restart: \"no\"\n"), []byte("    restart: unless-stopped\n"))
	for _, network := range fullLocalCoreNetworks {
		anchor := "    name: " + network + "\n"
		block := anchor + coreFullLocalNetworkFragment(policy, network)
		if bytes.Count(compose, []byte(block)) != 1 {
			return nil, false
		}
		compose = bytes.Replace(compose, []byte(block), []byte(anchor), 1)
	}
	services, err := coreDNSServices(compose)
	if err != nil {
		return nil, false
	}
	for _, service := range services {
		anchor := "  " + service + ":\n"
		resolver := policy.Resolver
		if service == "lan-dns" {
			resolver = "127.0.0.1"
		}
		block := anchor + fmt.Sprintf("    dns: [%q]\n", resolver)
		if bytes.Count(compose, []byte(block)) != 1 {
			return nil, false
		}
		compose = bytes.Replace(compose, []byte(block), []byte(anchor), 1)
	}
	return compose, true
}
