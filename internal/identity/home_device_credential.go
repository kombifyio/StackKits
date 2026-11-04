package identity

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// The device credential of the homelab identity target (§6.1, §12): the
// client generates the device key, the Authority signs a key-bound credential
// that lives one hour, and the client renews it with a DPoP proof (RFC 9449).
// Every request carries the credential and a fresh proof; the server running
// the service verifies both. There is no X.509 device certificate.
const (
	// HomeDeviceCredentialType is the JWS "typ" of the device credential.
	HomeDeviceCredentialType = "stackkit-device-credential+jwt"
	// HomeDeviceProofType is the JWS "typ" of a DPoP proof.
	HomeDeviceProofType = "dpop+jwt"

	// The lifetimes of #HomelabIdentityLifetimesV1; none is a caller choice.
	HomeDeviceCredentialLifetime       = time.Hour
	HomeDeviceProofWindow              = 30 * time.Second
	HomeDeviceUserVerificationInterval = 24 * time.Hour
	HomeDeviceRegistryEntryUnused      = 180 * 24 * time.Hour
	HomeRevocationTarget               = 60 * time.Second
	// HomeUserVerificationFreshness bounds the age of the passkey assertion a
	// renewal presents, as for an Owner step-up.
	HomeUserVerificationFreshness = 60 * time.Second
	// HomeClockLeeway tolerates clock skew between device, issuer and verifier.
	HomeClockLeeway = 10 * time.Second

	homeDeviceIssuerPath     = ".stackkit/custody/home-verifier/device-issuer.json"
	homeDeviceIssuerSchema   = "stackkit.home-device-issuer/v1"
	homeDeviceIssuerMaxBytes = 4 << 10
	homeDeviceMaxTokenBytes  = 8 << 10
)

var (
	// ErrHomeAccessDenied is the one answer to every refused credential,
	// proof, renewal or enrollment, so a refusal reveals no reason.
	ErrHomeAccessDenied = errors.New("identity: a current device credential and request-bound proof are required")
	// ErrHomeUserVerificationRequired means the renewal was otherwise valid
	// but the last user verification is older than 24 hours: the client signs
	// the person in with a passkey through HomeDeviceClientID and renews again
	// with the ID token and a new proof.
	ErrHomeUserVerificationRequired = errors.New("identity: renewal needs a passkey user verification")
)

// HomeConfirmation is the key binding of a device credential (RFC 7800).
type HomeConfirmation struct {
	JWKThumbprint string `json:"jkt"`
}

type homeDeviceCredentialClaims struct {
	jwt.Claims
	Account      string           `json:"acct"`
	AuthTime     int64            `json:"auth_time"`
	Confirmation HomeConfirmation `json:"cnf"`
}

// HomeDeviceCredential is what enrollment and renewal return to the device.
// Credential is a bearer-shaped token bound to the device key: it must never
// be logged, and it is useless without a proof signed by that key.
type HomeDeviceCredential struct {
	Credential            string     `json:"credential"`
	ExpiresAt             time.Time  `json:"expiresAt"`
	UserVerificationDueAt time.Time  `json:"userVerificationDueAt"`
	Device                HomeDevice `json:"device"`
}

// HomeDeviceUserVerificationNonce is the OIDC nonce a device client sends to
// Pocket ID for the passkey assertion of a renewal. It binds the assertion to
// the device key (its JWK thumbprint) and to the one DPoP proof (its jti) that
// presents it: base64url(SHA-256("stackkit-device-user-verification/v1" 0x00
// thumbprint 0x00 jti)).
func HomeDeviceUserVerificationNonce(thumbprint, proofID string) string {
	digest := sha256.Sum256([]byte("stackkit-device-user-verification/v1\x00" + thumbprint + "\x00" + proofID))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

type homeDeviceIssuerFile struct {
	Schema    string    `json:"schema"`
	Issuer    string    `json:"issuer"`
	Audience  string    `json:"audience"`
	KeyID     string    `json:"keyId"`
	Seed      string    `json:"seed"`
	CreatedAt time.Time `json:"createdAt"`
}

// homeDeviceIssuerPublic is the verification material of the device issuer.
type homeDeviceIssuerPublic struct {
	issuer   string
	audience string
	keyID    string
	key      ed25519.PublicKey
}

type homeDeviceIssuer struct {
	homeDeviceIssuerPublic
	private ed25519.PrivateKey
}

// ensureHomeDeviceIssuer loads the Authority's device-credential signing key
// or generates it into local custody on first use. It is separate from the
// Owner key and from every step-ca key.
func ensureHomeDeviceIssuer(root string, now time.Time) (homeDeviceIssuer, error) {
	issuer, err := loadHomeDeviceIssuer(root)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return issuer, err
	}
	authority, err := homeAuthority(root)
	if err != nil || strings.TrimSpace(authority.SiteRef) == "" {
		return homeDeviceIssuer{}, ErrHomeAccessDenied
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return homeDeviceIssuer{}, err
	}
	keyID, err := homeKeyThumbprint(ed25519.NewKeyFromSeed(seed).Public())
	if err != nil {
		return homeDeviceIssuer{}, err
	}
	raw, err := json.Marshal(homeDeviceIssuerFile{
		Schema: homeDeviceIssuerSchema, KeyID: keyID, Seed: base64.RawStdEncoding.EncodeToString(seed), CreatedAt: now.UTC(),
		Issuer:   "urn:stackkit:" + authority.SiteRef + ":issuer:home-device",
		Audience: "urn:stackkit:" + authority.SiteRef + ":audience:home-device",
	})
	if err != nil {
		return homeDeviceIssuer{}, err
	}
	// No-replace: a concurrent first use in another process keeps its key.
	if writeErr := writeHomePrivate(root, homeDeviceIssuerPath, raw, true); writeErr != nil {
		if loaded, loadErr := loadHomeDeviceIssuer(root); loadErr == nil {
			return loaded, nil
		}
		return homeDeviceIssuer{}, writeErr
	}
	return loadHomeDeviceIssuer(root)
}

