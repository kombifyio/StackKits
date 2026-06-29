package kombifyme

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
)

const deviceFingerprintEnv = "KOMBIFY_DEVICE_FINGERPRINT"

// ServiceDef defines a service to register on kombify.me.
type ServiceDef struct {
	Name        string // kombify.me service name (e.g. "base", "auth")
	Description string
	Primary     bool
}

// BaseKitServices returns the service definitions for the basement-kit based on compute tier.
// Deprecated: use ServiceRegistrationsFromCatalog so the registration layer is
// driven by the same canonical catalog as access.json and generated routes.
func BaseKitServices(tier string) []ServiceDef {
	return BaseKitServicesForSpec(&models.StackSpec{
		StackKit: "basement-kit",
		Compute:  models.ComputeSpec{Tier: tier},
	})
}

// BaseKitServicesForSpec returns service definitions for the BaseKit spec's
// generated default state. The platform service follows the same PaaS resolver
// as tfvars generation: omitted PaaS resolves to Coolify.
func BaseKitServicesForSpec(spec *models.StackSpec) []ServiceDef {
	return ServiceRegistrationsFromCatalogForSpec(servicecatalog.Default(), spec)
}

// ServiceRegistrationsFromCatalog converts the canonical service catalog into
// kombify.me service registrations. Primary services are the public URL
// contract; legacy aliases are registered only to keep existing links alive.
func ServiceRegistrationsFromCatalog(catalog []servicecatalog.Service, tier string) []ServiceDef {
	return ServiceRegistrationsFromCatalogForSpec(catalog, &models.StackSpec{
		StackKit: "basement-kit",
		Compute:  models.ComputeSpec{Tier: tier},
	})
}

// ServiceRegistrationsFromCatalogForSpec converts the canonical service catalog
// into kombify.me service registrations for the generated service state.
func ServiceRegistrationsFromCatalogForSpec(catalog []servicecatalog.Service, spec *models.StackSpec) []ServiceDef {
	var services []ServiceDef
	for _, svc := range catalog {
		if !includeServiceForSpec(svc, spec) {
			continue
		}
		services = append(services, ServiceDef{
			Name:        svc.PublicSlug,
			Description: firstNonEmpty(svc.Description, svc.DisplayName),
			Primary:     true,
		})
		for _, alias := range svc.LegacyAliases {
			if !isKombifyLegacyRouteAlias(alias) {
				continue
			}
			services = append(services, ServiceDef{
				Name:        alias,
				Description: firstNonEmpty(svc.Description, svc.DisplayName) + " (legacy alias)",
				Primary:     false,
			})
		}
	}
	return services
}

func includeServiceForSpec(svc servicecatalog.Service, spec *models.StackSpec) bool {
	if spec == nil {
		spec = &models.StackSpec{}
	}
	if enabled, ok := explicitServiceEnable(svc, spec); ok {
		return enabled
	}

	tier := spec.Compute.Tier
	if tier == "" {
		tier = models.ComputeTierStandard
	}

	switch svc.Key {
	case "point":
		return false
	case "traefik":
		paas := spec.ResolvePAASForContext(models.NodeContext(spec.Context))
		return paas == models.PAASDockge || paas == models.PAASKomodo
	case "coolify":
		return spec.ResolvePAASForContext(models.NodeContext(spec.Context)) == models.PAASCoolify
	case "komodo":
		return spec.ResolvePAASForContext(models.NodeContext(spec.Context)) == models.PAASKomodo
	case "dokploy":
		return spec.ResolvePAASForContext(models.NodeContext(spec.Context)) == models.PAASDokploy
	case "dockge":
		return spec.ResolvePAASForContext(models.NodeContext(spec.Context)) == models.PAASDockge
	case "media":
		return false
	case "photos":
		return tier == models.ComputeTierStandard || tier == models.ComputeTierHigh
	default:
		return svc.Default
	}
}

