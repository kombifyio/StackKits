package kombifyme

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/netenv"
)

const deviceFingerprintEnv = "KOMBIFY_DEVICE_FINGERPRINT"

// ServiceDef defines a service to register on kombify.me.
type ServiceDef struct {
	Name        string // kombify.me service name (e.g. "dash", "tinyauth")
	Description string
}

// BaseKitServices returns the service definitions for the base-kit based on compute tier.
// Service names match the flat naming used in main.tf locals.domains.
func BaseKitServices(tier string) []ServiceDef {
	// Core services (all tiers)
	services := []ServiceDef{
		{Name: "traefik", Description: "Reverse proxy & TLS"},
		{Name: "tinyauth", Description: "Authentication proxy"},
		{Name: "id", Description: "PocketID identity provider"},
		{Name: "dash", Description: "Dashboard"},
		{Name: "kuma", Description: "Uptime Kuma monitoring"},
		{Name: "whoami", Description: "Whoami test service"},
	}

	// L3 Application use cases — all tiers
	services = append(services, ServiceDef{Name: "vault", Description: "Vaultwarden password manager"})

	// Tier-specific PaaS
	switch tier {
	case "low":
		services = append(services, ServiceDef{Name: "dockge", Description: "Dockge container manager"})
	default: // standard, high
		services = append(services, ServiceDef{Name: "dokploy", Description: "Dokploy PaaS"})
		// L3 Application use cases — standard+ only
		services = append(services,
			ServiceDef{Name: "media", Description: "Jellyfin media server"},
			ServiceDef{Name: "photos", Description: "Immich photo management"},
		)
	}

	return services
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
	services := BaseKitServices(tier)
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

func proxyTargetAddr(publicIP string) string {
	if publicIP == "" {
		return "http://localhost:80"
	}
	return fmt.Sprintf("http://%s:80", publicIP)
}
