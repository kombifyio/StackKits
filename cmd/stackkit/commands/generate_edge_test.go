package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEdgeCases_MinimalSpec verifies that the absolute minimum spec
// (only Name) produces valid TFVars with sensible defaults.
func TestEdgeCases_MinimalSpec(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name: "minimal",
	}

	vars := decodeTFVars(t, spec)

	// Should get all defaults
	assert.Equal(t, models.DomainHomeLab, stringVar(t, vars, "domain"))
	assert.Equal(t, "base_net", stringVar(t, vars, "network_name"))
	assert.Equal(t, "", stringVar(t, vars, "network_subnet"))
	assert.False(t, boolVar(t, vars, "enable_https"))
	assert.False(t, boolVar(t, vars, "enable_dnsmasq"))
	assert.False(t, boolVar(t, vars, "enable_kombify_point"))
	assert.False(t, boolVar(t, vars, "step_ca_enabled"))
	assert.Equal(t, "admin@example.com", stringVar(t, vars, "admin_email"))
	assert.Equal(t, "minimal", stringVar(t, vars, "dashboard_title"))
	assert.Equal(t, "#F97316", stringVar(t, vars, "brand_color"))
	assert.Equal(t, "bridge", stringVar(t, vars, "network_mode"))
	assert.Equal(t, models.StorageOverlay2, stringVar(t, vars, "storage_driver"))
}

// TestEdgeCases_EmptySpec verifies that a completely empty spec still
// produces valid JSON (no panic, no missing keys).
func TestEdgeCases_EmptySpec(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{}

	data, err := generateTfvarsJSON(spec, nil)
	require.NoError(t, err, "empty spec should not fail generateTfvarsJSON")
	require.NotEmpty(t, data, "should produce output")

	vars := decodeTFVars(t, spec)

	// Dashboard title defaults to "My Homelab" when Name is empty
	assert.Equal(t, "My Homelab", stringVar(t, vars, "dashboard_title"))
	assert.Equal(t, "admin@example.com", stringVar(t, vars, "admin_email"))
}

// TestEdgeCases_NoNodes verifies local mode defaults to browser-native names.
func TestEdgeCases_NoNodes(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name: "no-nodes",
	}

	vars := decodeTFVars(t, spec)

	assert.False(t, boolVar(t, vars, "enable_dnsmasq"), "home.localhost does not need Kombify Point")
	assert.False(t, boolVar(t, vars, "enable_kombify_point"), "home.localhost is browser-native")
}

// TestEdgeCases_NodeWithEmptyIP verifies that a node with no IP falls back
// to the detected LAN IP.
func TestEdgeCases_NodeWithEmptyIP(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:   "empty-ip-node",
		Domain: models.DomainStackHome,
		Nodes: []models.NodeSpec{
			{Name: "node1", Role: "standalone"},
		},
	}

	vars := decodeTFVars(t, spec)
	assert.Equal(t, "192.168.1.50", stringVar(t, vars, "server_lan_ip"), "node without IP should use detected LAN IP")
}

// TestEdgeCases_SubdomainPrefixBranding verifies that subdomain prefix
// is injected into the TinyAuth app URL and related fields.
func TestEdgeCases_SubdomainPrefixBranding(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	spec := &models.StackSpec{
		Name:            "prefix-test",
		Domain:          models.DomainKombifyMe,
		SubdomainPrefix: "sh-mylab-abc123",
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, "sh-mylab-abc123", stringVar(t, vars, "subdomain_prefix"))
	assert.Equal(t, "https://sh-mylab-abc123-auth.kombify.me", stringVar(t, vars, "tinyauth_app_url"))
	assert.Contains(t, stringVar(t, vars, "tinyauth_app_url"), models.DomainKombifyMe)
}

