package identity

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"
)

// The local removal chain (homelab identity target §6.2, §10). Every person
// revokes their own devices and the Owner all of them; removing a member
// revokes every device of the account and the account itself. Revocation
// ends what the device holds. The report names each artifact of
// revocation-registry.v1.json the chain covers and its outcome, including
// the ones no code issues yet.

// Revocation registry artifact IDs (workspace revocation-registry.v1.json).
const (
	HomeArtifactAccount       = "homelab.account"
	HomeArtifactDevice        = "homelab.device"
	HomeArtifactSession       = "homelab.session"
	HomeArtifactAppCredential = "homelab.app_credential"
	HomeArtifactVPNPeer       = "homelab.vpn_peer"
)

// Revocation reasons recorded on a registry entry.
const (
	HomeRevokedByPerson  = "person"
	HomeRevokedByOwner   = "owner"
	HomeRevokedByUndo    = "undo"
	HomeRevokedByRemoval = "account-removed"
)

// HomeRevocation is the outcome of one revocation: the revoked entries and
// one line per revocation-registry artifact.
type HomeRevocation struct {
	Devices   []HomeDevice          `json:"devices"`
	Artifacts []HomeRevokedArtifact `json:"artifacts"`
}

// HomeRevokedArtifact is what the chain did for one artifact. Status is
// "revoked", "removed", "none-issued" (no code issues this artifact yet) or
// "not-current" (the step could not run and the detail says what remains).
type HomeRevokedArtifact struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// RevokeHomeDevice permanently revokes one device key for actor: a person
// revokes only their own account's devices, the Owner any device. A device of
// another account is reported as not found. Revocation only removes access,
// so it needs no fresh step-up; the key can never enroll again.
func RevokeHomeDevice(root, deviceID string, actor HomeActor, now time.Time) (HomeRevocation, error) {
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return HomeRevocation{}, err
	}
	device, ok := findHomeDevice(registry.Devices, deviceID)
	if !ok || (!actor.Owner && (actor.Account == "" || device.AccountSubject != actor.Account)) {
		return HomeRevocation{}, ErrHomeDeviceNotFound
	}
	if deviceID != actor.Device && homeActorInUndoWindow(registry.Devices, actor, now) {
		return HomeRevocation{}, ErrHomeActorInUndoWindow
	}
	reason := HomeRevokedByPerson
	if actor.Owner {
		reason = HomeRevokedByOwner
	}
	return revokeHomeDevices(root, func(entry HomeDevice) bool { return entry.ID == deviceID }, reason, actor, now)
}

// RevokeHomeAccount is the local part of removing a member: every device of
// the account (active, pending or lapsed) is revoked with all it holds, and
// the account joins the revoked accounts of the Owner-signed Home trust, so
// no passkey assertion of it is accepted again. The Owner's account is
// refused. Deleting the Pocket ID user is the caller's next step.
func RevokeHomeAccount(root, account string, now time.Time) (HomeRevocation, error) {
	now = now.UTC()
	if account == "" {
		return HomeRevocation{}, ErrHomeDeviceNotFound
	}
	state, stateErr := loadHomeVerifierState(root)
	if stateErr == nil && state.Trust.PocketID.Subject == account {
		return HomeRevocation{}, errors.New("identity: the Owner's account cannot be removed")
	}
	// The removed account is recorded first, so a failure of any later step
	// still keeps it from enrolling and every trust refresh revokes it.
	if err := updateHomeDeviceRegistry(root, func(registry *homeDeviceRegistry) error {
		if !slices.Contains(registry.RemovedAccounts, account) {
			registry.RemovedAccounts = append(registry.RemovedAccounts, account)
		}
		return nil
	}); err != nil {
		return HomeRevocation{}, err
	}
	report, err := revokeHomeDevices(root, func(entry HomeDevice) bool { return entry.AccountSubject == account }, HomeRevokedByRemoval, HomeActor{Owner: true}, now)
	if err != nil {
		return report, err
	}
	accountArtifact := HomeRevokedArtifact{ID: HomeArtifactAccount, Status: "revoked", Detail: "the account is a revoked account of the Owner-signed Home trust and can never enroll again; its passkey sign-ins are refused"}
	switch err := revokeHomeTrustSubject(root, account, now); {
	case errors.Is(err, fs.ErrNotExist):
		accountArtifact = HomeRevokedArtifact{ID: HomeArtifactAccount, Status: "revoked", Detail: "no Home trust is bound on this server yet; the account is recorded as removed and the first trust binding revokes it"}
	case err != nil:
		accountArtifact = HomeRevokedArtifact{ID: HomeArtifactAccount, Status: "not-current", Detail: "the Home trust could not be re-signed (" + err.Error() + "); the account is recorded as removed and the next trust refresh revokes it"}
	}
	report.Artifacts = append([]HomeRevokedArtifact{accountArtifact}, report.Artifacts...)
	return report, nil
}

