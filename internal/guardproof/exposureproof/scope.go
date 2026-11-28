// Package exposureproof is the observation side of Guard J: it compares the
// access policy an owner declared for known owned addresses with bounded IPv4
// and IPv6 direct-origin reachability observations, and reports structured
// exposure findings. It answers one question per probed port: is this port
// reachable directly from outside although the declared policy says traffic
// reaches the workload only through the intended Connect or relay route?
//
// Guard observes; the route or firewall owner corrects. Nothing here changes a
// firewall, a route or a listener.
//
// The rules the package keeps:
//
//   - The scope is exact and closed. Only the owned single addresses and the
//     explicit TCP ports a Scope names are ever probed; there is no prefix,
//     range or name expansion, and a TCPProber built from a Scope refuses any
//     other address or port. An address prefix is recorded for freshness only
//     and is never enumerated.
//   - Absence of evidence is unknown, never contained. A missing, stale or
//     future-dated observation, a stale address binding, a vantage that is not
//     outside the owner's network or cannot speak the address family, and a
//     probe that errored all yield an unknown entry with a stable reason.
//   - A timeout proves containment only when the same vantage demonstrably
//     reached something over the same address family in the same run.
//   - Every vantage is judged on its own and reach wins: a port that any fresh
//     external vantage reached is exposed (or intended), whatever another
//     vantage observed later.
//   - A report is compliant only when every probed port is contained or
//     intended and every intended route journey was observed to work.
package exposureproof

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/accessgatetable"
)

// Schema identifies the exposure proof report document.
const Schema = "stackkit.exposure-proof/v1"

// Bounds of one scope. They keep a scope a short owner-authored list and make
// a probe run bounded work.
const (
	MaxTargets        = 32
	MaxPortsPerTarget = 16
	maxRefBytes       = 128
)

// Family is the IP address family of a probed address.
type Family string

const (
	FamilyIPv4 Family = "ipv4"
	FamilyIPv6 Family = "ipv6"
)

// FamilyOf returns the family of an address.
func FamilyOf(address netip.Addr) Family {
	if address.Is4() {
		return FamilyIPv4
	}
	return FamilyIPv6
}

// DirectReach is what the declared policy says about reaching a port directly,
// without the intended Connect or relay route.
type DirectReach string

const (
	// DirectForbidden is the default: the port must not answer from outside.
	DirectForbidden DirectReach = "forbidden"
	// DirectAllowed declares the port deliberately public. It is only valid for
	// an address whose exposure class is public.
	DirectAllowed DirectReach = "allowed"
)

// PortPolicy is the declared policy of one TCP port of an owned address.
type PortPolicy struct {
	Port   int         `json:"port"`
	Direct DirectReach `json:"direct"`
}

// AddressSource says where the owner binding of an address came from.
type AddressSource string

const (
	SourcePlan          AddressSource = "plan"
	SourceHostInventory AddressSource = "host-inventory"
	SourceDNS           AddressSource = "dns"
)

// Target is one known owned address with the access policy declared for it.
type Target struct {
	// Ref is the canonical reference of the node or service the address
	// belongs to. A node reachable over IPv4 and IPv6 has one Target per
	// address under the same Ref.
	Ref     string     `json:"ref"`
	Address netip.Addr `json:"address"`
	// Prefix is the prefix the address was delegated from, kept so a report
	// shows how fresh the prefix binding is. It is never enumerated.
	Prefix netip.Prefix  `json:"prefix,omitzero"`
	Source AddressSource `json:"source"`
	// ValidatedAt is when the owner binding of the address (and prefix) was
	// last confirmed against the Source.
	ValidatedAt time.Time `json:"validatedAt"`
	// Exposure is the #AccessPolicyV2 exposure class the policy declares.
	Exposure string `json:"exposure"`
	// OriginOfRoute names the Connect or relay route this address is the
	// origin behind. When set, direct reach of a forbidden port is an origin
	// bypass of that route. Empty means the address serves no intended route.
	OriginOfRoute string       `json:"originOfRoute,omitempty"`
	Ports         []PortPolicy `json:"ports"`
}

// Scope is the exact set of owned addresses and ports one proof covers.
type Scope struct {
	Targets []Target `json:"targets"`
}