func explicitServiceEnable(svc servicecatalog.Service, spec *models.StackSpec) (bool, bool) {
	if spec == nil || spec.Services == nil {
		return false, false
	}
	for _, key := range []string{
		svc.Key,
		svc.Name,
		svc.ToolName,
		svc.ModuleSlug,
		svc.LocalSlug,
		svc.PublicSlug,
	} {
		if key == "" {
			continue
		}
		if enabled, ok := serviceEnabled(spec.Services[key]); ok {
			return enabled, true
		}
	}
	seenDisabledAlias := false
	for _, key := range svc.LegacyAliases {
		if enabled, ok := serviceEnabled(spec.Services[key]); ok {
			if enabled {
				return true, true
			}
			seenDisabledAlias = true
		}
	}
	if seenDisabledAlias {
		return false, true
	}
	return false, false
}

func serviceEnabled(config any) (bool, bool) {
	svcMap, ok := config.(map[string]any)
	if !ok {
		return false, false
	}
	enabled, ok := svcMap["enabled"].(bool)
	return enabled, ok
}

func isKombifyLegacyRouteAlias(alias string) bool {
	switch alias {
	case "dash", "tinyauth":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// DeviceFingerprint generates a short device fingerprint from hostname and machine-id.
func DeviceFingerprint() string {
	if override := strings.TrimSpace(os.Getenv(deviceFingerprintEnv)); override != "" {
		return normalizeDeviceFingerprint(override)
	}
	hostname, _ := os.Hostname()
	machineID, _ := os.ReadFile("/etc/machine-id")
	if len(machineID) == 0 {
		machineID, _ = os.ReadFile("/var/lib/dbus/machine-id")
	}
	input := hostname + ":" + strings.TrimSpace(string(machineID))
	return shortHash(input)
}

func normalizeDeviceFingerprint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var clean strings.Builder
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			clean.WriteRune(r)
		}
		if clean.Len() >= 6 {
			break
		}
	}
	if clean.Len() == 6 {
		return clean.String()
	}
	return shortHash(value)
}

func shortHash(input string) string {
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash[:3]) // 6 hex chars
}

// RegisterResult holds the result of a full registration flow.
type RegisterResult struct {
	BaseSubdomain *Subdomain
	Services      []*Subdomain
	Prefix        string // The subdomain prefix (e.g. "sh-mylab-abc123")
}

// RegisterAll registers a base subdomain and all service subdomains for a StackKit deployment.
// It returns the subdomain prefix to use in tfvars.
func RegisterAll(apiKey, homelabName, fingerprint, tier string) (*RegisterResult, error) {
	return RegisterAllWithServices(apiKey, homelabName, fingerprint, tier, nil)
}

// RegisterAllWithServices registers the base subdomain, canonical BaseKit
// services, and any additional platform app services.
func RegisterAllWithServices(apiKey, homelabName, fingerprint, tier string, extraServices []ServiceDef) (*RegisterResult, error) {
	return RegisterAllForSpec(apiKey, homelabName, fingerprint, &models.StackSpec{
		StackKit: "basement-kit",
		Compute:  models.ComputeSpec{Tier: tier},
	}, extraServices)
}

// RegisterAllForSpec registers the base subdomain, generated BaseKit services,
// and any additional platform app services.
func RegisterAllForSpec(apiKey, homelabName, fingerprint string, spec *models.StackSpec, extraServices []ServiceDef) (*RegisterResult, error) {
	client := NewClient(apiKey)

	// Detect public IP as a fallback for direct StackKit CLI runs. Orchestrated
	// rollouts pass the actual managed runtime target in the StackSpec metadata
	// and node fields, which must win over the CLI host environment.
	detected := netenv.Detect(context.Background())
	targetAddr := registrationTargetAddr(spec, detected.PublicIP)

	// 1. Register base subdomain
	base, err := client.AutoRegister(homelabName, fingerprint, "StackKit: basement-kit")
	if err != nil {
		return nil, err
	}

	result := &RegisterResult{
		BaseSubdomain: base,
		Prefix:        base.Name,
	}

	// 2. Register service subdomains with real target_addr
	services := MergeServiceRegistrations(BaseKitServicesForSpec(spec), extraServices)
	for _, svc := range services {
		sub, err := client.RegisterService(base.Name, svc.Name, targetAddr, svc.Description)
		if err != nil {
			return nil, fmt.Errorf("register service %s: %w", svc.Name, err)
		}
		result.Services = append(result.Services, sub)
	}

	// 3. Expose all service subdomains
	for _, svc := range result.Services {
		if !svc.Exposed {
			if err := client.ExposeService(base.ID, svc.ID, true); err != nil {
				return nil, fmt.Errorf("expose service %s: %w", svc.Name, err)
			}
		}
	}

	return result, nil
}

