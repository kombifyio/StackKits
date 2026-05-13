package commands

import (
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
)

// TestSpecMatrix_TierContextPAAS systematically tests all meaningful
// combinations of ComputeTier × NodeContext × explicit PAAS selection.
// Each subtest verifies the resolved PAAS, reverse proxy backend, and
// the corresponding enable flags in the generated TFVars.
func TestSpecMatrix_TierContextPAAS(t *testing.T) {
	tests := []struct {
		name    string
		tier    string
		context models.NodeContext
		paas    string // explicit PAAS override (empty = auto-resolve)
		domain  string

		wantPAAS         string
		wantReverseProxy string
		wantDokploy      bool
		wantDockge       bool
		wantCoolify      bool
		wantTraefik      bool
	}{
		// --- Local context: tier-based resolution ---
		{
			name: "local/standard/auto",
			tier: models.ComputeTierStandard, context: models.ContextLocal,
			wantPAAS: models.PAASDokploy, wantReverseProxy: models.ReverseProxyDokploy,
			wantDokploy: true, wantDockge: false, wantCoolify: false, wantTraefik: false,
		},
		{
			name: "local/low/auto",
			tier: models.ComputeTierLow, context: models.ContextLocal,
			wantPAAS: models.PAASDokploy, wantReverseProxy: models.ReverseProxyDokploy,
			wantDokploy: true, wantDockge: false, wantCoolify: false, wantTraefik: false,
		},
		{
			name: "local/high/auto",
			tier: models.ComputeTierHigh, context: models.ContextLocal,
			wantPAAS: models.PAASDokploy, wantReverseProxy: models.ReverseProxyDokploy,
			wantDokploy: true, wantDockge: false, wantCoolify: false, wantTraefik: false,
		},
		{
			name: "local/standard/explicit-coolify",
			tier: models.ComputeTierStandard, context: models.ContextLocal, paas: models.PAASCoolify,
			wantPAAS: models.PAASCoolify, wantReverseProxy: models.ReverseProxyCoolify,
			wantDokploy: false, wantDockge: false, wantCoolify: true, wantTraefik: false,
		},
		{
			name: "local/low/explicit-dokploy",
			tier: models.ComputeTierLow, context: models.ContextLocal, paas: models.PAASDokploy,
			wantPAAS: models.PAASDokploy, wantReverseProxy: models.ReverseProxyDokploy,
			wantDokploy: true, wantDockge: false, wantCoolify: false, wantTraefik: false,
		},

		// --- Cloud context: domain-aware resolution ---
		{
			name: "cloud/standard/no-domain",
			tier: models.ComputeTierStandard, context: models.ContextCloud,
			// Empty domain + cloud → SuggestDomainForContext → kombify.me → PAASDokploy
			wantPAAS: models.PAASDokploy, wantReverseProxy: models.ReverseProxyDokploy,
			wantDokploy: true, wantDockge: false, wantCoolify: false, wantTraefik: false,
		},
		{
			name: "cloud/standard/custom-domain",
			tier: models.ComputeTierStandard, context: models.ContextCloud, domain: "apps.example.com",
			// Custom public domain + cloud → PAASCoolify
			wantPAAS: models.PAASCoolify, wantReverseProxy: models.ReverseProxyCoolify,
			wantDokploy: false, wantDockge: false, wantCoolify: true, wantTraefik: false,
		},
		{
			name: "cloud/low/custom-domain",
			tier: models.ComputeTierLow, context: models.ContextCloud, domain: "my.server.io",
			// Custom public domain + cloud → PAASCoolify (domain wins over tier)
			wantPAAS: models.PAASCoolify, wantReverseProxy: models.ReverseProxyCoolify,
			wantDokploy: false, wantDockge: false, wantCoolify: true, wantTraefik: false,
		},
		{
			name: "cloud/standard/kombify-me",
			tier: models.ComputeTierStandard, context: models.ContextCloud, domain: models.DomainKombifyMe,
			wantPAAS: models.PAASDokploy, wantReverseProxy: models.ReverseProxyDokploy,
			wantDokploy: true, wantDockge: false, wantCoolify: false, wantTraefik: false,
		},
		{
			name: "cloud/high/explicit-dockge",
			tier: models.ComputeTierHigh, context: models.ContextCloud, paas: models.PAASDockge, domain: "example.com",
			// Explicit PAAS always wins
			wantPAAS: models.PAASDockge, wantReverseProxy: models.ReverseProxyStandalone,
			wantDokploy: false, wantDockge: true, wantCoolify: false, wantTraefik: true,
		},

		// --- Pi context: low-resource, tier-based ---
		{
			name: "pi/low/auto",
			tier: models.ComputeTierLow, context: models.ContextPi,
			wantPAAS: models.PAASDokploy, wantReverseProxy: models.ReverseProxyDokploy,
			wantDokploy: true, wantDockge: false, wantCoolify: false, wantTraefik: false,
		},
		{
			name: "pi/standard/auto",
			tier: models.ComputeTierStandard, context: models.ContextPi,
			wantPAAS: models.PAASDokploy, wantReverseProxy: models.ReverseProxyDokploy,
			wantDokploy: true, wantDockge: false, wantCoolify: false, wantTraefik: false,
		},

		// --- Default tier (empty string = standard) ---
		{
			name:    "local/default-tier/auto",
			context: models.ContextLocal,
			// Empty tier → ComputeTierStandard → PAASDokploy
			wantPAAS: models.PAASDokploy, wantReverseProxy: models.ReverseProxyDokploy,
			wantDokploy: true, wantDockge: false, wantCoolify: false, wantTraefik: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setCapabilitiesHome(t, tt.context)

			spec := &models.StackSpec{
				Name:    "matrix-" + tt.name,
				Domain:  tt.domain,
				PAAS:    tt.paas,
				Compute: models.ComputeSpec{Tier: tt.tier},
			}

			vars := decodeTFVars(t, spec)

			assert.Equal(t, tt.wantPAAS, stringVar(t, vars, "paas"), "paas mismatch")
			assert.Equal(t, tt.wantReverseProxy, stringVar(t, vars, "reverse_proxy_backend"), "reverse_proxy_backend mismatch")
			assert.Equal(t, tt.wantDokploy, boolVar(t, vars, "enable_dokploy"), "enable_dokploy mismatch")
			assert.Equal(t, tt.wantDockge, boolVar(t, vars, "enable_dockge"), "enable_dockge mismatch")
			assert.Equal(t, tt.wantCoolify, boolVar(t, vars, "enable_coolify"), "enable_coolify mismatch")
			assert.Equal(t, tt.wantTraefik, boolVar(t, vars, "enable_traefik"), "enable_traefik mismatch")
		})
	}
}

