package identity

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/kombifyio/stackkits/internal/localowner"
)

// Step-up classes of the homelab identity target (§6.2, §10, §12). An act on
// homelab identity is a passkey step-up valid for 60 seconds and bound to the
// digest of the one action it approves; each step-up is accepted once.
type HomeStepUpClass string

const (
	// HomeStepUpEnrollConfirm confirms an away enrollment.
	HomeStepUpEnrollConfirm HomeStepUpClass = "enroll-confirm"
	// HomeStepUpJoin approves a further server joining the Authority (P2).
	HomeStepUpJoin HomeStepUpClass = "join"
	// HomeStepUpClientRegistration approves an OIDC client registration at
	// the Authority's Pocket ID for another server or an app.
	HomeStepUpClientRegistration HomeStepUpClass = "client-registration"
	// HomeStepUpCustodyChange approves a change of identity custody (recovery
	// set, recovery code, Owner passkey reset).
	HomeStepUpCustodyChange HomeStepUpClass = "custody-change"

	// HomeStepUpWindow is #HomelabIdentityLifetimesV1.ownerStepUpSeconds: the
	// passkey authentication must be at most this old when it is presented.
	HomeStepUpWindow = 60 * time.Second
	// HomeOwnerStepUpFreshness bounds every Owner passkey step-up.
	HomeOwnerStepUpFreshness = HomeStepUpWindow

	homeStepUpConsumedDir   = ".stackkit/custody/home-verifier/step-up"
	homeStepUpMinChallenge  = 32
	homeStepUpMaxChallenge  = 128
	homeStepUpActionContext = "stackkit.homelab-step-up-action/v1"
	homeStepUpNonceContext  = "stackkit.homelab-step-up/v1"
)

var (
	// ErrHomeStepUpDenied is the one answer to a step-up that does not prove a
	// fresh passkey authentication of the required approver for exactly this
	// action, or that was already used.
	ErrHomeStepUpDenied = errors.New("identity: a fresh passkey step-up for exactly this action is required")
	// ErrOwnerStepUpDenied is returned for any Owner step-up ID token that does
	// not prove a fresh passkey authentication of the pinned Home Owner.
	ErrOwnerStepUpDenied = errors.New("identity: a fresh Owner passkey step-up for this Home is required")
)

// HomeStepUpAction is the one action a step-up approves: its class and the
// SHA-256 digest (lowercase hex) of the action's canonical JSON.
type HomeStepUpAction struct {
	Class  HomeStepUpClass `json:"class"`
	Digest string          `json:"digest"`
}

// NewHomeStepUpAction derives the action digest from the class and the
// action's JSON encoding. The caller passes the exact object it will act on,
// so a step-up for one action can never approve another.
func NewHomeStepUpAction(class HomeStepUpClass, action any) (HomeStepUpAction, error) {
	if !validHomeStepUpClass(class) {
		return HomeStepUpAction{}, ErrHomeStepUpDenied
	}
	raw, err := json.Marshal(action)
	if err != nil {
		return HomeStepUpAction{}, err
	}
	digest := sha256.Sum256(append([]byte(homeStepUpActionContext+"\x00"+string(class)+"\x00"), raw...))
	return HomeStepUpAction{Class: class, Digest: hex.EncodeToString(digest[:])}, nil
}

