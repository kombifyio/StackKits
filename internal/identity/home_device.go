package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	homeDeviceRegistryPath     = ".stackkit/custody/home-verifier/devices.json"
	homeDeviceRegistryMaxBytes = 512 << 10
	// homeDeviceRegistryGrowthBytes is the size an enrollment may grow the
	// registry to; the rest stays free for revocations, renewals and
	// confirmations, so the registry can never become unreadable.
	homeDeviceRegistryGrowthBytes = 448 << 10
	homeDeviceRegistrySchema      = "stackkit.home-device-registry/v3"
	// A revoked entry becomes a tombstone (its key thumbprint only) once
	// every credential it could hold has expired; a lapsed entry is dropped a
	// week after it lapsed.
	homeRevokedTombstoneAfter = 24 * time.Hour
	homeLapsedRetention       = 7 * 24 * time.Hour
)

// HomeDeviceRegistryFile is the device registry below the workspace root;
// the VPN add-on resync watches it for revocations.
const HomeDeviceRegistryFile = homeDeviceRegistryPath

// Approval modes of a registry entry (#DeviceEnrollmentPolicy): approved
// automatically at home, confirmed away by a step-up from the person's own
// enrolled device or by the Owner at the Authority, or still pending.
const (
	HomeApprovalHome      = "home"
	HomeApprovalOwnDevice = "own-device"
	HomeApprovalOwner     = "owner"
	HomeApprovalPending   = "pending"
)

// Custody classes of a device key (#DeviceCustodyClassV1, Connect D4).
const (
	HomeCustodyHardware    = "hardware"
	HomeCustodyOSProtected = "os-protected"
	HomeCustodySoftware    = "software"
)

var (
	// ErrHomeRegistryFull refuses a change that would grow the registry past
	// its bound; revoking devices still works.
	ErrHomeRegistryFull   = errors.New("identity: the device registry is full; revoke unused devices first")
	ErrHomeDeviceRevoked  = errors.New("identity: this device key was revoked and cannot enroll again; generate a new key")
	ErrHomeDeviceConflict = errors.New("identity: this device key is already enrolled or pending, or another device of this account uses this name")
	ErrHomeDeviceNotFound = errors.New("identity: no enrolled device has this id")
)

// HomeDevice is the registry entry of one device key at the Authority. ID is
// the RFC 7638 thumbprint of the device-generated public key; the entry binds
// to one Homelab account (the Pocket ID subject of the person who enrolled
// it) and expires 180 days after its last use. It holds no private key or
// credential.
type HomeDevice struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	AccountSubject     string             `json:"accountSubject"`
	Custody            string             `json:"custody"`
	PublicKey          jose.JSONWebKey    `json:"publicKey"`
	EnrolledAt         time.Time          `json:"enrolledAt"`
	LastUsedAt         time.Time          `json:"lastUsedAt"`
	LastUserVerifiedAt time.Time          `json:"lastUserVerifiedAt"`
	Approval           HomeDeviceApproval `json:"approval"`
	Revoked            bool               `json:"revoked"`
	RevokedAt          *time.Time         `json:"revokedAt,omitempty"`
	RevokedBy          string             `json:"revokedBy,omitempty"`
}

// HomeDeviceApproval records how an entry was admitted. A pending entry
// (an away enrollment) has PendingUntil and no credential until it is
// confirmed; By is the confirming device ID or the Owner subject.
type HomeDeviceApproval struct {
	Mode         string     `json:"mode"`
	By           string     `json:"by,omitempty"`
	At           *time.Time `json:"at,omitempty"`
	PendingUntil *time.Time `json:"pendingUntil,omitempty"`
}

// ExpiresAt is when the entry lapses without another use.
func (d HomeDevice) ExpiresAt() time.Time {
	return d.LastUsedAt.Add(HomeDeviceRegistryEntryUnused)
}

// Active reports whether the entry is approved, not revoked and not lapsed.
func (d HomeDevice) Active(now time.Time) bool {
	return !d.Revoked && d.ID != "" && d.AccountSubject != "" && d.Approval.Mode != HomeApprovalPending &&
		d.Approval.Mode != "" && now.Before(d.ExpiresAt())
}

// Pending reports whether the entry is an away enrollment awaiting its
// confirmation.
func (d HomeDevice) Pending(now time.Time) bool {
	return !d.Revoked && d.Approval.Mode == HomeApprovalPending && d.Approval.PendingUntil != nil && now.Before(*d.Approval.PendingUntil)
}

// holdsSlot reports whether the entry counts against the account's device cap
// and its device names.
func (d HomeDevice) holdsSlot(now time.Time) bool {
	return d.Active(now) || d.Pending(now)
}

// homeDeviceRegistry is the Authority's one device registry. RevokedKeys are
// the tombstones of revoked entries: key thumbprints that never enroll
// again. RemovedAccounts are the Homelab accounts the removal chain ended;
// they never enroll again and every Home trust refresh carries them as
// revoked accounts.
type homeDeviceRegistry struct {
	Schema          string                  `json:"schema"`
	Devices         []HomeDevice            `json:"devices"`
	Notices         []HomeEnrollmentNotice  `json:"notices"`
	Attempts        []homeEnrollmentAttempt `json:"attempts"`
	RevokedKeys     []string                `json:"revokedKeys"`
	RemovedAccounts []string                `json:"removedAccounts"`
}

