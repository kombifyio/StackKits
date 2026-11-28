package stackspecintent

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// AddressZoneKind identifies one of the three orthogonal address zones defined
// by ADR-0055 §2.
type AddressZoneKind string

const (
	// AddressZoneCustomDomain serves an operator-owned public domain
	// (example.com) with ACME DNS-01/HTTP-01 directly on Kit-Traefik and zero
	// kombify.me or Kombify account dependency.
	AddressZoneCustomDomain AddressZoneKind = "custom-domain"
	// AddressZoneKombifyMe serves a managed kombify.me installation zone
	// ({base}.kombify.me) or flat host prefix ({base}-{app}.kombify.me) with
	// zero own domain required and PSL-gated cookie scoping (__Host- per-host
	// default until PSL is active).
	AddressZoneKombifyMe AddressZoneKind = "kombify-me"
	// AddressZoneLocalOnly serves the local-only lab.home zone with Owner CA
	// and local resolver, requiring zero internet, cloud, or account dependency.
	AddressZoneLocalOnly AddressZoneKind = "local-only"
)

// CookieScopeMode describes how session cookies are scoped within an address
// zone.
type CookieScopeMode string

const (
	// CookieScopeHostOnly enforces __Host- per-host cookies (default on
	// kombify.me until the Public Suffix List entry is active).
	CookieScopeHostOnly CookieScopeMode = "host-only"
	// CookieScopeZoneParent scopes cookies to the installation-owned zone
	// parent (custom-domain, local-only lab.home, or kombify.me once PSL is
	// active).
	CookieScopeZoneParent CookieScopeMode = "zone-parent"
)

// TLSTrustMode identifies the certificate authority chain serving a zone.
type TLSTrustMode string

const (
	TLSTrustACME    TLSTrustMode = "acme-public"
	TLSTrustOwnerCA TLSTrustMode = "owner-ca"
)

const (
	LocalOnlyCanonicalZone = "lab.home"
	KombifyMeParentDomain  = "kombify.me"
)

var (
	// ErrSilentDowngradeRefused is returned when a zone transition or fallback
	// attempts to weaken TLS, Access Gate, PocketID, privacy, or password
	// posture.
	ErrSilentDowngradeRefused = errors.New("silent security posture downgrade refused")
	// ErrEndpointUnavailableNoDowngrade is returned when a connected endpoint
	// is unavailable and a caller asks for a fallback; StackKits fails closed
	// rather than silently weakening TLS, auth, privacy, or domain ownership.
	ErrEndpointUnavailableNoDowngrade = errors.New("connected endpoint unavailable: refusing silent downgrade or domain fallback")

	zoneLabelPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	prefixOnlyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// AddressZoneOptions are the explicit authoring inputs for resolving a 3-zone
// address contract.
type AddressZoneOptions struct {
	ZoneKind        AddressZoneKind
	Domain          string
	SubdomainPrefix string
	PSLActive       bool
}

// AddressZoneContract is the validated, zone-agnostic address contract for one
// StackKit installation (ADR-0055 §2).
type AddressZoneContract struct {
	Kind                    AddressZoneKind `json:"kind"`
	Zone                    string          `json:"zone"`
	SubdomainPrefix         string          `json:"subdomainPrefix,omitempty"`
	FlatHostLayout          bool            `json:"flatHostLayout,omitempty"`
	IdentityHost            string          `json:"identityHost"`
	AccessGateHost          string          `json:"accessGateHost"`
	TLSMode                 TLSTrustMode    `json:"tlsMode"`
	ACMEChallenges          []string        `json:"acmeChallenges,omitempty"`
	OwnerCARequired         bool            `json:"ownerCaRequired"`
	LocalResolverRequired   bool            `json:"localResolverRequired"`
	PSLActive               bool            `json:"pslActive"`
	CookieMode              CookieScopeMode `json:"cookieMode"`
	CookiePrefix            string          `json:"cookiePrefix"`
	RequiresOwnDomain       bool            `json:"requiresOwnDomain"`
	RequiresKombifyMe       bool            `json:"requiresKombifyMe"`
	RequiresKombifyAccount  bool            `json:"requiresKombifyAccount"`
	RequiresInternet        bool            `json:"requiresInternet"`
	RequiresCloudDependency bool            `json:"requiresCloudDependency"`
}

// ParseAddressZoneKind validates an explicit CLI/API address-zone selector.
func ParseAddressZoneKind(raw string) (AddressZoneKind, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case string(AddressZoneCustomDomain), "custom", "own-domain":
		return AddressZoneCustomDomain, nil
	case string(AddressZoneKombifyMe), "kombify.me":
		return AddressZoneKombifyMe, nil
	case string(AddressZoneLocalOnly), "local":
		return AddressZoneLocalOnly, nil
	default:
		return "", fmt.Errorf("invalid address zone %q: expected %q, %q, or %q", raw, AddressZoneCustomDomain, AddressZoneKombifyMe, AddressZoneLocalOnly)
	}
}

