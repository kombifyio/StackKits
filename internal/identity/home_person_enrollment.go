package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"sort"
	"time"
)

// Enrollment by the person (homelab identity target §6.2, D-2026-11-02-11):
// the client generates the device key, signs its person in with their own
// passkey through the device sign-in client, and submits the key with a DPoP
// proof. The entry binds to that person's Homelab account. At home, which
// only the Authority's LAN enrollment listener attests, the entry is approved
// at once and the person's other devices and the Owner get a notice with a
// 24 hour undo. Away, the entry waits for a passkey step-up from the
// person's own enrolled device, or the Owner's at the Authority when the
// person has none.

// Notice kinds and states (records the device clients and the State Console
// deliver; Web Push and the Companion Feed are later slices).
const (
	HomeNoticeEnrolledAtHome    = "enrolled-at-home"
	HomeNoticeEnrollmentPending = "enrollment-pending"

	HomeNoticeOpen      = "open"
	HomeNoticeUndone    = "undone"
	HomeNoticeConfirmed = "confirmed"
	HomeNoticeClosed    = "closed"

	// Confirmers of an away enrollment, in the contract's order.
	HomeConfirmerOwnDevice = "own-enrolled-device"
	HomeConfirmerOwner     = "owner"

	homeNoticeRetention   = 7 * 24 * time.Hour
	homeNoticeCapacity    = 512
	homeNoticesPerAccount = 32
	homePendingCapacity   = 64
	homePendingPerAccount = 4
	homePendingRetention  = 7 * 24 * time.Hour
	homeAttemptsRetention = 24 * time.Hour
)

var (
	ErrHomeEnrollmentRateLimited = errors.New("identity: too many enrollment attempts for this account; try again later")
	ErrHomeDeviceCapReached      = errors.New("identity: this account has reached its device limit; revoke a device first")
	ErrHomeEnrollmentPending     = errors.New("identity: this device's enrollment waits for its confirmation")
	ErrHomeNoticeNotFound        = errors.New("identity: no open notice with this id is visible to this caller")
	// ErrHomeActorInUndoWindow refuses a device enrolled at home less than
	// 24 hours ago when it acts on another device: within its own undo window
	// it may revoke only itself, so a device enrolled by someone else cannot
	// lock the person out before they undo it.
	ErrHomeActorInUndoWindow = errors.New("identity: a device enrolled at home within the last 24 hours can revoke only itself")
)

// HomeEnrollmentSubmission is one enrollment as the server observed it.
// Proof is a DPoP proof (htm POST) by the device-generated key; the key's
// thumbprint becomes the device ID. Person is a Pocket ID ID token of the
// enrolling person from a passkey sign-in through HomeDeviceClientID with the
// nonce HomeDeviceEnrollmentNonce(thumbprint, proof jti). Home is set only by
// the LAN enrollment listener, for a connection it accepted directly; Targets
// are then that listener's enrollment URLs from the server's own custody (the
// site address and the Hub host on the listener port), one of which the proof
// must bind.
type HomeEnrollmentSubmission struct {
	Name    string
	Custody string
	Proof   string
	Person  string
	Home    bool
	Targets []string
}

// HomeEnrollmentOutcome is the answer to a submission: at home the active
// entry and its first credential, away the pending entry. Notice is the
// record the person's other devices and the Owner receive.
type HomeEnrollmentOutcome struct {
	Status     string                `json:"status"`
	Device     HomeDevice            `json:"device"`
	Credential *HomeDeviceCredential `json:"credential,omitempty"`
	Notice     HomeEnrollmentNotice  `json:"notice"`
	// VPN announces the VPN add-on's peer service to a device that received
	// its credential, so it asks for its peer in the same enrollment.
	VPN *HomeVPNAnnouncement `json:"vpn,omitempty"`
}

