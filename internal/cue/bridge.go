// Package cue provides CUE schema validation and Terraform bridge for StackKits.
package cue

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/pkg/models"
	"golang.org/x/crypto/bcrypt"
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

	// Docker network subnet (e.g. "172.20.0.0/16")
	NetworkSubnet string `json:"network_subnet"`
	ServerLANIP   string `json:"server_lan_ip,omitempty"`

	EnableDNSMasq bool   `json:"enable_dnsmasq,omitempty"`
	EnableHTTPS   bool   `json:"enable_https"`
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
	EnableUptimeKuma  bool `json:"enable_uptime_kuma"`
	EnableVaultwarden bool `json:"enable_vaultwarden"`
	EnableJellyfin    bool `json:"enable_jellyfin"`
	EnableImmich      bool `json:"enable_immich"`

	MediaPath string `json:"media_path"`

	// TinyAuth configuration
	AdminEmail             string `json:"admin_email"`
	AdminPasswordPlaintext string `json:"admin_password_plaintext"`
	TinyauthUsers          string `json:"tinyauth_users"`
	TinyauthAppURL         string `json:"tinyauth_app_url"`

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
func (b *TerraformBridge) specToTFVars(spec *models.StackSpec) (*TFVars, error) { //nolint:gocyclo
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
		tfvars.Domain = models.DomainHomeLab
		tfvars.EnableDNSMasq = true
		if len(spec.Nodes) > 0 && spec.Nodes[0].IP != "" {
			tfvars.ServerLANIP = spec.Nodes[0].IP
		}
	}

	tfvars.EnableHTTPS = !isLocalMode && !isKombifyMe
	if tfvars.EnableHTTPS {
		acmeEmail := spec.AdminEmail
		if acmeEmail == "" || acmeEmail == "admin" {
			acmeEmail = "admin@" + tfvars.Domain
		}
		tfvars.AcmeEmail = acmeEmail

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

	tfvars.NetworkName = "base_net"

	if spec.Network.Subnet != "" {
		tfvars.NetworkSubnet = spec.Network.Subnet
	} else {
		tfvars.NetworkSubnet = "172.20.0.0/16"
	}

	tier := spec.Compute.Tier
	if tier == "" {
		tier = models.ComputeTierStandard
	}

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

	tfvars.EnableDashboard = true
	tfvars.EnableUptimeKuma = true
	tfvars.EnableVaultwarden = true

	isStandardPlus := tier == models.ComputeTierStandard || tier == models.ComputeTierHigh
	tfvars.EnableJellyfin = isStandardPlus
	tfvars.EnableImmich = isStandardPlus
	tfvars.MediaPath = "/opt/media"

	adminEmail := spec.AdminEmail
	if adminEmail == "" {
		adminEmail = "admin"
	}
	tfvars.AdminEmail = adminEmail

	adminPassword, err := generateRandomPassword(16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate admin password: %w", err)
	}
	tfvars.AdminPasswordPlaintext = adminPassword

	hash, err := bcryptHash(adminPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to hash admin password: %w", err)
	}

	proto := "http"
	if tfvars.EnableHTTPS {
		proto = "https"
	}
	if spec.SubdomainPrefix != "" {
		tfvars.TinyauthAppURL = fmt.Sprintf("%s://%s-tinyauth.%s", proto, spec.SubdomainPrefix, tfvars.Domain)
	} else {
		tfvars.TinyauthAppURL = fmt.Sprintf("%s://auth.%s", proto, tfvars.Domain)
	}
	tfvars.TinyauthUsers = fmt.Sprintf("%s:%s", adminEmail, hash)

	tfvars.BrandColor = "#F97316"
	if spec.Name != "" {
		tfvars.DashboardTitle = spec.Name
	} else {
		tfvars.DashboardTitle = "My Homelab"
	}

	tfvars.NetworkMode = "bridge"
	tfvars.StorageDriver = models.StorageOverlay2
	if caps != nil {
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

	if spec.Services != nil {
		b.applyServiceEnables(spec.Services, tfvars)
	}

	if dockerHost := os.Getenv("DOCKER_HOST"); dockerHost != "" {
		tfvars.DockerHost = dockerHost
	}

	return tfvars, nil
}

// applyServiceEnables reads enabled/disabled flags from the spec's services map.
func (b *TerraformBridge) applyServiceEnables(services map[string]any, tfvars *TFVars) {
	enables := map[string]*bool{
		"traefik":     &tfvars.EnableTraefik,
		"tinyauth":    &tfvars.EnableTinyauth,
		"pocketid":    &tfvars.EnablePocketID,
		"dokploy":     &tfvars.EnableDokploy,
		"dockge":      &tfvars.EnableDockge,
		"coolify":     &tfvars.EnableCoolify,
		"dashboard":   &tfvars.EnableDashboard,
		"uptime_kuma": &tfvars.EnableUptimeKuma,
		"vaultwarden": &tfvars.EnableVaultwarden,
		"jellyfin":    &tfvars.EnableJellyfin,
		"immich":      &tfvars.EnableImmich,
		"uptime-kuma": &tfvars.EnableUptimeKuma,
	}
	for svcName, ptr := range enables {
		if svcConfig, ok := services[svcName]; ok {
			if svcMap, ok := svcConfig.(map[string]any); ok {
				if enabled, ok := svcMap["enabled"]; ok {
					if v, ok := enabled.(bool); ok {
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
		EnableDashboard:   false,
	}
}

// GenerateTFVars reads CUE stackfile and generates terraform.tfvars.json.
// This is the CUE-only path used when no StackSpec is available.
func (b *TerraformBridge) GenerateTFVars(outputDir string) error {
	cfg := &load.Config{
		Dir: b.stackkitDir,
	}

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

func generateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", fmt.Errorf("generate random password: %w", err)
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}

func bcryptHash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt hash: %w", err)
	}
	return string(hash), nil
}

func loadDockerCapabilities() *models.DockerCapabilities {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".stackkits", "capabilities.json"))
	if err != nil {
		return nil
	}
	var caps models.DockerCapabilities
	if err := json.Unmarshal(data, &caps); err != nil {
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
