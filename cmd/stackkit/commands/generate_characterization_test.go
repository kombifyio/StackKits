package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cuebridge "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTfvarsJSON_LocalModeCoreDecisions(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name: "local-lab",
		Compute: models.ComputeSpec{
			Tier: models.ComputeTierStandard,
		},
		Nodes: []models.NodeSpec{
			{Name: "node1", Role: "standalone", IP: "192.168.1.50"},
		},
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, models.DomainHomeLab, stringVar(t, vars, "domain"))
	assert.False(t, boolVar(t, vars, "enable_dnsmasq"))
	assert.False(t, boolVar(t, vars, "enable_kombify_point"))
	assert.False(t, boolVar(t, vars, "enable_https"))
	assert.False(t, boolVar(t, vars, "step_ca_enabled"))
	assert.Equal(t, models.PAASCoolify, stringVar(t, vars, "paas"))
	assert.False(t, boolVar(t, vars, "enable_dokploy"))
	assert.False(t, boolVar(t, vars, "enable_dockge"))
	assert.True(t, boolVar(t, vars, "enable_coolify"))
	assert.True(t, boolVar(t, vars, "enable_dashboard"))
}

func TestGenerateTfvarsJSON_PublicDomainEnablesManagedTLS(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	spec := &models.StackSpec{
		Name:   "public-lab",
		Domain: "example.com",
		TLS: models.TLSSpec{
			Provider: "cloudflare",
		},
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, "example.com", stringVar(t, vars, "domain"))
	assert.True(t, boolVar(t, vars, "enable_https"))
	assert.Equal(t, "dns", stringVar(t, vars, "acme_challenge"))
	assert.Equal(t, "cloudflare", stringVar(t, vars, "dns_provider"))
	assert.Equal(t, "admin@example.com", stringVar(t, vars, "acme_email"))
}

func TestGenerateTfvarsJSON_LowTierKeepsRequiredPlatform(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:   "small-lab",
		Domain: models.DomainHomeLab,
		Compute: models.ComputeSpec{
			Tier: models.ComputeTierLow,
		},
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, models.PAASCoolify, stringVar(t, vars, "paas"))
	assert.False(t, boolVar(t, vars, "enable_dokploy"))
	assert.False(t, boolVar(t, vars, "enable_dockge"))
	assert.True(t, boolVar(t, vars, "enable_coolify"))
	assert.False(t, boolVar(t, vars, "enable_jellyfin"))
	assert.False(t, boolVar(t, vars, "enable_immich"))
	assert.True(t, boolVar(t, vars, "enable_vaultwarden"))
}

func TestGenerateTfvarsJSON_ExplicitCoolifyOverridesDefaultPAAS(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	spec := &models.StackSpec{
		Name:   "coolify-lab",
		Domain: "example.com",
		PAAS:   models.PAASCoolify,
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, models.PAASCoolify, stringVar(t, vars, "paas"))
	assert.Equal(t, models.ReverseProxyCoolify, stringVar(t, vars, "reverse_proxy_backend"))
	assert.True(t, boolVar(t, vars, "enable_coolify"))
	assert.False(t, boolVar(t, vars, "enable_dokploy"))
	assert.False(t, boolVar(t, vars, "enable_dockge"))
	assert.False(t, boolVar(t, vars, "enable_traefik"))
}

func TestGenerateTfvarsJSON_KombifyMePreservesFlatRoutingInputs(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	spec := &models.StackSpec{
		Name:            "sphere-lab",
		Domain:          models.DomainKombifyMe,
		SubdomainPrefix: "sh-sphere-abc123",
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, models.DomainKombifyMe, stringVar(t, vars, "domain"))
	assert.Equal(t, "sh-sphere-abc123", stringVar(t, vars, "subdomain_prefix"))
	assert.False(t, boolVar(t, vars, "enable_https"))
	assert.True(t, boolVar(t, vars, "step_ca_enabled"))
	assert.Equal(t, "step-ca", stringVar(t, vars, "tls_provider"))
}

func TestGenerateTfvarsJSON_CloudWithoutDomainDefaultsToKombifyMeCoolify(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	spec := &models.StackSpec{
		Name: "cloud-defaults",
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, models.DomainKombifyMe, stringVar(t, vars, "domain"))
	assert.Equal(t, models.PAASCoolify, stringVar(t, vars, "paas"))
	assert.Equal(t, models.ReverseProxyCoolify, stringVar(t, vars, "reverse_proxy_backend"))
	assert.False(t, boolVar(t, vars, "enable_dokploy"))
	assert.True(t, boolVar(t, vars, "enable_coolify"))
	assert.False(t, boolVar(t, vars, "enable_https"))
	assert.True(t, boolVar(t, vars, "step_ca_enabled"))
	assert.False(t, boolVar(t, vars, "enable_dnsmasq"))
}

func TestGenerateTfvarsJSON_CloudCustomDomainDefaultsToCoolify(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	spec := &models.StackSpec{
		Name:   "cloud-custom-domain",
		Domain: "apps.example.com",
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, "apps.example.com", stringVar(t, vars, "domain"))
	assert.Equal(t, models.PAASCoolify, stringVar(t, vars, "paas"))
	assert.Equal(t, models.ReverseProxyCoolify, stringVar(t, vars, "reverse_proxy_backend"))
	assert.True(t, boolVar(t, vars, "enable_coolify"))
	assert.False(t, boolVar(t, vars, "enable_dokploy"))
	assert.True(t, boolVar(t, vars, "enable_https"))
}