// HomeEnrollmentNotice tells the person's other devices and the Owner about
// one enrollment. An at-home notice offers the undo until ActionUntil; a
// pending notice asks for the confirmation until ActionUntil, by the
// Confirmer (computed when it is listed). StepUp is the action a
// confirmation step-up approves. NotifyDevices are the person's devices that
// were enrolled when the notice was created.
type HomeEnrollmentNotice struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	AccountSubject string            `json:"accountSubject"`
	DeviceID       string            `json:"deviceId"`
	DeviceName     string            `json:"deviceName"`
	Custody        string            `json:"custody"`
	CreatedAt      time.Time         `json:"createdAt"`
	ActionUntil    time.Time         `json:"actionUntil"`
	NotifyDevices  []string          `json:"notifyDevices"`
	NotifyOwner    bool              `json:"notifyOwner"`
	Confirmer      string            `json:"confirmer,omitempty"`
	StepUp         *HomeStepUpAction `json:"stepUp,omitempty"`
	State          string            `json:"state"`
	ResolvedAt     *time.Time        `json:"resolvedAt,omitempty"`
	ResolvedBy     string            `json:"resolvedBy,omitempty"`
}

// HomeActor is who acts on registry entries: a device of a Homelab account
// (verified by its credential and proof), or the Owner, who is either a
// device of the Owner's account or the local CLI at the Authority.
type HomeActor struct {
	Account string
	Device  string
	Owner   bool
}

