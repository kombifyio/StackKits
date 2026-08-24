// Package cue provides CUE schema validation and Terraform bridge for StackKits.
package cue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/placement"
	"github.com/kombifyio/stackkits/pkg/models"
)

// TerraformBridge projects admitted StackSpecs into Terraform variables.
type TerraformBridge struct{}

// TFVars represents the complete structure of terraform.tfvars.json,
// matching all variables declared in basement-kit/templates/simple/main.tf.
type TFVars struct {
	InstallMode string `json:"installation_mode"`

	// Legacy variable name consumed by existing platform bootstrap scripts.
	BootstrapMode string `json:"bootstrap_mode"`

	PlacementMode         string                       `json:"placement_mode,omitempty"`
	PlacementExposure     string                       `json:"placement_exposure,omitempty"`
	PlacementCoupling     string                       `json:"placement_coupling,omitempty"`
	PlacementCapabilities map[string]map[string]string `json:"placement_capabilities,omitempty"`

	// Domain for Traefik routing (e.g. "stack.local")
	Domain string `json:"domain"`

	SubdomainPrefix string `json:"subdomain_prefix,omitempty"`

	// Docker network name
	NetworkName string `json:"network_name"`

	// Optional Docker network subnet. Empty lets Docker choose a non-overlapping subnet.
	NetworkSubnet string `json:"network_subnet"`
	ServerLANIP   string `json:"server_lan_ip,omitempty"`

	ComputeTier string `json:"compute_tier"`

	EnableKombifyPoint bool `json:"enable_kombify_point"`
	// Deprecated alias kept for older generated templates and external tests.
	EnableDNSMasq bool `json:"enable_dnsmasq"`

	// EnableMDNS turns on the Basement-Kit mDNS responder that advertises flat
	// <service>.local names for zero-config LAN reachability. Independent of the
	// primary domain; on for local deployments, off for cloud/kombify.me.
	EnableMDNS bool `json:"enable_mdns"`

	EnableHTTPS   bool   `json:"enable_https"`
	TLSProvider   string `json:"tls_provider,omitempty"`
	StepCAEnabled bool   `json:"step_ca_enabled"`
	AcmeEmail     string `json:"acme_email,omitempty"`
	AcmeChallenge string `json:"acme_challenge,omitempty"`
	DNSProvider   string `json:"dns_provider,omitempty"`
	DNSAPIToken   string `json:"dns_api_token,omitempty"`
	DNSAPIEmail   string `json:"dns_api_email,omitempty"`

	Paas                   string `json:"paas"`
	ReverseProxyBackend    string `json:"reverse_proxy_backend"`
	EnablePlatformFallback bool   `json:"enable_platform_fallback"`
	PlatformFallbackMode   string `json:"platform_fallback_mode"`

	// Service enable flags
	EnableTraefik       bool   `json:"enable_traefik"`
	EnableTinyauth      bool   `json:"enable_tinyauth"`
	EnablePocketID      bool   `json:"enable_pocketid"`
	EnableDokploy       bool   `json:"enable_dokploy"`
	EnableDokployApps   bool   `json:"enable_dokploy_apps"`
	EnableDockge        bool   `json:"enable_dockge"`
	EnableCoolify       bool   `json:"enable_coolify"`
	EnableKomodo        bool   `json:"enable_komodo"`
	EnableDashboard     bool   `json:"enable_dashboard"`
	EnableHomepage      bool   `json:"enable_homepage"`
	EnableUptimeKuma    bool   `json:"enable_uptime_kuma"`
	EnableWhoami        bool   `json:"enable_whoami"`
	EnableVaultwarden   bool   `json:"enable_vaultwarden"`
	EnableJellyfin      bool   `json:"enable_jellyfin"`
	EnableImmich        bool   `json:"enable_immich"`
	EnableFiles         bool   `json:"enable_files"`
	FilesProvider       string `json:"files_provider"`
	EnableCloudreve     bool   `json:"enable_cloudreve"`
	EnableNextcloud     bool   `json:"enable_nextcloud"`
	EnableHomeAssistant bool   `json:"enable_home_assistant,omitempty"`

	MediaPath string `json:"media_path"`

	DemoDataEnabled               bool   `json:"demo_data_enabled"`
	SetupPolicyPlatform           string `json:"setup_policy_platform"`
	SetupPolicyApplicationDefault string `json:"setup_policy_application_default"`
	SetupPolicyKuma               string `json:"setup_policy_kuma"`
	SetupPolicyWhoami             string `json:"setup_policy_whoami"`
	SetupPolicyVaultwarden        string `json:"setup_policy_vaultwarden"`
	SetupPolicyImmich             string `json:"setup_policy_immich"`
	SetupPolicyFiles              string `json:"setup_policy_files"`

	// TinyAuth configuration
	AdminEmail             string `json:"admin_email"`
	AdminPasswordPlaintext string `json:"admin_password_plaintext"`
	TinyauthUsers          string `json:"tinyauth_users"`
	TinyauthAppURL         string `json:"tinyauth_app_url"`
	TinyauthSessionSecret  string `json:"tinyauth_session_secret,omitempty"`

	// TinyAuth OIDC (PocketID integration)
	TinyauthOIDCEnabled      bool   `json:"tinyauth_oidc_enabled,omitempty"`
	TinyauthOIDCIssuer       string `json:"tinyauth_oidc_issuer,omitempty"`
	TinyauthOIDCClientID     string `json:"tinyauth_oidc_client_id,omitempty"`
	TinyauthOIDCClientSecret string `json:"tinyauth_oidc_client_secret,omitempty"`

	// PocketID configuration
	PocketIDAppURL        string `json:"pocketid_app_url,omitempty"`
	PocketIDEncryptionKey string `json:"pocketid_encryption_key,omitempty"`

	// Branding
	BrandColor     string `json:"brand_color"`
	DashboardTitle string `json:"dashboard_title"`

	// Runtime system app images
	StackKitServerImage string `json:"stackkit_server_image,omitempty"`
	Timezone            string `json:"timezone,omitempty"`

	NetworkMode           string `json:"network_mode"`
	DNSFixed              bool   `json:"dns_fixed"`
	DNSFixMethod          string `json:"dns_fix_method"`
	StorageDriverDegraded bool   `json:"storage_driver_degraded"`
	StorageDriver         string `json:"storage_driver"`

	// Docker host (for remote daemon)
	DockerHost string `json:"docker_host,omitempty"`
}