// ServiceHost returns the canonical host for one application or core service
// under this address zone.
func (c AddressZoneContract) ServiceHost(service string) string {
	service = strings.ToLower(strings.TrimSpace(service))
	if service == "" {
		return c.Zone
	}
	if c.FlatHostLayout && c.SubdomainPrefix != "" {
		return c.SubdomainPrefix + "-" + service + "." + c.Zone
	}
	return service + "." + c.Zone
}

// Validate enforces all ADR-0055 §2 invariants for the address zone contract,
// including zero kombify.me leakage on custom-domain and local-only zones and
// PSL-gated cookie scoping on kombify.me.
func (c AddressZoneContract) Validate() error {
	zone := normalizeDomain(c.Zone)
	if zone == "" {
		return errors.New("address zone domain is required")
	}
	if !validDNSZone(zone) {
		return fmt.Errorf("address zone %q is not a valid DNS domain", c.Zone)
	}
	switch c.Kind {
	case AddressZoneCustomDomain:
		if isKombifyMeName(zone) || strings.Contains(strings.ToLower(c.IdentityHost), KombifyMeParentDomain) || strings.Contains(strings.ToLower(c.AccessGateHost), KombifyMeParentDomain) {
			return fmt.Errorf("custom-domain address zone %q must have zero kombify.me dependency", zone)
		}
		if zone == LocalOnlyCanonicalZone {
			return fmt.Errorf("custom-domain address zone cannot use local-only domain %q", LocalOnlyCanonicalZone)
		}
		if _, source := resolvedplan.IdentityOriginOf(zone); source == "reserved-name" {
			return fmt.Errorf("custom-domain address zone %q cannot use a reserved local name", zone)
		}
		if c.SubdomainPrefix != "" || c.FlatHostLayout {
			return errors.New("custom-domain address zone must use <service>.<zone> routing without flat kombify.me prefix")
		}
		if c.RequiresKombifyMe || c.RequiresKombifyAccount || c.RequiresCloudDependency {
			return errors.New("custom-domain address zone must have zero kombify.me, account, or cloud dependency")
		}
		if !c.RequiresOwnDomain || c.TLSMode != TLSTrustACME || c.OwnerCARequired {
			return errors.New("custom-domain address zone requires own domain with ACME directly on Kit-Traefik")
		}
		if c.IdentityHost != "id."+zone || c.AccessGateHost != "auth."+zone {
			return fmt.Errorf("custom-domain address zone hosts must be id.%s and auth.%s", zone, zone)
		}
		return nil

	case AddressZoneKombifyMe:
		if !isKombifyMeName(zone) {
			return fmt.Errorf("kombify-me address zone %q must be %s or a child zone of %s", zone, KombifyMeParentDomain, KombifyMeParentDomain)
		}
		if c.RequiresOwnDomain {
			return errors.New("kombify-me address zone must not require an own domain")
		}
		if !c.RequiresKombifyMe || c.TLSMode != TLSTrustACME || c.OwnerCARequired {
			return errors.New("kombify-me address zone requires ACME TLS and kombify-me zone binding")
		}
		if c.FlatHostLayout {
			if zone != KombifyMeParentDomain || !prefixOnlyPattern.MatchString(c.SubdomainPrefix) {
				return errors.New("flat kombify.me layout requires base domain kombify.me and a valid subdomain prefix")
			}
			if c.PSLActive || c.CookieMode != CookieScopeHostOnly || c.CookiePrefix != "__Host-" {
				return errors.New("flat kombify.me layout must enforce __Host- per-host cookie isolation")
			}
		} else {
			if zone == KombifyMeParentDomain {
				return errors.New("kombify.me install zone requires either {base}.kombify.me or flat {base}-{app}.kombify.me prefix")
			}
			if c.SubdomainPrefix != "" {
				return errors.New("install-zone {base}.kombify.me layout cannot also set a flat subdomain prefix")
			}
			if !c.PSLActive && (c.CookieMode != CookieScopeHostOnly || c.CookiePrefix != "__Host-") {
				return errors.New("kombify.me zone must default to __Host- per-host cookies until PSL is active")
			}
			if c.PSLActive && c.CookieMode != CookieScopeZoneParent {
				return errors.New("kombify.me zone with active PSL must use zone-parent cookie mode")
			}
		}
		if c.IdentityHost != c.ServiceHost("id") || c.AccessGateHost != c.ServiceHost("auth") {
			return errors.New("kombify-me identity and access gate hosts do not match the zone layout")
		}
		return nil

	case AddressZoneLocalOnly:
		if zone != LocalOnlyCanonicalZone {
			return fmt.Errorf("local-only address zone must be %q, got %q", LocalOnlyCanonicalZone, zone)
		}
		if isKombifyMeName(zone) || strings.Contains(strings.ToLower(c.IdentityHost), KombifyMeParentDomain) || strings.Contains(strings.ToLower(c.AccessGateHost), KombifyMeParentDomain) {
			return errors.New("local-only address zone must have zero kombify.me dependency")
		}
		if c.SubdomainPrefix != "" || c.FlatHostLayout {
			return errors.New("local-only address zone cannot use a flat subdomain prefix")
		}
		if c.RequiresInternet || c.RequiresCloudDependency || c.RequiresKombifyAccount || c.RequiresKombifyMe || c.RequiresOwnDomain {
			return errors.New("local-only address zone must have zero internet, cloud, or account dependency")
		}
		if c.TLSMode != TLSTrustOwnerCA || !c.OwnerCARequired || !c.LocalResolverRequired || len(c.ACMEChallenges) > 0 {
			return errors.New("local-only address zone requires Owner CA and local resolver without public ACME")
		}
		if c.IdentityHost != "id."+LocalOnlyCanonicalZone || c.AccessGateHost != "auth."+LocalOnlyCanonicalZone {
			return errors.New("local-only address zone hosts must be id.lab.home and auth.lab.home")
		}
		return nil

	default:
		return fmt.Errorf("unknown address zone kind %q", c.Kind)
	}
}

