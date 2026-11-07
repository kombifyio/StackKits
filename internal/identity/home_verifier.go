package identity

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	HomePublicTrustSchema        = "stackkit.home-public-trust/v2"
	homeVerifierStatePath        = ".stackkit/custody/home-verifier/state.json"
	homeVerifierMaxTrustLifetime = 24 * time.Hour
	homeVerifierMaxProofBytes    = 64 << 10
	homeVerifierMaxStateBytes    = 512 << 10

	// The Authority's own verifier rereads its registry at least this often,
	// well inside the 60 second revocation target, and at most once a second
	// for a credential whose device it has not seen yet.
	homeRevocationRefresh     = 10 * time.Second
	homeRevocationMissRefresh = time.Second
	// Replay state is bounded overall and per device key, so one key cannot
	// lock every other device out by filling it.
	homeReplayCapacity = 1 << 18
	homeReplayPerKey   = 1 << 12
	homeReplaySweep    = 5 * time.Second
	// A proof is kept one second past the last instant the parser accepts it.
	homeProofReplayRetention = HomeDeviceProofWindow + HomeClockLeeway + time.Second
	// A renewal proof's jti also binds its passkey assertion, so it is kept
	// for as long as that assertion could still be fresh.
	homeRenewalReplayRetention = HomeUserVerificationFreshness + HomeDeviceProofWindow + 2*HomeClockLeeway + time.Second
)

var homeVerifierAllowedJWTAlgs = []jose.SignatureAlgorithm{jose.EdDSA, jose.ES256, jose.RS256}

// HomePocketIDTrust is the live PocketID OIDC authority. The CUE human issuer
// URN cannot alias this HTTPS issuer or its OIDC client audience. ClientID is
// always HomeDeviceClientID, the device sign-in client the pin was bound with.
type HomePocketIDTrust struct {
	Issuer   string             `json:"issuer"`
	ClientID string             `json:"clientId"`
	Subject  string             `json:"subject"`
	Keys     jose.JSONWebKeySet `json:"keys"`
}

// HomePublicTrust is the Owner-signed Pocket ID pin of this Home: issuer,
// signing keys, the Owner subject and the revoked Homelab accounts. It backs
// every passkey verification (enrollment, renewal user verification, every
// step-up). RefreshHomePublicTrust binds it in production.
// Device credentials are verified against the device issuer and registry, not
// against this pin.
type HomePublicTrust struct {
	Schema          string                                  `json:"schema"`
	HomeSiteRef     string                                  `json:"homeSiteRef"`
	PocketID        HomePocketIDTrust                       `json:"pocketId"`
	RevokedSubjects []string                                `json:"revokedSubjects"`
	IssuedAt        time.Time                               `json:"issuedAt"`
	ExpiresAt       time.Time                               `json:"expiresAt"`
	Signature       localevidence.OwnerPolicyStateSignature `json:"signature"`
}

type homeVerifierState struct {
	Trust  HomePublicTrust `json:"trust"`
	Active bool            `json:"active"`
}

