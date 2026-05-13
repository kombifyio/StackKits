package kombifyme

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
)

const deviceFingerprintEnv = "KOMBIFY_DEVICE_FINGERPRINT"

// ServiceDef defines a service to register on kombify.me.
type ServiceDef struct {
	Name        string // kombify.me service name (e.g. "base", "auth")
	Description string
	Primary     bool
}

// BaseKitServices returns the service definitions for the base-kit based on compute tier.
// Deprecated: use ServiceRegistrationsFromCatalog so the registration layer is
// driven by the same canonical catalog as access.json and generated routes.
func BaseKitServices(tier string) []ServiceDef {
	return ServiceRegistrationsFromCatalog(servicecatalog.Default(), tier)
}

// ServiceRegistrationsFromCatalog converts the canonical service catalog into
// kombify.me service registrations. Primary services are the public URL
// contract; legacy aliases are registered only to keep existing links alive.
func ServiceRegistrationsFromCatalog(catalog []servicecatalog.Service, tier string) []ServiceDef {
	var services []ServiceDef
	for _, svc := range catalog {
		if !includeServiceForTier(svc, tier) {
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

func includeServiceForTier(svc servicecatalog.Service, tier string) bool {
	switch svc.Key {
	case "point", "coolify":
		return false
	case "dockge":
		return tier == "low"
	case "dokploy", "media", "photos":
		return tier != "low"
	default:
		return svc.Default
	}
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
	client := NewClient(apiKey)

	// Detect public IP for target_addr.
	// kombify.me terminates public HTTPS at Cloudflare and proxies to the
	// StackKit origin over port 80. Step-CA still runs on the origin as the
	// local control-plane CA, but the public worker cannot trust a private CA
	// certificate for an IP-derived origin alias.
	detected := netenv.Detect(context.Background())
	targetAddr := proxyTargetAddr(detected.PublicIP)

	// 1. Register base subdomain
	base, err := client.AutoRegister(homelabName, fingerprint, "StackKit: base-kit")
	if err != nil {
		return nil, err
	}

	result := &RegisterResult{
		BaseSubdomain: base,
		Prefix:        base.Name,
	}

	// 2. Register service subdomains with real target_addr
	services := MergeServiceRegistrations(BaseKitServices(tier), extraServices)
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
	if publicIP == "" {
		return "http://localhost:80"
	}
	return fmt.Sprintf("http://%s:80", publicIP)
}