// ResolveAddressZone constructs and validates an AddressZoneContract from
// explicit or inferred options.
func ResolveAddressZone(opts AddressZoneOptions) (AddressZoneContract, error) {
	kind, err := ParseAddressZoneKind(string(opts.ZoneKind))
	if err != nil {
		return AddressZoneContract{}, err
	}
	domain := normalizeDomain(opts.Domain)
	prefix := strings.ToLower(strings.TrimSpace(opts.SubdomainPrefix))

	if kind == "" {
		switch {
		case domain == "" || domain == LocalOnlyCanonicalZone:
			kind = AddressZoneLocalOnly
			domain = LocalOnlyCanonicalZone
		case isKombifyMeName(domain):
			kind = AddressZoneKombifyMe
		default:
			if _, source := resolvedplan.IdentityOriginOf(domain); source == "reserved-name" {
				return AddressZoneContract{}, fmt.Errorf("%s cannot be an address zone: reserved names never carry publicly trusted certificates; use a custom domain, a kombify.me zone, or %s for local-only", domain, LocalOnlyCanonicalZone)
			}
			kind = AddressZoneCustomDomain
		}
	}

	switch kind {
	case AddressZoneLocalOnly:
		if domain == "" {
			domain = LocalOnlyCanonicalZone
		}
		if domain != LocalOnlyCanonicalZone {
			return AddressZoneContract{}, fmt.Errorf("address zone %q requires domain %q, got %q", AddressZoneLocalOnly, LocalOnlyCanonicalZone, domain)
		}
		contract := AddressZoneContract{
			Kind:                    AddressZoneLocalOnly,
			Zone:                    LocalOnlyCanonicalZone,
			IdentityHost:            "id." + LocalOnlyCanonicalZone,
			AccessGateHost:          "auth." + LocalOnlyCanonicalZone,
			TLSMode:                 TLSTrustOwnerCA,
			OwnerCARequired:         true,
			LocalResolverRequired:   true,
			PSLActive:               false,
			CookieMode:              CookieScopeZoneParent,
			CookiePrefix:            "__Secure-",
			RequiresOwnDomain:       false,
			RequiresKombifyMe:       false,
			RequiresKombifyAccount:  false,
			RequiresInternet:        false,
			RequiresCloudDependency: false,
		}
		return contract, contract.Validate()

	case AddressZoneCustomDomain:
		if domain == "" {
			return AddressZoneContract{}, fmt.Errorf("address zone %q requires an explicit --domain (e.g. example.com)", AddressZoneCustomDomain)
		}
		if isKombifyMeName(domain) {
			return AddressZoneContract{}, fmt.Errorf("address zone %q refuses kombify.me domain %q; use --address-zone=%s for kombify.me", AddressZoneCustomDomain, domain, AddressZoneKombifyMe)
		}
		if domain == LocalOnlyCanonicalZone {
			return AddressZoneContract{}, fmt.Errorf("address zone %q refuses local-only domain %q; use --address-zone=%s", AddressZoneCustomDomain, domain, AddressZoneLocalOnly)
		}
		contract := AddressZoneContract{
			Kind:                    AddressZoneCustomDomain,
			Zone:                    domain,
			IdentityHost:            "id." + domain,
			AccessGateHost:          "auth." + domain,
			TLSMode:                 TLSTrustACME,
			ACMEChallenges:          []string{"dns-01", "http-01"},
			OwnerCARequired:         false,
			LocalResolverRequired:   false,
			PSLActive:               false,
			CookieMode:              CookieScopeZoneParent,
			CookiePrefix:            "__Secure-",
			RequiresOwnDomain:       true,
			RequiresKombifyMe:       false,
			RequiresKombifyAccount:  false,
			RequiresInternet:        true,
			RequiresCloudDependency: false,
		}
		return contract, contract.Validate()

	case AddressZoneKombifyMe:
		if domain == "" {
			return AddressZoneContract{}, fmt.Errorf("address zone %q requires a {base}.kombify.me zone or kombify.me with a flat subdomain prefix", AddressZoneKombifyMe)
		}
		if !isKombifyMeName(domain) {
			return AddressZoneContract{}, fmt.Errorf("address zone %q requires a kombify.me domain, got %q", AddressZoneKombifyMe, domain)
		}
		flat := domain == KombifyMeParentDomain && prefix != ""
		if domain == KombifyMeParentDomain && prefix == "" {
			return AddressZoneContract{}, fmt.Errorf("bare %q is a shared parent; specify an allocated {base}.kombify.me zone or a flat subdomain prefix", KombifyMeParentDomain)
		}
		cookieMode := CookieScopeHostOnly
		cookiePrefix := "__Host-"
		pslActive := opts.PSLActive && !flat
		if pslActive {
			cookieMode = CookieScopeZoneParent
			cookiePrefix = "__Secure-"
		}
		contract := AddressZoneContract{
			Kind:                    AddressZoneKombifyMe,
			Zone:                    domain,
			SubdomainPrefix:         prefix,
			FlatHostLayout:          flat,
			TLSMode:                 TLSTrustACME,
			ACMEChallenges:          []string{"http-01"},
			OwnerCARequired:         false,
			LocalResolverRequired:   false,
			PSLActive:               pslActive,
			CookieMode:              cookieMode,
			CookiePrefix:            cookiePrefix,
			RequiresOwnDomain:       false,
			RequiresKombifyMe:       true,
			RequiresKombifyAccount:  false,
			RequiresInternet:        true,
			RequiresCloudDependency: false,
		}
		contract.IdentityHost = contract.ServiceHost("id")
		contract.AccessGateHost = contract.ServiceHost("auth")
		return contract, contract.Validate()

	default:
		return AddressZoneContract{}, fmt.Errorf("unsupported address zone kind %q", kind)
	}
}