// TestSpecMatrix_DomainResolution tests that domain -> TLS -> local DNS logic
// is correct across all domain types and contexts.
func TestSpecMatrix_DomainResolution(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		context models.NodeContext

		wantDomain       string
		wantHTTPS        bool
		wantKombifyPoint bool
	}{
		// Local defaults use Kombify Point DNS and StackKit-managed Step-CA
		// HTTPS. Browser-local `.localhost` remains the explicit no-PKI legacy
		// exception.
		{
			name: "empty-domain/local", context: models.ContextLocal,
			wantDomain: models.DomainHomeLab, wantHTTPS: true, wantKombifyPoint: true,
		},
		{
			name: "homelab-literal/local", domain: "homelab", context: models.ContextLocal,
			wantDomain: models.DomainHomeLab, wantHTTPS: true, wantKombifyPoint: true,
		},
		{
			name: "home.lab/local", domain: "home.lab", context: models.ContextLocal,
			wantDomain: "home.lab", wantHTTPS: true, wantKombifyPoint: true,
		},
		{
			name: "stack.local/local", domain: "stack.local", context: models.ContextLocal,
			wantDomain: "stack.local", wantHTTPS: true, wantKombifyPoint: true,
		},
		{
			name: "mylab.lan/local", domain: "mylab.lan", context: models.ContextLocal,
			wantDomain: "mylab.lan", wantHTTPS: true, wantKombifyPoint: true,
		},
		{
			name: "home.localhost/local", domain: "home.localhost", context: models.ContextLocal,
			wantDomain: "home.localhost", wantHTTPS: false, wantKombifyPoint: false,
		},
		{
			name: "home/local", domain: "home", context: models.ContextLocal,
			wantDomain: models.DomainHomeLab, wantHTTPS: true, wantKombifyPoint: true,
		},

		// Cloud + local domain → redirect to kombify.me (no HTTPS, no Kombify Point)
		{
			name: "empty-domain/cloud", context: models.ContextCloud,
			// Empty → DomainHomelab("homelab") → SuggestDomain → kombify.me
			wantDomain: models.DomainKombifyMe, wantHTTPS: false, wantKombifyPoint: false,
		},
		{
			name: "homelab/cloud", domain: "homelab", context: models.ContextCloud,
			wantDomain: models.DomainKombifyMe, wantHTTPS: false, wantKombifyPoint: false,
		},

		// kombify.me → no HTTPS (Cloudflare handles TLS), no Kombify Point
		{
			name: "kombify.me/cloud", domain: models.DomainKombifyMe, context: models.ContextCloud,
			wantDomain: models.DomainKombifyMe, wantHTTPS: false, wantKombifyPoint: false,
		},

		// Public domain → HTTPS enabled, no Kombify Point
		{
			name: "custom-domain/cloud", domain: "example.com", context: models.ContextCloud,
			wantDomain: "example.com", wantHTTPS: true, wantKombifyPoint: false,
		},
		{
			name: "apps.myserver.io/cloud", domain: "apps.myserver.io", context: models.ContextCloud,
			wantDomain: "apps.myserver.io", wantHTTPS: true, wantKombifyPoint: false,
		},

		// Pi context (same as local for domain logic)
		{
			name: "empty-domain/pi", context: models.ContextPi,
			wantDomain: models.DomainHomeLab, wantHTTPS: true, wantKombifyPoint: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setCapabilitiesHome(t, tt.context)

			spec := &models.StackSpec{
				Name:   "domain-" + tt.name,
				Domain: tt.domain,
			}

			vars := decodeTFVars(t, spec)

			assert.Equal(t, tt.wantDomain, stringVar(t, vars, "domain"), "domain mismatch")
			assert.Equal(t, tt.wantHTTPS, boolVar(t, vars, "enable_https"), "enable_https mismatch")
			assert.Contains(t, vars, "step_ca_enabled", "step_ca_enabled must be rendered explicitly so Terraform defaults cannot re-enable Step-CA")
			wantStepCA := models.IsKombifyMeDomain(tt.wantDomain) || (tt.wantKombifyPoint && tt.wantHTTPS)
			assert.Equal(t, wantStepCA, boolVar(t, vars, "step_ca_enabled"), "step_ca_enabled mismatch")
			assert.Equal(t, tt.wantKombifyPoint, boolVar(t, vars, "enable_kombify_point"), "enable_kombify_point mismatch")
			assert.Equal(t, tt.wantKombifyPoint, boolVar(t, vars, "enable_dnsmasq"), "deprecated enable_dnsmasq alias mismatch")
		})
	}
}

