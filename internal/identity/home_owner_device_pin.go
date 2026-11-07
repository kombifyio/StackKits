package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"slices"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The Owner-device pin (homelab identity target §9; StackKits 4vhd). Recovery
// from an enrolled Owner device is a second factor next to the recovery code,
// so the host-side signer decides which keys are Owner devices itself. The
// device registry is the core's (it can write it), so a forged registry entry
// must never become a recovery factor: the signer keeps its own pin in the
// recovery custody, which only the host holds, and adds a key only from an
// Owner passkey ID token it verifies itself against the Owner-signed Home
// trust, for exactly this key (the device sign-in nonce of an enrollment or of
// a 24 hour user verification, bound to the key and to the one proof that
// presents it).
const (
	// RecoveryOwnerDevicePath pins the calling Owner device. The DPoP proof
	// carries the key; the body is {"idToken": "..."}.
	RecoveryOwnerDevicePath = "/api/v1/identity/recovery/owner-device"

	ownerDevicePinPath      = ".stackkit/custody/recovery/owner-devices.json"
	ownerDevicePinSchema    = "stackkit.identity-owner-device-pins/v1"
	ownerDevicePinMaxBytes  = 64 << 10
	ownerDevicePinLimit     = 16
	ownerDevicePinLifetime  = HomeDeviceRegistryEntryUnused
	ownerDevicePinBodyBytes = recoveryMaxBodyBytes
)

// ownerDevicePin is one Owner device key the signer verified itself.
type ownerDevicePin struct {
	ID         string          `json:"id"`
	Account    string          `json:"account"`
	PublicKey  jose.JSONWebKey `json:"publicKey"`
	PinnedAt   time.Time       `json:"pinnedAt"`
	VerifiedAt time.Time       `json:"verifiedAt"`
	// Via is the sign-in that proved the Owner: "enrollment" or
	// "user-verification".
	Via string `json:"via"`
	// Revoked is the signer's own record that the registry showed this key
	// revoked. It stays until the pin expires; only PinOwnerDevice, after a
	// fresh Owner passkey sign-in for this key, clears it.
	Revoked   bool      `json:"revoked,omitempty"`
	RevokedAt time.Time `json:"revokedAt,omitempty"`
}

type ownerDevicePins struct {
	Schema  string           `json:"schema"`
	Devices []ownerDevicePin `json:"devices"`
}

// active reports whether the pin still counts: not older than the registry's
// unused lifetime since the Owner last verified on this key.
func (p ownerDevicePin) active(now time.Time) bool {
	return p.ID != "" && p.Account != "" && now.Before(p.VerifiedAt.Add(ownerDevicePinLifetime))
}

// PinOwnerDevice verifies an Owner passkey sign-in bound to the proof's key and
// pins that key. It runs only in the host-side signer. The ID token is the
// device sign-in client's token of the Owner subject, with the nonce of the
// enrollment or of the user verification for exactly this key and this
// proof, fresh within HomeUserVerificationFreshness. The Home trust it is
// checked against is the Owner-signed one, so the core can neither forge a
// token nor widen the pin.
func PinOwnerDevice(root string, request RecoveryDeviceRequest, now time.Time) (ownerDevicePin, error) {
	now = now.UTC()
	deny := func() (ownerDevicePin, error) { return ownerDevicePin{}, ErrRecoveryDenied }
	proof, err := parseHomeDeviceProof(request.Proof, now)
	if err != nil || request.Method != http.MethodPost || proof.Method != http.MethodPost || request.IDToken == "" ||
		!homeIdentityOriginAdmitted(root, proof.Target, RecoveryOwnerDevicePath) {
		return deny()
	}
	verifier, err := HomeVerifierFor(root)
	if err != nil || !verifier.afterStart(proof.IssuedAt) {
		return deny()
	}
	trust, err := loadCurrentHomeTrust(root, now)
	if err != nil {
		return deny()
	}
	owner, err := recoveryOwnerSubject(root)
	if err != nil || owner != trust.PocketID.Subject {
		return deny()
	}
	via := "user-verification"
	claims, err := verifyHomePasskeyIDToken(request.IDToken, trust, HomeDeviceClientID, HomeDeviceUserVerificationNonce(proof.Thumbprint, proof.ID), now)
	if err != nil {
		via = "enrollment"
		claims, err = verifyHomePasskeyIDToken(request.IDToken, trust, HomeDeviceClientID, HomeDeviceEnrollmentNonce(proof.Thumbprint, proof.ID), now)
	}
	if err != nil || claims.IssuedAt == nil || claims.Subject != owner {
		return deny()
	}
	verified := claims.IssuedAt.Time().UTC()
	if age := now.Sub(verified); age < -HomeClockLeeway || age > HomeUserVerificationFreshness || !verifier.afterStart(verified) {
		return deny()
	}
	if verified.After(now) {
		verified = now
	}
	verifier.mu.Lock()
	fresh := verifier.rememberLocked(proof.Thumbprint, "owner-pin\x00"+proof.ID, proof.IssuedAt.Add(homeProofReplayRetention), now)
	verifier.mu.Unlock()
	if !fresh {
		return deny()
	}
	pin := ownerDevicePin{ID: proof.Thumbprint, Account: owner, PublicKey: proof.Key, PinnedAt: now, VerifiedAt: verified, Via: via}
	release, err := localevidence.AcquireCustodyLock(root, ownerDevicePinPath, 202)
	if err != nil {
		return ownerDevicePin{}, err
	}
	defer release()
	pins, err := loadOwnerDevicePins(root)
	if err != nil {
		return ownerDevicePin{}, err
	}
	kept := pins.Devices[:0]
	for _, existing := range pins.Devices {
		if existing.ID == pin.ID {
			pin.PinnedAt = existing.PinnedAt
			continue
		}
		if existing.active(now) {
			kept = append(kept, existing)
		}
	}
	if len(kept) >= ownerDevicePinLimit {
		return ownerDevicePin{}, ErrRecoveryDenied
	}
	pins.Devices = append(kept, pin)
	return pin, writeOwnerDevicePins(root, pins)
}