// HomeDeviceEnrollmentNonce is the OIDC nonce of the passkey sign-in that
// enrolls a device: base64url(SHA-256("stackkit-device-enrollment/v1" 0x00
// thumbprint 0x00 jti)) of the device key and the one DPoP proof that
// presents it.
func HomeDeviceEnrollmentNonce(thumbprint, proofID string) string {
	digest := sha256.Sum256([]byte("stackkit-device-enrollment/v1\x00" + thumbprint + "\x00" + proofID))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// Enroll decides one enrollment submission. It verifies the proof of
// possession and the person's fresh passkey sign-in against the current
// Home trust, applies the rate limit and the device cap of the applied
// #DeviceEnrollmentPolicy, and records the entry: active with its first
// credential at home, pending away. Every failure before the decision is
// ErrHomeAccessDenied; the decision's own refusals are their named errors.
// It fails closed whenever the trust, the registry or the policy is not
// current (partition rule).
func (v *HomeVerifier) Enroll(submission HomeEnrollmentSubmission) (HomeEnrollmentOutcome, error) {
	deny := func() (HomeEnrollmentOutcome, error) { return HomeEnrollmentOutcome{}, ErrHomeAccessDenied }
	now := v.now().UTC()
	proof, err := parseHomeDeviceProof(submission.Proof, now)
	if err != nil || proof.Method != http.MethodPost || !v.afterStart(proof.IssuedAt) || !ValidHomeDeviceName(submission.Name) {
		return deny()
	}
	if submission.Home {
		if proof.Target == "" || !slices.Contains(submission.Targets, proof.Target) {
			return deny()
		}
	} else if !homeIdentityOriginAdmitted(v.root, proof.Target, HomeEnrollmentPath) {
		return deny()
	}
	if !validHomeCustody(submission.Custody) {
		return deny()
	}
	// The person's passkey sign-in is checked before the applied plan is
	// read, so a self-made proof costs one signature check, not a plan parse.
	trust, err := loadCurrentHomeTrust(v.root, now)
	if err != nil {
		return deny()
	}
	person, err := verifyHomePasskeyIDToken(submission.Person, trust, HomeDeviceClientID, HomeDeviceEnrollmentNonce(proof.Thumbprint, proof.ID), now)
	if err != nil || person.IssuedAt == nil {
		return deny()
	}
	verified := person.IssuedAt.Time().UTC()
	if age := now.Sub(verified); age < -HomeClockLeeway || age > HomeUserVerificationFreshness || !v.afterStart(verified) {
		return deny()
	}
	if verified.After(now) {
		verified = now
	}
	account := person.Subject
	policy, err := LoadHomeEnrollmentPolicy(v.root)
	if err != nil {
		return HomeEnrollmentOutcome{}, err
	}
	if !slices.Contains(policy.CustodyClasses, submission.Custody) {
		return deny()
	}
	v.mu.Lock()
	fresh := v.rememberLocked(proof.Thumbprint, proof.ID, now.Add(homeRenewalReplayRetention), now)
	v.mu.Unlock()
	if !fresh {
		return deny()
	}
	var issuer homeDeviceIssuer
	if submission.Home {
		if issuer, err = ensureHomeDeviceIssuer(v.root, now); err != nil {
			return deny()
		}
	}
	noticeID, err := homeRandomID()
	if err != nil {
		return deny()
	}
	device := HomeDevice{
		ID: proof.Thumbprint, Name: submission.Name, AccountSubject: account, Custody: submission.Custody, PublicKey: proof.Key,
		EnrolledAt: now, LastUsedAt: now, LastUserVerifiedAt: verified,
	}
	notice := HomeEnrollmentNotice{
		ID: noticeID, AccountSubject: account, DeviceID: device.ID, DeviceName: device.Name, Custody: device.Custody,
		CreatedAt: now, NotifyOwner: true, State: HomeNoticeOpen,
	}
	if submission.Home {
		device.Approval = HomeDeviceApproval{Mode: HomeApprovalHome, At: &now}
		notice.Kind, notice.ActionUntil = HomeNoticeEnrolledAtHome, now.Add(HomeEnrollmentUndoWindow)
	} else {
		until := now.Add(HomeEnrollmentPendingLifetime)
		device.Approval = HomeDeviceApproval{Mode: HomeApprovalPending, PendingUntil: &until}
		action, actionErr := homeEnrollConfirmAction(device)
		if actionErr != nil {
			return deny()
		}
		notice.Kind, notice.ActionUntil, notice.StepUp = HomeNoticeEnrollmentPending, until, &action
	}
	var refusal error
	err = updateHomeDeviceRegistry(v.root, func(registry *homeDeviceRegistry) error {
		pruneHomeRegistry(registry, now, policy.RateLimitWindow, account)
		attempts := 0
		for _, attempt := range registry.Attempts {
			if attempt.Account == account && now.Sub(attempt.At) < policy.RateLimitWindow {
				attempts++
			}
		}
		if attempts >= policy.RateLimitAttempts {
			return ErrHomeEnrollmentRateLimited
		}
		registry.Attempts = append(registry.Attempts, homeEnrollmentAttempt{Account: account, At: now})
		refusal = admitHomeEnrollment(registry, device, policy, now)
		if refusal != nil {
			return nil
		}
		for _, other := range registry.Devices {
			if other.AccountSubject == account && other.ID != device.ID && other.Active(now) {
				notice.NotifyDevices = append(notice.NotifyDevices, other.ID)
			}
		}
		kept := registry.Devices[:0]
		for _, existing := range registry.Devices {
			if existing.ID != device.ID {
				kept = append(kept, existing)
			}
		}
		registry.Devices = append(kept, device)
		registry.Notices = append(registry.Notices, notice)
		if homeRegistrySize(*registry) > homeDeviceRegistryGrowthBytes {
			// Keep the counted attempt; refuse the entry.
			registry.Devices = registry.Devices[:len(registry.Devices)-1]
			registry.Notices = registry.Notices[:len(registry.Notices)-1]
			refusal = ErrHomeRegistryFull
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrHomeEnrollmentRateLimited) {
			return HomeEnrollmentOutcome{}, err
		}
		return deny()
	}
	if refusal != nil {
		return HomeEnrollmentOutcome{}, refusal
	}
	invalidateHomeVerifier(v.root)
	outcome := HomeEnrollmentOutcome{Status: "pending", Device: device, Notice: notice}
	if submission.Home {
		credential, mintErr := issuer.mint(device, now)
		if mintErr != nil {
			return deny()
		}
		outcome.Status, outcome.Credential, outcome.VPN = "enrolled", &credential, homeVPNAnnouncement(v.root)
	}
	return outcome, nil
}

// admitHomeEnrollment applies the registry rules to a new entry: a revoked
// key never enrolls again, a key that is active or pending is not enrolled
// twice, device names are unique per account, the account stays within its
// device cap and the Authority within its pending capacity.
func admitHomeEnrollment(registry *homeDeviceRegistry, device HomeDevice, policy HomeEnrollmentPolicy, now time.Time) error {
	if slices.Contains(registry.RemovedAccounts, device.AccountSubject) {
		return ErrHomeAccessDenied
	}
	if slices.Contains(registry.RevokedKeys, device.ID) {
		return ErrHomeDeviceRevoked
	}
	slots, pending, accountPending := 0, 0, 0
	for _, existing := range registry.Devices {
		if existing.ID == device.ID {
			if existing.Revoked {
				return ErrHomeDeviceRevoked
			}
			if existing.holdsSlot(now) {
				return ErrHomeDeviceConflict
			}
			continue
		}
		if existing.Pending(now) {
			pending++
		}
		if existing.AccountSubject != device.AccountSubject || !existing.holdsSlot(now) {
			continue
		}
		if existing.Name == device.Name {
			return ErrHomeDeviceConflict
		}
		if existing.Pending(now) {
			accountPending++
		}
		slots++
	}
	if slots >= policy.MaxDevicesPerAccount {
		return ErrHomeDeviceCapReached
	}
	if device.Approval.Mode == HomeApprovalPending && (pending >= homePendingCapacity || accountPending >= homePendingPerAccount) {
		return ErrHomeEnrollmentRateLimited
	}
	return nil
}

// homeEnrollConfirmAction is the enroll-confirm step-up action of a pending
// entry: the exact key, account, name, custody class and submission time.
func homeEnrollConfirmAction(device HomeDevice) (HomeStepUpAction, error) {
	return NewHomeStepUpAction(HomeStepUpEnrollConfirm, struct {
		DeviceID    string    `json:"deviceId"`
		Account     string    `json:"account"`
		Name        string    `json:"name"`
		Custody     string    `json:"custody"`
		SubmittedAt time.Time `json:"submittedAt"`
	}{device.ID, device.AccountSubject, device.Name, device.Custody, device.EnrolledAt.UTC()})
}

// ConfirmEnrollmentFromDevice confirms the pending enrollment deviceID from
// an enrolled device: one of the same person's (the confirmer while the
// person has one), or else one of the Owner's (D-2026-11-02-11: own device
// first, the Owner otherwise). caller is the result of Authenticate for the
// confirming request; idToken is the confirmer's passkey step-up through
// HomeDeviceClientID for the entry's enroll-confirm action and challenge.
func (v *HomeVerifier) ConfirmEnrollmentFromDevice(caller HomeAuthentication, deviceID, challenge, idToken string) (HomeDevice, error) {
	now := v.now().UTC()
	registry, err := loadHomeDeviceRegistry(v.root)
	if err != nil {
		return HomeDevice{}, ErrHomeAccessDenied
	}
	pending, ok := findHomeDevice(registry.Devices, deviceID)
	if !ok || !pending.Pending(now) || caller.Subject == "" || caller.Device == deviceID {
		return HomeDevice{}, ErrHomeNoticeNotFound
	}
	byOwner := caller.Subject != pending.AccountSubject
	if byOwner {
		trust, trustErr := loadCurrentHomeTrust(v.root, now)
		if trustErr != nil || trust.PocketID.Subject != caller.Subject || homeConfirmer(registry.Devices, pending, now) != HomeConfirmerOwner {
			return HomeDevice{}, ErrHomeNoticeNotFound
		}
	}
	confirmingActive := func(devices []HomeDevice) bool {
		confirming, ok := findHomeDevice(devices, caller.Device)
		return ok && confirming.Active(now) && confirming.AccountSubject == caller.Subject
	}
	if !confirmingActive(registry.Devices) {
		return HomeDevice{}, ErrHomeAccessDenied
	}
	action, err := homeEnrollConfirmAction(pending)
	if err != nil {
		return HomeDevice{}, ErrHomeAccessDenied
	}
	if _, err := VerifyHomeStepUp(v.root, action, HomeStepUpApprover{Account: caller.Subject}, challenge, idToken, now); err != nil {
		return HomeDevice{}, err
	}
	mode := HomeApprovalOwnDevice
	if byOwner {
		mode = HomeApprovalOwner
	}
	return confirmHomeEnrollment(v.root, pending, action, mode, caller.Device, now, func(registry *homeDeviceRegistry, entry HomeDevice) error {
		if !confirmingActive(registry.Devices) {
			return ErrHomeAccessDenied
		}
		// The person's own device confirms whenever one became active.
		if byOwner && homeConfirmer(registry.Devices, entry, now) != HomeConfirmerOwner {
			return ErrHomeNoticeNotFound
		}
		return nil
	})
}

// ConfirmHomeEnrollmentByOwner confirms a pending enrollment with the Owner's
// passkey step-up at the Authority (the Owner step-up client) for the entry's
// enroll-confirm action and challenge. The Owner confirms only for a person
// who has no enrolled device of their own; otherwise that device confirms.
func ConfirmHomeEnrollmentByOwner(root, deviceID, challenge, idToken string, now time.Time) (HomeDevice, error) {
	now = now.UTC()
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return HomeDevice{}, ErrHomeAccessDenied
	}
	pending, ok := findHomeDevice(registry.Devices, deviceID)
	if !ok || !pending.Pending(now) || homeConfirmer(registry.Devices, pending, now) != HomeConfirmerOwner {
		return HomeDevice{}, ErrHomeNoticeNotFound
	}
	action, err := homeEnrollConfirmAction(pending)
	if err != nil {
		return HomeDevice{}, ErrHomeAccessDenied
	}
	owner, err := VerifyHomeStepUp(root, action, HomeStepUpApprover{Owner: true}, challenge, idToken, now)
	if err != nil {
		return HomeDevice{}, err
	}
	return confirmHomeEnrollment(root, pending, action, HomeApprovalOwner, owner, now, func(registry *homeDeviceRegistry, entry HomeDevice) error {
		if homeConfirmer(registry.Devices, entry, now) != HomeConfirmerOwner {
			return ErrHomeNoticeNotFound
		}
		return nil
	})
}

// PendingHomeEnrollment returns a pending entry and its enroll-confirm action
// for the Owner's confirmation page, only while the Owner is its confirmer.
func PendingHomeEnrollment(root, deviceID string, now time.Time) (HomeDevice, HomeStepUpAction, error) {
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return HomeDevice{}, HomeStepUpAction{}, ErrHomeAccessDenied
	}
	pending, ok := findHomeDevice(registry.Devices, deviceID)
	if !ok || !pending.Pending(now) || homeConfirmer(registry.Devices, pending, now) != HomeConfirmerOwner {
		return HomeDevice{}, HomeStepUpAction{}, ErrHomeNoticeNotFound
	}
	action, err := homeEnrollConfirmAction(pending)
	return pending, action, err
}

