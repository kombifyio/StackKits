package commands

import (
	"testing"

	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAccessSummary_KombifyMeUsesFlatHTTPSHub(t *testing.T) {
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
		"enable_dokploy":     true,
		"enable_uptime_kuma": true,
		"enable_vaultwarden": true,
		"enable_jellyfin":    false,
		"enable_immich":      false,
	}

	summary := buildAccessSummaryFromInputs(spec, tfvars, cueval.ServiceCatalog())

	require.Equal(t, "https://sh-cloud-abc123-dash.kombify.me", summary.HubURL)
	urls := serviceURLMap(summary)
	assert.Equal(t, "https://sh-cloud-abc123-tinyauth.kombify.me", urls["auth"])
	assert.Equal(t, "https://sh-cloud-abc123-id.kombify.me", urls["pocketid"])
	assert.Equal(t, "https://sh-cloud-abc123-dokploy.kombify.me", urls["dokploy"])
	assert.Equal(t, "https://sh-cloud-abc123-kuma.kombify.me", urls["kuma"])
}

func TestBuildAccessSummary_LocalUsesNestedHTTPSHub(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "local-lab",
		StackKit: "base-kit",
		Mode:     "simple",
		Domain:   models.DomainHomeLab,
	}
	tfvars := map[string]any{
		"domain":             models.DomainHomeLab,
		"enable_https":       true,
		"enable_dashboard":   true,
		"enable_tinyauth":    true,
		"enable_pocketid":    true,
		"enable_dockge":      true,
		"enable_dokploy":     false,
		"enable_uptime_kuma": true,
	}

	summary := buildAccessSummaryFromInputs(spec, tfvars, cueval.ServiceCatalog())

	require.Equal(t, "https://base.home.localhost", summary.HubURL)
	urls := serviceURLMap(summary)
	assert.Equal(t, "https://auth.home.localhost", urls["auth"])
	assert.Equal(t, "https://id.home.localhost", urls["pocketid"])
	assert.Equal(t, "https://dockge.home.localhost", urls["dockge"])
	assert.NotContains(t, urls, "dokploy")
}

func TestBuildAccessSummary_LocalDNSIncludesKombifyPoint(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "lan-lab",
		StackKit: "base-kit",
		Mode:     "simple",
		Domain:   models.DomainHomeDNS,
	}
	tfvars := map[string]any{
		"domain":               models.DomainHomeDNS,
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

	summary := buildAccessSummaryFromInputs(spec, tfvars, cueval.ServiceCatalog())

	require.Equal(t, "https://base.home", summary.HubURL)
	urls := serviceURLMap(summary)
	assert.Equal(t, "https://point.home", urls["point"])
	assert.Equal(t, "https://auth.home", urls["auth"])
	assert.Equal(t, "https://id.home", urls["pocketid"])
}

func serviceURLMap(summary *accessSummary) map[string]string {
	urls := make(map[string]string, len(summary.Services))
	for _, svc := range summary.Services {
		urls[svc.Key] = svc.URL
	}
	return urls
}