// compactHomeRegistry turns revoked entries into tombstones once their
// credentials have expired, drops entries a week after they lapsed unused,
// and drops the notices of entries it removes.
func compactHomeRegistry(registry *homeDeviceRegistry, now time.Time) {
	removed := map[string]bool{}
	kept := registry.Devices[:0]
	for _, device := range registry.Devices {
		switch {
		case device.Revoked && device.RevokedAt != nil && now.Sub(*device.RevokedAt) > homeRevokedTombstoneAfter:
			if !slices.Contains(registry.RevokedKeys, device.ID) {
				registry.RevokedKeys = append(registry.RevokedKeys, device.ID)
			}
			removed[device.ID] = true
		case !device.Revoked && device.Approval.Mode != HomeApprovalPending && now.Sub(device.ExpiresAt()) > homeLapsedRetention:
			removed[device.ID] = true
		default:
			kept = append(kept, device)
		}
	}
	registry.Devices = kept
	if len(removed) == 0 {
		return
	}
	notices := registry.Notices[:0]
	for _, notice := range registry.Notices {
		if !removed[notice.DeviceID] {
			notices = append(notices, notice)
		}
	}
	registry.Notices = notices
}

// homeEnrollmentAttempt is one verified enrollment submission of an account,
// kept for the #DeviceEnrollmentPolicy rate limit.
type homeEnrollmentAttempt struct {
	Account string    `json:"account"`
	At      time.Time `json:"at"`
}

// ListHomeDevices returns every registry entry, newest first.
func ListHomeDevices(root string) ([]HomeDevice, error) {
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return nil, err
	}
	devices := append(make([]HomeDevice, 0, len(registry.Devices)), registry.Devices...)
	sort.SliceStable(devices, func(i, j int) bool { return devices[i].EnrolledAt.After(devices[j].EnrolledAt) })
	return devices, nil
}

// ObservedLANAddress classifies a transport peer address the server observed
// (a connection RemoteAddr): private (RFC 1918 / ULA) or link-local peers
// only, never loopback, never read from a request header or body. It narrows
// who may use the LAN enrollment listener; it never establishes home on its
// own, which only the listener the connection arrived on does.
func ObservedLANAddress(address string) bool {
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() && (ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// ValidHomeDeviceName bounds the name a person gives a device: a lowercase
// DNS-label-like name, never a URL or principal.
func ValidHomeDeviceName(value string) bool {
	if value == "" || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func validHomeCustody(value string) bool {
	return value == HomeCustodyHardware || value == HomeCustodyOSProtected || value == HomeCustodySoftware
}

func validHomeApproval(approval HomeDeviceApproval) bool {
	switch approval.Mode {
	case HomeApprovalHome, HomeApprovalOwnDevice, HomeApprovalOwner:
		return approval.PendingUntil == nil
	case HomeApprovalPending:
		return approval.PendingUntil != nil
	}
	return false
}

// updateHomeDeviceRegistry serializes every read-modify-write of the registry
// across goroutines and processes, so a renewal can never write back an entry
// a concurrent revocation just revoked.
func updateHomeDeviceRegistry(root string, mutate func(*homeDeviceRegistry) error) error {
	release, err := localevidence.AcquireCustodyLock(root, homeDeviceRegistryPath, 101)
	if err != nil {
		return err
	}
	defer release()
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return err
	}
	if err := mutate(&registry); err != nil {
		return err
	}
	compactHomeRegistry(&registry, time.Now().UTC())
	return writeHomeDeviceRegistry(root, registry)
}

func loadHomeDeviceRegistry(root string) (homeDeviceRegistry, error) {
	registry := homeDeviceRegistry{Schema: homeDeviceRegistrySchema}
	raw, err := readHomePrivate(root, homeDeviceRegistryPath, homeDeviceRegistryMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return registry, err
	}
	unreadable := errors.New("identity: unreadable device registry")
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&registry) != nil || decoder.Decode(new(any)) != io.EOF || registry.Schema != homeDeviceRegistrySchema {
		return registry, unreadable
	}
	seen := map[string]bool{}
	for _, device := range registry.Devices {
		thumbprint, thumbErr := homeKeyThumbprint(device.PublicKey.Key)
		if device.ID == "" || seen[device.ID] || thumbErr != nil || thumbprint != device.ID || !validHomeDeviceKey(device.PublicKey, device.PublicKey.Algorithm) ||
			!validHomeCustody(device.Custody) || !validHomeApproval(device.Approval) {
			return registry, unreadable
		}
		seen[device.ID] = true
	}
	notices := map[string]bool{}
	for _, notice := range registry.Notices {
		if notice.ID == "" || notices[notice.ID] || !seen[notice.DeviceID] {
			return registry, unreadable
		}
		notices[notice.ID] = true
	}
	tombstones := map[string]bool{}
	for _, key := range registry.RevokedKeys {
		if len(key) != 43 || tombstones[key] || seen[key] || strings.Trim(key, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			return registry, unreadable
		}
		tombstones[key] = true
	}
	return registry, nil
}

// homeRegistrySize is the encoded size of the registry.
func homeRegistrySize(registry homeDeviceRegistry) int {
	registry.Schema = homeDeviceRegistrySchema
	raw, err := json.Marshal(registry)
	if err != nil {
		return homeDeviceRegistryMaxBytes + 1
	}
	return len(raw)
}

func writeHomeDeviceRegistry(root string, registry homeDeviceRegistry) error {
	registry.Schema = homeDeviceRegistrySchema
	raw, err := json.Marshal(registry)
	if err != nil {
		return err
	}
	if len(raw) > homeDeviceRegistryMaxBytes {
		return ErrHomeRegistryFull
	}
	return writeHomePrivate(root, homeDeviceRegistryPath, raw, false)
}