// NewTerraformBridge creates the canonical StackSpec-to-Terraform bridge.
func NewTerraformBridge() *TerraformBridge {
	return &TerraformBridge{}
}

// GenerateTFVarsFromSpec generates terraform.tfvars.json from a StackSpec.
// This is the canonical generation path used by the CLI.
func (b *TerraformBridge) GenerateTFVarsFromSpec(spec *models.StackSpec, outputDir string) error {
	tfvars, err := b.specToTFVars(spec)
	if err != nil {
		return err
	}
	return b.writeTFVars(tfvars, outputDir)
}

// GenerateTFVarsBytesFromSpec generates terraform.tfvars.json content from a StackSpec.
func (b *TerraformBridge) GenerateTFVarsBytesFromSpec(spec *models.StackSpec) ([]byte, error) {
	tfvars, err := b.specToTFVars(spec)
	if err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(tfvars, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tfvars: %w", err)
	}

	return append(data, '\n'), nil
}

// specToTFVars converts a StackSpec into TFVars aligned with main.tf variables.
func (b *TerraformBridge) specToTFVars(spec *models.StackSpec) (*TFVars, error) {
	tfvars := newDefaultTFVars()

	resolver := models.NewSetupPolicyResolver(spec)
	tfvars.InstallMode = resolver.InstallMode()
	tfvars.BootstrapMode = resolver.InstallMode()
	tfvars.SetupPolicyPlatform = resolver.PlatformPolicy()
	tfvars.SetupPolicyApplicationDefault = resolver.ApplicationDefaultPolicy()
	tfvars.SetupPolicyKuma = resolver.EffectivePlatformServicePolicy("uptime-kuma", "kuma")
	tfvars.SetupPolicyWhoami = resolver.EffectivePlatformServicePolicy("whoami")
	tfvars.SetupPolicyVaultwarden = resolver.EffectiveApplicationPolicy("vault", "vaultwarden", "vault")
	tfvars.SetupPolicyImmich = resolver.EffectiveApplicationPolicy("photos", "immich")
	tfvars.SetupPolicyFiles = resolver.EffectiveApplicationPolicy("files", "files", "cloudreve", "nextcloud")
	tfvars.DemoDataEnabled = spec.DemoData.EffectiveEnabled()
	if err := applyPlacementFromSpec(spec, tfvars); err != nil {
		return nil, err
	}

	domain := models.DomainHomelab
	if spec.Domain != "" {
		domain = spec.Domain
	}
	tfvars.Domain = domain

	if spec.SubdomainPrefix != "" {
		tfvars.SubdomainPrefix = spec.SubdomainPrefix
	}

	caps := loadDockerCapabilities()
	resolvedCtx := resolveNodeContextFromCaps(caps, spec)

	if suggested, _ := netenv.SuggestDomainForContext(resolvedCtx, domain); suggested != "" {
		domain = suggested
		tfvars.Domain = domain
	}

	isLocalMode := models.IsLocalDomain(domain) ||
		strings.HasSuffix(domain, ".internal") ||
		strings.HasSuffix(domain, ".test")
	isKombifyMe := isKombifyMeDomain(domain)

	if isLocalMode {
		if domain == "" || domain == models.DomainHomelab || domain == models.DomainHomeDNS {
			tfvars.Domain = models.DomainHomeLab
		} else {
			tfvars.Domain = domain
		}
		if models.RequiresKombifyPoint(tfvars.Domain) {
			tfvars.EnableKombifyPoint = true
			tfvars.EnableDNSMasq = true
			tfvars.ServerLANIP = serverLANIPForLocalDNS(spec, caps)
		}
		// Basement-Kit mDNS: on for every local deployment (incl. the
		// home.localhost default, which kombify-point excludes) so <svc>.local
		// works zero-config on the LAN. Needs the box LAN IP to advertise.
		if models.RequiresMDNS(tfvars.Domain) {
			tfvars.EnableMDNS = true
			if tfvars.ServerLANIP == "" {
				tfvars.ServerLANIP = serverLANIPForLocalDNS(spec, caps)
			}
		}
	}

	b.configureHTTPS(spec, tfvars, isLocalMode, isKombifyMe)
	b.configureNetwork(spec, tfvars)

	tier := spec.Compute.Tier
	if tier == "" {
		tier = models.ComputeTierStandard
	}
	tfvars.ComputeTier = tier

	b.configurePaaS(spec, tfvars, resolvedCtx)
	b.configureServiceDefaults(tfvars, tier)
	b.configureSystemStorage(spec, tfvars)
	b.applyInstallModeDefaults(spec, tfvars)

	tfvars.AdminEmail = models.ResolveAdminEmailForDomain(spec, tfvars.Domain)

	// NOTE: Admin password and identity credentials (OIDC, PocketID, TinyAuth
	// session secret) are generated by the CompositionEngine and overlaid in
	// generateTfvarsJSON. Bridge only sets structural defaults here.

	b.configureBranding(spec, tfvars)
	applyDockerCapabilities(caps, tfvars)

	if spec.Services != nil {
		b.applyServiceEnables(spec.Services, tfvars)
	}
	if spec.Application != nil {
		b.applyApplicationEnables(spec.Application, tfvars)
	}
	if err := b.applyFilesProviderFromConfig(spec.Services, spec.Application, tfvars); err != nil {
		return nil, err
	}
	b.applyInstallModeDefaults(spec, tfvars)
	if tfvars.EnablePlatformFallback {
		enforcePlatformFallback(tfvars)
	}

	if dockerHost := defaultDockerHost(); dockerHost != "" {
		tfvars.DockerHost = dockerHost
	}

	return tfvars, nil
}

