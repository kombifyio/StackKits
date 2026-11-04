package architecturev2renderer

import (
	"slices"
	"strings"
)

// homelabDeviceEnrollment is the closed renderer view of
// #DeviceEnrollmentPolicy (homelab identity target §6): enrollment by the
// person, home attested only by the Authority's LAN listener, automatic at
// home with a 24 h undo, confirmed away by an own enrolled device or else
// the Owner.
type homelabDeviceEnrollment struct {
	Mode             string `json:"mode"`
	AuthoritySiteRef string `json:"authoritySiteRef"`
	AccountBinding   string `json:"accountBinding"`
	HomeAttestation  string `json:"homeAttestation"`
	AtHome           struct {
		Approval          string   `json:"approval"`
		Notify            []string `json:"notify"`
		UndoWindowSeconds int      `json:"undoWindowSeconds"`
	} `json:"atHome"`
	Away struct {
		Approval   string   `json:"approval"`
		Confirmers []string `json:"confirmers"`
	} `json:"away"`
	Completion                string   `json:"completion"`
	PrivilegedAccountPasskeys string   `json:"privilegedAccountPasskeys"`
	RequireDeviceGeneratedKey bool     `json:"requireDeviceGeneratedKey"`
	RequirePossessionProof    bool     `json:"requirePossessionProof"`
	CustodyClasses            []string `json:"custodyClasses"`
	MaxDevicesPerAccount      int      `json:"maxDevicesPerAccount"`
	RateLimit                 struct {
		Attempts      int `json:"attempts"`
		WindowSeconds int `json:"windowSeconds"`
	} `json:"rateLimit"`
	Revocation struct {
		ByPerson string   `json:"byPerson"`
		ByOwner  string   `json:"byOwner"`
		Ends     []string `json:"ends"`
	} `json:"revocation"`
	Lifetimes struct {
		RegistryEntryUnusedSeconds int `json:"registryEntryUnusedSeconds"`
		DeviceLifetimeSeconds      int `json:"deviceLifetimeSeconds"`
		DPoPProofSeconds           int `json:"dpopProofSeconds"`
		UserVerificationSeconds    int `json:"userVerificationSeconds"`
	} `json:"lifetimes"`
}

// validHomelabDeviceEnrollment refuses any enrollment policy that widens the
// contract: another home attestation, an Owner-only or automatic away path, a
// device key that is not device-generated and possession-bound, an unknown
// custody class, no device cap or rate limit, a revocation that leaves a
// session or VPN peer behind, or lifetimes other than those of target §12.
func validHomelabDeviceEnrollment(policy homelabDeviceEnrollment) bool {
	if policy.Mode != "person" || policy.AccountBinding != "homelab-account" || policy.HomeAttestation != "authority-lan-listener" ||
		policy.Completion != "client-probes-every-enabled-path" || policy.PrivilegedAccountPasskeys != "device-bound-where-offered" ||
		!policy.RequireDeviceGeneratedKey || !policy.RequirePossessionProof {
		return false
	}
	if policy.AtHome.Approval != "automatic" || !slices.Equal(policy.AtHome.Notify, []string{"own-devices", "owner"}) ||
		policy.AtHome.UndoWindowSeconds != 86400 {
		return false
	}
	if policy.Away.Approval != "step-up-confirmation" || !slices.Equal(policy.Away.Confirmers, []string{"own-enrolled-device", "owner"}) {
		return false
	}
	if len(policy.CustodyClasses) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, class := range policy.CustodyClasses {
		if class != "hardware" && class != "os-protected" && class != "software" || seen[class] {
			return false
		}
		seen[class] = true
	}
	if policy.MaxDevicesPerAccount < 1 || policy.MaxDevicesPerAccount > 64 ||
		policy.RateLimit.Attempts < 1 || policy.RateLimit.Attempts > 20 ||
		policy.RateLimit.WindowSeconds < 60 || policy.RateLimit.WindowSeconds > 86400 {
		return false
	}
	if policy.Revocation.ByPerson != "own-devices" || policy.Revocation.ByOwner != "all-devices" ||
		!slices.Equal(policy.Revocation.Ends, []string{"sessions", "app-passes", "app-oidc-sessions", "vpn-peers"}) {
		return false
	}
	return policy.Lifetimes.RegistryEntryUnusedSeconds == 15552000 && policy.Lifetimes.DeviceLifetimeSeconds == 3600 &&
		policy.Lifetimes.DPoPProofSeconds == 30 && policy.Lifetimes.UserVerificationSeconds == 86400
}

// validPartitionSessionTerms is the partition rule of #PartitionPolicy:
// issued credentials and sessions stay valid to their lifetime against the
// last-known revocation state; new sign-ins, enrollments and renewals fail
// closed.
func validPartitionSessionTerms(issuedSessions, newSessions string) bool {
	return issuedSessions == "valid-to-lifetime-against-last-known-revocation" && newSessions == "fail-closed"
}

// homelabIdentityOrigin is the closed renderer view of #IdentityOriginV1.
type homelabIdentityOrigin struct {
	Name         string `json:"name"`
	Host         string `json:"host"`
	Source       string `json:"source"`
	Certificates string `json:"certificates"`
}

func validHomelabIdentityOrigin(origin homelabIdentityOrigin) bool {
	if origin.Name == "" || origin.Host != "id."+origin.Name {
		return false
	}
	lower := strings.ToLower(origin.Name)
	if lower == "lab.home" {
		return origin.Source == "local-only" && origin.Certificates == "homelab-root"
	}
	if homelabReservedName(lower) {
		return origin.Source == "reserved-name" && origin.Certificates == "homelab-root"
	}
	return validPublicHomelabIdentityOrigin(origin, lower)
}

// homelabReservedName reports a special-use name: no public DNS, no publicly
// trusted certificate and no remote reach path carries it (lab.home included).
func homelabReservedName(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{"localhost", "local", "internal", "arpa", "test", "invalid", "example", "home", "lan"} {
		if lower == suffix || strings.HasSuffix(lower, "."+suffix) {
			return true
		}
	}
	return false
}

func validPublicHomelabIdentityOrigin(origin homelabIdentityOrigin, lower string) bool {
	wantSource := "own-domain"
	if strings.HasSuffix(lower, ".kombify.me") {
		wantSource = "kombify-me"
	}
	return origin.Source == wantSource && origin.Certificates == "publicly-trusted"
}