func TestCloudKombifyMeDefaultEnablesWhoamiDiagnostic(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	spec := &models.StackSpec{
		Name:    "cloud-whoami",
		Domain:  models.DomainKombifyMe,
		Context: string(models.ContextCloud),
		Compute: models.ComputeSpec{
			Tier: models.ComputeTierStandard,
		},
	}

	vars := decodeTFVars(t, spec)
	assert.True(t, boolVar(t, vars, "enable_whoami"), "cloud kombify.me default should include whoami diagnostics")
}

// TestSpecMatrix_TLSConfiguration verifies ACME/TLS fields across provider combinations.
func TestSpecMatrix_TLSConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		domain       string
		adminEmail   string
		tlsProvider  string
		tlsChallenge string

		wantHTTPS     bool
		wantChallenge string
		wantProvider  string
		wantAcmeEmail string
	}{
		{
			name:   "public-domain-dns-challenge",
			domain: "example.com", tlsProvider: "cloudflare",
			wantHTTPS: true, wantChallenge: "dns", wantProvider: "cloudflare",
			wantAcmeEmail: "admin@example.com",
		},
		{
			name:   "public-domain-tls-challenge",
			domain: "example.com",
			// No provider → TLS challenge
			wantHTTPS: true, wantChallenge: "tls", wantProvider: "",
			wantAcmeEmail: "admin@example.com",
		},
		{
			name:   "public-domain-explicit-challenge",
			domain: "example.com", tlsChallenge: "http",
			wantHTTPS: true, wantChallenge: "http", wantProvider: "",
			wantAcmeEmail: "admin@example.com",
		},
		{
			name:   "public-domain-custom-email",
			domain: "example.com", adminEmail: "ops@company.com",
			wantHTTPS: true, wantChallenge: "tls", wantProvider: "",
			wantAcmeEmail: "ops@company.com",
		},
		{
			name:   "public-domain-admin-only-email",
			domain: "example.com", adminEmail: "admin",
			// "admin" alone → fallback to admin@domain
			wantHTTPS: true, wantChallenge: "tls", wantProvider: "",
			wantAcmeEmail: "admin@example.com",
		},
		{
			name:      "local-domain-no-tls",
			domain:    models.DomainHomeLab,
			wantHTTPS: false,
		},
		{
			name:      "kombify-me-no-tls",
			domain:    models.DomainKombifyMe,
			wantHTTPS: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setCapabilitiesHome(t, models.ContextCloud)

			spec := &models.StackSpec{
				Name:       "tls-" + tt.name,
				Domain:     tt.domain,
				AdminEmail: tt.adminEmail,
				TLS: models.TLSSpec{
					Provider:  tt.tlsProvider,
					Challenge: tt.tlsChallenge,
				},
			}

			vars := decodeTFVars(t, spec)

			assert.Equal(t, tt.wantHTTPS, boolVar(t, vars, "enable_https"), "enable_https")

			if tt.wantHTTPS {
				assert.Equal(t, tt.wantChallenge, stringVar(t, vars, "acme_challenge"), "acme_challenge")
				assert.Equal(t, tt.wantAcmeEmail, stringVar(t, vars, "acme_email"), "acme_email")
				if tt.wantProvider != "" {
					assert.Equal(t, tt.wantProvider, stringVar(t, vars, "dns_provider"), "dns_provider")
				}
			}
		})
	}
}

