package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

const (
	// homeResolvedPlanPath is the applied canonical plan of this workspace.
	homeResolvedPlanPath     = "deploy/.stackkit/resolved-plan.json"
	homeResolvedPlanMaxBytes = 32 << 20
)

// ErrHomeEnrollmentUnavailable means the applied plan carries no admissible
// #DeviceEnrollmentPolicy, so this server enrolls nothing.
var ErrHomeEnrollmentUnavailable = errors.New("identity: this server has no applied device enrollment policy")

// HomeEnrollmentPolicy is the runtime form of the applied plan's
// #DeviceEnrollmentPolicy (identity.deviceEnrollment): the per-account device
// cap, the per-account rate limit of enrollment submissions and the admitted
// device key custody classes. Every other field of the contract is fixed and
// checked, never configurable here.
type HomeEnrollmentPolicy struct {
	MaxDevicesPerAccount int
	RateLimitAttempts    int
	RateLimitWindow      time.Duration
	CustodyClasses       []string
}

// HomeEnrollmentUndoWindow is #HomelabIdentityLifetimesV1.enrollmentUndoSeconds.
const HomeEnrollmentUndoWindow = 24 * time.Hour

// HomeEnrollmentPendingLifetime bounds how long an away enrollment waits for
// its confirmation before it lapses.
const HomeEnrollmentPendingLifetime = 24 * time.Hour

type homeDeviceEnrollmentContract struct {
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

// LoadHomeEnrollmentPolicy reads identity.deviceEnrollment from the applied
// plan and admits it only when it is exactly the person enrollment of the
// homelab identity target within the contract's bounds; anything else fails
// closed with ErrHomeEnrollmentUnavailable.
func LoadHomeEnrollmentPolicy(root string) (HomeEnrollmentPolicy, error) {
	// The applied plan is large and changes only with an Apply: its parsed
	// policy is kept per file identity (size and modification time).
	planFile := filepath.Join(root, filepath.FromSlash(homeResolvedPlanPath))
	info, statErr := os.Stat(planFile)
	if statErr != nil {
		return HomeEnrollmentPolicy{}, ErrHomeEnrollmentUnavailable
	}
	if cached, ok := homePolicyCache.Load(planFile); ok {
		entry := cached.(homePolicyCacheEntry)
		if entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
			return entry.policy.clone(), nil
		}
	}
	policy, err := parseHomeEnrollmentPolicy(root)
	if err != nil {
		return HomeEnrollmentPolicy{}, err
	}
	homePolicyCache.Store(planFile, homePolicyCacheEntry{size: info.Size(), modTime: info.ModTime(), policy: policy.clone()})
	return policy, nil
}

var homePolicyCache sync.Map

type homePolicyCacheEntry struct {
	size    int64
	modTime time.Time
	policy  HomeEnrollmentPolicy
}

func (p HomeEnrollmentPolicy) clone() HomeEnrollmentPolicy {
	p.CustodyClasses = append([]string(nil), p.CustodyClasses...)
	return p
}

func parseHomeEnrollmentPolicy(root string) (HomeEnrollmentPolicy, error) {
	raw, err := readHomePrivate(root, homeResolvedPlanPath, homeResolvedPlanMaxBytes)
	if err != nil {
		return HomeEnrollmentPolicy{}, ErrHomeEnrollmentUnavailable
	}
	var plan struct {
		Identity struct {
			DeviceEnrollment json.RawMessage `json:"deviceEnrollment"`
		} `json:"identity"`
	}
	if json.Unmarshal(raw, &plan) != nil || len(plan.Identity.DeviceEnrollment) == 0 {
		return HomeEnrollmentPolicy{}, ErrHomeEnrollmentUnavailable
	}
	var contract homeDeviceEnrollmentContract
	decoder := json.NewDecoder(bytes.NewReader(plan.Identity.DeviceEnrollment))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&contract) != nil || decoder.Decode(new(any)) != io.EOF {
		return HomeEnrollmentPolicy{}, ErrHomeEnrollmentUnavailable
	}
	if !admissibleHomeEnrollmentContract(contract) {
		return HomeEnrollmentPolicy{}, ErrHomeEnrollmentUnavailable
	}
	return HomeEnrollmentPolicy{
		MaxDevicesPerAccount: contract.MaxDevicesPerAccount, RateLimitAttempts: contract.RateLimit.Attempts,
		RateLimitWindow: time.Duration(contract.RateLimit.WindowSeconds) * time.Second,
		CustodyClasses:  append([]string(nil), contract.CustodyClasses...),
	}, nil
}

func admissibleHomeEnrollmentContract(c homeDeviceEnrollmentContract) bool {
	if c.Mode != "person" || c.AuthoritySiteRef == "" || c.AccountBinding != "homelab-account" || c.HomeAttestation != "authority-lan-listener" ||
		c.AtHome.Approval != "automatic" || !slices.Equal(c.AtHome.Notify, []string{"own-devices", "owner"}) ||
		c.AtHome.UndoWindowSeconds != int(HomeEnrollmentUndoWindow/time.Second) ||
		c.Away.Approval != "step-up-confirmation" || !slices.Equal(c.Away.Confirmers, []string{"own-enrolled-device", "owner"}) ||
		c.Completion != "client-probes-every-enabled-path" || c.PrivilegedAccountPasskeys != "device-bound-where-offered" ||
		!c.RequireDeviceGeneratedKey || !c.RequirePossessionProof {
		return false
	}
	if c.MaxDevicesPerAccount < 1 || c.MaxDevicesPerAccount > 64 || c.RateLimit.Attempts < 1 || c.RateLimit.Attempts > 20 ||
		c.RateLimit.WindowSeconds < 60 || c.RateLimit.WindowSeconds > 86400 {
		return false
	}
	if len(c.CustodyClasses) == 0 || len(c.CustodyClasses) > 3 {
		return false
	}
	seen := map[string]bool{}
	for _, class := range c.CustodyClasses {
		if !validHomeCustody(class) || seen[class] {
			return false
		}
		seen[class] = true
	}
	if c.Revocation.ByPerson != "own-devices" || c.Revocation.ByOwner != "all-devices" ||
		!slices.Equal(c.Revocation.Ends, []string{"sessions", "app-passes", "app-oidc-sessions", "vpn-peers"}) {
		return false
	}
	return c.Lifetimes.RegistryEntryUnusedSeconds == int(HomeDeviceRegistryEntryUnused/time.Second) &&
		c.Lifetimes.DeviceLifetimeSeconds == int(HomeDeviceCredentialLifetime/time.Second) &&
		c.Lifetimes.DPoPProofSeconds == int(HomeDeviceProofWindow/time.Second) &&
		c.Lifetimes.UserVerificationSeconds == int(HomeDeviceUserVerificationInterval/time.Second)
}