// Nonce is the OIDC nonce of the passkey sign-in that approves this action:
// base64url(SHA-256("stackkit.homelab-step-up/v1" 0x00 class 0x00 digest
// 0x00 challenge)). The challenge is a fresh random value (32 to 128
// base64url characters) of the approving flow, so a failed attempt can be
// retried with a new sign-in; every nonce is accepted once.
func (a HomeStepUpAction) Nonce(challenge string) string {
	digest := sha256.Sum256([]byte(homeStepUpNonceContext + "\x00" + string(a.Class) + "\x00" + a.Digest + "\x00" + challenge))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// HomeStepUpApprover is who must approve an action: the pinned Owner through
// the Owner step-up client at the Authority, or one person (Account) through
// the device sign-in client on their own enrolled device.
type HomeStepUpApprover struct {
	Owner   bool
	Account string
}

// VerifyHomeStepUp accepts a step-up only when the ID token proves a passkey
// authentication (amr "phr") of the required approver, at most
// HomeStepUpWindow old, for exactly this action and challenge, against the
// current Owner-signed Home trust; it then records the step-up as used, so
// the same token never approves twice. It returns the approver's subject.
// While the Home trust is not current it fails closed.
func VerifyHomeStepUp(root string, action HomeStepUpAction, approver HomeStepUpApprover, challenge, idToken string, now time.Time) (string, error) {
	now = now.UTC()
	if !validHomeStepUpClass(action.Class) || !validHomeDigest(action.Digest) || !validHomeStepUpChallenge(challenge) ||
		approver.Owner == (approver.Account != "") {
		return "", ErrHomeStepUpDenied
	}
	audience := HomeDeviceClientID
	if approver.Owner {
		audience = localowner.StepUpClientID
	}
	nonce := action.Nonce(challenge)
	claims, err := verifyFreshHomeOwnerOrPerson(root, now, func(trust HomePublicTrust, now time.Time) (jwt.Claims, error) {
		claims, err := verifyHomePasskeyIDToken(idToken, trust, audience, nonce, now)
		if err != nil {
			return jwt.Claims{}, err
		}
		expected := approver.Account
		if approver.Owner {
			expected = trust.PocketID.Subject
		}
		if subtle.ConstantTimeCompare([]byte(claims.Subject), []byte(expected)) != 1 {
			return jwt.Claims{}, ErrHomeAccessDenied
		}
		return claims, nil
	})
	if err != nil {
		return "", ErrHomeStepUpDenied
	}
	record, err := json.Marshal(struct {
		Class  HomeStepUpClass `json:"class"`
		Digest string          `json:"digest"`
		At     time.Time       `json:"at"`
	}{action.Class, action.Digest, now})
	if err != nil {
		return "", err
	}
	// The journal name is the lowercase hex digest of the nonce, so it is
	// distinct even on a case-insensitive filesystem; writing it no-replace
	// makes the step-up single use across processes and restarts.
	used := sha256.Sum256([]byte(nonce))
	if err := writeHomePrivate(root, homeStepUpConsumedDir+"/"+hex.EncodeToString(used[:])+".json", record, true); err != nil {
		return "", ErrHomeStepUpDenied
	}
	return claims.Subject, nil
}

// VerifyOwnerStepUp verifies an ID token of the local Owner step-up client for
// an Owner decision that is not bound to a homelab identity action, such as
// answering a StackKits MCP agent's write approval request. It checks the
// active bound Home trust, Owner custody of this Home, the pinned Owner
// subject, signing keys, audience, the flow nonce, a passkey authentication,
// revocation and HomeOwnerStepUpFreshness, and returns the Owner subject. It
// changes no state.
func VerifyOwnerStepUp(root, idToken, nonce string, now time.Time) (string, error) {
	if len(nonce) < 43 || len(nonce) > 128 {
		return "", ErrOwnerStepUpDenied
	}
	human, err := verifyFreshHomeOwnerOrPerson(root, now.UTC(), func(trust HomePublicTrust, now time.Time) (jwt.Claims, error) {
		claims, err := verifyHomePasskeyIDToken(idToken, trust, localowner.StepUpClientID, nonce, now)
		if err != nil || claims.Subject != trust.PocketID.Subject {
			return jwt.Claims{}, ErrHomeAccessDenied
		}
		return claims, nil
	})
	if err != nil {
		return "", ErrOwnerStepUpDenied
	}
	return human.Subject, nil
}

// verifyHomePasskeyIDToken is the one Pocket ID passkey verification path:
// the pinned issuer and keys, exactly one audience, the caller's nonce, a
// passkey authentication, valid time claims and an unrevoked subject.
// Enrollment, renewal user verification and every step-up use it.
//
// The passkey authentication is Pocket ID's re-authentication (claim "rat"):
// a WebAuthn assertion that the device sign-in client and the Owner step-up
// client require on every sign-in through the authorization code flow, also
// inside a session that began with a passkey (amr "phr") or a one-time link
// (amr "otp"). A token without it (the device authorization grant ignores
// re-authentication) proves no fresh passkey and is refused. The returned
// IssuedAt is the time of that passkey assertion, so every caller measures
// freshness from it.
func verifyHomePasskeyIDToken(raw string, trust HomePublicTrust, audience, nonce string, now time.Time) (jwt.Claims, error) {
	var claims struct {
		jwt.Claims
		Type              string           `json:"type"`
		Nonce             string           `json:"nonce"`
		AMR               []string         `json:"amr"`
		ReauthenticatedAt *jwt.NumericDate `json:"rat"`
	}
	if nonce == "" || audience == "" {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	if err := verifyHomeJWT(raw, "JWT", trust.PocketID.Keys, &claims); err != nil {
		return jwt.Claims{}, err
	}
	if claims.Type != "id-token" || claims.Issuer != trust.PocketID.Issuer || claims.Subject == "" ||
		len(claims.Audience) != 1 || claims.Audience[0] != audience ||
		subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	if !validHomeTimeClaims(claims.Claims, now) || slices.Contains(trust.RevokedSubjects, claims.Subject) {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	if claims.ReauthenticatedAt == nil || len(claims.AMR) != 1 || (claims.AMR[0] != "phr" && claims.AMR[0] != "otp") {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	// The re-authentication belongs to this sign-in: at most one step-up
	// window before the token, never after it.
	passkey, issued := claims.ReauthenticatedAt.Time(), claims.IssuedAt.Time()
	if passkey.After(issued.Add(HomeClockLeeway)) || issued.Sub(passkey) > HomeStepUpWindow {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	claims.IssuedAt = claims.ReauthenticatedAt
	return claims.Claims, nil
}

// verifyFreshHomeOwnerOrPerson checks the active bound Home trust of this
// Authority (its public record), then that verify proves a passkey
// authentication at most HomeStepUpWindow old.
func verifyFreshHomeOwnerOrPerson(root string, now time.Time, verify func(HomePublicTrust, time.Time) (jwt.Claims, error)) (jwt.Claims, error) {
	trust, err := loadCurrentHomeTrust(root, now)
	if err != nil {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	authority, err := homeAuthority(root)
	if err != nil || authority.SiteRef != trust.HomeSiteRef {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	human, err := verify(trust, now)
	if err != nil || human.IssuedAt == nil {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	if age := now.Sub(human.IssuedAt.Time()); age < -HomeClockLeeway || age > HomeStepUpWindow {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	return human, nil
}

// loadCurrentHomeTrust returns the active, Owner-signed and unexpired Home
// trust, or fails closed.
func loadCurrentHomeTrust(root string, now time.Time) (HomePublicTrust, error) {
	state, err := loadHomeVerifierState(root)
	if err != nil || !state.Active {
		return HomePublicTrust{}, ErrHomeAccessDenied
	}
	if err := validateBoundHomePublicTrust(root, state.Trust, now); err != nil {
		return HomePublicTrust{}, ErrHomeAccessDenied
	}
	return state.Trust, nil
}

func validHomeStepUpClass(class HomeStepUpClass) bool {
	switch class {
	case HomeStepUpEnrollConfirm, HomeStepUpJoin, HomeStepUpClientRegistration, HomeStepUpCustodyChange:
		return true
	}
	return false
}

func validHomeDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validHomeStepUpChallenge(value string) bool {
	if len(value) < homeStepUpMinChallenge || len(value) > homeStepUpMaxChallenge {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