func (b *TerraformBridge) applyInstallModeDefaults(spec *models.StackSpec, tfvars *TFVars) {
	mode := models.InstallModeDefault
	if spec != nil {
		mode = spec.EffectiveInstallMode()
	}
	if mode != models.InstallModeBare {
		return
	}
	tfvars.EnableDashboard = false
	tfvars.EnableHomepage = false
	tfvars.DemoDataEnabled = false
	tfvars.SetupPolicyPlatform = models.SetupPolicyManual
	tfvars.SetupPolicyApplicationDefault = models.SetupPolicyManual
	tfvars.SetupPolicyKuma = models.SetupPolicyManual
	tfvars.SetupPolicyWhoami = models.SetupPolicyManual
	tfvars.SetupPolicyVaultwarden = models.SetupPolicyManual
	tfvars.SetupPolicyImmich = models.SetupPolicyManual
	tfvars.SetupPolicyFiles = models.SetupPolicyManual
}

func (b *TerraformBridge) configureSystemStorage(spec *models.StackSpec, tfvars *TFVars) {
	if spec == nil {
		return
	}
	if spec.System != nil && strings.TrimSpace(spec.System.Timezone) != "" {
		tfvars.Timezone = strings.TrimSpace(spec.System.Timezone)
	}
	if strings.TrimSpace(spec.Storage.MediaPath) != "" {
		tfvars.MediaPath = strings.TrimSpace(spec.Storage.MediaPath)
	}
}