// AddressZoneFromCanonicalStackSpec extracts and validates the address zone
// contract from a canonical StackSpec v2 JSON document.
func AddressZoneFromCanonicalStackSpec(canonical []byte) (AddressZoneContract, error) {
	var spec struct {
		Network struct {
			Domain struct {
				Base            string `json:"base"`
				SubdomainPrefix string `json:"subdomainPrefix"`
			} `json:"domain"`
		} `json:"network"`
	}
	if err := json.Unmarshal(canonical, &spec); err != nil {
		return AddressZoneContract{}, fmt.Errorf("decode canonical StackSpec network domain: %w", err)
	}
	return ResolveAddressZone(AddressZoneOptions{
		Domain:          spec.Network.Domain.Base,
		SubdomainPrefix: spec.Network.Domain.SubdomainPrefix,
	})
}

// SecurityPosture captures the Tier-1 security invariants that must never be
// silently weakened across zone switches or connected endpoint outages.
type SecurityPosture struct {
	TLSEnforced             bool `json:"tlsEnforced"`
	AccessGateEnforced      bool `json:"accessGateEnforced"`
	PocketIDRequired        bool `json:"pocketIdRequired"`
	PrivateByDefaultRouting bool `json:"privateByDefaultRouting"`
	ZeroDefaultPasswords    bool `json:"zeroDefaultPasswords"`
}