func MergeServiceRegistrations(primary, extra []ServiceDef) []ServiceDef {
	merged := make([]ServiceDef, 0, len(primary)+len(extra))
	seen := map[string]bool{}
	appendUnique := func(svc ServiceDef) {
		name := strings.TrimSpace(svc.Name)
		if name == "" || seen[name] {
			return
		}
		svc.Name = name
		seen[name] = true
		merged = append(merged, svc)
	}
	for _, svc := range primary {
		appendUnique(svc)
	}
	for _, svc := range extra {
		appendUnique(svc)
	}
	return merged
}

func proxyTargetAddr(publicIP string) string {
	target := strings.TrimSpace(publicIP)
	if target == "" {
		return "http://localhost:80"
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	if _, _, err := net.SplitHostPort(target); err == nil {
		return "http://" + target
	}
	return fmt.Sprintf("http://%s:80", target)
}

func registrationTargetAddr(spec *models.StackSpec, detectedPublicIP string) string {
	return proxyTargetAddr(registrationTargetHost(spec, detectedPublicIP))
}

func registrationTargetHost(spec *models.StackSpec, detectedPublicIP string) string {
	for _, candidate := range registrationTargetCandidates(spec) {
		if publicOriginHost(candidate) {
			return strings.TrimSpace(candidate)
		}
	}
	return strings.TrimSpace(detectedPublicIP)
}

func registrationTargetCandidates(spec *models.StackSpec) []string {
	if spec == nil {
		return registrationTargetEnvCandidates()
	}
	var out []string
	out = append(out, registrationTargetEnvCandidates()...)
	if spec.Metadata != nil {
		for _, key := range []string{
			"kombify_me_target_addr",
			"target_addr",
			"runtime_public_ip",
			"node_public_ip",
			"public_ip",
			"runtime_ssh_host",
			"ssh_host",
			"host",
		} {
			if value := strings.TrimSpace(spec.Metadata[key]); value != "" {
				out = append(out, value)
			}
		}
	}
	for _, node := range spec.Nodes {
		if value := strings.TrimSpace(node.IP); value != "" {
			out = append(out, value)
		}
		if value := strings.TrimSpace(node.Host); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func registrationTargetEnvCandidates() []string {
	var out []string
	for _, key := range []string{
		"STACKKIT_RUNTIME_TARGET_ADDR",
		"KOMBIFY_RUNTIME_TARGET_ADDR",
		"STACKKIT_RUNTIME_PUBLIC_IP",
		"KOMBIFY_RUNTIME_PUBLIC_IP",
		"STACKKIT_NODE_PUBLIC_IP",
		"KOMBIFY_NODE_PUBLIC_IP",
	} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func publicOriginHost(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		parsed, err := url.Parse(value)
		if err != nil || strings.TrimSpace(parsed.Host) == "" {
			return false
		}
		return publicOriginHost(parsed.Host)
	}
	host := value
	if parsedHost, _, err := net.SplitHostPort(value); err == nil {
		host = parsedHost
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" ||
		strings.HasSuffix(lowerHost, ".localhost") ||
		strings.HasSuffix(lowerHost, ".local") ||
		strings.HasSuffix(lowerHost, ".internal") ||
		strings.HasSuffix(lowerHost, ".test") ||
		strings.HasSuffix(lowerHost, ".invalid") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast()
	}
	return strings.Contains(lowerHost, ".")
}