// TestEdgeCases_SubdomainPrefixHTTPS verifies proto in TinyAuth URL
// matches HTTPS status.
func TestEdgeCases_SubdomainPrefixHTTPS(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	// Public domain with HTTPS
	spec := &models.StackSpec{
		Name:            "https-prefix",
		Domain:          "example.com",
		SubdomainPrefix: "lab",
	}

	vars := decodeTFVars(t, spec)
	assert.True(t, boolVar(t, vars, "enable_https"))
	assert.Contains(t, stringVar(t, vars, "tinyauth_app_url"), "https://")
}

// TestEdgeCases_CustomNetworkSubnet verifies custom subnet propagation.
func TestEdgeCases_CustomNetworkSubnet(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name: "custom-subnet",
		Network: models.NetworkSpec{
			Subnet: "10.0.0.0/8",
		},
	}

	vars := decodeTFVars(t, spec)
	assert.Equal(t, "10.0.0.0/8", stringVar(t, vars, "network_subnet"))
}

// TestEdgeCases_DegradedDockerCapabilities verifies that degraded Docker
// capabilities (no bridge, VFS storage, DNS fix needed) correctly adjust TFVars.
func TestEdgeCases_DegradedDockerCapabilities(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	capsDir := filepath.Join(home, ".stackkits")
	require.NoError(t, os.MkdirAll(capsDir, 0750))

	caps := models.DockerCapabilities{
		ResolvedContext:  models.ContextLocal,
		BridgeNetworking: false,
		StorageDriver:    models.StorageVFS,
		DNSFix:           "resolvconf",
		CPUCores:         2,
		MemoryGB:         2,
	}

	data, err := json.Marshal(caps)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(capsDir, "capabilities.json"), data, 0600))

	spec := &models.StackSpec{
		Name:    "degraded-docker",
		Compute: models.ComputeSpec{Tier: models.ComputeTierLow},
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, "host", stringVar(t, vars, "network_mode"), "no bridge → host mode")
	assert.True(t, boolVar(t, vars, "dns_fixed"), "dns fix needed")
	assert.Equal(t, "resolvconf", stringVar(t, vars, "dns_fix_method"))
	assert.True(t, boolVar(t, vars, "storage_driver_degraded"), "VFS is degraded")
	assert.Equal(t, models.StorageVFS, stringVar(t, vars, "storage_driver"))
}

func TestDockerMemoryLimitsEnabled(t *testing.T) {
	assert.True(t, dockerMemoryLimitsEnabled(nil))
	assert.True(t, dockerMemoryLimitsEnabled(&models.DockerCapabilities{
		CgroupVersion:    "v2",
		UnshareAvailable: true,
		MemoryLimits:     true,
	}))
	assert.False(t, dockerMemoryLimitsEnabled(&models.DockerCapabilities{
		CgroupVersion:    "v2",
		UnshareAvailable: true,
		MemoryLimits:     false,
	}))
	assert.False(t, dockerMemoryLimitsEnabled(&models.DockerCapabilities{
		CgroupVersion:    "v2",
		UnshareAvailable: false,
		MemoryLimits:     true,
	}))
	assert.True(t, dockerMemoryLimitsEnabled(&models.DockerCapabilities{
		MemoryLimits: false,
	}))
}

// TestEdgeCases_DockerHostEnvVar verifies DOCKER_HOST env propagation.
func TestEdgeCases_DockerHostEnvVar(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)
	t.Setenv("DOCKER_HOST", "tcp://remote:2375")

	spec := &models.StackSpec{Name: "docker-host-test"}

	vars := decodeTFVars(t, spec)
	assert.Equal(t, "tcp://remote:2375", stringVar(t, vars, "docker_host"))
}

// TestEdgeCases_NilCompositionResult verifies that generateTfvarsJSON
// handles a nil composition result gracefully.
func TestEdgeCases_NilCompositionResult(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:    "nil-composition",
		Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
	}

	data, err := generateTfvarsJSON(spec, nil)
	require.NoError(t, err, "nil composition should not fail")
	require.NotEmpty(t, data)
}