// confirmHomeEnrollment activates a pending entry. Under the registry lock it
// checks again that the entry is the submission the step-up approved, that
// the confirmer rule still holds (approver) and that the cap is kept.
func confirmHomeEnrollment(root string, pending HomeDevice, action HomeStepUpAction, mode, by string, now time.Time, approver func(*homeDeviceRegistry, HomeDevice) error) (HomeDevice, error) {
	policy, err := LoadHomeEnrollmentPolicy(root)
	if err != nil {
		return HomeDevice{}, err
	}
	var confirmed HomeDevice
	err = updateHomeDeviceRegistry(root, func(registry *homeDeviceRegistry) error {
		slots := 0
		var entry *HomeDevice
		for index := range registry.Devices {
			existing := &registry.Devices[index]
			if existing.ID == pending.ID {
				entry = existing
			} else if existing.AccountSubject == pending.AccountSubject && existing.Active(now) {
				slots++
			}
		}
		if entry == nil || !entry.Pending(now) {
			return ErrHomeNoticeNotFound
		}
		// The step-up approved exactly this submission.
		if current, actionErr := homeEnrollConfirmAction(*entry); actionErr != nil || current != action {
			return ErrHomeStepUpDenied
		}
		if err := approver(registry, *entry); err != nil {
			return err
		}
		if slots >= policy.MaxDevicesPerAccount {
			return ErrHomeDeviceCapReached
		}
		entry.Approval = HomeDeviceApproval{Mode: mode, By: by, At: &now}
		entry.LastUsedAt = now
		confirmed = *entry
		resolveHomeNotices(registry, pending.ID, HomeNoticeConfirmed, by, now)
		return nil
	})
	if err != nil {
		return HomeDevice{}, err
	}
	invalidateHomeVerifier(root)
	return confirmed, nil
}

