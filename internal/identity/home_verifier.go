package identity

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	HomePublicTrustSchema        = "stackkit.home-human-device-trust/v1"
	HomeDeviceCredentialType     = "stackkit-device-credential+jwt"
	homeVerifierStatePath        = ".stackkit/custody/home-verifier/state.json"
	homeVerifierReplayDir        = ".stackkit/custody/home-verifier/replay"
	homeVerifierMaxTrustLifetime = 24 * time.Hour
	homeVerifierDPoPWindow       = 30 * time.Second
	homeVerifierMaxProofBytes    = 64 << 10
	homeVerifierMaxDPoPBytes     = 16 << 10
	homeVerifierMaxStateBytes    = 512 << 10
)

var (
	ErrHomeAccessDenied          = errors.New("identity: current independent Home human and device proof required")
	ErrHomeEnrollmentUnavailable = errors.New("identity: Home enrollment remains unavailable until local pairing and human step-up authority are connected")
	homeVerifierAllowedJWTAlgs   = []jose.SignatureAlgorithm{jose.EdDSA, jose.ES256, jose.RS256}
)

// HomePocketIDTrust is the live PocketID OIDC authority. The CUE human issuer
// URN cannot alias this HTTPS issuer or its OIDC client audience.
type HomePocketIDTrust struct {
	Issuer   string             `json:"issuer"`
	ClientID string             `json:"clientId"`
	Subject  string             `json:"subject"`
	Keys     jose.JSONWebKeySet `json:"keys"`
}

// HomePublicTrust is current Home public verifier material. Device issuer
// fields rebound HomeDeviceAuthority; partition denial rebounds Modern
// IdentityTrust. Private keys, pairing state and enrollment tokens are absent.
type HomePublicTrust struct {
	Schema                        string                                  `json:"schema"`
	HomeSiteRef                   string                                  `json:"homeSiteRef"`
	PublicOrigin                  string                                  `json:"publicOrigin"`
	DeviceAuthorityRef            string                                  `json:"deviceAuthorityRef"`
	DeviceIssuer                  string                                  `json:"deviceIssuer"`
	DeviceAudiences               []string                                `json:"deviceAudiences"`
	DeviceKeySetRef               string                                  `json:"deviceKeySetRef"`
	DeviceCredentialTTLSeconds    int                                     `json:"deviceCredentialTTLSeconds"`
	DeviceSessionTTLSeconds       int                                     `json:"deviceSessionTTLSeconds"`
	HumanSessionTTLSeconds        int                                     `json:"humanSessionTTLSeconds"`
	RevocationMaxStalenessSeconds int                                     `json:"revocationMaxStalenessSeconds"`
	PartitionDenied               bool                                    `json:"partitionDenied"`
	PocketID                      HomePocketIDTrust                       `json:"pocketId"`
	DeviceKeys                    jose.JSONWebKeySet                      `json:"deviceKeys"`
	RevokedCredentialIDs          []string                                `json:"revokedCredentialIds"`
	RevokedSubjects               []string                                `json:"revokedSubjects"`
	IssuedAt                      time.Time                               `json:"issuedAt"`
	ExpiresAt                     time.Time                               `json:"expiresAt"`
	Signature                     localevidence.OwnerPolicyStateSignature `json:"signature"`
}

type HomeConfirmation struct {
	JWKThumbprint string `json:"jkt"`
}

type homeDeviceCredentialClaims struct {
	jwt.Claims
	HumanSubject string           `json:"humanSubject"`
	Confirmation HomeConfirmation `json:"cnf"`
}

// HomeAccessProof is a credential and must never be logged. HumanSession is
// the original PocketID ID token. DeviceCredential is signed by the Home
// device-authority issuer, never the workload mTLS issuer.
type HomeAccessProof struct {
	HumanSession     string `json:"humanSession"`
	DeviceCredential string `json:"deviceCredential"`
	DPoP             string `json:"dpop"`
}