func loadHomeDeviceIssuer(root string) (homeDeviceIssuer, error) {
	raw, err := readHomePrivate(root, homeDeviceIssuerPath, homeDeviceIssuerMaxBytes)
	if err != nil {
		return homeDeviceIssuer{}, err
	}
	var file homeDeviceIssuerFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&file) != nil || decoder.Decode(new(any)) != io.EOF || file.Schema != homeDeviceIssuerSchema ||
		!strings.HasPrefix(file.Issuer, "urn:stackkit:") || !strings.HasPrefix(file.Audience, "urn:stackkit:") || file.Issuer == file.Audience {
		return homeDeviceIssuer{}, errors.New("identity: unreadable device issuer custody")
	}
	seed, err := base64.RawStdEncoding.DecodeString(file.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return homeDeviceIssuer{}, errors.New("identity: unreadable device issuer custody")
	}
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)
	if keyID, err := homeKeyThumbprint(public); err != nil || keyID != file.KeyID {
		return homeDeviceIssuer{}, errors.New("identity: device issuer key does not match its key id")
	}
	return homeDeviceIssuer{
		homeDeviceIssuerPublic: homeDeviceIssuerPublic{issuer: file.Issuer, audience: file.Audience, keyID: file.KeyID, key: public},
		private:                private,
	}, nil
}

// mint signs a one-hour credential for an active registry entry. The device
// ID is the key thumbprint, so subject and confirmation are the same value.
func (i homeDeviceIssuer) mint(device HomeDevice, now time.Time) (HomeDeviceCredential, error) {
	now = now.UTC().Truncate(time.Second)
	jti := make([]byte, 18)
	if _, err := rand.Read(jti); err != nil {
		return HomeDeviceCredential{}, err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: i.private},
		(&jose.SignerOptions{}).WithType(HomeDeviceCredentialType).WithHeader("kid", i.keyID))
	if err != nil {
		return HomeDeviceCredential{}, err
	}
	expires := now.Add(HomeDeviceCredentialLifetime)
	token, err := jwt.Signed(signer).Claims(homeDeviceCredentialClaims{
		Claims: jwt.Claims{
			Issuer: i.issuer, Subject: device.ID, Audience: jwt.Audience{i.audience}, ID: base64.RawURLEncoding.EncodeToString(jti),
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(expires),
		},
		Account: device.AccountSubject, AuthTime: device.LastUserVerifiedAt.Unix(), Confirmation: HomeConfirmation{JWKThumbprint: device.ID},
	}).Serialize()
	if err != nil {
		return HomeDeviceCredential{}, err
	}
	return HomeDeviceCredential{
		Credential: token, ExpiresAt: expires, UserVerificationDueAt: device.LastUserVerifiedAt.Add(HomeDeviceUserVerificationInterval).UTC(), Device: device,
	}, nil
}