// configureHTTPS sets TLS/ACME configuration based on domain mode.
func (b *TerraformBridge) configureHTTPS(spec *models.StackSpec, tfvars *TFVars, isLocalMode, isKombifyMe bool) {
	if isKombifyMe {
		tfvars.EnableHTTPS = false
		tfvars.TLSProvider = "step-ca"
		tfvars.StepCAEnabled = true
		tfvars.AcmeEmail = localAcmeEmail(spec, tfvars.Domain)
		tfvars.AcmeChallenge = "tls"
		return
	}
	if isLocalMode {
		if models.IsLocalhostDomain(tfvars.Domain) {
			tfvars.EnableHTTPS = false
			tfvars.TLSProvider = ""
			tfvars.StepCAEnabled = false
			tfvars.AcmeEmail = ""
			tfvars.AcmeChallenge = ""
			return
		}
		tfvars.EnableHTTPS = true
		tfvars.TLSProvider = "step-ca"
		tfvars.StepCAEnabled = true
		tfvars.AcmeEmail = ""
		tfvars.AcmeChallenge = ""
		return
	}
	tfvars.EnableHTTPS = true
	tfvars.TLSProvider = "letsencrypt"

	tfvars.AcmeEmail = localAcmeEmail(spec, tfvars.Domain)

	challenge := spec.TLS.Challenge
	if challenge == "" {
		if spec.TLS.Provider != "" {
			challenge = "dns"
		} else {
			challenge = "tls"
		}
	}
	tfvars.AcmeChallenge = challenge
	tfvars.DNSProvider = spec.TLS.Provider
	tfvars.DNSAPIToken = os.Getenv("STACKKIT_DNS_TOKEN")
	tfvars.DNSAPIEmail = os.Getenv("STACKKIT_DNS_EMAIL")
}

func localAcmeEmail(spec *models.StackSpec, domain string) string {
	email := ""
	subdomainPrefix := ""
	if spec != nil {
		email = models.ResolveAdminEmailForDomain(spec, domain)
		subdomainPrefix = spec.SubdomainPrefix
	}
	return models.NormalizeAdminEmail(email, domain, subdomainPrefix)
}

// configureNetwork sets network name and subnet.
func (b *TerraformBridge) configureNetwork(spec *models.StackSpec, tfvars *TFVars) {
	tfvars.NetworkName = "base_net"
	if spec.Network.Subnet != "" {
		tfvars.NetworkSubnet = spec.Network.Subnet
	} else {
		tfvars.NetworkSubnet = ""
	}
}