// HomeAuthentication is request-scoped admission, not privileged approval.
type HomeAuthentication struct {
	Subject   string    `json:"subject"`
	Device    string    `json:"device"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type homeVerifierState struct {
	Trust  HomePublicTrust `json:"trust"`
	Active bool            `json:"active"`
}

type homeAccessDecision struct {
	Subject   string
	Device    string
	ProofID   string
	ExpiresAt time.Time
}

// EnrollHomeDevice stays closed until pairing and human step-up exist.
func EnrollHomeDevice(string, json.RawMessage) error {
	return ErrHomeEnrollmentUnavailable
}

// BindHomePublicTrust is local Owner publication of current public trust.
// It does not enroll a device or mint a credential.
func BindHomePublicTrust(root string, trust HomePublicTrust) error {
	trust, err := signHomePublicTrust(root, trust)
	if err != nil {
		return err
	}
	fs, err := confinedfs.Open(root)
	if err != nil {
		return err
	}
	defer func() { _ = fs.Close() }()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Close() }()
	lock, err := tx.TryAcquireOutputLock(homeVerifierStatePath)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	if prior, loadErr := loadHomeVerifierState(root); loadErr == nil {
		if !sameHomePublicTrustPin(prior.Trust, trust) || !trust.IssuedAt.After(prior.Trust.IssuedAt) {
			return ErrHomeAccessDenied
		}
		for _, id := range prior.Trust.RevokedCredentialIDs {
			if !slices.Contains(trust.RevokedCredentialIDs, id) {
				return ErrHomeAccessDenied
			}
		}
		for _, id := range prior.Trust.RevokedSubjects {
			if !slices.Contains(trust.RevokedSubjects, id) {
				return ErrHomeAccessDenied
			}
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}
	return writeHomeVerifierState(root, homeVerifierState{Trust: trust, Active: true})
}

// AuthenticateHomeRequest reloads current signed Home trust for every request
// and durably consumes the DPoP proof. Workload mTLS, API keys and forwarded
// identity headers cannot satisfy this boundary.
func AuthenticateHomeRequest(root, method, target string, proof HomeAccessProof) (HomeAuthentication, error) {
	var result HomeAuthentication
	state, err := loadHomeVerifierState(root)
	if err != nil || !state.Active {
		return result, ErrHomeAccessDenied
	}
	now := time.Now().UTC()
	if err := validateBoundHomePublicTrust(root, state.Trust, now); err != nil {
		return result, err
	}
	decision, err := verifyHomeAccess(proof, method, target, state.Trust, now)
	if err != nil {
		return result, err
	}
	claim, err := json.Marshal(struct {
		ExpiresAt time.Time `json:"expiresAt"`
	}{decision.ExpiresAt})
	if err != nil {
		return result, ErrHomeAccessDenied
	}
	if err := writeHomePrivate(root, homeVerifierReplayDir+"/"+decision.ProofID+".json", claim, true); err != nil {
		return result, ErrHomeAccessDenied
	}
	current, err := loadHomeVerifierState(root)
	if err != nil || !current.Active || !bytes.Equal(homeTrustBytes(current.Trust), homeTrustBytes(state.Trust)) || !time.Now().UTC().Before(decision.ExpiresAt) {
		return result, ErrHomeAccessDenied
	}
	return HomeAuthentication{Subject: decision.Subject, Device: decision.Device, ExpiresAt: decision.ExpiresAt}, nil
}

func signHomePublicTrust(root string, trust HomePublicTrust) (HomePublicTrust, error) {
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil || owner.Binding.SiteRef != trust.HomeSiteRef {
		return trust, ErrHomeAccessDenied
	}
	trust.Schema = HomePublicTrustSchema
	trust.IssuedAt, trust.ExpiresAt = trust.IssuedAt.UTC(), trust.ExpiresAt.UTC()
	if err := validateHomePublicTrustFields(root, trust, owner, time.Now().UTC()); err != nil {
		return trust, err
	}
	trust.Signature, err = localevidence.SignHomeHumanDeviceTrust(root, homeTrustBytes(trust))
	return trust, err
}

func validateBoundHomePublicTrust(root string, trust HomePublicTrust, now time.Time) error {
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return ErrHomeAccessDenied
	}
	if err := validateHomePublicTrustFields(root, trust, owner, now); err != nil {
		return err
	}
	return localevidence.VerifyHomeHumanDeviceTrust(root, homeTrustBytes(trust), trust.Signature)
}

func validateHomePublicTrustFields(root string, trust HomePublicTrust, owner localevidence.OwnerCustody, now time.Time) error {
	if trust.Schema != HomePublicTrustSchema || trust.HomeSiteRef != owner.Binding.SiteRef || !trust.PartitionDenied {
		return ErrHomeAccessDenied
	}
	if err := validateHomeOrigin(trust.PublicOrigin); err != nil {
		return err
	}
	if err := validateHomeDeviceIssuerFields(trust); err != nil {
		return err
	}
	if err := validateHomeTrustTiming(trust, now); err != nil {
		return err
	}
	if !validHomePublicKeys(trust.PocketID.Keys) || !validHomePublicKeys(trust.DeviceKeys) || overlappingHomeKeys(trust.PocketID.Keys, trust.DeviceKeys) {
		return ErrHomeAccessDenied
	}
	return validateHomePocketIDPin(root, trust.PocketID, owner)
}

func validateHomeDeviceIssuerFields(trust HomePublicTrust) error {
	if strings.TrimSpace(trust.DeviceAuthorityRef) == "" || !strings.HasPrefix(trust.DeviceIssuer, "urn:stackkit:") ||
		!strings.HasPrefix(trust.DeviceKeySetRef, "urn:stackkit:") || len(trust.DeviceAudiences) == 0 {
		return ErrHomeAccessDenied
	}
	if trust.DeviceCredentialTTLSeconds <= 0 || trust.DeviceSessionTTLSeconds <= 0 || trust.HumanSessionTTLSeconds <= 0 ||
		trust.DeviceSessionTTLSeconds > trust.DeviceCredentialTTLSeconds || trust.RevocationMaxStalenessSeconds < 0 {
		return ErrHomeAccessDenied
	}
	return nil
}

func validateHomeTrustTiming(trust HomePublicTrust, now time.Time) error {
	if trust.IssuedAt.Location() != time.UTC || trust.ExpiresAt.Location() != time.UTC ||
		!trust.ExpiresAt.After(trust.IssuedAt) || trust.IssuedAt.After(now) || !trust.ExpiresAt.After(now) ||
		trust.ExpiresAt.Sub(trust.IssuedAt) > homeVerifierMaxTrustLifetime {
		return ErrHomeAccessDenied
	}
	return nil
}

func validateHomePocketIDPin(root string, trust HomePocketIDTrust, owner localevidence.OwnerCustody) error {
	if trust.Subject == "" || trust.ClientID == "" {
		return ErrHomeAccessDenied
	}
	issuer, err := url.Parse(trust.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return ErrHomeAccessDenied
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(root)
	if err == nil {
		if binding.PocketIDSubject != trust.Subject || binding.OwnerRef != owner.OwnerRef {
			return ErrHomeAccessDenied
		}
		return nil
	}
	if !errors.Is(err, localevidence.ErrOwnerRuntimeBindingMissing) || trust.Subject != owner.PocketID.Subject {
		return ErrHomeAccessDenied
	}
	return nil
}

func validateHomeOrigin(origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != origin {
		return ErrHomeAccessDenied
	}
	return nil
}

func overlappingHomeKeys(sets ...jose.JSONWebKeySet) bool {
	seen := map[string]bool{}
	for _, keys := range sets {
		for _, key := range keys.Keys {
			thumb, err := key.Thumbprint(crypto.SHA256)
			if err != nil || seen[string(thumb)] {
				return true
			}
			seen[string(thumb)] = true
		}
	}
	return false
}

func sameHomePublicTrustPin(prior, next HomePublicTrust) bool {
	return prior.HomeSiteRef == next.HomeSiteRef && prior.PublicOrigin == next.PublicOrigin &&
		prior.DeviceAuthorityRef == next.DeviceAuthorityRef && prior.DeviceIssuer == next.DeviceIssuer &&
		prior.DeviceKeySetRef == next.DeviceKeySetRef && slices.Equal(prior.DeviceAudiences, next.DeviceAudiences) &&
		prior.PocketID.Issuer == next.PocketID.Issuer && prior.PocketID.ClientID == next.PocketID.ClientID &&
		prior.PocketID.Subject == next.PocketID.Subject
}

func verifyHomeAccess(proof HomeAccessProof, method, target string, trust HomePublicTrust, now time.Time) (homeAccessDecision, error) {
	deny := func() (homeAccessDecision, error) { return homeAccessDecision{}, ErrHomeAccessDenied }
	if err := validateHomeTarget(method, target, trust.PublicOrigin); err != nil {
		return deny()
	}
	human, err := verifyHomeHumanToken(proof.HumanSession, trust, now)
	if err != nil {
		return deny()
	}
	device, err := verifyHomeDeviceToken(proof.DeviceCredential, human.Subject, trust, now)
	if err != nil {
		return deny()
	}
	dpopID, dpopExpiry, err := verifyHomeDPoP(proof, method, target, device.Confirmation.JWKThumbprint, now)
	if err != nil {
		return deny()
	}
	sessionTTL := trust.DeviceSessionTTLSeconds
	if trust.HumanSessionTTLSeconds < sessionTTL {
		sessionTTL = trust.HumanSessionTTLSeconds
	}
	expires := now.Add(time.Duration(sessionTTL) * time.Second)
	for _, deadline := range []time.Time{human.Expiry.Time(), device.Expiry.Time(), trust.ExpiresAt, dpopExpiry} {
		if deadline.Before(expires) {
			expires = deadline
		}
	}
	if !expires.After(now) {
		return deny()
	}
	return homeAccessDecision{
		Subject: human.Subject, Device: device.Subject,
		ProofID: homeTokenHash(device.Confirmation.JWKThumbprint + "\x00" + dpopID), ExpiresAt: expires,
	}, nil
}

func validateHomeTarget(method, target, origin string) error {
	if method == "" || strings.ContainsAny(method, " \t\r\n") {
		return ErrHomeAccessDenied
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != target {
		return ErrHomeAccessDenied
	}
	if parsed.Scheme+"://"+parsed.Host != origin {
		return ErrHomeAccessDenied
	}
	return nil
}

func verifyHomeHumanToken(raw string, trust HomePublicTrust, now time.Time) (jwt.Claims, error) {
	var claims struct {
		jwt.Claims
		Type string `json:"type"`
	}
	if err := verifyHomeJWT(raw, "JWT", trust.PocketID.Keys, &claims); err != nil {
		return jwt.Claims{}, err
	}
	if claims.Type != "id-token" || claims.Issuer != trust.PocketID.Issuer || claims.Subject != trust.PocketID.Subject ||
		len(claims.Audience) != 1 || claims.Audience[0] != trust.PocketID.ClientID {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	if !validHomeTimeClaims(claims.Claims, now) || slices.Contains(trust.RevokedSubjects, claims.Subject) {
		return jwt.Claims{}, ErrHomeAccessDenied
	}
	return claims.Claims, nil
}

func verifyHomeDeviceToken(raw, humanSubject string, trust HomePublicTrust, now time.Time) (homeDeviceCredentialClaims, error) {
	var claims homeDeviceCredentialClaims
	if err := verifyHomeJWT(raw, HomeDeviceCredentialType, trust.DeviceKeys, &claims); err != nil {
		return claims, err
	}
	if claims.Issuer != trust.DeviceIssuer || claims.Subject == "" || claims.ID == "" || claims.HumanSubject != humanSubject ||
		len(claims.Audience) != 1 || !slices.Contains(trust.DeviceAudiences, claims.Audience[0]) ||
		claims.Confirmation.JWKThumbprint == "" {
		return claims, ErrHomeAccessDenied
	}
	if !validHomeTimeClaims(claims.Claims, now) || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) > time.Duration(trust.DeviceCredentialTTLSeconds)*time.Second {
		return claims, ErrHomeAccessDenied
	}
	if slices.Contains(trust.RevokedCredentialIDs, claims.ID) || slices.Contains(trust.RevokedSubjects, claims.HumanSubject) {
		return claims, ErrHomeAccessDenied
	}
	return claims, nil
}

func verifyHomeDPoP(proof HomeAccessProof, method, target, jkt string, now time.Time) (string, time.Time, error) {
	key, token, err := parseHomeDPoPKey(proof.DPoP, jkt)
	if err != nil {
		return "", time.Time{}, err
	}
	var dpop struct {
		jwt.Claims
		Method     string `json:"htm"`
		Target     string `json:"htu"`
		AccessHash string `json:"ath"`
		HumanHash  string `json:"hth"`
	}
	if token.Claims(key.Key, &dpop) != nil || len(dpop.ID) < 22 || len(dpop.ID) > 128 || dpop.IssuedAt == nil ||
		dpop.IssuedAt.Time().After(now) || now.Sub(dpop.IssuedAt.Time()) > homeVerifierDPoPWindow ||
		dpop.Method != method || dpop.Target != target || dpop.AccessHash != homeTokenHash(proof.DeviceCredential) ||
		dpop.HumanHash != homeTokenHash(proof.HumanSession) {
		return "", time.Time{}, ErrHomeAccessDenied
	}
	return dpop.ID, dpop.IssuedAt.Time().Add(homeVerifierDPoPWindow), nil
}

func parseHomeDPoPKey(raw, jkt string) (jose.JSONWebKey, *jwt.JSONWebToken, error) {
	var empty jose.JSONWebKey
	if len(raw) == 0 || len(raw) > homeVerifierMaxDPoPBytes || strings.Count(raw, ".") != 2 {
		return empty, nil, ErrHomeAccessDenied
	}
	token, err := jwt.ParseSigned(raw, homeVerifierAllowedJWTAlgs)
	if err != nil || len(token.Headers) != 1 {
		return empty, nil, ErrHomeAccessDenied
	}
	header := token.Headers[0]
	if header.ExtraHeaders[jose.HeaderType] != "dpop+jwt" || header.JSONWebKey == nil {
		return empty, nil, ErrHomeAccessDenied
	}
	key := *header.JSONWebKey
	key.KeyID, key.Use, key.Algorithm = "dpop", "sig", header.Algorithm
	if !validHomePublicKeys(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{key}}) {
		return empty, nil, ErrHomeAccessDenied
	}
	thumb, err := key.Thumbprint(crypto.SHA256)
	if err != nil || base64.RawURLEncoding.EncodeToString(thumb) != jkt {
		return empty, nil, ErrHomeAccessDenied
	}
	return key, token, nil
}

func verifyHomeJWT(raw, typ string, keys jose.JSONWebKeySet, claims any) error {
	if len(raw) == 0 || len(raw) > homeVerifierMaxProofBytes || strings.Count(raw, ".") != 2 {
		return ErrHomeAccessDenied
	}
	token, err := jwt.ParseSigned(raw, homeVerifierAllowedJWTAlgs)
	if err != nil || len(token.Headers) != 1 {
		return ErrHomeAccessDenied
	}
	header := token.Headers[0]
	if header.ExtraHeaders[jose.HeaderType] != typ || header.KeyID == "" {
		return ErrHomeAccessDenied
	}
	matched := keys.Key(header.KeyID)
	if len(matched) != 1 || matched[0].Algorithm != header.Algorithm || token.Claims(matched[0].Key, claims) != nil {
		return ErrHomeAccessDenied
	}
	return nil
}

func validHomeTimeClaims(claims jwt.Claims, now time.Time) bool {
	return claims.IssuedAt != nil && claims.NotBefore != nil && claims.Expiry != nil &&
		!claims.IssuedAt.Time().After(now) && !claims.NotBefore.Time().After(now) &&
		claims.NotBefore.Time().Equal(claims.IssuedAt.Time()) && claims.Expiry.Time().After(now) &&
		claims.Expiry.Time().After(claims.IssuedAt.Time())
}

func validHomePublicKeys(keys jose.JSONWebKeySet) bool {
	if len(keys.Keys) == 0 || len(keys.Keys) > 16 {
		return false
	}
	seen := map[string]bool{}
	for _, key := range keys.Keys {
		if !key.IsPublic() || !key.Valid() || key.KeyID == "" || seen[key.KeyID] || key.Use != "sig" {
			return false
		}
		seen[key.KeyID] = true
		switch material := key.Key.(type) {
		case ed25519.PublicKey:
			if len(material) != ed25519.PublicKeySize || key.Algorithm != string(jose.EdDSA) {
				return false
			}
		case *ecdsa.PublicKey:
			if material.Curve != elliptic.P256() || key.Algorithm != string(jose.ES256) {
				return false
			}
		case *rsa.PublicKey:
			if material.N.BitLen() < 2048 || key.Algorithm != string(jose.RS256) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func homeTokenHash(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func homeTrustBytes(trust HomePublicTrust) []byte {
	trust.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, _ := json.Marshal(trust)
	return raw
}

func loadHomeVerifierState(root string) (homeVerifierState, error) {
	var state homeVerifierState
	raw, err := readHomePrivate(root, homeVerifierStatePath, homeVerifierMaxStateBytes)
	if err != nil {
		return state, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF {
		return state, ErrHomeAccessDenied
	}
	return state, nil
}

func writeHomeVerifierState(root string, state homeVerifierState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeHomePrivate(root, homeVerifierStatePath, raw, false)
}

func readHomePrivate(root, path string, limit int64) ([]byte, error) {
	fs, err := confinedfs.Open(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fs.Close() }()
	view, err := fs.View(".")
	if err != nil {
		return nil, err
	}
	file, err := view.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrHomeAccessDenied
	}
	return data, nil
}

func writeHomePrivate(root, path string, raw []byte, noReplace bool) error {
	fs, err := confinedfs.Open(root)
	if err != nil {
		return err
	}
	defer func() { _ = fs.Close() }()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	dir := filepath.ToSlash(filepath.Dir(path))
	if err := tx.MkdirAll(dir, 0700); err != nil {
		_ = tx.Close()
		return err
	}
	if err := tx.Close(); err != nil {
		return err
	}
	view, err := fs.View(".")
	if err != nil {
		return err
	}
	var result confinedfs.AtomicWriteResult
	if noReplace {
		result, err = view.WriteAtomic0600NoReplace(path, raw)
	} else {
		result, err = view.WriteAtomic0600(path, raw)
	}
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return ErrHomeAccessDenied
	}
	return nil
}