// ErrInvalidScope rejects a scope that is not exact, owned and bounded.
var ErrInvalidScope = errors.New("exposureproof: invalid scope")

func invalidScope(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidScope, fmt.Sprintf(format, args...))
}

// Validate checks that the scope is a closed list of single owned addresses
// with explicit ports and an unambiguous declared policy.
func (s Scope) Validate() error {
	if len(s.Targets) == 0 {
		return invalidScope("no targets")
	}
	if len(s.Targets) > MaxTargets {
		return invalidScope("%d targets exceed the bound of %d", len(s.Targets), MaxTargets)
	}
	seen := map[netip.Addr]bool{}
	for index, target := range s.Targets {
		if err := target.validate(); err != nil {
			return invalidScope("target[%d]: %v", index, err)
		}
		if seen[target.Address] {
			return invalidScope("target[%d]: address %s listed twice", index, target.Address)
		}
		seen[target.Address] = true
	}
	return nil
}

func (t Target) validate() error {
	if !canonicalRef(t.Ref) {
		return errors.New("ref must be a canonical non-empty reference")
	}
	address := t.Address
	switch {
	case !address.IsValid():
		return errors.New("address is invalid")
	case address.Zone() != "":
		return errors.New("address must not carry a zone")
	case address.Is4In6():
		return errors.New("address must be a plain IPv4 or IPv6 address, not IPv4-mapped IPv6")
	case address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast():
		return errors.New("address is not a unicast owned address")
	}
	if t.Prefix.IsValid() && !t.Prefix.Contains(address) {
		return errors.New("prefix does not contain the address")
	}
	switch t.Source {
	case SourcePlan, SourceHostInventory, SourceDNS:
	default:
		return fmt.Errorf("address source %q is unsupported", t.Source)
	}
	if t.ValidatedAt.IsZero() || t.ValidatedAt.Location() != time.UTC {
		return errors.New("validatedAt must be an exact UTC instant")
	}
	switch t.Exposure {
	case accessgatetable.ExposureLocalOnly, accessgatetable.ExposureRemotePrivate, accessgatetable.ExposurePublic:
	default:
		return fmt.Errorf("exposure %q is unsupported", t.Exposure)
	}
	if t.OriginOfRoute != "" && !canonicalRef(t.OriginOfRoute) {
		return errors.New("originOfRoute must be a canonical reference")
	}
	if len(t.Ports) == 0 || len(t.Ports) > MaxPortsPerTarget {
		return fmt.Errorf("a target declares 1 to %d explicit ports", MaxPortsPerTarget)
	}
	ports := map[int]bool{}
	for _, policy := range t.Ports {
		if policy.Port < 1 || policy.Port > 65535 {
			return fmt.Errorf("port %d is out of range", policy.Port)
		}
		if ports[policy.Port] {
			return fmt.Errorf("port %d listed twice", policy.Port)
		}
		ports[policy.Port] = true
		switch policy.Direct {
		case DirectForbidden:
		case DirectAllowed:
			if t.Exposure != accessgatetable.ExposurePublic {
				return fmt.Errorf("port %d allows direct reach but exposure is %q", policy.Port, t.Exposure)
			}
		default:
			return fmt.Errorf("port %d has unsupported direct policy %q", policy.Port, policy.Direct)
		}
	}
	return nil
}

func canonicalRef(value string) bool {
	return value != "" && len(value) <= maxRefBytes && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, " \t\r\n|")
}

// Digest is the stable identity of the exact target and port scope with its
// declared policy, so a report states precisely what was and was not covered.
func (s Scope) Digest() string {
	lines := make([]string, 0, len(s.Targets))
	for _, target := range s.Targets {
		ports := append([]PortPolicy(nil), target.Ports...)
		sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
		var b strings.Builder
		fmt.Fprintf(&b, "%s|%s|%s|%s", target.Ref, target.Address, target.Exposure, target.OriginOfRoute)
		for _, port := range ports {
			fmt.Fprintf(&b, "|%d:%s", port.Port, port.Direct)
		}
		lines = append(lines, b.String())
	}
	sort.Strings(lines)
	return digestLines(lines)
}

// publiclyRoutable reports whether an address can be reached from the public
// internet at all: not private, unique-local or carrier-grade NAT space.
func publiclyRoutable(address netip.Addr) bool {
	if !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	return !cgnat.Contains(address)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")
