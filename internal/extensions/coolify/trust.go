package coolify

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// TrustMode classifies the honest two-tier host trust boundary for the
// optional Layer-3 Coolify 4.7.5+ extensibility module (ADR-0054 §3).
type TrustMode string

const (
	// TrustModeIsolatedExtensionVM places Coolify on a dedicated Extension VM
	// or node with zero SSH access to the Core host. Coolify's internal HTTP
	// Traefik binds strictly to the Extension VM's private Homelab site/mesh IP
	// (mTLS/WireGuard, zero public ports).
	TrustModeIsolatedExtensionVM TrustMode = "isolated-extension-vm"

	// TrustModeSingleNodeTrustedOwner runs Coolify on the single-node Core host
	// (Basement Standalone). Because Coolify holds bridge-IP-pinned root SSH
	// and Docker access on that node, a Coolify admin is host-root-equivalent
	// and node custody is NOT isolated from a Coolify admin.
	TrustModeSingleNodeTrustedOwner TrustMode = "single-node-trusted-owner"
)

// TrustedOwnerDisclosureVersion identifies the mandatory onboarding disclosure
// required before enabling TrustModeSingleNodeTrustedOwner.
const TrustedOwnerDisclosureVersion = "adr-0054-single-node-trusted-owner-v1"

// TrustedOwnerDisclosureText is the exact trust-boundary disclosure surfaced
// during single-node onboarding.
const TrustedOwnerDisclosureText = "Single-node Coolify runs in trusted-owner-only mode and is NOT host-isolated: a Coolify admin holds bridge-IP-pinned root SSH and Docker access and is host-root-equivalent; node custody is not isolated from a Coolify admin."

// DefaultExtensionVMProxyPort is the non-privileged HTTP port used by Coolify's
// internal proxy on an Extension VM's private mesh interface (never 80 or 443).
const DefaultExtensionVMProxyPort = 8088

// TrustBoundaryConfig captures operator intent for the Coolify host trust
// boundary.
type TrustBoundaryConfig struct {
	Mode TrustMode

	// ExtensionVMMeshIP is required for TrustModeIsolatedExtensionVM and must
	// be a private site/mesh IP (RFC 1918, RFC 6598 CGNAT/WireGuard mesh, or
	// IPv6 ULA). Public, loopback, and unspecified addresses are rejected.
	ExtensionVMMeshIP string

	// ExtensionVMProxyPort is the HTTP port on ExtensionVMMeshIP used by
	// Coolify's internal HTTP proxy. Must not be 80 or 443; defaults to
	// DefaultExtensionVMProxyPort when 0.
	ExtensionVMProxyPort int

	// CoreHostSSHAccess reports whether Coolify has SSH access to the Core
	// host. Must be false for TrustModeIsolatedExtensionVM.
	CoreHostSSHAccess bool

	// TrustedOwnerDisclosureAccepted must be true for
	// TrustModeSingleNodeTrustedOwner, confirming explicit operator acceptance
	// of TrustedOwnerDisclosureText.
	TrustedOwnerDisclosureAccepted bool

	// DisclosureVersion optionally records the accepted disclosure version.
	// When supplied for TrustModeSingleNodeTrustedOwner it must match
	// TrustedOwnerDisclosureVersion.
	DisclosureVersion string
}

// TrustPosture summarizes the validated trust boundary for admission and UI
// disclosure.
type TrustPosture struct {
	Mode                 TrustMode
	HostIsolated         bool
	CoolifyAdminHostRoot bool
	CoreHostSSHAllowed   bool
	PrivateListenerAddr  string
	DisclosureRequired   bool
	DisclosureVersion    string
	DisclosureText       string
}

// Validate enforces the two-tier trust boundary invariants fail-closed.
func (c TrustBoundaryConfig) Validate() error {
	_, err := EvaluateTrustBoundary(c)
	return err
}

// EvaluateTrustBoundary validates cfg and returns the resulting TrustPosture.
func EvaluateTrustBoundary(cfg TrustBoundaryConfig) (TrustPosture, error) {
	switch cfg.Mode {
	case TrustModeIsolatedExtensionVM:
		if cfg.CoreHostSSHAccess {
			return TrustPosture{}, errors.New("coolify trust boundary: isolated Extension-VM mode forbids Core-host SSH access")
		}
		addr, err := parsePrivateMeshIP(cfg.ExtensionVMMeshIP)
		if err != nil {
			return TrustPosture{}, err
		}
		port := cfg.ExtensionVMProxyPort
		if port == 0 {
			port = DefaultExtensionVMProxyPort
		}
		if port <= 0 || port > 65535 {
			return TrustPosture{}, fmt.Errorf("coolify trust boundary: invalid Extension-VM proxy port %d", port)
		}
		if port == 80 || port == 443 {
			return TrustPosture{}, fmt.Errorf("coolify trust boundary: Extension-VM proxy port %d is forbidden; Kit-Traefik exclusively owns ports 80 and 443", port)
		}
		return TrustPosture{
			Mode:                 TrustModeIsolatedExtensionVM,
			HostIsolated:         true,
			CoolifyAdminHostRoot: false,
			CoreHostSSHAllowed:   false,
			PrivateListenerAddr:  netip.AddrPortFrom(addr, uint16(port)).String(),
			DisclosureRequired:   false,
		}, nil

	case TrustModeSingleNodeTrustedOwner:
		if !cfg.TrustedOwnerDisclosureAccepted {
			return TrustPosture{}, errors.New("coolify trust boundary: single-node trusted-owner mode requires explicit onboarding disclosure acceptance (Coolify admin is host-root-equivalent and node custody is not isolated)")
		}
		if v := strings.TrimSpace(cfg.DisclosureVersion); v != "" && v != TrustedOwnerDisclosureVersion {
			return TrustPosture{}, fmt.Errorf("coolify trust boundary: disclosure version %q does not match required %q", v, TrustedOwnerDisclosureVersion)
		}
		return TrustPosture{
			Mode:                 TrustModeSingleNodeTrustedOwner,
			HostIsolated:         false,
			CoolifyAdminHostRoot: true,
			CoreHostSSHAllowed:   true,
			PrivateListenerAddr:  "",
			DisclosureRequired:   true,
			DisclosureVersion:    TrustedOwnerDisclosureVersion,
			DisclosureText:       TrustedOwnerDisclosureText,
		}, nil

	default:
		return TrustPosture{}, fmt.Errorf("coolify trust boundary: unknown trust mode %q", cfg.Mode)
	}
}

var cgnatMeshPrefix = netip.MustParsePrefix("100.64.0.0/10")

func parsePrivateMeshIP(raw string) (netip.Addr, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return netip.Addr{}, errors.New("coolify trust boundary: isolated Extension-VM mode requires a private mesh IP")
	}
	addr, err := netip.ParseAddr(trimmed)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("coolify trust boundary: invalid Extension-VM mesh IP %q: %w", raw, err)
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return netip.Addr{}, fmt.Errorf("coolify trust boundary: Extension-VM mesh IP %q must be a routable private site/mesh address", raw)
	}
	if addr.IsPrivate() || cgnatMeshPrefix.Contains(addr) {
		return addr, nil
	}
	return netip.Addr{}, fmt.Errorf("coolify trust boundary: Extension-VM mesh IP %q must not be a public address", raw)
}
