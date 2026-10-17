package identity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/kombifyio/stackkits/internal/localevidence"
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
	now = now.UTC()
	state, err := loadHomeVerifierState(root)
	if err != nil || !state.Active {
		return HomePairing{}, ErrHomePairingDenied
	}
	if err := validateBoundHomePublicTrust(root, state.Trust, now); err != nil {
		return HomePairing{}, ErrHomePairingDenied
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil || owner.Binding.SiteRef != state.Trust.HomeSiteRef {
		return HomePairing{}, ErrHomePairingDenied
	}
	human, err := verifyHomeHumanToken(humanSession, state.Trust, now)
	if err != nil || human.IssuedAt == nil {
		return HomePairing{}, ErrHomePairingDenied
	}
	if age := now.Sub(human.IssuedAt.Time()); age < -homeVerifierDPoPWindow || age > HomeOwnerStepUpFreshness {
		return HomePairing{}, ErrHomePairingDenied
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
