// Package cue provides CUE schema validation and Terraform bridge for StackKits.
package cue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/pkg/models"
)

// TerraformBridge generates terraform.tfvars.json from CUE specifications
type TerraformBridge struct {
	ctx         *cue.Context
	stackkitDir string
}

// TFVars represents the complete structure of terraform.tfvars.json,
// matching all variables declared in base-kit/templates/simple/main.tf.
type TFVars struct {
	// Domain for Traefik routing (e.g. "stack.local")
	Domain string `json:"domain"`

	SubdomainPrefix string `json:"subdomain_prefix,omitempty"`

	// Docker network name
	NetworkName string `json:"network_name"`

	// Optional Docker network subnet. Empty lets Docker choose a non-overlapping subnet.
	NetworkSubnet string `json:"network_subnet"`
	ServerLANIP   string `json:"server_lan_ip,omitempty"`

	EnableKombifyPoint bool `json:"enable_kombify_point"`
	// Deprecated alias kept for older generated templates and external tests.
	EnableDNSMasq bool `json:"enable_dnsmasq"`

	EnableHTTPS   bool   `json:"enable_https"`
	TLSProvider   string `json:"tls_provider,omitempty"`
	StepCAEnabled bool   `json:"step_ca_enabled"`
	AcmeEmail     string `json:"acme_email,omitempty"`
	AcmeChallenge string `json:"acme_challenge,omitempty"`
	DNSProvider   string `json:"dns_provider,omitempty"`
	DNSAPIToken   string `json:"dns_api_token,omitempty"`
	DNSAPIEmail   string `json:"dns_api_email,omitempty"`

	Paas                string `json:"paas"`
	ReverseProxyBackend string `json:"reverse_proxy_backend"`

	// Service enable flags
	EnableTraefik     bool `json:"enable_traefik"`
	EnableTinyauth    bool `json:"enable_tinyauth"`
	EnablePocketID    bool `json:"enable_pocketid"`
	EnableDokploy     bool `json:"enable_dokploy"`
	EnableDokployApps bool `json:"enable_dokploy_apps"`
	EnableDockge      bool `json:"enable_dockge"`
	EnableCoolify     bool `json:"enable_coolify"`
	EnableDashboard   bool `json:"enable_dashboard"`
	EnableHomepage    bool `json:"enable_homepage"`
	EnableUptimeKuma  bool `json:"enable_uptime_kuma"`
	EnableWhoami      bool `json:"enable_whoami"`
	EnableVaultwarden bool `json:"enable_vaultwarden"`
	EnableJellyfin    bool `json:"enable_jellyfin"`
	EnableImmich      bool `json:"enable_immich"`

	MediaPath string `json:"media_path"`

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

	NetworkMode           string `json:"network_mode"`
	DNSFixed              bool   `json:"dns_fixed"`
	DNSFixMethod          string `json:"dns_fix_method"`
	StorageDriverDegraded bool   `json:"storage_driver_degraded"`
	StorageDriver         string `json:"storage_driver"`

	// Docker host (for remote daemon)
	DockerHost string `json:"docker_host,omitempty"`
}

// NewTerraformBridge creates a new Terraform bridge for CUE-based generation
func NewTerraformBridge(stackkitDir string) *TerraformBridge {
	return &TerraformBridge{
		ctx:         cuecontext.New(),
		stackkitDir: stackkitDir,
	}
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
	}

	b.configureHTTPS(spec, tfvars, isLocalMode, isKombifyMe)
	b.configureNetwork(spec, tfvars)

	tier := spec.Compute.Tier
	if tier == "" {
		tier = models.ComputeTierStandard
	}

	b.configurePaaS(spec, tfvars, resolvedCtx)
	b.configureServiceDefaults(tfvars, tier)

	adminEmail := spec.AdminEmail
	if adminEmail == "" {
		adminEmail = spec.Email
	}
	tfvars.AdminEmail = models.NormalizeAdminEmail(adminEmail, tfvars.Domain, spec.SubdomainPrefix)

	// NOTE: Admin password and identity credentials (OIDC, PocketID, TinyAuth
	// session secret) are generated by the CompositionEngine and overlaid in
	// generateTfvarsJSON. Bridge only sets structural defaults here.

	b.configureBranding(spec, tfvars)
	applyDockerCapabilities(caps, tfvars)

	if spec.Services != nil {
		b.applyServiceEnables(spec.Services, tfvars)
	}

	if dockerHost := defaultDockerHost(); dockerHost != "" {
		tfvars.DockerHost = dockerHost
	}

	return tfvars, nil
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
		email = spec.AdminEmail
		if email == "" {
			email = spec.Email
		}
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
	tfvars.Paas = paas
	tfvars.ReverseProxyBackend = reverseProxy
	tfvars.EnableTraefik = reverseProxy == models.ReverseProxyStandalone

	switch paas {
	case models.PAASDockge:
		tfvars.EnableDokploy = false
		tfvars.EnableDokployApps = false
		tfvars.EnableDockge = true
		tfvars.EnableCoolify = false
	case models.PAASCoolify:
		tfvars.EnableDokploy = false
		tfvars.EnableDokployApps = false
		tfvars.EnableDockge = false
		tfvars.EnableCoolify = true
	default:
		tfvars.EnableDokploy = true
		tfvars.EnableDokployApps = true
		tfvars.EnableDockge = false
		tfvars.EnableCoolify = false
	}
}

