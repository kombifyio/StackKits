package identity

// Owner recovery at the Authority (homelab identity target §9, §12, §14.9).
//
// The Owner holds two recovery secrets, each shown exactly once at setup and
// never stored by StackKits:
//
//   - the recovery key, an age hybrid (ML-KEM-768 + X25519) identity that
//     decrypts the recovery set (bundle.go). The Authority keeps only its
//     public recipient, so it can write new sets but can never read one;
//   - the recovery code, a single-use code that lets the Owner register a new
//     passkey: at the local console at once, or from an enrolled Owner device
//     after 24 hours with a notice to every Owner device, any of which (or the
//     console) can cancel the request in between.
//
// The break-glass bundle is the encrypted recovery set; there is no
// break-glass account and no long-lived Pocket ID setup token. A redeemed code
// yields one short-lived Pocket ID one-time link for the bound Owner subject.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	stackcrypto "github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

const (
	RecoveryRequestPath  = "/api/v1/identity/recovery/request"
	RecoveryCompletePath = "/api/v1/identity/recovery/complete"
	RecoveryCancelPath   = "/api/v1/identity/recovery/cancel"
	RecoveryNoticesPath  = "/api/v1/identity/recovery/notices"
	// RecoveryDeviceNoticesPath lets an enrolled Owner device fetch the
	// notices addressed to it with a DPoP proof (GET), until push delivery
	// (Companion Feed, Hub Web Push) carries them.
	RecoveryDeviceNoticesPath = "/api/v1/identity/recovery/device-notices"

	// RecoveryDeviceDelay is how long a recovery started from an enrolled
	// Owner device waits, with notices to every Owner device, before its code
	// registers a new passkey. It is not a caller choice.
	RecoveryDeviceDelay = 24 * time.Hour
	// RecoveryRequestWindow is how long a pending device recovery stays
	// completable once it became eligible.
	RecoveryRequestWindow = 72 * time.Hour
	// RecoveryPasskeyLinkLifetime bounds the Pocket ID one-time link a
	// redeemed recovery code yields.
	// Pocket ID issues a six-character typed login code for a lifetime of
	// 15 minutes or less, which its one-time link page does not redeem, so the
	// link lives 20 minutes and carries a full token.
	RecoveryPasskeyLinkLifetime = 20 * time.Minute

	// Notice kinds; each notice lists the Owner devices it is addressed to.
	RecoveryNoticeRequested    = "recovery-requested"
	RecoveryNoticeCompleted    = "recovery-completed"
	RecoveryNoticeCancelled    = "recovery-cancelled"
	RecoveryNoticeRedeemed     = "recovery-redeemed-at-console"
	RecoveryNoticeCodeRejected = "recovery-code-rejected"
	RecoveryNoticeCodeRotated  = "recovery-code-rotated"
	RecoveryNoticeRotated      = "recovery-rotated"
	RecoveryNoticeRestored     = "recovery-restored"

	recoveryStatePath   = ".stackkit/custody/recovery/state.json"
	recoveryStateSchema = "stackkit.identity-recovery-state/v1"
	// The secret-free notice projection in the verification custody.
	recoveryNoticesProjectionPath   = ".stackkit/custody/home-verifier/recovery-notices.json"
	recoveryNoticesProjectionSchema = "stackkit.identity-recovery-notices/v1"
	recoveryStateMaxBytes           = 512 << 10
	recoveryNoticeLimit             = 256
	recoveryCodeBytes               = 15 // 120 bits, 24 base32 characters
	recoveryCodeGroup               = 4
	// A device may present at most this many wrong codes per hour; the local
	// console is never throttled.
	recoveryDeviceFailureLimit  = 5
	recoveryDeviceFailureWindow = time.Hour
	recoveryMaxBodyBytes        = 4 << 10
)