// configurePaaS resolves the PaaS platform and sets enable flags accordingly.
func (b *TerraformBridge) configurePaaS(spec *models.StackSpec, tfvars *TFVars, resolvedCtx models.NodeContext) {
	paas := spec.ResolvePAASForContext(resolvedCtx)
	reverseProxy := models.ResolveReverseProxyForPAAS(paas)
	if platformFallbackEnabled(spec) {
		reverseProxy = models.ReverseProxyStandalone
		tfvars.EnablePlatformFallback = true
		tfvars.PlatformFallbackMode = models.PlatformFallbackStandaloneCompose
	} else {
		tfvars.EnablePlatformFallback = false
		tfvars.PlatformFallbackMode = models.PlatformFallbackDisabled
	}
	tfvars.Paas = paas
	tfvars.ReverseProxyBackend = reverseProxy
	tfvars.EnableTraefik = reverseProxy == models.ReverseProxyStandalone || reverseProxy == models.ReverseProxyStackKit

	if tfvars.EnablePlatformFallback {
		enforcePlatformFallback(tfvars)
		return
	}

	applyPAASPlatformFlags(tfvars, paas)
}

func applyPAASPlatformFlags(tfvars *TFVars, paas string) {
	switch paas {
	case models.PAASDockge:
		tfvars.EnableDokploy = false
		tfvars.EnableDokployApps = false
		tfvars.EnableDockge = true
		tfvars.EnableCoolify = false
		tfvars.EnableKomodo = false
	case models.PAASCoolify:
		tfvars.EnableDokploy = false
		tfvars.EnableDokployApps = false
		tfvars.EnableDockge = false
		tfvars.EnableCoolify = true
		tfvars.EnableKomodo = false
	case models.PAASKomodo:
		tfvars.EnableDokploy = false
		tfvars.EnableDokployApps = false
		tfvars.EnableDockge = false
		tfvars.EnableCoolify = false
		tfvars.EnableKomodo = true
	default:
		tfvars.EnableDokploy = true
		tfvars.EnableDokployApps = true
		tfvars.EnableDockge = false
		tfvars.EnableCoolify = false
		tfvars.EnableKomodo = false
	}
}

func platformFallbackEnabled(spec *models.StackSpec) bool {
	if spec == nil || !spec.PlatformFallback.Enabled {
		return false
	}
	mode := strings.TrimSpace(spec.PlatformFallback.Mode)
	return mode == "" || mode == models.PlatformFallbackStandaloneCompose
}

func enforcePlatformFallback(tfvars *TFVars) {
	tfvars.EnablePlatformFallback = true
	tfvars.EnableTraefik = true
	tfvars.EnableDokploy = false
	tfvars.EnableDokployApps = false
	tfvars.EnableDockge = false
	tfvars.EnableCoolify = false
	tfvars.EnableKomodo = false
	tfvars.ReverseProxyBackend = models.ReverseProxyStandalone
	tfvars.PlatformFallbackMode = models.PlatformFallbackStandaloneCompose
}

// configureServiceDefaults sets default enable flags based on compute tier.
func (b *TerraformBridge) configureServiceDefaults(tfvars *TFVars, tier string) {
	tfvars.EnableDashboard = true
	tfvars.EnableUptimeKuma = true
	tfvars.EnableWhoami = true
	tfvars.EnableVaultwarden = true
	tfvars.EnablePocketID = true

	isStandardPlus := tier == models.ComputeTierStandard || tier == models.ComputeTierHigh
	tfvars.EnableJellyfin = false
	tfvars.EnableImmich = isStandardPlus
	tfvars.EnableFiles = true
	tfvars.FilesProvider = "cloudreve"
	tfvars.EnableCloudreve = true
	tfvars.EnableNextcloud = false
	tfvars.EnableHomeAssistant = false
	tfvars.MediaPath = "/opt/media"
}

