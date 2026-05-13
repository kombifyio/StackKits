// Package netenv provides network environment detection and NodeContext resolution.
package netenv

import (
	"os"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/pkg/models"
)

const localDomainEnv = "STACKKIT_LOCAL_DOMAIN"

// ContextInput holds the inputs used for NodeContext resolution.
type ContextInput struct {
	// NetworkEnv is the detected network environment (home/vps/cloud).
	NetworkEnv models.NetworkEnvironment
	// Arch is the CPU architecture (from runtime.GOARCH or detected on target).
	Arch string
	// CPUCores is the number of CPU cores (0 if unknown).
	CPUCores int
	// MemoryGB is the total system memory in GB (0 if unknown).
	MemoryGB float64
}

// ResolveNodeContext determines the NodeContext from network environment and hardware.
//
// Mapping:
//   - NetEnvCloud or NetEnvVPS on x86_64 → "cloud"
//   - NetEnvHome on x86_64 → "local"
//   - ARM64 + low resources (< 4 cores or < 4 GB) → "pi"
//   - ARM64 + adequate resources → uses network env (local or cloud)
//   - Unknown → "local" (safe default)
func ResolveNodeContext(input ContextInput) models.NodeContext {
	isARM := input.Arch == "arm64" || input.Arch == "aarch64" || input.Arch == "arm"
	isLowResource := (input.CPUCores > 0 && input.CPUCores < 4) ||
		(input.MemoryGB > 0 && input.MemoryGB < 4)

	// ARM64 with low resources → pi context
	if isARM && isLowResource {
		return models.ContextPi
	}

	switch input.NetworkEnv {
	case models.NetEnvCloud, models.NetEnvVPS:
		return models.ContextCloud
	case models.NetEnvHome:
		return models.ContextLocal
	default:
		// Unknown network — fall back to local (safe default)
		return models.ContextLocal
	}
}

// ResolveFromResult builds a ContextInput from a netenv.Result and optional hardware info,
// then resolves the NodeContext.
func ResolveFromResult(result *Result, cpuCores int, memoryGB float64) models.NodeContext {
	arch := runtime.GOARCH
	return ResolveNodeContext(ContextInput{
		NetworkEnv: result.Environment,
		Arch:       arch,
		CPUCores:   cpuCores,
		MemoryGB:   memoryGB,
	})
}

// FormatNodeContext returns a human-readable description of a NodeContext.
func FormatNodeContext(ctx models.NodeContext) string {
	switch ctx {
	case models.ContextLocal:
		return "local (home/office server)"
	case models.ContextCloud:
		return "cloud (VPS/dedicated/managed)"
	case models.ContextPi:
		return "pi (ARM64 low-resource device)"
	default:
		return "unknown"
	}
}

// NodeContextIsCloud returns true if the context represents a public-facing server.
func NodeContextIsCloud(ctx models.NodeContext) bool {
	return ctx == models.ContextCloud
}

// DefaultLocalDomain returns the local-only domain suffix used when no explicit
// domain was provided. STACKKIT_LOCAL_DOMAIN lets local test runs switch between
// DNS-backed names like stack.home and browser-native names like home.localhost.
func DefaultLocalDomain() string {
	domain := strings.ToLower(strings.Trim(strings.TrimSpace(os.Getenv(localDomainEnv)), "."))
	if domain == "" {
		return models.DomainHomeLab
	}
	if models.IsLocalDomain(domain) && !models.IsKombifyMeDomain(domain) {
		return domain
	}
	return models.DomainHomeLab
}

// SuggestDomainForContext returns the recommended domain for a NodeContext.
// This replaces the NetworkEnvironment-based SuggestDomain for external callers.
func SuggestDomainForContext(ctx models.NodeContext, currentDomain string) (domain string, reason string) {
	switch ctx {
	case models.ContextCloud:
		// Cloud/VPS with local domains → correct to kombify.me
		if models.IsLocalDomain(currentDomain) {
			return models.DomainKombifyMe, "running on a public server — local domain '" + currentDomain + "' won't be reachable from outside"
		}
		if currentDomain == "" {
			return models.DomainKombifyMe, "running on a public server — using kombify.me for public access"
		}
		return currentDomain, ""
	case models.ContextLocal, models.ContextPi:
		if currentDomain == "" {
			return DefaultLocalDomain(), "local/home network detected — using configured local domain"
		}
		return currentDomain, ""
	default:
		return currentDomain, ""
	}
}

// isLocalDomain returns true if the domain is a local/non-routable domain.
func isLocalDomain(d string) bool {
	return models.IsLocalDomain(d)
}
