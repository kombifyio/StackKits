package commands

import (
	"testing"

	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAccessSummary_KombifyMeUsesCanonicalServiceSlugs(t *testing.T) {
	spec := &models.StackSpec{
		Name:            "cloud-lab",
		StackKit:        "base-kit",
		Mode:            "simple",
		Domain:          models.DomainKombifyMe,
		SubdomainPrefix: "sh-cloud-abc123",
	}
	tfvars := map[string]any{
		"domain":             models.DomainKombifyMe,
		"subdomain_prefix":   "sh-cloud-abc123",
		"enable_https":       false,
		"enable_dashboard":   true,
		"enable_tinyauth":    true,
		"enable_pocketid":    true,
		"enable_coolify":     true,
		"enable_dokploy":     false,
		"enable_uptime_kuma": true,
		"enable_vaultwarden": true,
		"enable_jellyfin":    false,
		"enable_immich":      false,
	}

	summary := buildAccessSummaryFromInputs(spec, tfvars, servicecatalog.Default())

	require.Equal(t, "https://sh-cloud-abc123-base.kombify.me", summary.HubURL)
	urls := serviceURLMap(summary)
	assert.Equal(t, "https://sh-cloud-abc123-base.kombify.me", urls["base"])
	assert.Equal(t, "https://sh-cloud-abc123-auth.kombify.me", urls["auth"])
	assert.Equal(t, "https://sh-cloud-abc123-id.kombify.me", urls["id"])
	assert.Equal(t, "https://sh-cloud-abc123-coolify.kombify.me", urls["coolify"])
	assert.Equal(t, "https://sh-cloud-abc123-kuma.kombify.me", urls["kuma"])

	services := servicesByAccessKey(summary)
	assert.Equal(t, "dashboard", services["base"].ToolName)
	assert.Equal(t, "dashboard", services["base"].ModuleSlug)
	assert.Equal(t, "base", services["base"].RouteSlug)
	assert.Contains(t, services["base"].LegacyAliases, "dash")
	assert.Equal(t, "pocketid", services["id"].ToolName)
	assert.Equal(t, "id", services["id"].RouteSlug)
}

func TestBuildAccessSummary_LocalDefaultUsesBrowserNativeLocalhostHub(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "local-lab",
		StackKit: "base-kit",
		Mode:     "simple",
		Domain:   models.DomainHomeLab,
	}
	tfvars := map[string]any{
		"domain":             models.DomainHomeLab,
		"enable_https":       false,
		"enable_dashboard":   true,
		"enable_tinyauth":    true,
		"enable_pocketid":    true,
		"enable_dockge":      true,
		"enable_dokploy":     false,
		"enable_uptime_kuma": true,
	}

	summary := buildAccessSummaryFromInputs(spec, tfvars, servicecatalog.Default())

	require.Equal(t, "http://base.home.localhost", summary.HubURL)
	urls := serviceURLMap(summary)
	assert.Equal(t, "http://base.home.localhost", urls["base"])
	assert.Equal(t, "http://auth.home.localhost", urls["auth"])
	assert.Equal(t, "http://id.home.localhost", urls["id"])
	assert.Equal(t, "http://dockge.home.localhost", urls["dockge"])
	assert.NotContains(t, urls, "dokploy")
}

func TestBuildAccessSummary_ExplicitLocalDNSIncludesKombifyPoint(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "lan-lab",
		StackKit: "base-kit",
		Mode:     "simple",
		Domain:   models.DomainStackHome,
	}
	tfvars := map[string]any{
		"domain":               models.DomainStackHome,
		"enable_https":         true,
		"enable_dashboard":     true,
		"enable_tinyauth":      true,
		"enable_kombify_point": true,
		"enable_dnsmasq":       true,
		"enable_pocketid":      true,
		"enable_dockge":        false,
		"enable_dokploy":       false,
		"enable_uptime_kuma":   true,
		"enable_vaultwarden":   true,
		"enable_jellyfin":      false,
		"enable_immich":        false,
	}

	summary := buildAccessSummaryFromInputs(spec, tfvars, servicecatalog.Default())

	require.Equal(t, "https://base.stack.home", summary.HubURL)
	urls := serviceURLMap(summary)
	assert.Equal(t, "https://point.stack.home", urls["point"])
	assert.Equal(t, "https://auth.stack.home", urls["auth"])
	assert.Equal(t, "https://id.stack.home", urls["id"])
}

func serviceURLMap(summary *accessSummary) map[string]string {
	urls := make(map[string]string, len(summary.Services))
	for _, svc := range summary.Services {
		urls[svc.Key] = svc.URL
	}
	return urls
}

func servicesByAccessKey(summary *accessSummary) map[string]accessService {
	services := make(map[string]accessService, len(summary.Services))
	for _, svc := range summary.Services {
		services[svc.Key] = svc
	}
	return services
}