// configureBranding sets TinyAuth app URL, brand color, and dashboard title.
func (b *TerraformBridge) configureBranding(spec *models.StackSpec, tfvars *TFVars) {
	proto := "http"
	if tfvars.EnableHTTPS || models.IsKombifyMeDomain(tfvars.Domain) {
		proto = "https"
	}
	if spec.SubdomainPrefix != "" {
		tfvars.TinyauthAppURL = fmt.Sprintf("%s://%s-auth.%s", proto, spec.SubdomainPrefix, tfvars.Domain)
	} else {
		tfvars.TinyauthAppURL = fmt.Sprintf("%s://auth.%s", proto, tfvars.Domain)
	}

	tfvars.BrandColor = "#F97316"
	if spec.Name != "" {
		tfvars.DashboardTitle = spec.Name
	} else {
		tfvars.DashboardTitle = "My Homelab"
	}
}

// applyDockerCapabilities adjusts TFVars based on detected Docker capabilities.
func applyDockerCapabilities(caps *models.DockerCapabilities, tfvars *TFVars) {
	tfvars.NetworkMode = "bridge"
	tfvars.StorageDriver = models.StorageOverlay2
	if caps == nil {
		return
	}
	if !caps.BridgeNetworking {
		tfvars.NetworkMode = "host"
	}
	if caps.DNSFix != "" && caps.DNSFix != models.DNSFixNone {
		tfvars.DNSFixed = true
		tfvars.DNSFixMethod = caps.DNSFix
	}
	if caps.StorageDriver != "" && caps.StorageDriver != models.StorageOverlay2 {
		tfvars.StorageDriverDegraded = true
		tfvars.StorageDriver = caps.StorageDriver
	}
}

// applyServiceEnables reads enabled/disabled flags from the spec's services map.
func (b *TerraformBridge) applyServiceEnables(services map[string]any, tfvars *TFVars) {
	// Keep in sync with overlayEnableFlags() in cmd/stackkit/commands/generate.go
	enables := map[string]*bool{
		"traefik":           &tfvars.EnableTraefik,
		"tinyauth":          &tfvars.EnableTinyauth,
		"pocketid":          &tfvars.EnablePocketID,
		"id":                &tfvars.EnablePocketID,
		"dokploy":           &tfvars.EnableDokploy,
		"dockge":            &tfvars.EnableDockge,
		"coolify":           &tfvars.EnableCoolify,
		"komodo":            &tfvars.EnableKomodo,
		"dashboard":         &tfvars.EnableDashboard,
		"base":              &tfvars.EnableDashboard,
		"homepage":          &tfvars.EnableHomepage,
		"home":              &tfvars.EnableHomepage,
		"homelab-dashboard": &tfvars.EnableHomepage,
		"uptime_kuma":       &tfvars.EnableUptimeKuma,
		"whoami":            &tfvars.EnableWhoami,
		"vaultwarden":       &tfvars.EnableVaultwarden,
		"jellyfin":          &tfvars.EnableJellyfin,
		"immich":            &tfvars.EnableImmich,
		"files":             &tfvars.EnableFiles,
		"cloudreve":         &tfvars.EnableCloudreve,
		"nextcloud":         &tfvars.EnableNextcloud,
		"home-assistant":    &tfvars.EnableHomeAssistant,
	}
	// Alias: CUE modules use "uptime-kuma" but TFVars uses "uptime_kuma"
	enables["uptime-kuma"] = enables["uptime_kuma"]

	for svcName, ptr := range enables {
		if svcConfig, ok := services[svcName]; ok {
			if svcMap, ok := svcConfig.(map[string]any); ok {
				if enabled, ok := svcMap["enabled"]; ok {
					if v, ok := enabled.(bool); ok {
						if (svcName == "pocketid" || svcName == "id") && !v {
							// PocketID is the mandatory passkey provider until
							// another identity provider can satisfy that contract.
							continue
						}
						*ptr = v
					}
				}
			}
		}
	}
}

func (b *TerraformBridge) applyApplicationEnables(application map[string]any, tfvars *TFVars) {
	if len(application) == 0 {
		return
	}
	if filesConfig, ok := application["files"]; ok {
		if config, ok := filesConfig.(map[string]any); ok {
			if enabled, ok := config["enabled"].(bool); ok {
				tfvars.EnableFiles = enabled
			}
		}
	}
	if smartHomeConfig, ok := application["smart-home"]; ok {
		if config, ok := smartHomeConfig.(map[string]any); ok {
			if enabled, ok := config["enabled"].(bool); ok {
				tfvars.EnableHomeAssistant = enabled
			}
		}
	}
}