var (
	ErrRecoveryNotConfigured = errors.New("identity: recovery is not set up on this Authority; run `stackkit break-glass setup --owner-approve`")
	ErrRecoveryConfigured    = errors.New("identity: recovery is already set up; replace it with `stackkit break-glass setup --rotate --owner-approve`")
	// ErrRecoveryDenied is the one answer to a wrong, consumed or throttled
	// code and to a refused device proof, so a refusal reveals no reason.
	ErrRecoveryDenied  = errors.New("identity: recovery code or Owner device proof refused")
	ErrRecoveryNotYet  = errors.New("identity: a device recovery becomes eligible 24 hours after it was requested")
	ErrRecoveryPending = errors.New("identity: another Owner device recovery is pending; cancel it first")

	recoveryCodeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)
)

// RecoverySecrets are shown to the Owner once and never persisted.
type RecoverySecrets struct {
	// RecoveryKey decrypts every recovery set ("AGE-SECRET-KEY-PQ-1...").
	RecoveryKey string `json:"recoveryKey"`
	// RecoveryCode registers a new Owner passkey once.
	RecoveryCode string `json:"recoveryCode"`
	// Recipient is the public half the Authority keeps to write sets.
	Recipient string `json:"recipient"`
}

// RecoveryRequest is a pending recovery started from an enrolled Owner device.
type RecoveryRequest struct {
	ID          string    `json:"id"`
	Device      string    `json:"device"`
	Account     string    `json:"account"`
	RequestedAt time.Time `json:"requestedAt"`
	EligibleAt  time.Time `json:"eligibleAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// RecoveryNotice is one recovery event addressed to the Owner devices that
// were enrolled when it happened. Delivery (Companion Feed, Hub Web Push,
// State Console) reads these records; they hold no secret.
type RecoveryNotice struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Account    string     `json:"account"`
	Devices    []string   `json:"devices"`
	Origin     string     `json:"origin"`
	RequestID  string     `json:"requestId,omitempty"`
	EligibleAt *time.Time `json:"eligibleAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// RecoveryStatus is the secret-free recovery state of the Authority.
type RecoveryStatus struct {
	Configured   bool             `json:"configured"`
	Recipient    string           `json:"recipient,omitempty"`
	ConfiguredAt time.Time        `json:"configuredAt,omitzero"`
	CodeIssuedAt time.Time        `json:"codeIssuedAt,omitzero"`
	CodeConsumed bool             `json:"codeConsumed"`
	Pending      *RecoveryRequest `json:"pending,omitempty"`
}

// RecoveryPasskeyLink is the Pocket ID one-time link a redeemed code yields.
type RecoveryPasskeyLink struct {
	URL       string    `json:"passkeyRegistrationUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// RecoveryPasskeyIssuer mints the one-time link for the bound Owner subject.
type RecoveryPasskeyIssuer func(ctx context.Context, subject string) (RecoveryPasskeyLink, error)

// RecoveryDeviceRequest is a recovery call by an enrolled Owner device: a DPoP
// proof by its registry key for the exact route, and the recovery code where
// the route takes one. No device credential is needed: the device may no
// longer pass a user verification once the passkey is gone.
type RecoveryDeviceRequest struct {
	Method string
	Proof  string
	Code   string
}

type recoveryCodeRecord struct {
	Verifier   string     `json:"verifier"`
	IssuedAt   time.Time  `json:"issuedAt"`
	ConsumedAt *time.Time `json:"consumedAt,omitempty"`
}

type recoveryState struct {
	Schema       string    `json:"schema"`
	Recipient    string    `json:"recipient"`
	ConfiguredAt time.Time `json:"configuredAt"`
	// SetAuthKey authenticates the sets this Authority writes. It is derived
	// one-way from the recovery key and cannot decrypt a set (bundle.go).
	SetAuthKey     []byte             `json:"setAuthKey"`
	Code           recoveryCodeRecord `json:"code"`
	Pending        *RecoveryRequest   `json:"pending,omitempty"`
	DeviceFailures []time.Time        `json:"deviceFailures,omitempty"`
	Notices        []RecoveryNotice   `json:"notices"`
}

// SetupRecovery creates the recovery key and code once and returns both for a
// single display; only the public recipient and a slow verifier of the code
// are stored. With rotate it replaces an existing key and code (the Owner
// lost one of them but still runs the Authority): every pending device
// recovery ends and sets written before stay readable only with the old key.
func SetupRecovery(root string, rotate bool, now time.Time) (RecoverySecrets, error) {
	now = now.UTC()
	if _, err := localevidence.LoadOwnerCustody(root); err != nil {
		return RecoverySecrets{}, err
	}
	var secrets RecoverySecrets
	err := updateRecoveryState(root, true, func(state *recoveryState, exists bool) error {
		if exists && !rotate {
			return ErrRecoveryConfigured
		}
		identity, err := age.GenerateHybridIdentity()
		if err != nil {
			return err
		}
		authKey, err := recoverySetAuthKey(identity)
		if err != nil {
			return err
		}
		code, record, err := newRecoveryCode(now)
		if err != nil {
			return err
		}
		secrets = RecoverySecrets{RecoveryKey: identity.String(), RecoveryCode: code, Recipient: identity.Recipient().String()}
		state.Recipient, state.ConfiguredAt, state.Code, state.SetAuthKey = secrets.Recipient, now, record, authKey
		state.DeviceFailures = nil
		if exists {
			ended := state.Pending
			state.Pending = nil
			addRecoveryNotice(root, state, RecoveryNoticeRotated, "console", ended, now)
		}
		return nil
	})
	if err != nil {
		return RecoverySecrets{}, err
	}
	return secrets, nil
}

// RotateRecoveryCode replaces the recovery code, for example after it was
// consumed, and returns the new code for a single display. A pending device
// recovery ends, because it was bound to the old code.
func RotateRecoveryCode(root string, now time.Time) (string, error) {
	now = now.UTC()
	var code string
	err := updateRecoveryState(root, false, func(state *recoveryState, _ bool) error {
		next, record, err := newRecoveryCode(now)
		if err != nil {
			return err
		}
		code, state.Code, state.DeviceFailures = next, record, nil
		ended := state.Pending
		state.Pending = nil
		addRecoveryNotice(root, state, RecoveryNoticeCodeRotated, "console", ended, now)
		return nil
	})
	return code, err
}

// LoadRecoveryStatus reads the secret-free recovery state.
func LoadRecoveryStatus(root string) (RecoveryStatus, error) {
	state, err := loadRecoveryState(root)
	if errors.Is(err, fs.ErrNotExist) {
		return RecoveryStatus{}, nil
	}
	if err != nil {
		return RecoveryStatus{}, err
	}
	return RecoveryStatus{
		Configured: true, Recipient: state.Recipient, ConfiguredAt: state.ConfiguredAt,
		CodeIssuedAt: state.Code.IssuedAt, CodeConsumed: state.Code.ConsumedAt != nil, Pending: state.Pending,
	}, nil
}

// ListRecoveryNotices returns the recovery notices, newest first. This is the
// record igti.6 delivers to every listed Owner device.
//
// The recovery state stays in the host's custody; its notices, which hold no
// secret, are projected into the verification custody
// (recoveryNoticesProjectionPath) on every write, so the core's Hub delivers
// them by Web Push and lists them to Owner devices without reading the state.
func ListRecoveryNotices(root string) ([]RecoveryNotice, error) {
	raw, err := readHomePrivate(root, recoveryNoticesProjectionPath, recoveryStateMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return []RecoveryNotice{}, nil
	}
	if err != nil {
		return nil, err
	}
	var projection recoveryNoticeProjection
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&projection) != nil || decoder.Decode(new(any)) != io.EOF || projection.Schema != recoveryNoticesProjectionSchema {
		return nil, errors.New("identity: unreadable recovery notices")
	}
	notices := slices.Clone(projection.Notices)
	slices.Reverse(notices)
	return notices, nil
}

// SyncRecoveryNoticeProjection rewrites the notice projection from the
// recovery state (host only); without a state there is nothing to project.
func SyncRecoveryNoticeProjection(root string) error {
	state, err := loadRecoveryState(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return writeRecoveryNoticeProjection(root, state.Notices)
}

type recoveryNoticeProjection struct {
	Schema  string           `json:"schema"`
	Notices []RecoveryNotice `json:"notices"`
}

func writeRecoveryNoticeProjection(root string, notices []RecoveryNotice) error {
	if notices == nil {
		notices = []RecoveryNotice{}
	}
	raw, err := json.Marshal(recoveryNoticeProjection{Schema: recoveryNoticesProjectionSchema, Notices: notices})
	if err != nil {
		return err
	}
	return writeHomePrivate(root, recoveryNoticesProjectionPath, raw, false)
}

// RedeemRecoveryCodeAtConsole is the local console path: a valid, unconsumed
// code yields the Owner's passkey registration link at once. Any pending
// device recovery ends. The code is consumed only after the link exists.
func RedeemRecoveryCodeAtConsole(ctx context.Context, root, code string, issue RecoveryPasskeyIssuer, now time.Time) (RecoveryPasskeyLink, error) {
	now = now.UTC()
	subject, err := recoveryOwnerSubject(root)
	if err != nil {
		return RecoveryPasskeyLink{}, err
	}
	var link RecoveryPasskeyLink
	err = updateRecoveryState(root, false, func(state *recoveryState, _ bool) error {
		if !state.codeMatches(code) {
			return ErrRecoveryDenied
		}
		issued, err := issue(ctx, subject)
		if err != nil {
			return err
		}
		link = issued
		consumed := now
		state.Code.ConsumedAt = &consumed
		ended := state.Pending
		state.Pending = nil
		addRecoveryNotice(root, state, RecoveryNoticeRedeemed, "console", ended, now)
		return nil
	})
	return link, err
}

// RequestDeviceRecovery starts the delayed path from an enrolled Owner device:
// the proof's key must be an active registry entry of the Owner's account and
// the code must be valid. The recovery becomes eligible after 24 hours; every
// Owner device receives a notice and the console or any Owner device can
// cancel it meanwhile.
func RequestDeviceRecovery(root string, request RecoveryDeviceRequest, now time.Time) (RecoveryRequest, error) {
	now = now.UTC()
	device, subject, err := verifyRecoveryDeviceProof(root, request, http.MethodPost, RecoveryRequestPath, now)
	if err != nil {
		return RecoveryRequest{}, err
	}
	var pending RecoveryRequest
	err = updateRecoveryState(root, false, func(state *recoveryState, _ bool) error {
		state.endInactivePending(root, now)
		// The code is checked first, so a device without it learns nothing
		// about a pending recovery.
		if !state.deviceCodeMatches(root, request.Code, "device:"+device.ID, now) {
			return ErrRecoveryDenied
		}
		if state.Pending != nil {
			if state.Pending.Device != device.ID {
				return ErrRecoveryPending
			}
			pending = *state.Pending
			return nil
		}
		id, err := randomRecoveryID()
		if err != nil {
			return err
		}
		pending = RecoveryRequest{
			ID: id, Device: device.ID, Account: subject, RequestedAt: now,
			EligibleAt: now.Add(RecoveryDeviceDelay), ExpiresAt: now.Add(RecoveryDeviceDelay + RecoveryRequestWindow),
		}
		state.Pending = &pending
		addRecoveryNotice(root, state, RecoveryNoticeRequested, "device:"+device.ID, &pending, now)
		return nil
	})
	return pending, err
}

// CompleteDeviceRecovery finishes a pending device recovery: the same device
// key, the same valid code, no earlier than 24 hours after the request and
// before it expired. The code is consumed only after the link exists.
func CompleteDeviceRecovery(ctx context.Context, root string, request RecoveryDeviceRequest, issue RecoveryPasskeyIssuer, now time.Time) (RecoveryPasskeyLink, error) {
	now = now.UTC()
	device, subject, err := verifyRecoveryDeviceProof(root, request, http.MethodPost, RecoveryCompletePath, now)
	if err != nil {
		return RecoveryPasskeyLink{}, err
	}
	var link RecoveryPasskeyLink
	err = updateRecoveryState(root, false, func(state *recoveryState, _ bool) error {
		// Re-read the registry under the state lock: a revocation that raced
		// the proof check ends the request here.
		state.endInactivePending(root, now)
		pending := state.Pending
		if pending == nil || pending.Device != device.ID || pending.Account != subject {
			return ErrRecoveryDenied
		}
		if now.Before(pending.EligibleAt) {
			return ErrRecoveryNotYet
		}
		if !state.deviceCodeMatches(root, request.Code, "device:"+device.ID, now) {
			return ErrRecoveryDenied
		}
		issued, err := issue(ctx, subject)
		if err != nil {
			return err
		}
		link = issued
		consumed := now
		state.Code.ConsumedAt = &consumed
		state.Pending = nil
		addRecoveryNotice(root, state, RecoveryNoticeCompleted, "device:"+device.ID, pending, now)
		return nil
	})
	return link, err
}

// CancelRecoveryAtConsole ends a pending device recovery from the console.
func CancelRecoveryAtConsole(root string, now time.Time) (bool, error) {
	return cancelRecovery(root, "console", now.UTC())
}

// CancelDeviceRecovery ends a pending device recovery from any enrolled Owner
// device; no code is needed to stop a recovery.
func CancelDeviceRecovery(root string, request RecoveryDeviceRequest, now time.Time) (bool, error) {
	now = now.UTC()
	device, _, err := verifyRecoveryDeviceProof(root, request, http.MethodPost, RecoveryCancelPath, now)
	if err != nil {
		return false, err
	}
	return cancelRecovery(root, "device:"+device.ID, now)
}

// DeviceRecoveryNotices returns, newest first, the notices addressed to the
// enrolled Owner device whose key signed the proof (GET with DPoP), so an
// Owner device learns of a recovery started elsewhere within its 24 hours.
func DeviceRecoveryNotices(root string, request RecoveryDeviceRequest, now time.Time) ([]RecoveryNotice, error) {
	device, _, err := verifyRecoveryDeviceProof(root, request, http.MethodGet, RecoveryDeviceNoticesPath, now.UTC())
	if err != nil {
		return nil, err
	}
	notices, err := ListRecoveryNotices(root)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(notices, func(notice RecoveryNotice) bool { return !slices.Contains(notice.Devices, device.ID) }), nil
}

// ResetRecoveryAfterRestore runs when a set's custody was installed on a
// replacement Authority. The set may predate a cancellation, a new code or a
// device revocation, so it must not revive a pending device recovery, the
// failure count, or a code the Owner may since have replaced: the pending
// recovery ends and the restored code counts as used until the Owner issues a
// new one (stackkit break-glass code).
func ResetRecoveryAfterRestore(root string, now time.Time) error {
	now = now.UTC()
	return updateRecoveryState(root, false, func(state *recoveryState, _ bool) error {
		ended := state.Pending
		state.Pending, state.DeviceFailures = nil, nil
		if state.Code.ConsumedAt == nil {
			consumed := now
			state.Code.ConsumedAt = &consumed
		}
		addRecoveryNotice(root, state, RecoveryNoticeRestored, "console", ended, now)
		return nil
	})
}

func cancelRecovery(root, origin string, now time.Time) (bool, error) {
	cancelled := false
	err := updateRecoveryState(root, false, func(state *recoveryState, _ bool) error {
		state.endInactivePending(root, now)
		if state.Pending == nil {
			return nil
		}
		ended := state.Pending
		state.Pending, cancelled = nil, true
		addRecoveryNotice(root, state, RecoveryNoticeCancelled, origin, ended, now)
		return nil
	})
	return cancelled, err
}

// LocalRecoveryPasskeyIssuer mints the Owner's one-time Pocket ID link through
// the Authority's local Pocket ID admin API, for the public identity origin.
func LocalRecoveryPasskeyIssuer(root string) RecoveryPasskeyIssuer {
	return func(ctx context.Context, subject string) (RecoveryPasskeyLink, error) {
		key, err := localevidence.ReadBasementRuntimePocketIDAdminKey(root)
		if err != nil {
			return RecoveryPasskeyLink{}, err
		}
		address, err := localevidence.LocalIdentityRuntimeAddress(root)
		if err != nil {
			return RecoveryPasskeyLink{}, err
		}
		token, err := pocketid.NewClient(localowner.PocketIDLocalAPI(), key).CreateOneTimeAccessToken(ctx, subject, RecoveryPasskeyLinkLifetime)
		if err != nil || strings.TrimSpace(token) == "" {
			return RecoveryPasskeyLink{}, errors.New("identity: Pocket ID refused the Owner passkey registration link")
		}
		return RecoveryPasskeyLink{
			URL:       pocketid.ActivationURL(address.PocketIDOrigin(), token),
			ExpiresAt: time.Now().UTC().Add(RecoveryPasskeyLinkLifetime).Truncate(time.Second),
		}, nil
	}
}

// RecoveryDeviceHandler serves the device recovery routes. The DPoP proof in
// the "DPoP" header is the authentication (no node API key); the body is
// {"code": "..."} for request and complete, empty for cancel, and the notices
// route is a GET.
func RecoveryDeviceHandler(root string, issue RecoveryPasskeyIssuer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proofs := r.Header.Values("DPoP")
		var body struct {
			Code string `json:"code"`
		}
		method := http.MethodPost
		if r.URL.Path == RecoveryDeviceNoticesPath {
			method = http.MethodGet
		}
		optional := r.URL.Path == RecoveryCancelPath || r.URL.Path == RecoveryDeviceNoticesPath
		if r.Method != method || len(proofs) != 1 || !decodeHomeJSON(r.Body, recoveryMaxBodyBytes, &body, optional) {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_recovery_request", false)
			return
		}
		request := RecoveryDeviceRequest{Method: r.Method, Proof: proofs[0], Code: body.Code}
		now := time.Now()
		var result any
		var err error
		switch r.URL.Path {
		case RecoveryDeviceNoticesPath:
			var notices []RecoveryNotice
			notices, err = DeviceRecoveryNotices(root, request, now)
			result = map[string]any{"notices": notices}
		case RecoveryRequestPath:
			result, err = RequestDeviceRecovery(root, request, now)
		case RecoveryCompletePath:
			result, err = CompleteDeviceRecovery(r.Context(), root, request, issue, now)
		case RecoveryCancelPath:
			var cancelled bool
			cancelled, err = CancelDeviceRecovery(root, request, now)
			result = map[string]bool{"cancelled": cancelled}
		default:
			http.NotFound(w, r)
			return
		}
		switch {
		case errors.Is(err, ErrRecoveryNotYet):
			denyHomeIdentity(w, http.StatusConflict, "recovery_not_yet_eligible", true)
			return
		case errors.Is(err, ErrRecoveryPending):
			denyHomeIdentity(w, http.StatusConflict, "recovery_pending", true)
			return
		case errors.Is(err, ErrRecoveryDenied), errors.Is(err, ErrHomeAccessDenied), errors.Is(err, ErrRecoveryNotConfigured):
			denyHomeIdentity(w, http.StatusUnauthorized, "recovery_denied", false)
			return
		case err != nil:
			denyHomeIdentity(w, http.StatusServiceUnavailable, "recovery_unavailable", true)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// RecoveryNoticesHandler lists the recovery notices behind the node API key,
// for the State Console and the notice delivery of igti.6.
func RecoveryNoticesHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != RecoveryNoticesPath {
			http.NotFound(w, r)
			return
		}
		notices, err := ListRecoveryNotices(root)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "workspace_unavailable", false)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"notices": notices})
	})
}

// RecoveryRecipient is the public recipient every recovery set is encrypted
// to. The Authority holds nothing that decrypts a set.
func RecoveryRecipient(root string) (*age.HybridRecipient, error) {
	state, err := loadRecoveryState(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrRecoveryNotConfigured
	}
	if err != nil {
		return nil, err
	}
	return age.ParseHybridRecipient(state.Recipient)
}

// FormatRecoveryCode prints a code in groups of four for the Owner to write down.
func FormatRecoveryCode(code string) string {
	normalized, ok := normalizeRecoveryCode(code)
	if !ok {
		return ""
	}
	groups := make([]string, 0, len(normalized)/recoveryCodeGroup)
	for start := 0; start < len(normalized); start += recoveryCodeGroup {
		groups = append(groups, normalized[start:start+recoveryCodeGroup])
	}
	return strings.Join(groups, "-")
}

func newRecoveryCode(now time.Time) (string, recoveryCodeRecord, error) {
	raw := make([]byte, recoveryCodeBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", recoveryCodeRecord{}, err
	}
	code := recoveryCodeEncoding.EncodeToString(raw)
	clear(raw)
	verifier, err := stackcrypto.HashPassphrase(code)
	if err != nil {
		return "", recoveryCodeRecord{}, err
	}
	return FormatRecoveryCode(code), recoveryCodeRecord{Verifier: verifier, IssuedAt: now}, nil
}

// normalizeRecoveryCode accepts a code with or without separators, in any
// case, and with the digits a person may write for look-alike letters.
func normalizeRecoveryCode(code string) (string, bool) {
	var out strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch {
		case r == '-' || r == ' ' || r == '\t':
			continue
		case r == '0':
			r = 'O'
		case r == '1':
			r = 'I'
		case r == '8':
			r = 'B'
		}
		if !(r >= 'A' && r <= 'Z' || r >= '2' && r <= '7') {
			return "", false
		}
		out.WriteRune(r)
	}
	normalized := out.String()
	if len(normalized) != recoveryCodeEncoding.EncodedLen(recoveryCodeBytes) {
		return "", false
	}
	return normalized, true
}

// codeMatches reports whether code is the current, unconsumed recovery code.
func (s *recoveryState) codeMatches(code string) bool {
	normalized, ok := normalizeRecoveryCode(code)
	return ok && s.Code.ConsumedAt == nil && s.Code.Verifier != "" && stackcrypto.VerifyPassphrase(normalized, s.Code.Verifier)
}

// endInactivePending ends a pending device recovery that expired or whose
// requesting device is no longer an active Owner registry entry (revoked or
// lapsed), so a revocation stops the recovery and frees the slot for the
// Owner's other devices. An unreadable registry ends it too.
func (s *recoveryState) endInactivePending(root string, now time.Time) {
	pending := s.Pending
	if pending == nil {
		return
	}
	if !now.Before(pending.ExpiresAt) {
		s.Pending = nil
		return
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err == nil && slices.ContainsFunc(registry.Devices, func(device HomeDevice) bool {
		return device.ID == pending.Device && device.AccountSubject == pending.Account && device.Active(now)
	}) {
		return
	}
	s.Pending = nil
	addRecoveryNotice(root, s, RecoveryNoticeCancelled, "revocation", pending, now)
}

// deviceCodeMatches is codeMatches for the device path: at most five wrong
// codes per hour reach the verifier, and every wrong code notifies the Owner
// devices.
func (s *recoveryState) deviceCodeMatches(root, code, origin string, now time.Time) bool {
	recent := s.DeviceFailures[:0]
	for _, at := range s.DeviceFailures {
		if now.Sub(at) < recoveryDeviceFailureWindow {
			recent = append(recent, at)
		}
	}
	s.DeviceFailures = recent
	if len(s.DeviceFailures) >= recoveryDeviceFailureLimit {
		return false
	}
	if s.codeMatches(code) {
		return true
	}
	s.DeviceFailures = append(s.DeviceFailures, now)
	addRecoveryNotice(root, s, RecoveryNoticeCodeRejected, origin, s.Pending, now)
	return false
}

// verifyRecoveryDeviceProof admits a fresh, unused DPoP proof for exactly
// this route, signed by the key of an active registry entry of the Owner's
// account. Keys that are not such entries are refused before replay state.
func verifyRecoveryDeviceProof(root string, request RecoveryDeviceRequest, method, path string, now time.Time) (HomeDevice, string, error) {
	deny := func() (HomeDevice, string, error) { return HomeDevice{}, "", ErrRecoveryDenied }
	proof, err := parseHomeDeviceProof(request.Proof, now)
	if err != nil || request.Method != method || proof.Method != method || !homeIdentityOriginAdmitted(root, proof.Target, path) {
		return deny()
	}
	verifier, err := HomeVerifierFor(root)
	if err != nil || !verifier.afterStart(proof.IssuedAt) {
		return deny()
	}
	subject, err := recoveryOwnerSubject(root)
	if err != nil {
		return deny()
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return deny()
	}
	index := slices.IndexFunc(registry.Devices, func(device HomeDevice) bool {
		return device.ID == proof.Thumbprint && device.AccountSubject == subject && device.Active(now)
	})
	if index < 0 {
		return deny()
	}
	verifier.mu.Lock()
	fresh := verifier.rememberLocked(proof.Thumbprint, proof.ID, proof.IssuedAt.Add(homeProofReplayRetention), now)
	verifier.mu.Unlock()
	if !fresh {
		return deny()
	}
	return registry.Devices[index], subject, nil
}

// recoveryOwnerSubject is the Pocket ID subject bound to the local Owner: the
// signed owner runtime binding where it exists, the Owner custody projection
// before the first Apply bound it (the Authority's public record carries it).
func recoveryOwnerSubject(root string) (string, error) {
	authority, err := homeAuthority(root)
	if err != nil {
		return "", err
	}
	if authority.OwnerPocketIDSubject == "" {
		return "", ErrRecoveryDenied
	}
	return authority.OwnerPocketIDSubject, nil
}

// addRecoveryNotice records one event for every Owner device enrolled now.
func addRecoveryNotice(root string, state *recoveryState, kind, origin string, request *RecoveryRequest, now time.Time) {
	notice := RecoveryNotice{Kind: kind, Origin: origin, CreatedAt: now, Devices: []string{}}
	notice.ID, _ = randomRecoveryID()
	if subject, err := recoveryOwnerSubject(root); err == nil {
		notice.Account = subject
		if registry, err := loadHomeDeviceRegistry(root); err == nil {
			for _, device := range registry.Devices {
				if device.AccountSubject == subject && device.Active(now) {
					notice.Devices = append(notice.Devices, device.ID)
				}
			}
		}
	}
	if request != nil {
		eligible := request.EligibleAt
		notice.RequestID, notice.EligibleAt = request.ID, &eligible
	}
	state.Notices = append(state.Notices, notice)
	if extra := len(state.Notices) - recoveryNoticeLimit; extra > 0 {
		state.Notices = slices.Delete(state.Notices, 0, extra)
	}
}

func randomRecoveryID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// updateRecoveryState runs one read-modify-write of the recovery state under
// a cross-process lock. Without create, a missing state is
// ErrRecoveryNotConfigured. A refusal (ErrRecoveryDenied) still writes what
// the mutation recorded: the device failure count and the rejection notice.
func updateRecoveryState(root string, create bool, mutate func(*recoveryState, bool) error) error {
	release, err := localevidence.AcquireCustodyLock(root, recoveryStatePath, 201)
	if err != nil {
		return err
	}
	defer release()
	state, err := loadRecoveryState(root)
	exists := err == nil
	switch {
	case errors.Is(err, fs.ErrNotExist) && create:
		state = recoveryState{Schema: recoveryStateSchema, Notices: []RecoveryNotice{}}
	case errors.Is(err, fs.ErrNotExist):
		return ErrRecoveryNotConfigured
	case err != nil:
		return err
	}
	before, _ := json.Marshal(state)
	mutateErr := mutate(&state, exists)
	if mutateErr == nil || errors.Is(mutateErr, ErrRecoveryDenied) && recoveryStateChanged(before, state) {
		if writeErr := writeRecoveryState(root, state); writeErr != nil {
			return writeErr
		}
	}
	return mutateErr
}

// recoveryStateChanged reports whether a refused device call recorded a
// rejection (failure count and notice) that must persist.
func recoveryStateChanged(before []byte, state recoveryState) bool {
	after, err := json.Marshal(state)
	return err == nil && !bytes.Equal(before, after)
}

func loadRecoveryState(root string) (recoveryState, error) {
	var state recoveryState
	raw, err := readHomePrivate(root, recoveryStatePath, recoveryStateMaxBytes)
	if err != nil {
		return state, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.Schema != recoveryStateSchema {
		return state, errors.New("identity: unreadable recovery state")
	}
	if _, err := age.ParseHybridRecipient(state.Recipient); err != nil || state.Code.Verifier == "" || len(state.SetAuthKey) != sha256.Size {
		return state, errors.New("identity: unreadable recovery state")
	}
	return state, nil
}

func writeRecoveryState(root string, state recoveryState) error {
	state.Schema = recoveryStateSchema
	if state.Notices == nil {
		state.Notices = []RecoveryNotice{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := writeHomePrivate(root, recoveryStatePath, raw, false); err != nil {
		return err
	}
	return writeRecoveryNoticeProjection(root, state.Notices)
}
