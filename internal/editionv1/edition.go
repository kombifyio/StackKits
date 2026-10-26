// Package edition resolves the KOMBIFY_EDITION boot-config and exposes the
// provider/auth/billing wiring decisions it implies (ENTITLEMENTS-ARCHITECTURE.md §14,
// ENTITLEMENTS-ARCHITECTURE §7). It is dependency-free so every repo — SaaS and
// self-host — can import it.
//
// Invariant (PRODUCT-SEGMENTATION): selfhost-oss has NO runtime gating — flags
// come from the build-time release manifest (static provider), and there are no
// Auth0/Stripe/FGA calls. The helpers below encode that invariant so call sites
// cannot accidentally violate it.
package edition

import (
	"os"
	"strings"
)

// Edition is the deployment mode.
type Edition string

const (
	SaaSStandalone Edition = "saas-standalone"
	SaaSEmbedded   Edition = "saas-embedded"
	SelfhostOSS    Edition = "selfhost-oss"
	Preview        Edition = "preview"
)

// EnvVar is the boot-config environment variable name.
const EnvVar = "KOMBIFY_EDITION"

// Resolve reads KOMBIFY_EDITION from the environment. An unset or unrecognized
// value normalises to SelfhostOSS — the fail-safe that disables gating rather
// than risking an accidental lockout. SaaS deployments must set it explicitly;
// use ResolveStrict to enforce that.
func Resolve() Edition {
	return Normalize(os.Getenv(EnvVar))
}

// ResolveStrict is like Resolve but reports whether the value was explicitly and
// validly set. Use in SaaS bootstraps to fail fast on misconfiguration.
func ResolveStrict() (Edition, bool) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(EnvVar)))
	switch Edition(raw) {
	case SaaSStandalone, SaaSEmbedded, SelfhostOSS, Preview:
		return Edition(raw), true
	default:
		return SelfhostOSS, false
	}
}

// Normalize maps a raw string to a known Edition, defaulting to SelfhostOSS.
func Normalize(raw string) Edition {
	switch Edition(strings.ToLower(strings.TrimSpace(raw))) {
	case SaaSStandalone:
		return SaaSStandalone
	case SaaSEmbedded:
		return SaaSEmbedded
	case Preview:
		return Preview
	case SelfhostOSS:
		return SelfhostOSS
	default:
		return SelfhostOSS
	}
}

// IsSaaS reports whether this is a hosted SaaS edition.
func (e Edition) IsSaaS() bool { return e == SaaSStandalone || e == SaaSEmbedded }

// GatingEnabled reports whether runtime feature-gating applies. False for
// selfhost-oss (all features on).
func (e Edition) GatingEnabled() bool { return e != SelfhostOSS }

// FlagProvider returns the OpenFeature provider name this edition selects:
// "static" (selfhost-oss), "flagship-preview" (preview), or "flagship" (SaaS).
func (e Edition) FlagProvider() string {
	switch e {
	case SelfhostOSS:
		return "static"
	case Preview:
		return "flagship-preview"
	default:
		return "flagship"
	}
}

// UsesAuth0 reports whether Auth0 identity is wired.
func (e Edition) UsesAuth0() bool { return e != SelfhostOSS }

// UsesStripe reports whether Stripe billing/entitlements are wired.
func (e Edition) UsesStripe() bool { return e != SelfhostOSS }

// UsesFGA reports whether FGA authorization is enforced.
func (e Edition) UsesFGA() bool { return e != SelfhostOSS }