func TestGenerateTfvarsJSON_LocalDNSDomainEnablesKombifyPointWithExplicitPAAS(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:   "local-coolify",
		Domain: models.DomainStackHome,
		PAAS:   models.PAASCoolify,
		Nodes: []models.NodeSpec{
			{Name: "node1", Role: "standalone", IP: "192.168.1.60"},
		},
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, models.DomainStackHome, stringVar(t, vars, "domain"))
	assert.True(t, boolVar(t, vars, "enable_kombify_point"))
	assert.True(t, boolVar(t, vars, "enable_dnsmasq"))
	assert.Equal(t, "192.168.1.60", stringVar(t, vars, "server_lan_ip"))
	assert.Equal(t, models.PAASCoolify, stringVar(t, vars, "paas"))
	assert.True(t, boolVar(t, vars, "enable_coolify"))
	assert.True(t, boolVar(t, vars, "enable_https"))
	assert.True(t, boolVar(t, vars, "step_ca_enabled"))
}

func TestGenerateTfvarsJSON_MultiNodeDefaultsToCoolifyAndHomepage(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:   "cluster-lab",
		Domain: models.DomainHomeLab,
		Nodes: []models.NodeSpec{
			{Name: "main-1", Role: models.NodeRoleMain, IP: "192.168.1.10"},
			{Name: "worker-1", Role: models.NodeRoleWorker, IP: "192.168.1.11"},
		},
	}

	vars := decodeTFVars(t, spec)

	assert.Equal(t, models.PAASCoolify, stringVar(t, vars, "paas"))
	assert.Equal(t, models.ReverseProxyCoolify, stringVar(t, vars, "reverse_proxy_backend"))
	assert.True(t, boolVar(t, vars, "enable_coolify"))
	assert.False(t, boolVar(t, vars, "enable_dokploy"))
	assert.True(t, boolVar(t, vars, "enable_dashboard"))
	assert.True(t, boolVar(t, vars, "enable_homepage"))
	assert.True(t, boolVar(t, vars, "enable_uptime_kuma"))
}

func TestGenerateTfvarsJSON_BridgeParityOnCoreKeys(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	spec := &models.StackSpec{
		Name:   "bridge-parity",
		Domain: "example.com",
		PAAS:   models.PAASCoolify,
		TLS: models.TLSSpec{
			Provider: "cloudflare",
		},
		Compute: models.ComputeSpec{
			Tier: models.ComputeTierHigh,
		},
	}

	expected := decodeTFVars(t, spec)

	bridge := cuebridge.NewTerraformBridge(".")
	outDir := t.TempDir()
	require.NoError(t, bridge.GenerateTFVarsFromSpec(spec, outDir))

	data, err := os.ReadFile(filepath.Join(outDir, "terraform.tfvars.json"))
	require.NoError(t, err)

	var actual map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &actual))

	keys := []string{
		"domain",
		"network_name",
		"network_subnet",
		"enable_https",
		"acme_challenge",
		"dns_provider",
		"paas",
		"reverse_proxy_backend",
		"enable_platform_fallback",
		"platform_fallback_mode",
		"enable_traefik",
		"enable_tinyauth",
		"enable_pocketid",
		"enable_dokploy",
		"enable_dokploy_apps",
		"enable_dockge",
		"enable_coolify",
		"enable_dashboard",
		"enable_homepage",
		"enable_uptime_kuma",
		"enable_vaultwarden",
		"enable_jellyfin",
		"enable_immich",
		"brand_color",
		"dashboard_title",
		"tinyauth_app_url",
	}

	for _, key := range keys {
		assert.Equalf(t, expected[key], actual[key], "bridge mismatch for key %s", key)
	}
}

func decodeTFVars(t *testing.T, spec *models.StackSpec) map[string]interface{} {
	t.Helper()

	data, err := generateTfvarsJSON(spec, nil)
	require.NoError(t, err)

	var vars map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &vars))
	return vars
}

func setCapabilitiesHome(t *testing.T, ctx models.NodeContext) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	capsDir := filepath.Join(home, ".stackkits")
	require.NoError(t, os.MkdirAll(capsDir, 0750))

	caps := models.DockerCapabilities{
		ResolvedContext:  ctx,
		BridgeNetworking: true,
		StorageDriver:    models.StorageOverlay2,
		CPUCores:         4,
		MemoryGB:         8,
		PrivateIP:        "192.168.1.50",
	}

	data, err := json.Marshal(caps)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(capsDir, "capabilities.json"), data, 0600))
}

func boolVar(t *testing.T, vars map[string]interface{}, key string) bool {
	t.Helper()
	raw, ok := vars[key]
	if !ok {
		return false
	}
	v, ok := raw.(bool)
	require.Truef(t, ok, "expected bool for key %s", key)
	return v
}

func stringVar(t *testing.T, vars map[string]interface{}, key string) string {
	t.Helper()
	v, ok := vars[key].(string)
	require.Truef(t, ok, "expected string for key %s", key)
	return v
}