// BindHomePublicTrust is local Owner publication of the current Pocket ID pin.
// It does not enroll a device or mint a credential. Every device of a revoked
// Homelab account is revoked in the registry before the pin is published, so
// the account's credentials stop renewing at once and stop verifying within
// the revocation refresh.
func BindHomePublicTrust(root string, trust HomePublicTrust) error {
	trust, err := signHomePublicTrust(root, trust)
	if err != nil {
		return err
	}
	release, err := localevidence.AcquireCustodyLock(root, homeVerifierStatePath, 1)
	if err != nil {
		return err
	}
	defer release()
	if prior, loadErr := loadOwnerSignedHomeVerifierState(root); loadErr == nil {
		if !sameHomePublicTrustPin(prior.Trust, trust) || !trust.IssuedAt.After(prior.Trust.IssuedAt) {
			return ErrHomeAccessDenied
		}
		for _, id := range prior.Trust.RevokedSubjects {
			if !slices.Contains(trust.RevokedSubjects, id) {
				return ErrHomeAccessDenied
			}
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}
	if err := revokeHomeAccountDevices(root, trust.RevokedSubjects, time.Now()); err != nil {
		return err
	}
	return writeHomeVerifierState(root, homeVerifierState{Trust: trust, Active: true})
}

// revokeHomeAccountDevices permanently revokes every device of the given
// Homelab accounts.
func revokeHomeAccountDevices(root string, accounts []string, now time.Time) error {
	if len(accounts) == 0 {
		return nil
	}
	var revoked []string
	err := updateHomeDeviceRegistry(root, func(registry *homeDeviceRegistry) error {
		at := now.UTC()
		for index := range registry.Devices {
			device := &registry.Devices[index]
			if slices.Contains(accounts, device.AccountSubject) {
				if !device.Revoked {
					device.Revoked, device.RevokedAt, device.RevokedBy = true, &at, HomeRevokedByRemoval
				}
				revoked = append(revoked, device.ID)
				resolveHomeNotices(registry, device.ID, HomeNoticeClosed, HomeRevokedByOwner, at)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	invalidateHomeVerifier(root)
	// A revoked account keeps no VPN add-on peer either (where this workspace
	// holds the add-on's custody; the host's resync drops it otherwise).
	if _, err = removeHomeOverlayPeers(root, revoked); errors.Is(err, ErrHomeOverlayCustodyAbsent) {
		return nil
	}
	return err
}

// HomeDeviceRequest is one request to a service, as the server running the
// service observed it. RouteOrigin is the https origin of the matched route
// taken from the server's own route table, never from a request header; Path
// is the request target in origin form (a query is allowed and is not part of
// the proof). Credential is the token of "Authorization: DPoP <credential>",
// Proof the "DPoP" header (see ParseHomeDeviceAuthorization).
type HomeDeviceRequest struct {
	Method      string `json:"method"`
	RouteOrigin string `json:"routeOrigin"`
	Path        string `json:"path"`
	Credential  string `json:"credential"`
	Proof       string `json:"proof"`
}

// HomeAuthentication is request-scoped admission of a Homelab account on an
// enrolled device, not privileged approval. Subject is the account (Pocket ID
// subject); Device is the device ID (key thumbprint).
type HomeAuthentication struct {
	Subject        string    `json:"subject"`
	Device         string    `json:"device"`
	CredentialID   string    `json:"credentialId"`
	ExpiresAt      time.Time `json:"expiresAt"`
	UserVerifiedAt time.Time `json:"userVerifiedAt"`
}

// HomeRenewalRequest is one credential renewal at the Authority. Proof is a
// DPoP proof (htm POST, htu https://<host>HomeDeviceRenewalPath) signed by the
// enrolled device key. UserVerification is optional until the last user
// verification is 24 hours old: then it must be a Pocket ID ID token of the
// device's account from a passkey sign-in through HomeDeviceClientID with the
// nonce HomeDeviceUserVerificationNonce(thumbprint, proof jti).
type HomeRenewalRequest struct {
	Method           string
	Proof            string
	UserVerification string
}

// HomeVerifier verifies device credentials and DPoP proofs for the services of
// this server and renews credentials on the Authority. It keeps the replay
// state of every proof it accepted until the proof's window has passed, so one
// long-lived verifier serves a process; use HomeVerifierFor. A proof whose
// time, less the clock leeway, is not after the verifier's start is refused,
// so a restart cannot reopen a replay; the cost is that proofs of the first
// seconds after a start are refused and the client retries.
type HomeVerifier struct {
	root    string
	now     func() time.Time
	started time.Time
	load    func(string) (homeDeviceSnapshot, error)

	mu        sync.Mutex
	snapshot  *homeDeviceSnapshot
	attempted time.Time
	replay    map[string]homeReplayEntry
	perKey    map[string]int
	swept     time.Time
}

type homeReplayEntry struct {
	expires time.Time
	key     string
}

// homeDeviceSnapshot is the last-known verification state: the device issuer
// and the registry entries. A failed refresh keeps it (partition rule).
type homeDeviceSnapshot struct {
	issuer   homeDeviceIssuerPublic
	devices  map[string]HomeDevice
	loadedAt time.Time

	// A joined server's snapshot (home_join_state.go) holds no registry: a
	// credential of the Authority's issuer is current unless the Authority's
	// last verified state revokes its device or account. stateAt is when the
	// Authority signed that state.
	joined          bool
	stateAt         time.Time
	revokedDevices  map[string]bool
	revokedAccounts map[string]bool
}

// joinedActive reports a device of account as not revoked in a joined
// server's snapshot.
func (s *homeDeviceSnapshot) joinedActive(id, account string) bool {
	return id != "" && account != "" && !s.revokedDevices[id] && !s.revokedAccounts[account]
}

var homeVerifiers sync.Map

// HomeVerifierFor returns the process-wide verifier of a workspace.
func HomeVerifierFor(root string) (*HomeVerifier, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if existing, ok := homeVerifiers.Load(absolute); ok {
		return existing.(*HomeVerifier), nil
	}
	verifier, _ := homeVerifiers.LoadOrStore(absolute, newHomeVerifier(absolute, time.Now))
	return verifier.(*HomeVerifier), nil
}

func invalidateHomeVerifier(root string) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return
	}
	if existing, ok := homeVerifiers.Load(absolute); ok {
		existing.(*HomeVerifier).invalidate()
	}
}

func newHomeVerifier(root string, now func() time.Time) *HomeVerifier {
	return &HomeVerifier{
		root: root, now: now, started: now().UTC(), load: loadHomeAuthoritySnapshot,
		replay: map[string]homeReplayEntry{}, perKey: map[string]int{},
	}
}

// Authenticate admits one request only when its credential is a current
// credential of an active registry entry and its proof is a fresh, unused DPoP
// proof signed by the credential's key for exactly this method, route origin
// and path. Every refusal is ErrHomeAccessDenied. While the registry cannot be
// refreshed, issued credentials stay valid to their lifetime against the
// last-known state; a device the verifier has never seen stays refused.
func (v *HomeVerifier) Authenticate(request HomeDeviceRequest) (HomeAuthentication, error) {
	deny := func() (HomeAuthentication, error) { return HomeAuthentication{}, ErrHomeAccessDenied }
	now := v.now().UTC()
	target, ok := homeProofTarget(request.RouteOrigin, request.Path)
	if !ok || !validHomeMethod(request.Method) {
		return deny()
	}
	proof, err := parseHomeDeviceProof(request.Proof, now)
	if err != nil || proof.Method != request.Method || proof.Target != target || !v.afterStart(proof.IssuedAt) ||
		subtle.ConstantTimeCompare([]byte(proof.AccessHash), []byte(homeTokenHash(request.Credential))) != 1 {
		return deny()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	claims, device, err := v.credentialLocked(request.Credential, now)
	if err != nil || claims.Confirmation.JWKThumbprint != proof.Thumbprint || device.AccountSubject != claims.Account {
		return deny()
	}
	if !v.rememberLocked(proof.Thumbprint, proof.ID, proof.IssuedAt.Add(homeProofReplayRetention), now) {
		return deny()
	}
	return HomeAuthentication{
		Subject: claims.Account, Device: claims.Subject, CredentialID: claims.ID,
		ExpiresAt: claims.Expiry.Time().UTC(), UserVerifiedAt: time.Unix(claims.AuthTime, 0).UTC(),
	}, nil
}

// RenewDeviceCredential signs a new one-hour credential for an active
// registry entry whose key signed the proof. After 24 hours without user
// verification it returns ErrHomeUserVerificationRequired unless the request
// carries a valid passkey assertion; every other refusal is
// ErrHomeAccessDenied. It runs only on the Authority (the holder of the device
// issuer key and registry) and fails closed whenever the registry, or for a
// user verification the Pocket ID pin, is not current. A key that is not an
// active registry entry is refused before it reaches replay state or the
// registry lock.
func (v *HomeVerifier) RenewDeviceCredential(request HomeRenewalRequest) (HomeDeviceCredential, error) {
	deny := func() (HomeDeviceCredential, error) { return HomeDeviceCredential{}, ErrHomeAccessDenied }
	now := v.now().UTC()
	proof, err := parseHomeDeviceProof(request.Proof, now)
	if err != nil || request.Method != http.MethodPost || proof.Method != http.MethodPost || !homeIdentityOriginAdmitted(v.root, proof.Target, HomeDeviceRenewalPath) || !v.afterStart(proof.IssuedAt) {
		return deny()
	}
	current, err := loadHomeDeviceRegistry(v.root)
	if err != nil {
		return deny()
	}
	entry, known := findHomeDevice(current.Devices, proof.Thumbprint)
	if known && entry.Pending(now) {
		// Only the holder of the pending key can sign this proof, so the
		// answer reveals nothing to anyone else.
		return HomeDeviceCredential{}, ErrHomeEnrollmentPending
	}
	if !known || !entry.Active(now) {
		return deny()
	}
	var trust HomePublicTrust
	if request.UserVerification != "" {
		state, stateErr := loadHomeVerifierState(v.root)
		if stateErr != nil || !state.Active || validateBoundHomePublicTrust(v.root, state.Trust, now) != nil {
			return deny()
		}
		trust = state.Trust
	}
	v.mu.Lock()
	fresh := v.rememberLocked(proof.Thumbprint, proof.ID, now.Add(homeRenewalReplayRetention), now)
	v.mu.Unlock()
	if !fresh {
		return deny()
	}
	issuer, err := loadHomeDeviceIssuer(v.root)
	if err != nil {
		return deny()
	}
	var renewed HomeDevice
	err = updateHomeDeviceRegistry(v.root, func(registry *homeDeviceRegistry) error {
		for index := range registry.Devices {
			device := &registry.Devices[index]
			if device.ID != proof.Thumbprint {
				continue
			}
			if !device.Active(now) {
				return ErrHomeAccessDenied
			}
			if request.UserVerification != "" {
				verified, uvErr := verifyHomeUserVerification(request.UserVerification, trust, device.AccountSubject,
					HomeDeviceUserVerificationNonce(proof.Thumbprint, proof.ID), v.started, now)
				if uvErr != nil {
					return ErrHomeAccessDenied
				}
				device.LastUserVerifiedAt = verified
			} else if now.Sub(device.LastUserVerifiedAt) >= HomeDeviceUserVerificationInterval {
				return ErrHomeUserVerificationRequired
			}
			device.LastUsedAt = now
			renewed = *device
			return nil
		}
		return ErrHomeAccessDenied
	})
	if errors.Is(err, ErrHomeUserVerificationRequired) {
		return HomeDeviceCredential{}, err
	}
	if err != nil {
		return deny()
	}
	return issuer.mint(renewed, now)
}

// credentialLocked verifies the credential against the current snapshot and
// returns its active registry entry. It refreshes the snapshot when it is due
// or, rate limited, when a correctly signed credential names an unseen device.
func (v *HomeVerifier) credentialLocked(raw string, now time.Time) (homeDeviceCredentialClaims, HomeDevice, error) {
	refreshed := v.refreshLocked(now, now.Sub(v.attempted) >= homeRevocationRefresh)
	for {
		if v.snapshot == nil {
			return homeDeviceCredentialClaims{}, HomeDevice{}, ErrHomeAccessDenied
		}
		claims, err := v.snapshot.issuer.verify(raw, now)
		if err != nil {
			return claims, HomeDevice{}, err
		}
		if v.snapshot.joined {
			// Partition rule: the last verified state of the Authority stands
			// while no newer one can be read.
			if !v.snapshot.joinedActive(claims.Subject, claims.Account) {
				return claims, HomeDevice{}, ErrHomeAccessDenied
			}
			return claims, HomeDevice{ID: claims.Subject, AccountSubject: claims.Account}, nil
		}
		if device, ok := v.snapshot.devices[claims.Subject]; ok {
			if !device.Active(now) {
				return claims, HomeDevice{}, ErrHomeAccessDenied
			}
			return claims, device, nil
		}
		if refreshed || now.Sub(v.attempted) < homeRevocationMissRefresh {
			return claims, HomeDevice{}, ErrHomeAccessDenied
		}
		refreshed = v.refreshLocked(now, true)
	}
}

// refreshLocked reloads the snapshot when forced or absent. A failed reload
// keeps the last-known snapshot. It reports whether it attempted a load.
func (v *HomeVerifier) refreshLocked(now time.Time, force bool) bool {
	if !force && v.snapshot != nil {
		return false
	}
	if v.snapshot == nil && !force && now.Sub(v.attempted) < homeRevocationMissRefresh {
		return false
	}
	v.attempted = now
	next, err := v.load(v.root)
	if err != nil {
		return true
	}
	next.loadedAt = now
	v.snapshot = &next
	return true
}

func (v *HomeVerifier) invalidate() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.attempted = time.Time{}
}

// rememberLocked records a device key's proof until its expiry and refuses
// one it has seen. Expired entries are pruned; a full cache, overall or for
// this key, refuses rather than forgets.
func (v *HomeVerifier) rememberLocked(thumbprint, proofID string, until, now time.Time) bool {
	if now.Sub(v.swept) >= homeReplaySweep || len(v.replay) >= homeReplayCapacity || v.perKey[thumbprint] >= homeReplayPerKey {
		for seen, entry := range v.replay {
			if !now.Before(entry.expires) {
				delete(v.replay, seen)
				if v.perKey[entry.key]--; v.perKey[entry.key] <= 0 {
					delete(v.perKey, entry.key)
				}
			}
		}
		v.swept = now
	}
	id := homeTokenHash(thumbprint + "\x00" + proofID)
	if entry, seen := v.replay[id]; seen && now.Before(entry.expires) {
		return false
	}
	if len(v.replay) >= homeReplayCapacity || v.perKey[thumbprint] >= homeReplayPerKey {
		return false
	}
	v.replay[id] = homeReplayEntry{expires: until, key: thumbprint}
	v.perKey[thumbprint]++
	return true
}

// afterStart refuses a proof that a previous process could have accepted: one
// issued, within the clock leeway, before this verifier started.
func (v *HomeVerifier) afterStart(issued time.Time) bool {
	return issued.Add(-HomeClockLeeway).After(v.started.Truncate(time.Second))
}

func loadHomeAuthoritySnapshot(root string) (homeDeviceSnapshot, error) {
	// A joined server verifies the Authority's credentials, never its own.
	if bundle, err := LoadHomeJoinedTrust(root); err == nil {
		return loadHomeJoinedSnapshot(root, bundle)
	} else if !errors.Is(err, os.ErrNotExist) {
		return homeDeviceSnapshot{}, err
	}
	issuer, err := loadHomeDeviceIssuer(root)
	if err != nil {
		return homeDeviceSnapshot{}, err
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return homeDeviceSnapshot{}, err
	}
	devices := make(map[string]HomeDevice, len(registry.Devices))
	for _, device := range registry.Devices {
		devices[device.ID] = device
	}
	return homeDeviceSnapshot{issuer: issuer.homeDeviceIssuerPublic, devices: devices}, nil
}

// verifyHomeUserVerification checks the passkey assertion of a renewal and
// returns when the person verified.
func verifyHomeUserVerification(raw string, trust HomePublicTrust, subject, nonce string, started, now time.Time) (time.Time, error) {
	claims, err := verifyHomePasskeyIDToken(raw, trust, HomeDeviceClientID, nonce, now)
	if err != nil || claims.Subject != subject || claims.IssuedAt == nil {
		return time.Time{}, ErrHomeAccessDenied
	}
	verified := claims.IssuedAt.Time().UTC()
	if !verified.Add(-HomeClockLeeway).After(started.Truncate(time.Second)) || now.Sub(verified) > HomeUserVerificationFreshness {
		return time.Time{}, ErrHomeAccessDenied
	}
	if verified.After(now) {
		verified = now
	}
	return verified, nil
}

func homeProofTarget(origin, path string) (string, bool) {
	if validateHomeOrigin(origin) != nil {
		return "", false
	}
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || len(path) > 8<<10 || strings.ContainsAny(path, "#\\ \t\r\n") {
		return "", false
	}
	return origin + path, true
}

func validHomeMethod(method string) bool {
	if method == "" || len(method) > 16 {
		return false
	}
	for _, r := range method {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// signHomePublicTrust signs the Home trust with the Owner key. Only a
// process that holds the Owner custody (the host) can; the core's identity
// workspace holds none and fails here.
func signHomePublicTrust(root string, trust HomePublicTrust) (HomePublicTrust, error) {
	authority, err := homeAuthority(root)
	if err != nil || authority.SiteRef != trust.HomeSiteRef {
		return trust, ErrHomeAccessDenied
	}
	trust.Schema = HomePublicTrustSchema
	trust.IssuedAt, trust.ExpiresAt = trust.IssuedAt.UTC(), trust.ExpiresAt.UTC()
	if err := validateHomePublicTrustFields(trust, authority, time.Now().UTC()); err != nil {
		return trust, err
	}
	trust.Signature, err = localevidence.SignHomeHumanDeviceTrust(root, homeTrustBytes(trust))
	return trust, err
}

// validateBoundHomePublicTrust verifies the bound Home trust against the
// Authority's public record: its Owner key, site and Owner subject.
func validateBoundHomePublicTrust(root string, trust HomePublicTrust, now time.Time) error {
	authority, err := homeAuthority(root)
	if err != nil {
		return ErrHomeAccessDenied
	}
	if err := validateHomePublicTrustFields(trust, authority, now); err != nil {
		return err
	}
	if authority.VerifyHomeHumanDeviceTrust(homeTrustBytes(trust), trust.Signature) != nil {
		return ErrHomeAccessDenied
	}
	return nil
}

func validateHomePublicTrustFields(trust HomePublicTrust, authority localevidence.HomeAuthorityPublic, now time.Time) error {
	if trust.Schema != HomePublicTrustSchema || trust.HomeSiteRef != authority.SiteRef {
		return ErrHomeAccessDenied
	}
	if err := validateHomeTrustTiming(trust, now); err != nil {
		return err
	}
	if !validHomePublicKeys(trust.PocketID.Keys) {
		return ErrHomeAccessDenied
	}
	return validateHomePocketIDPin(trust.PocketID, authority)
}

func validateHomeTrustTiming(trust HomePublicTrust, now time.Time) error {
	if trust.IssuedAt.Location() != time.UTC || trust.ExpiresAt.Location() != time.UTC ||
		!trust.ExpiresAt.After(trust.IssuedAt) || trust.IssuedAt.After(now.Add(HomeClockLeeway)) || !trust.ExpiresAt.After(now) ||
		trust.ExpiresAt.Sub(trust.IssuedAt) > homeVerifierMaxTrustLifetime {
		return ErrHomeAccessDenied
	}
	return nil
}

// validateHomePocketIDPin admits the pin only for the Owner subject the
// Authority bound (its owner runtime binding, or the Owner custody projection
// before the first binding) and an https issuer.
func validateHomePocketIDPin(trust HomePocketIDTrust, authority localevidence.HomeAuthorityPublic) error {
	if trust.Subject == "" || trust.ClientID != HomeDeviceClientID || trust.Subject != authority.OwnerPocketIDSubject {
		return ErrHomeAccessDenied
	}
	issuer, err := url.Parse(trust.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return ErrHomeAccessDenied
	}
	return nil
}

// validateHomeOrigin admits only a canonical https origin: lowercase host,
// no default port, no path, query, fragment or user information.
func validateHomeOrigin(origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != origin || strings.ToLower(parsed.Host) != parsed.Host || parsed.Port() == "443" {
		return ErrHomeAccessDenied
	}
	return nil
}

func sameHomePublicTrustPin(prior, next HomePublicTrust) bool {
	return prior.HomeSiteRef == next.HomeSiteRef && prior.PocketID.Issuer == next.PocketID.Issuer &&
		prior.PocketID.ClientID == next.PocketID.ClientID && prior.PocketID.Subject == next.PocketID.Subject
}

// verifyHomeJWT verifies a Pocket ID token against the pinned key set: the
// header must name one pinned key, whose own algorithm must equal the header
// algorithm, so no token can choose its verification algorithm.
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

// validHomeTimeClaims requires iat and exp; nbf is optional (Pocket ID omits
// it) and, when present, must equal iat.
func validHomeTimeClaims(claims jwt.Claims, now time.Time) bool {
	return claims.IssuedAt != nil && claims.Expiry != nil &&
		!claims.IssuedAt.Time().After(now.Add(HomeClockLeeway)) &&
		(claims.NotBefore == nil || claims.NotBefore.Time().Equal(claims.IssuedAt.Time())) &&
		now.Before(claims.Expiry.Time().Add(HomeClockLeeway)) && claims.Expiry.Time().After(claims.IssuedAt.Time())
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

// loadOwnerSignedHomeVerifierState is the bound state only when the Owner
// signed its trust. The state sits in the verification custody the core can
// write, so the Owner's signing path carries over nothing it did not sign: a
// state without a valid Owner signature counts as absent.
func loadOwnerSignedHomeVerifierState(root string) (homeVerifierState, error) {
	state, err := loadHomeVerifierState(root)
	if err != nil {
		return state, err
	}
	authority, err := homeAuthority(root)
	if err != nil {
		return homeVerifierState{}, err
	}
	if authority.VerifyHomeHumanDeviceTrust(homeTrustBytes(state.Trust), state.Trust.Signature) != nil {
		return homeVerifierState{}, os.ErrNotExist
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