// ownerDevicePinFor returns the active pin of key id for the Owner account, or
// false. A pinned key that the registry now shows as revoked, or whose
// tombstone exists, is refused too: the pin is the grant, the registry can only
// take it away.
func ownerDevicePinFor(root, id, account string, now time.Time) (ownerDevicePin, bool) {
	// The signer learns revocations from the registry but keeps them in its own
	// host-only pin: a registry that is missing, unreadable or unparsable
	// refuses recovery outright, and a recorded revocation is never undone by
	// the registry, only by a fresh Owner-passkey pin admission.
	if _, err := readHomePrivate(root, homeDeviceRegistryPath, homeDeviceRegistryMaxBytes); err != nil {
		return ownerDevicePin{}, false
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return ownerDevicePin{}, false
	}
	pins, err := recordOwnerDevicePinRevocations(root, registry, now)
	if err != nil {
		return ownerDevicePin{}, false
	}
	index := slices.IndexFunc(pins.Devices, func(pin ownerDevicePin) bool {
		return pin.ID == id && pin.Account == account && pin.active(now) && !pin.Revoked
	})
	if index < 0 {
		return ownerDevicePin{}, false
	}
	return pins.Devices[index], true
}

// recordOwnerDevicePinRevocations marks every pin whose key the registry shows
// as revoked or tombstoned as revoked in the signer's own pin, and returns the
// pins as stored.
func recordOwnerDevicePinRevocations(root string, registry homeDeviceRegistry, now time.Time) (ownerDevicePins, error) {
	revoked := func(id string) bool {
		return slices.Contains(registry.RevokedKeys, id) || slices.ContainsFunc(registry.Devices, func(device HomeDevice) bool {
			return device.ID == id && device.Revoked
		})
	}
	release, err := localevidence.AcquireCustodyLock(root, ownerDevicePinPath, 202)
	if err != nil {
		return ownerDevicePins{}, err
	}
	defer release()
	pins, err := loadOwnerDevicePins(root)
	if err != nil {
		return ownerDevicePins{}, err
	}
	changed := false
	for index := range pins.Devices {
		if pin := &pins.Devices[index]; !pin.Revoked && revoked(pin.ID) {
			pin.Revoked, pin.RevokedAt = true, now.UTC()
			changed = true
		}
	}
	if changed {
		if err := writeOwnerDevicePins(root, pins); err != nil {
			return ownerDevicePins{}, err
		}
	}
	return pins, nil
}

// ownerDevicePinIDs are the pinned Owner devices a recovery notice addresses.
func ownerDevicePinIDs(root, account string, now time.Time) []string {
	ids := []string{}
	pins, err := loadOwnerDevicePins(root)
	if err != nil {
		return ids
	}
	for _, pin := range pins.Devices {
		if pin.Account == account && pin.active(now) && !pin.Revoked {
			ids = append(ids, pin.ID)
		}
	}
	return ids
}

func loadOwnerDevicePins(root string) (ownerDevicePins, error) {
	pins := ownerDevicePins{Schema: ownerDevicePinSchema}
	raw, err := readHomePrivate(root, ownerDevicePinPath, ownerDevicePinMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return pins, nil
	}
	if err != nil {
		return pins, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&pins) != nil || decoder.Decode(new(any)) != io.EOF || pins.Schema != ownerDevicePinSchema {
		return pins, errors.New("identity: unreadable Owner device pins")
	}
	for _, pin := range pins.Devices {
		thumbprint, err := homeKeyThumbprint(pin.PublicKey.Key)
		if err != nil || thumbprint != pin.ID || !validHomeDeviceKey(pin.PublicKey, pin.PublicKey.Algorithm) {
			return ownerDevicePins{Schema: ownerDevicePinSchema}, errors.New("identity: unreadable Owner device pins")
		}
	}
	return pins, nil
}

func writeOwnerDevicePins(root string, pins ownerDevicePins) error {
	pins.Schema = ownerDevicePinSchema
	raw, err := json.Marshal(pins)
	if err != nil {
		return err
	}
	return writeHomePrivate(root, ownerDevicePinPath, raw, false)
}