func (b *TerraformBridge) applyFilesProviderFromConfig(services, application map[string]any, tfvars *TFVars) error {
	var providers []string
	disabledProviders := map[string]bool{}
	for _, key := range []string{"files", "cloudreve", "nextcloud"} {
		raw, ok := services[key]
		if !ok {
			continue
		}
		config, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch key {
		case "cloudreve", "nextcloud":
			if enabled, ok := config["enabled"].(bool); ok {
				if enabled {
					providers = append(providers, key)
				} else {
					disabledProviders[key] = true
				}
			}
		}
		for _, providerKey := range []string{"provider", "tool", "defaultTool"} {
			if provider, ok := stringFromAny(config[providerKey]); ok {
				providers = append(providers, provider)
			}
		}
	}
	if raw, ok := application["files"]; ok {
		if config, ok := raw.(map[string]any); ok {
			for _, providerKey := range []string{"tool", "provider", "defaultTool"} {
				if provider, ok := stringFromAny(config[providerKey]); ok {
					providers = append(providers, provider)
				}
			}
		}
	}
	if len(providers) == 0 {
		if tfvars.EnableNextcloud && !tfvars.EnableCloudreve {
			providers = append(providers, "nextcloud")
		} else {
			providers = append(providers, "cloudreve")
		}
	}
	normalized := ""
	for _, provider := range providers {
		original := provider
		provider = normalizeFilesProvider(original)
		if provider == "" {
			return fmt.Errorf("unsupported files provider %q; expected cloudreve or nextcloud", original)
		}
		if normalized != "" && provider != normalized {
			return fmt.Errorf("conflicting files providers %q and %q", normalized, provider)
		}
		normalized = provider
	}
	if tfvars.EnableFiles && disabledProviders[normalized] {
		return fmt.Errorf("conflicting files providers: %q is selected but explicitly disabled", normalized)
	}
	if normalized == "nextcloud" && tfvars.EnableFiles && tfvars.ComputeTier != models.ComputeTierStandard && tfvars.ComputeTier != models.ComputeTierHigh {
		return fmt.Errorf("files provider nextcloud requires standard or high compute tier")
	}
	return setFilesProvider(tfvars, normalized)
}

func setFilesProvider(tfvars *TFVars, provider string) error {
	provider = normalizeFilesProvider(provider)
	if provider == "" {
		return fmt.Errorf("unsupported files provider %q; expected cloudreve or nextcloud", provider)
	}
	tfvars.FilesProvider = provider
	tfvars.EnableCloudreve = provider == "cloudreve" && tfvars.EnableFiles
	tfvars.EnableNextcloud = provider == "nextcloud" && tfvars.EnableFiles
	return nil
}

func normalizeFilesProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "cloudreve", "":
		return "cloudreve"
	case "nextcloud":
		return "nextcloud"
	default:
		return ""
	}
}