// revokeHomeDevices revokes every matching entry in one registry write,
// resolves their notices, removes their VPN add-on peers and reports the
// artifacts.
func revokeHomeDevices(root string, match func(HomeDevice) bool, reason string, actor HomeActor, now time.Time) (HomeRevocation, error) {
	now = now.UTC()
	by := actor.Device
	if by == "" && actor.Owner {
		by = HomeRevokedByOwner
	}
	if by == "" {
		by = actor.Account
	}
	noticeState := HomeNoticeClosed
	if reason == HomeRevokedByUndo {
		noticeState = HomeNoticeUndone
	}
	var revoked []HomeDevice
	err := updateHomeDeviceRegistry(root, func(registry *homeDeviceRegistry) error {
		for index := range registry.Devices {
			device := &registry.Devices[index]
			if !match(*device) {
				continue
			}
			if !device.Revoked {
				device.Revoked, device.RevokedAt, device.RevokedBy = true, &now, reason
			}
			revoked = append(revoked, *device)
			resolveHomeNotices(registry, device.ID, noticeState, by, now)
		}
		return nil
	})
	if err != nil {
		return HomeRevocation{}, err
	}
	invalidateHomeVerifier(root)
	ids := make([]string, 0, len(revoked))
	for _, device := range revoked {
		ids = append(ids, device.ID)
	}
	report := HomeRevocation{Devices: revoked}
	report.Artifacts = append(report.Artifacts, HomeRevokedArtifact{
		ID: HomeArtifactDevice, Status: "revoked",
		Detail: fmt.Sprintf("%d registry entries revoked; their renewals stop at once and every verifier refuses their credentials within %s", len(revoked), homeRevocationRefresh),
	})
	removed, peerErr := removeHomeOverlayPeers(root, ids)
	switch {
	case errors.Is(peerErr, ErrHomeOverlayCustodyAbsent):
		report.Artifacts = append(report.Artifacts, HomeRevokedArtifact{ID: HomeArtifactVPNPeer, Status: "not-current",
			Detail: "this process holds no VPN add-on custody; the host's resync, which the registry change triggers, renders only active devices and drops the revoked devices' peer records"})
	case peerErr != nil:
		report.Artifacts = append(report.Artifacts, HomeRevokedArtifact{ID: HomeArtifactVPNPeer, Status: "not-current",
			Detail: "the VPN add-on peer registry could not be updated (" + peerErr.Error() + "); its rendered configuration still omits revoked devices"})
	default:
		report.Artifacts = append(report.Artifacts, HomeRevokedArtifact{ID: HomeArtifactVPNPeer, Status: "removed",
			Detail: fmt.Sprintf("%d VPN add-on peers removed from the peer registry and the rendered interface; a running interface drops them at its next resync", removed)})
	}
	// A coordinated VPN tool's peers are the host reconciler's: it deletes
	// them on this registry change and in its periodic pass.
	if announcement := homeVPNAnnouncement(root); announcement != nil && announcement.Tool != HomeVPNToolWireGuard {
		detail := fmt.Sprintf("the %s add-on's host reconciler (stackkit-vpn.service) deletes these devices' nodes and join keys on this registry change, with a full pass every 30 seconds", announcement.Tool)
		if homeVPNCoordinatedCustodyHeld(root) {
			pending := 0
			if peers, ledgerErr := ListHomeVPNPeers(root); ledgerErr == nil {
				for _, peer := range peers {
					if peer.Tool == announcement.Tool && slices.Contains(ids, peer.DeviceID) {
						pending++
					}
				}
			}
			detail += fmt.Sprintf(" (%d in its ledger)", pending)
		}
		for index := range report.Artifacts {
			if report.Artifacts[index].ID == HomeArtifactVPNPeer {
				report.Artifacts[index] = HomeRevokedArtifact{ID: HomeArtifactVPNPeer, Status: "not-current", Detail: detail}
			}
		}
	}
	report.Artifacts = append(report.Artifacts, HomeRevokedArtifact{ID: HomeArtifactSession, Status: "revoked",
		Detail: fmt.Sprintf("Access Gate browser sessions of these devices end: every session check needs an active device (within %s)", homeRevocationRefresh)})
	passes, passErr := revokeHomeAppPassesOfDevices(root, ids, now)
	switch {
	case passErr != nil:
		report.Artifacts = append(report.Artifacts, HomeRevokedArtifact{ID: HomeArtifactAppCredential, Status: "revoked",
			Detail: "the app pass records could not be updated (" + passErr.Error() + "); every app pass check needs an active device, so the passes are refused"})
	default:
		report.Artifacts = append(report.Artifacts, HomeRevokedArtifact{ID: HomeArtifactAppCredential, Status: "revoked",
			Detail: fmt.Sprintf("%d app passes revoked; no per-device app OIDC session is issued", passes)})
	}
	return report, nil
}

// revokeHomeTrustSubject re-signs the bound Home trust with the account
// added to its revoked accounts. It needs no Pocket ID: the pin is
// unchanged and only the revocation list grows.
func revokeHomeTrustSubject(root, account string, now time.Time) error {
	state, err := loadHomeVerifierState(root)
	if err != nil {
		return err
	}
	if slices.Contains(state.Trust.RevokedSubjects, account) {
		return nil
	}
	next := state.Trust
	next.RevokedSubjects = append(append([]string{}, state.Trust.RevokedSubjects...), account)
	next.IssuedAt = now.Truncate(time.Second)
	if !next.IssuedAt.After(state.Trust.IssuedAt) {
		next.IssuedAt = state.Trust.IssuedAt.Add(time.Second)
	}
	if !next.ExpiresAt.After(next.IssuedAt) || !next.ExpiresAt.After(now) {
		return errors.New("the bound Home trust has expired; every passkey verification already fails closed")
	}
	return BindHomePublicTrust(root, next)
}
