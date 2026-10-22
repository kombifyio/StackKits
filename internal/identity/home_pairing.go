package identity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
)

const (
	// HomePairingLifetime is the #DeviceEnrollmentPolicy pairing code lifetime.
	HomePairingLifetime = 300 * time.Second
	// HomeOwnerStepUpFreshness is how recent the PocketID owner authentication
	// must be to issue a pairing code.
	HomeOwnerStepUpFreshness = 60 * time.Second

	homePairingDir      = ".stackkit/custody/home-verifier/pairing"
	homePairingMaxBytes = 4 << 10
	homeCSRMaxBytes     = 8 << 10
)

var (
	ErrHomePairingDenied = errors.New("identity: Home pairing requires a fresh Owner step-up, a LAN client and an unused pairing code")
	homePairingEncoding  = base32.StdEncoding.WithPadding(base32.NoPadding)
)

// HomePairing is shown once to the Owner (State Console QR/code or CLI token).
// The code is never stored; only its hash is.
type HomePairing struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// HomeEnrollmentRequest is what a device presents. LAN is the transport
// admission the server observed for the connection; it is never read from a
// request header or body.
type HomeEnrollmentRequest struct {
	Code   string
	CSRPEM []byte
	LAN    bool
}

// HomeEnrollmentGrant is the pairing outcome handed to the device provisioner.
// The Owner subject comes from the step-up that issued the code, and the key
// is the device-generated key proven by the CSR signature.
type HomeEnrollmentGrant struct {
	OwnerSubject         string
	DeviceKeyThumbprint  string
	DeviceCommonName     string
	CSRDER               []byte
	PairingExpiresAt     time.Time
	CredentialTTLSeconds int
}