// DefaultSecurityPosture returns the required baseline security posture.
func DefaultSecurityPosture() SecurityPosture {
	return SecurityPosture{
		TLSEnforced:             true,
		AccessGateEnforced:      true,
		PocketIDRequired:        true,
		PrivateByDefaultRouting: true,
		ZeroDefaultPasswords:    true,
	}
}

// ValidateZoneTransition enforces the ADR-0055 §2 No-Silent-Downgrade Guard:
// switching between any two valid address zones must preserve TLS, Access Gate,
// PocketID, private-by-default routing, and zero-default-password posture.
func ValidateZoneTransition(from, to AddressZoneContract, fromPosture, toPosture SecurityPosture) error {
	if err := from.Validate(); err != nil {
		return fmt.Errorf("source address zone invalid: %w", err)
	}
	if err := to.Validate(); err != nil {
		return fmt.Errorf("target address zone invalid: %w", err)
	}
	var weakened []string
	if fromPosture.TLSEnforced && !toPosture.TLSEnforced {
		weakened = append(weakened, "tls")
	}
	if fromPosture.AccessGateEnforced && !toPosture.AccessGateEnforced {
		weakened = append(weakened, "access-gate")
	}
	if fromPosture.PocketIDRequired && !toPosture.PocketIDRequired {
		weakened = append(weakened, "pocketid")
	}
	if fromPosture.PrivateByDefaultRouting && !toPosture.PrivateByDefaultRouting {
		weakened = append(weakened, "private-by-default-routing")
	}
	if fromPosture.ZeroDefaultPasswords && !toPosture.ZeroDefaultPasswords {
		weakened = append(weakened, "zero-default-passwords")
	}
	if len(weakened) > 0 {
		return fmt.Errorf("%w: transition %s -> %s cannot weaken %s", ErrSilentDowngradeRefused, from.Kind, to.Kind, strings.Join(weakened, ", "))
	}
	return nil
}

// GuardEndpointAvailability enforces the No-Silent-Downgrade Guard when a
// connected endpoint (e.g., DNS verifier, ACME endpoint, or Kombify service)
// is unavailable: it never silently weakens TLS, auth, or privacy posture, and
// never silently replaces a custom domain with kombify.me.
func GuardEndpointAvailability(contract AddressZoneContract, endpointAvailable bool, posture SecurityPosture) error {
	if err := contract.Validate(); err != nil {
		return err
	}
	required := DefaultSecurityPosture()
	if err := ValidateZoneTransition(contract, contract, required, posture); err != nil {
		return err
	}
	if !endpointAvailable && contract.RequiresInternet {
		return fmt.Errorf("%w: %s (%s) remains locked with TLS=%t, AccessGate=%t, PrivateByDefault=%t",
			ErrEndpointUnavailableNoDowngrade, contract.Kind, contract.Zone,
			posture.TLSEnforced, posture.AccessGateEnforced, posture.PrivateByDefaultRouting)
	}
	return nil
}