// ListHomeEnrollmentNotices returns the notices visible to actor, newest
// first: those of the actor's own account and, for the Owner, every notice.
// A notice is open only while its action window lasts and its device is
// still in the state the notice is about; Confirmer reflects the current
// registry.
func ListHomeEnrollmentNotices(root string, actor HomeActor, now time.Time) ([]HomeEnrollmentNotice, error) {
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return nil, err
	}
	result := []HomeEnrollmentNotice{}
	for _, notice := range registry.Notices {
		if !actor.Owner && (actor.Account == "" || notice.AccountSubject != actor.Account) {
			continue
		}
		device, _ := findHomeDevice(registry.Devices, notice.DeviceID)
		if notice.State == HomeNoticeOpen && !homeNoticeActionable(notice, device, now) {
			notice.State = HomeNoticeClosed
		}
		if notice.Kind == HomeNoticeEnrollmentPending && notice.State == HomeNoticeOpen {
			notice.Confirmer = homeConfirmer(registry.Devices, device, now)
		}
		notice.NotifyDevices = append([]string{}, notice.NotifyDevices...)
		result = append(result, notice)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

// UndoHomeEnrollment undoes an enrollment within its notice's action window:
// it revokes the enrolled device (an at-home enrollment within its 24 hour
// undo, or a pending one it declines) through the revocation chain. The
// person's devices and the Owner may undo; nobody else sees the notice.
func UndoHomeEnrollment(root, noticeID string, actor HomeActor, now time.Time) (HomeRevocation, error) {
	now = now.UTC()
	var target string
	err := updateHomeDeviceRegistry(root, func(registry *homeDeviceRegistry) error {
		for index := range registry.Notices {
			notice := &registry.Notices[index]
			if notice.ID != noticeID {
				continue
			}
			if !actor.Owner && (actor.Account == "" || actor.Account != notice.AccountSubject) {
				return ErrHomeNoticeNotFound
			}
			device, _ := findHomeDevice(registry.Devices, notice.DeviceID)
			if notice.State != HomeNoticeOpen || !homeNoticeActionable(*notice, device, now) {
				return ErrHomeNoticeNotFound
			}
			if notice.DeviceID != actor.Device && homeActorInUndoWindow(registry.Devices, actor, now) {
				return ErrHomeActorInUndoWindow
			}
			target = notice.DeviceID
			return nil
		}
		return ErrHomeNoticeNotFound
	})
	if err != nil {
		return HomeRevocation{}, err
	}
	return revokeHomeDevices(root, func(device HomeDevice) bool { return device.ID == target }, "undo", actor, now)
}

// homeNoticeActionable reports whether a notice's undo or confirmation is
// still possible: inside its window, and its device still active (at home)
// or pending (away).
func homeNoticeActionable(notice HomeEnrollmentNotice, device HomeDevice, now time.Time) bool {
	if !now.Before(notice.ActionUntil) || device.ID != notice.DeviceID {
		return false
	}
	if notice.Kind == HomeNoticeEnrollmentPending {
		return device.Pending(now)
	}
	return device.Active(now)
}

// homeActorInUndoWindow reports whether the acting device (not the local
// Owner console) was enrolled at home less than 24 hours ago. A device
// confirmed by a step-up is outside this rule.
func homeActorInUndoWindow(devices []HomeDevice, actor HomeActor, now time.Time) bool {
	if actor.Device == "" {
		return false
	}
	device, ok := findHomeDevice(devices, actor.Device)
	return !ok || device.Approval.Mode == HomeApprovalHome && now.Before(device.EnrolledAt.Add(HomeEnrollmentUndoWindow))
}

// homeConfirmer is who confirms a pending entry now: the person's own
// enrolled device where one exists, the Owner otherwise.
func homeConfirmer(devices []HomeDevice, pending HomeDevice, now time.Time) string {
	for _, device := range devices {
		if device.ID != pending.ID && device.AccountSubject == pending.AccountSubject && device.Active(now) {
			return HomeConfirmerOwnDevice
		}
	}
	return HomeConfirmerOwner
}

func resolveHomeNotices(registry *homeDeviceRegistry, deviceID, state, by string, now time.Time) {
	for index := range registry.Notices {
		notice := &registry.Notices[index]
		if notice.DeviceID == deviceID && notice.State == HomeNoticeOpen {
			notice.State, notice.ResolvedAt, notice.ResolvedBy = state, &now, by
		}
	}
}

// pruneHomeRegistry bounds the registry before an enrollment of account:
// rate-limit attempts outside the policy window, notices resolved or expired
// a week ago, lapsed pending entries a week after they lapsed, and the
// oldest notices beyond the account's and the registry's capacity.
func pruneHomeRegistry(registry *homeDeviceRegistry, now time.Time, window time.Duration, account string) {
	if window <= 0 || window > homeAttemptsRetention {
		window = homeAttemptsRetention
	}
	attempts := registry.Attempts[:0]
	for _, attempt := range registry.Attempts {
		if now.Sub(attempt.At) < window {
			attempts = append(attempts, attempt)
		}
	}
	registry.Attempts = attempts
	devices := registry.Devices[:0]
	removed := map[string]bool{}
	for _, device := range registry.Devices {
		if device.Approval.Mode == HomeApprovalPending && device.Approval.PendingUntil != nil && !device.Revoked &&
			now.Sub(*device.Approval.PendingUntil) > homePendingRetention {
			removed[device.ID] = true
			continue
		}
		devices = append(devices, device)
	}
	registry.Devices = devices
	notices := registry.Notices[:0]
	for _, notice := range registry.Notices {
		ended := notice.ActionUntil
		if notice.ResolvedAt != nil && notice.ResolvedAt.Before(ended) {
			ended = *notice.ResolvedAt
		}
		if removed[notice.DeviceID] || now.Sub(ended) > homeNoticeRetention {
			continue
		}
		notices = append(notices, notice)
	}
	notices = dropOldestHomeNotices(notices, homeNoticesPerAccount, func(notice HomeEnrollmentNotice) bool { return notice.AccountSubject == account })
	notices = dropOldestHomeNotices(notices, homeNoticeCapacity, func(HomeEnrollmentNotice) bool { return true })
	registry.Notices = notices
}

// dropOldestHomeNotices drops the oldest matching notices, resolved ones
// first, until fewer than capacity match, leaving room for one more.
func dropOldestHomeNotices(notices []HomeEnrollmentNotice, capacity int, match func(HomeEnrollmentNotice) bool) []HomeEnrollmentNotice {
	for {
		count, oldest, oldestOpen := 0, -1, -1
		for index, notice := range notices {
			if !match(notice) {
				continue
			}
			count++
			if notice.State != HomeNoticeOpen && (oldest < 0 || notice.CreatedAt.Before(notices[oldest].CreatedAt)) {
				oldest = index
			}
			if oldestOpen < 0 || notice.CreatedAt.Before(notices[oldestOpen].CreatedAt) {
				oldestOpen = index
			}
		}
		if count < capacity {
			return notices
		}
		if oldest < 0 {
			oldest = oldestOpen
		}
		notices = append(notices[:oldest], notices[oldest+1:]...)
	}
}

func findHomeDevice(devices []HomeDevice, id string) (HomeDevice, bool) {
	for _, device := range devices {
		if device.ID == id {
			return device, true
		}
	}
	return HomeDevice{}, false
}

func homeRandomID() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