func stringFromAny(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func applyPlacementFromSpec(spec *models.StackSpec, tfvars *TFVars) error {
	if !hasPlacementIntent(spec) {
		return nil
	}
	resolved, err := placement.ResolveS1(spec)
	if err != nil {
		var notS1 placement.ErrNotS1
		if errors.As(err, &notS1) {
			return nil
		}
		return fmt.Errorf("resolve placement: %w", err)
	}
	applyPlacementResult(tfvars, resolved)
	return nil
}

func hasPlacementIntent(spec *models.StackSpec) bool {
	if spec == nil {
		return false
	}
	return strings.TrimSpace(spec.Placement.Mode) != "" ||
		strings.TrimSpace(spec.Placement.Exposure) != "" ||
		strings.TrimSpace(spec.Placement.Coupling) != ""
}

func applyPlacementResult(tfvars *TFVars, resolved *placement.Result) {
	if tfvars == nil || resolved == nil {
		return
	}
	tfvars.PlacementMode = resolved.Mode
	tfvars.PlacementExposure = resolved.Exposure
	tfvars.PlacementCoupling = resolved.Coupling
	caps := make(map[string]map[string]string, len(resolved.Capabilities))
	for name, binding := range resolved.Capabilities {
		caps[name] = map[string]string{
			"provider": binding.Provider,
			"target":   binding.Target,
		}
	}
	tfvars.PlacementCapabilities = caps
}

// newDefaultTFVars returns TFVars with defaults matching main.tf variable defaults.
func newDefaultTFVars() *TFVars {
	return &TFVars{
		InstallMode:                   models.InstallModeBootstrapped,
		BootstrapMode:                 models.InstallModeBootstrapped,
		EnableTraefik:                 true,
		EnableTinyauth:                true,
		EnablePocketID:                true,
		ComputeTier:                   models.ComputeTierStandard,
		Paas:                          models.PAASCoolify,
		ReverseProxyBackend:           models.ReverseProxyCoolify,
		EnablePlatformFallback:        false,
		PlatformFallbackMode:          models.PlatformFallbackDisabled,
		EnableCoolify:                 true,
		EnableDashboard:               true,
		EnableHomepage:                true,
		EnableWhoami:                  true,
		EnableFiles:                   true,
		FilesProvider:                 "cloudreve",
		EnableCloudreve:               true,
		DemoDataEnabled:               false,
		SetupPolicyPlatform:           models.SetupPolicyAutomatic,
		SetupPolicyApplicationDefault: models.SetupPolicyOnDemand,
		SetupPolicyKuma:               models.SetupPolicyAutomatic,
		SetupPolicyWhoami:             models.SetupPolicyAutomatic,
		SetupPolicyVaultwarden:        models.SetupPolicyOnDemand,
		SetupPolicyImmich:             models.SetupPolicyOnDemand,
		SetupPolicyFiles:              models.SetupPolicyOnDemand,
	}
}

// writeTFVars writes terraform.tfvars.json to the output directory
func (b *TerraformBridge) writeTFVars(tfvars *TFVars, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	outputPath := filepath.Join(outputDir, "terraform.tfvars.json")

	data, err := json.MarshalIndent(tfvars, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tfvars: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write terraform.tfvars.json: %w", err)
	}

	return nil
}

func loadDockerCapabilities() *models.DockerCapabilities {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	capsPath := filepath.Join(home, ".stackkits", "capabilities.json")
	data, err := os.ReadFile(capsPath)
	if err != nil {
		// File not found is expected (prepare not run yet)
		return nil
	}
	var caps models.DockerCapabilities
	if err := json.Unmarshal(data, &caps); err != nil {
		slog.Warn("failed to parse capabilities.json", "path", capsPath, "error", err)
		return nil
	}
	return &caps
}

func resolveNodeContextFromCaps(caps *models.DockerCapabilities, spec *models.StackSpec) models.NodeContext {
	if spec.Context != "" {
		return models.NodeContext(spec.Context)
	}
	if caps != nil && caps.ResolvedContext != "" {
		return caps.ResolvedContext
	}

	detected := netenv.Detect(context.Background())
	if caps == nil {
		caps = &models.DockerCapabilities{}
	}
	caps.NetworkEnv = detected.Environment
	caps.PublicIP = detected.PublicIP
	caps.PrivateIP = detected.PrivateIP
	caps.IsNAT = detected.IsNAT
	caps.HasPublicInterface = detected.HasPublicInterface

	return netenv.ResolveFromResult(detected, caps.CPUCores, caps.MemoryGB)
}

func isKombifyMeDomain(domain string) bool {
	return strings.EqualFold(domain, models.DomainKombifyMe)
}

func serverLANIPForLocalDNS(spec *models.StackSpec, caps *models.DockerCapabilities) string {
	if spec != nil {
		for _, node := range spec.Nodes {
			if strings.TrimSpace(node.IP) != "" {
				return strings.TrimSpace(node.IP)
			}
		}
	}
	if caps != nil && strings.TrimSpace(caps.PrivateIP) != "" {
		return strings.TrimSpace(caps.PrivateIP)
	}
	return netenv.PrivateIP()
}