// OnboardingAxesInput captures the 3 user-facing onboarding axes (ADR-0055 §2):
//  1. Workloads / Kit (Kit + UseCases)
//  2. Address Zone (ZoneKind + Domain + SubdomainPrefix + PSLActive)
//  3. Setup Style (SetupStyle + CleanRollout)
type OnboardingAxesInput struct {
	Kit               string
	UseCases          []string
	ZoneKind          AddressZoneKind
	Domain            string
	SubdomainPrefix   string
	PSLActive         bool
	SetupStyle        string
	CleanRollout      bool
	SetupStyleChanged bool
}

// OnboardingAxes is the resolved 3-axis onboarding selection ready for
// materialization and rollout.
type OnboardingAxes struct {
	Kit         string              `json:"kit"`
	UseCases    []string            `json:"useCases,omitempty"`
	AddressZone AddressZoneContract `json:"addressZone"`
	SetupStyle  SetupStyle          `json:"setupStyle"`
	Rollout     RolloutTierPolicy   `json:"rollout"`
	Posture     SecurityPosture     `json:"posture"`
}

// ResolveOnboardingAxes resolves and validates the three orthogonal onboarding
// axes without breaking legacy kit defaults or allowing silent downgrades.
func ResolveOnboardingAxes(input OnboardingAxesInput) (OnboardingAxes, error) {
	kit := strings.TrimSpace(input.Kit)
	if err := productkits.Validate(kit); err != nil {
		return OnboardingAxes{}, err
	}
	setupStyle, err := ResolveSetupStyleFlags(input.SetupStyle, input.CleanRollout, input.SetupStyleChanged)
	if err != nil {
		return OnboardingAxes{}, err
	}
	rolloutPolicy, err := RolloutPolicyForSetupStyle(setupStyle)
	if err != nil {
		return OnboardingAxes{}, err
	}

	zoneKind, err := ParseAddressZoneKind(string(input.ZoneKind))
	if err != nil {
		return OnboardingAxes{}, err
	}
	domain := normalizeDomain(input.Domain)
	prefix := strings.ToLower(strings.TrimSpace(input.SubdomainPrefix))

	// Cloud Kit always requires a public address zone; local-only is refused
	// fail-closed.
	if kit == "cloud-kit" {
		if zoneKind == AddressZoneLocalOnly || domain == LocalOnlyCanonicalZone {
			return OnboardingAxes{}, fmt.Errorf("cloud-kit requires a public address zone (%q or %q); %q is only valid for local-only kits such as basement-kit", AddressZoneCustomDomain, AddressZoneKombifyMe, AddressZoneLocalOnly)
		}
		// Unbound cloud-kit init without an explicit zone/domain stays allowed
		// only when domain is explicitly kombify.me (allocated later via
		// `stackkit address bind`) or a concrete {base}.kombify.me / custom domain.
		if zoneKind == "" && domain == "" {
			return OnboardingAxes{}, fmt.Errorf("cloud-kit requires an explicit --domain (e.g. example.com or your-zone.kombify.me)")
		}
	}

	// For Basement Kit, empty zone + empty domain defaults to local-only lab.home.
	if zoneKind == "" && domain == "" {
		zoneKind = AddressZoneLocalOnly
		domain = LocalOnlyCanonicalZone
	}

	zoneContract, err := ResolveAddressZone(AddressZoneOptions{
		ZoneKind:        zoneKind,
		Domain:          domain,
		SubdomainPrefix: prefix,
		PSLActive:       input.PSLActive,
	})
	if err != nil {
		return OnboardingAxes{}, err
	}

	useCases := make([]string, 0, len(input.UseCases))
	for _, uc := range input.UseCases {
		if trimmed := strings.TrimSpace(uc); trimmed != "" {
			useCases = append(useCases, trimmed)
		}
	}

	return OnboardingAxes{
		Kit:         kit,
		UseCases:    useCases,
		AddressZone: zoneContract,
		SetupStyle:  setupStyle,
		Rollout:     rolloutPolicy,
		Posture:     DefaultSecurityPosture(),
	}, nil
}

func normalizeDomain(raw string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(raw), "."))
}

func isKombifyMeName(domain string) bool {
	domain = normalizeDomain(domain)
	return domain == KombifyMeParentDomain || strings.HasSuffix(domain, "."+KombifyMeParentDomain)
}

func validDNSZone(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !zoneLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}