type homePairingRecord struct {
	HomeSiteRef  string    `json:"homeSiteRef"`
	NodeRef      string    `json:"nodeRef"`
	OwnerSubject string    `json:"ownerSubject"`
	IssuedAt     time.Time `json:"issuedAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// IssueHomePairing issues a single-use, node-bound pairing code. It requires a
// PocketID human session that is the pinned Owner and was authenticated within
// HomeOwnerStepUpFreshness, against the current signed Home trust.
func IssueHomePairing(root, humanSession string, now time.Time) (HomePairing, error) {
	return issueHomePairing(root, now, func(trust HomePublicTrust, now time.Time) (jwt.Claims, error) {
		return verifyHomeHumanToken(humanSession, trust, now)
	})
}

// IssueHomePairingWithOwnerStepUp issues the pairing code from the local Owner
// step-up page (device-enroll mode). The ID token comes from the existing
// confidential step-up client, so its audience is localowner.StepUpClientID
// rather than the Home access client; everything else is the Home trust check
// of verifyHomeHumanToken (issuer, pinned Owner subject, signing keys,
// revocation, time claims), plus the flow nonce and a passkey (amr "phr")
// authentication, as for an action approval.
func IssueHomePairingWithOwnerStepUp(root, idToken, nonce string, now time.Time) (HomePairing, error) {
	if len(nonce) < 43 || len(nonce) > 128 {
		return HomePairing{}, ErrHomePairingDenied
	}
	return issueHomePairing(root, now, ownerStepUpIDTokenVerifier(idToken, nonce))
}

// ErrOwnerStepUpDenied is returned for any Owner step-up ID token that does not
// prove a fresh passkey authentication of the pinned Home Owner.
var ErrOwnerStepUpDenied = errors.New("identity: a fresh Owner passkey step-up for this Home is required")

// VerifyOwnerStepUp verifies an ID token of the local Owner step-up client for
// an Owner decision that is not a remote action approval, such as answering a
// StackKits MCP agent's write approval request. It applies exactly the checks
// of IssueHomePairingWithOwnerStepUp (active bound Home trust, Owner custody of
// this Home, pinned Owner subject, signing keys, audience, flow nonce, passkey
// authentication, revocation and HomeOwnerStepUpFreshness) and returns the
// Owner subject. It changes no state.
func VerifyOwnerStepUp(root, idToken, nonce string, now time.Time) (string, error) {
	if len(nonce) < 43 || len(nonce) > 128 {
		return "", ErrOwnerStepUpDenied
	}
	_, human, err := verifyFreshHomeOwner(root, now.UTC(), ownerStepUpIDTokenVerifier(idToken, nonce))
	if err != nil {
		return "", ErrOwnerStepUpDenied
	}
	return human.Subject, nil
}

func ownerStepUpIDTokenVerifier(idToken, nonce string) func(HomePublicTrust, time.Time) (jwt.Claims, error) {
	return func(trust HomePublicTrust, now time.Time) (jwt.Claims, error) {
		var claims struct {
			jwt.Claims
			Type  string   `json:"type"`
			Nonce string   `json:"nonce"`
			AMR   []string `json:"amr"`
		}
		if err := verifyHomeJWT(idToken, "JWT", trust.PocketID.Keys, &claims); err != nil {
			return jwt.Claims{}, err
		}
		if claims.Type != "id-token" || claims.Issuer != trust.PocketID.Issuer || claims.Subject != trust.PocketID.Subject ||
			len(claims.Audience) != 1 || claims.Audience[0] != localowner.StepUpClientID ||
			subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 || len(claims.AMR) != 1 || claims.AMR[0] != "phr" {
			return jwt.Claims{}, ErrHomeAccessDenied
		}
		if !validHomeTimeClaims(claims.Claims, now) || slices.Contains(trust.RevokedSubjects, claims.Subject) {
			return jwt.Claims{}, ErrHomeAccessDenied
		}
		return claims.Claims, nil
	}
}

// verifyFreshHomeOwner checks the active bound Home trust and Owner custody,
// then that verify proves an Owner authentication within
// HomeOwnerStepUpFreshness.
func verifyFreshHomeOwner(root string, now time.Time, verify func(HomePublicTrust, time.Time) (jwt.Claims, error)) (localevidence.OwnerCustody, jwt.Claims, error) {
	state, err := loadHomeVerifierState(root)
	if err != nil || !state.Active {
		return localevidence.OwnerCustody{}, jwt.Claims{}, ErrHomePairingDenied
	}
	if err := validateBoundHomePublicTrust(root, state.Trust, now); err != nil {
		return localevidence.OwnerCustody{}, jwt.Claims{}, ErrHomePairingDenied
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil || owner.Binding.SiteRef != state.Trust.HomeSiteRef {
		return localevidence.OwnerCustody{}, jwt.Claims{}, ErrHomePairingDenied
	}
	human, err := verify(state.Trust, now)
	if err != nil || human.IssuedAt == nil {
		return localevidence.OwnerCustody{}, jwt.Claims{}, ErrHomePairingDenied
	}
	if age := now.Sub(human.IssuedAt.Time()); age < -homeVerifierDPoPWindow || age > HomeOwnerStepUpFreshness {
		return localevidence.OwnerCustody{}, jwt.Claims{}, ErrHomePairingDenied
	}
	return owner, human, nil
}

func issueHomePairing(root string, now time.Time, verify func(HomePublicTrust, time.Time) (jwt.Claims, error)) (HomePairing, error) {
	now = now.UTC()
	owner, human, err := verifyFreshHomeOwner(root, now, verify)
	if err != nil {
		return HomePairing{}, err
	}
	random := make([]byte, 20)
	if _, err := rand.Read(random); err != nil {
		return HomePairing{}, err
	}
	code := homePairingEncoding.EncodeToString(random)
	record := homePairingRecord{
		HomeSiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef, OwnerSubject: human.Subject,
		IssuedAt: now, ExpiresAt: now.Add(HomePairingLifetime),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return HomePairing{}, err
	}
	if err := writeHomePrivate(root, homePairingPath(code), raw, true); err != nil {
		return HomePairing{}, err
	}
	return HomePairing{Code: code, ExpiresAt: record.ExpiresAt}, nil
}

// RedeemHomePairing consumes a pairing code exactly once and proves the device
// holds the private key of the CSR it submits. The code is consumed before the
// CSR is examined, so a wrong or replayed submission cannot probe the code. It
// issues no credential: the provisioner (step-ca) acts only on the grant.
func RedeemHomePairing(root string, request HomeEnrollmentRequest, now time.Time) (HomeEnrollmentGrant, error) {
	now = now.UTC()
	deny := HomeEnrollmentGrant{}
	if !request.LAN || len(request.Code) != homePairingEncoding.EncodedLen(20) || strings.ToUpper(request.Code) != request.Code {
		return deny, ErrHomePairingDenied
	}
	state, err := loadHomeVerifierState(root)
	if err != nil || !state.Active || validateBoundHomePublicTrust(root, state.Trust, now) != nil {
		return deny, ErrHomePairingDenied
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil || owner.Binding.SiteRef != state.Trust.HomeSiteRef {
		return deny, ErrHomePairingDenied
	}
	raw, err := readHomePrivate(root, homePairingPath(request.Code), homePairingMaxBytes)
	if err != nil {
		return deny, ErrHomePairingDenied
	}
	var record homePairingRecord
	if json.Unmarshal(raw, &record) != nil || record.HomeSiteRef != owner.Binding.SiteRef || record.NodeRef != owner.Binding.NodeRef ||
		!now.Before(record.ExpiresAt) || now.Before(record.IssuedAt.Add(-homeVerifierDPoPWindow)) {
		return deny, ErrHomePairingDenied
	}
	if err := writeHomePrivate(root, homePairingPath(request.Code)+".used", []byte("{}"), true); err != nil {
		return deny, ErrHomePairingDenied
	}
	csr, thumbprint, err := verifyDeviceCSR(request.CSRPEM)
	if err != nil {
		return deny, ErrHomePairingDenied
	}
	return HomeEnrollmentGrant{
		OwnerSubject: record.OwnerSubject, DeviceKeyThumbprint: thumbprint, DeviceCommonName: csr.Subject.CommonName,
		CSRDER: csr.Raw, PairingExpiresAt: record.ExpiresAt, CredentialTTLSeconds: state.Trust.DeviceCredentialTTLSeconds,
	}, nil
}

// verifyDeviceCSR requires a device-generated P-256 or Ed25519 key and checks
// the CSR self-signature, which is the proof of possession.
func verifyDeviceCSR(raw []byte) (*x509.CertificateRequest, string, error) {
	if len(raw) == 0 || len(raw) > homeCSRMaxBytes {
		return nil, "", ErrHomePairingDenied
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, "", ErrHomePairingDenied
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return nil, "", ErrHomePairingDenied
	}
	switch key := csr.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if key.Curve != elliptic.P256() {
			return nil, "", ErrHomePairingDenied
		}
	case ed25519.PublicKey:
	default:
		return nil, "", ErrHomePairingDenied
	}
	thumb, err := (&jose.JSONWebKey{Key: csr.PublicKey}).Thumbprint(crypto.SHA256)
	if err != nil {
		return nil, "", ErrHomePairingDenied
	}
	return csr, base64.RawURLEncoding.EncodeToString(thumb), nil
}

func homePairingPath(code string) string {
	digest := sha256.Sum256([]byte(code))
	return homePairingDir + "/" + base64.RawURLEncoding.EncodeToString(digest[:]) + ".json"
}