// configureServiceDefaults sets default enable flags based on compute tier.
func (b *TerraformBridge) configureServiceDefaults(tfvars *TFVars, tier string) {
	tfvars.EnableDashboard = true
	tfvars.EnableUptimeKuma = true
	tfvars.EnableWhoami = true
	tfvars.EnableVaultwarden = true
	tfvars.EnablePocketID = true

	isStandardPlus := tier == models.ComputeTierStandard || tier == models.ComputeTierHigh
	tfvars.EnableJellyfin = isStandardPlus
	tfvars.EnableImmich = isStandardPlus
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

// newDefaultTFVars returns TFVars with defaults matching main.tf variable defaults.
func newDefaultTFVars() *TFVars {
	return &TFVars{
		EnableTraefik:     true,
		EnableTinyauth:    true,
		EnablePocketID:    true,
		EnableDokploy:     true,
		EnableDokployApps: true,
		EnableDashboard:   true,
		EnableHomepage:    true,
		EnableWhoami:      true,
	}
}

// GenerateTFVars reads CUE stackfile and generates terraform.tfvars.json.
// This is the CUE-only path used when no StackSpec is available.
func (b *TerraformBridge) GenerateTFVars(outputDir string) error {
	cfg := cueLoadConfig(b.stackkitDir, b.stackkitDir)

	instances := load.Instances([]string{"."}, cfg)
	if len(instances) == 0 {
		return fmt.Errorf("no CUE files found in %s", b.stackkitDir)
	}

	inst := instances[0]
	if inst.Err != nil {
		return fmt.Errorf("failed to load CUE instance: %w", inst.Err)
	}

	value := b.ctx.BuildInstance(inst)
	if err := value.Err(); err != nil {
		return fmt.Errorf("failed to build CUE value: %w", err)
	}

	tfvars, err := b.extractTFVars(value)
	if err != nil {
		return fmt.Errorf("failed to extract terraform vars: %w", err)
	}

	return b.writeTFVars(tfvars, outputDir)
}

// extractTFVars extracts terraform variables from CUE value.
func (b *TerraformBridge) extractTFVars(value cue.Value) (*TFVars, error) {
	tfvars := newDefaultTFVars()

	if network := value.LookupPath(cue.ParsePath("network")); network.Exists() {
		b.extractNetwork(network, tfvars)
	}

	if stack := value.LookupPath(cue.ParsePath("stack")); stack.Exists() {
		b.extractFromStack(stack, tfvars)
	}

	if testStack := value.LookupPath(cue.ParsePath("testStack")); testStack.Exists() {
		b.extractFromStack(testStack, tfvars)
	}

	return tfvars, nil
}

// extractNetwork extracts domain and subnet from a CUE network value.
func (b *TerraformBridge) extractNetwork(network cue.Value, tfvars *TFVars) {
	if domain := network.LookupPath(cue.ParsePath("domain")); domain.Exists() {
		if d, err := domain.String(); err == nil && d != "" {
			tfvars.Domain = d
		}
	}
	if subnet := network.LookupPath(cue.ParsePath("subnet")); subnet.Exists() {
		if s, err := subnet.String(); err == nil && s != "" {
			tfvars.NetworkSubnet = s
		}
	}
}

// extractFromStack extracts configuration from a stack definition.
func (b *TerraformBridge) extractFromStack(stack cue.Value, tfvars *TFVars) {
	if network := stack.LookupPath(cue.ParsePath("network")); network.Exists() {
		b.extractNetwork(network, tfvars)
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

// ValidateBeforeGeneration validates CUE values before Terraform generation
func (b *TerraformBridge) ValidateBeforeGeneration() error {
	validator := NewValidator(b.stackkitDir)
	result, err := validator.ValidateStackKit(b.stackkitDir)
	if err != nil {
		return fmt.Errorf("validation error: %w", err)
	}

	if !result.Valid {
		errMsgs := ""
		for _, e := range result.Errors {
			errMsgs += fmt.Sprintf("\n  - %s: %s", e.Path, e.Message)
		}
		return fmt.Errorf("CUE validation failed:%s", errMsgs)
	}

	return nil
}

// GenerateWithValidation validates CUE and then generates tfvars
func (b *TerraformBridge) GenerateWithValidation(outputDir string) error {
	if err := b.ValidateBeforeGeneration(); err != nil {
		return err
	}
	return b.GenerateTFVars(outputDir)
}

// GenerateFromSpecWithValidation validates CUE schemas then generates tfvars from spec.
func (b *TerraformBridge) GenerateFromSpecWithValidation(spec *models.StackSpec, outputDir string) error {
	if err := b.ValidateBeforeGeneration(); err != nil {
		return err
	}
	return b.GenerateTFVarsFromSpec(spec, outputDir)
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