// verify checks one device credential against this issuer: EdDSA only, the
// issuer's key ID, the credential type, issuer, audience, the key binding and
// the one-hour lifetime with clock leeway. It does not consult revocation.
func (i homeDeviceIssuerPublic) verify(raw string, now time.Time) (homeDeviceCredentialClaims, error) {
	var claims homeDeviceCredentialClaims
	if len(raw) == 0 || len(raw) > homeDeviceMaxTokenBytes || strings.Count(raw, ".") != 2 || len(i.key) != ed25519.PublicKeySize {
		return claims, ErrHomeAccessDenied
	}
	token, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || len(token.Headers) != 1 {
		return claims, ErrHomeAccessDenied
	}
	header := token.Headers[0]
	if header.Algorithm != string(jose.EdDSA) || header.KeyID != i.keyID || header.JSONWebKey != nil ||
		header.ExtraHeaders[jose.HeaderType] != HomeDeviceCredentialType {
		return claims, ErrHomeAccessDenied
	}
	if token.Claims(i.key, &claims) != nil {
		return claims, ErrHomeAccessDenied
	}
	if claims.Issuer != i.issuer || len(claims.Audience) != 1 || claims.Audience[0] != i.audience ||
		claims.Subject == "" || claims.ID == "" || claims.Account == "" || claims.AuthTime <= 0 || claims.Confirmation.JWKThumbprint != claims.Subject {
		return claims, ErrHomeAccessDenied
	}
	if claims.IssuedAt == nil || claims.NotBefore == nil || claims.Expiry == nil {
		return claims, ErrHomeAccessDenied
	}
	issued, expires := claims.IssuedAt.Time(), claims.Expiry.Time()
	if !claims.NotBefore.Time().Equal(issued) || issued.After(now.Add(HomeClockLeeway)) || !now.Before(expires.Add(HomeClockLeeway)) ||
		expires.Sub(issued) != HomeDeviceCredentialLifetime {
		return claims, ErrHomeAccessDenied
	}
	return claims, nil
}

// homeDeviceProof is a verified DPoP proof: its signature matches the
// embedded device key, and its time is inside the 30 second window.
type homeDeviceProof struct {
	Thumbprint string
	Key        jose.JSONWebKey
	ID         string
	IssuedAt   time.Time
	Method     string
	Target     string
	AccessHash string
}

// parseHomeDeviceProof verifies a DPoP proof's own signature with the key in
// its header (ES256 on P-256 or EdDSA on Ed25519, nothing else) and the
// 30 second window with clock leeway. Binding the proof to a request, a
// credential and a registry entry is the caller's job.
func parseHomeDeviceProof(raw string, now time.Time) (homeDeviceProof, error) {
	return parseHomeKeyProof(raw, HomeDeviceProofType, HomeDeviceProofWindow, now)
}

// parseHomeKeyProof is the one parser of a proof signed by a device key: a
// DPoP proof, or a Hub launch proof (typ, window and extra claims of its own).
func parseHomeKeyProof(raw, typ string, window time.Duration, now time.Time, extra ...any) (homeDeviceProof, error) {
	var proof homeDeviceProof
	if len(raw) == 0 || len(raw) > homeDeviceMaxTokenBytes || strings.Count(raw, ".") != 2 {
		return proof, ErrHomeAccessDenied
	}
	token, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.ES256, jose.EdDSA})
	if err != nil || len(token.Headers) != 1 {
		return proof, ErrHomeAccessDenied
	}
	header := token.Headers[0]
	if header.ExtraHeaders[jose.HeaderType] != typ || header.JSONWebKey == nil {
		return proof, ErrHomeAccessDenied
	}
	key := *header.JSONWebKey
	if !validHomeDeviceKey(key, header.Algorithm) {
		return proof, ErrHomeAccessDenied
	}
	var claims struct {
		jwt.Claims
		Method     string `json:"htm"`
		Target     string `json:"htu"`
		AccessHash string `json:"ath"`
	}
	if token.Claims(key.Key, append([]any{&claims}, extra...)...) != nil || len(claims.ID) < 22 || len(claims.ID) > 128 || claims.IssuedAt == nil ||
		claims.Method == "" || claims.Target == "" {
		return proof, ErrHomeAccessDenied
	}
	issued := claims.IssuedAt.Time()
	if issued.After(now.Add(HomeClockLeeway)) || now.Sub(issued) > window+HomeClockLeeway {
		return proof, ErrHomeAccessDenied
	}
	thumbprint, err := homeKeyThumbprint(key.Key)
	if err != nil {
		return proof, ErrHomeAccessDenied
	}
	return homeDeviceProof{
		Thumbprint: thumbprint, Key: jose.JSONWebKey{Key: key.Key, Algorithm: header.Algorithm, Use: "sig"},
		ID: claims.ID, IssuedAt: issued, Method: claims.Method, Target: claims.Target, AccessHash: claims.AccessHash,
	}, nil
}

// validHomeDeviceKey admits only public P-256 keys for ES256 and Ed25519 keys
// for EdDSA, so a header algorithm can never reinterpret the key.
func validHomeDeviceKey(key jose.JSONWebKey, algorithm string) bool {
	if !key.IsPublic() || !key.Valid() {
		return false
	}
	switch material := key.Key.(type) {
	case ed25519.PublicKey:
		return len(material) == ed25519.PublicKeySize && algorithm == string(jose.EdDSA)
	case *ecdsa.PublicKey:
		return material.Curve == elliptic.P256() && algorithm == string(jose.ES256)
	default:
		return false
	}
}

func homeKeyThumbprint(key any) (string, error) {
	thumb, err := (&jose.JSONWebKey{Key: key}).Thumbprint(crypto.SHA256)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(thumb), nil
}