// TestSpecMatrix_TierServiceProfile verifies tier-dependent service enablement.
func TestSpecMatrix_TierServiceProfile(t *testing.T) {
	tests := []struct {
		name string
		tier string

		wantJellyfin    bool
		wantImmich      bool
		wantDashboard   bool
		wantUptimeKuma  bool
		wantVaultwarden bool
	}{
		{
			name:         "low-tier-lightweight",
			tier:         models.ComputeTierLow,
			wantJellyfin: false, wantImmich: false,
			wantDashboard: true, wantUptimeKuma: true, wantVaultwarden: true,
		},
		{
			name:         "standard-tier-full",
			tier:         models.ComputeTierStandard,
			wantJellyfin: true, wantImmich: true,
			wantDashboard: true, wantUptimeKuma: true, wantVaultwarden: true,
		},
		{
			name:         "high-tier-full",
			tier:         models.ComputeTierHigh,
			wantJellyfin: true, wantImmich: true,
			wantDashboard: true, wantUptimeKuma: true, wantVaultwarden: true,
		},
		{
			name:         "empty-tier-defaults-to-standard",
			tier:         "",
			wantJellyfin: true, wantImmich: true,
			wantDashboard: true, wantUptimeKuma: true, wantVaultwarden: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setCapabilitiesHome(t, models.ContextLocal)

			spec := &models.StackSpec{
				Name:    "tier-" + tt.name,
				Compute: models.ComputeSpec{Tier: tt.tier},
			}

			vars := decodeTFVars(t, spec)

			assert.Equal(t, tt.wantJellyfin, boolVar(t, vars, "enable_jellyfin"), "enable_jellyfin")
			assert.Equal(t, tt.wantImmich, boolVar(t, vars, "enable_immich"), "enable_immich")
			assert.Equal(t, tt.wantDashboard, boolVar(t, vars, "enable_dashboard"), "enable_dashboard")
			assert.Equal(t, tt.wantUptimeKuma, boolVar(t, vars, "enable_uptime_kuma"), "enable_uptime_kuma")
			assert.Equal(t, tt.wantVaultwarden, boolVar(t, vars, "enable_vaultwarden"), "enable_vaultwarden")
		})
	}
}
