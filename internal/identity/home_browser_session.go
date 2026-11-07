package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Browser sessions (homelab identity target §6.3, §12): a browser carries no
// per-request proof, so the Access Gate sends it to the Hub, which signs a
// 60 second launch proof with the device key for exactly one host; the Gate
// exchanges it for a per-host "__Host-" session recorded against the device:
// one hour away, four hours at home, never longer than the device stays
// active. Renewal is the same launch, silently, wherever the Hub key is
// reachable (on iOS by reopening from the Hub).
const (
	// HomeLaunchPath is the reserved path on every gated host at which the
	// Gate exchanges a launch proof for a session.
	HomeLaunchPath = "/.well-known/stackkit/launch"
	// HomeLaunchProofType is the JWS "typ" of a launch proof.
	HomeLaunchProofType = "stackkit-launch+jwt"
	// HomeLaunchProofWindow is #HomelabIdentityLifetimesV1.launchProofSeconds.
	HomeLaunchProofWindow = 60 * time.Second
	// HomeBrowserSessionCookie and HomeLaunchStateCookie are host-only
	// cookies of the gated host.
	HomeBrowserSessionCookie = "__Host-stackkit-session"
	HomeLaunchStateCookie    = "__Host-stackkit-launch"
	HomeLaunchStateLifetime  = 5 * time.Minute
	// The session lifetimes of #HomelabIdentityLifetimesV1.
	HomeBrowserSessionAway = time.Hour
	HomeBrowserSessionHome = 4 * time.Hour

	homeBrowserSessionKeyPath   = ".stackkit/custody/home-verifier/browser-session-key.json"
	homeBrowserSessionKeySchema = "stackkit.home-browser-session-key/v1"
	homeBrowserSessionDomain    = "stackkit.home-browser-session/v1\x00"
	homeBrowserSessionMaxBytes  = 2 << 10
	homeLaunchReplayRetention   = HomeLaunchProofWindow + HomeClockLeeway + time.Second
)

// HomeLaunch is one launch as the Gate observed it: the gated host the
// browser came to, the credential and proof the Hub put into the launch URL,
// and the state of the Gate's own launch cookie on that host.
type HomeLaunch struct {
	Host       string
	Credential string
	Proof      string
	State      string
	Target     string
}

// HomeBrowserSession is a verified session of one device on one host.
type HomeBrowserSession struct {
	ID        string
	Account   string
	Device    string
	Host      string
	Home      bool
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type homeBrowserSessionClaims struct {
	Version  int    `json:"v"`
	ID       string `json:"sid"`
	Host     string `json:"host"`
	Account  string `json:"acct"`
	Device   string `json:"dev"`
	Home     bool   `json:"home"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
	// DBSC marks the short-lived cookie of a device-bound session, which the
	// browser renews with its own key (home_dbsc.go).
	DBSC bool `json:"dbsc,omitempty"`
}

// AuthenticateLaunch admits one launch proof: signed by the key of an active
// device whose current credential it presents (ath), for GET on exactly this
// host's launch path, at most 60 seconds old, used once, and bound to the
// Gate's launch state and the target on this host. A state the Gate did not
// set in this browser is refused, so nobody can log a browser in as someone
// else.
func (v *HomeVerifier) AuthenticateLaunch(launch HomeLaunch) (HomeAuthentication, error) {
	deny := func() (HomeAuthentication, error) { return HomeAuthentication{}, ErrHomeAccessDenied }
	now := v.now().UTC()
	if launch.State == "" || len(launch.State) > 128 || !homeLaunchTargetOnHost(launch.Target, launch.Host) {
		return deny()
	}
	var bound struct {
		State  string `json:"state"`
		Target string `json:"target"`
	}
	proof, err := parseHomeKeyProof(launch.Proof, HomeLaunchProofType, HomeLaunchProofWindow, now, &bound)
	if err != nil || proof.Method != "GET" || proof.Target != "https://"+launch.Host+HomeLaunchPath || !v.afterStart(proof.IssuedAt) ||
		subtle.ConstantTimeCompare([]byte(bound.State), []byte(launch.State)) != 1 || bound.Target != launch.Target ||
		subtle.ConstantTimeCompare([]byte(proof.AccessHash), []byte(homeTokenHash(launch.Credential))) != 1 {
		return deny()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	claims, device, err := v.credentialLocked(launch.Credential, now)
	if err != nil || claims.Confirmation.JWKThumbprint != proof.Thumbprint || device.AccountSubject != claims.Account {
		return deny()
	}
	// A joined server opens a session only while the Authority's state is
	// current: during a partition new sign-ins fail closed.
	if v.snapshot.joined && now.Sub(v.snapshot.stateAt) > homeJoinedStateFresh {
		return deny()
	}
	if !v.rememberLocked(proof.Thumbprint, "launch\x00"+proof.ID, proof.IssuedAt.Add(homeLaunchReplayRetention), now) {
		return deny()
	}
	return HomeAuthentication{
		Subject: claims.Account, Device: claims.Subject, CredentialID: claims.ID,
		ExpiresAt: claims.Expiry.Time().UTC(), UserVerifiedAt: time.Unix(claims.AuthTime, 0).UTC(),
	}, nil
}

// IssueHomeBrowserSession mints the session value for a launch the Gate
// admitted: home when the launch came through the LAN listener.
func IssueHomeBrowserSession(root string, caller HomeAuthentication, host string, home bool, now time.Time) (string, HomeBrowserSession, error) {
	id, err := homeRandomID()
	if err != nil {
		return "", HomeBrowserSession{}, err
	}
	now = now.UTC().Truncate(time.Second)
	lifetime := HomeBrowserSessionAway
	if home {
		lifetime = HomeBrowserSessionHome
	}
	claims := homeBrowserSessionClaims{Version: 1, ID: id, Host: host, Account: caller.Subject, Device: caller.Device, Home: home,
		IssuedAt: now.Unix(), Expires: now.Add(lifetime).Unix()}
	value, err := signHomeBrowserSession(root, claims)
	if err != nil {
		return "", HomeBrowserSession{}, err
	}
	return value, HomeBrowserSession{ID: id, Account: caller.Subject, Device: caller.Device, Host: host, Home: home, IssuedAt: now, ExpiresAt: now.Add(lifetime)}, nil
}

// VerifyHomeBrowserSession admits a session value only for the host it was
// issued for, before its end, and while its device is an active registry
// entry of its account, so revoking the device ends its sessions within the
// registry refresh. Away (remote) a home session counts only for the away
// lifetime.
func (v *HomeVerifier) VerifyHomeBrowserSession(value, host string, remote bool) (HomeBrowserSession, error) {
	deny := func() (HomeBrowserSession, error) { return HomeBrowserSession{}, ErrHomeAccessDenied }
	claims, err := parseHomeBrowserSessionClaims(v.root, value)
	if err != nil || claims.Host != host {
		return deny()
	}
	now := v.now().UTC()
	issued, expires := time.Unix(claims.IssuedAt, 0).UTC(), time.Unix(claims.Expires, 0).UTC()
	if remote && claims.Home && expires.After(issued.Add(HomeBrowserSessionAway)) {
		expires = issued.Add(HomeBrowserSessionAway)
	}
	if !now.Before(expires) || issued.After(now.Add(HomeClockLeeway)) {
		return deny()
	}
	if !v.activeDevice(claims.Device, claims.Account, now) || !checkHomeDBSCSession(v.root, claims, now) {
		return deny()
	}
	return HomeBrowserSession{ID: claims.ID, Account: claims.Account, Device: claims.Device, Host: claims.Host, Home: claims.Home, IssuedAt: issued, ExpiresAt: expires}, nil
}

// parseHomeBrowserSessionClaims checks the session MAC and shape of a cookie
// value and returns its claims; lifetimes and the device are the caller's.
func parseHomeBrowserSessionClaims(root, value string) (homeBrowserSessionClaims, error) {
	var claims homeBrowserSessionClaims
	encoded, mac, ok := strings.Cut(value, ".")
	if !ok || len(value) > homeBrowserSessionMaxBytes || encoded == "" {
		return claims, ErrHomeAccessDenied
	}
	key, err := homeBrowserSessionKey(root, false)
	if err != nil {
		return claims, ErrHomeAccessDenied
	}
	got, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil || !hmac.Equal(got, homeBrowserSessionMAC(key, encoded)) {
		return claims, ErrHomeAccessDenied
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.Version != 1 || claims.Host == "" || claims.Account == "" || claims.Device == "" || claims.ID == "" {
		return homeBrowserSessionClaims{}, ErrHomeAccessDenied
	}
	return claims, nil
}

// signHomeBrowserSession encodes and MACs claims into a cookie value.
func signHomeBrowserSession(root string, claims homeBrowserSessionClaims) (string, error) {
	key, err := homeBrowserSessionKey(root, true)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(homeBrowserSessionMAC(key, encoded)), nil
}

// activeDevice reports whether id is an active registry entry of account in
// the verifier's current snapshot.
func (v *HomeVerifier) activeDevice(id, account string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.refreshLocked(now, now.Sub(v.attempted) >= homeRevocationRefresh)
	if v.snapshot == nil {
		return false
	}
	if v.snapshot.joined {
		return v.snapshot.joinedActive(id, account)
	}
	device, ok := v.snapshot.devices[id]
	return ok && device.Active(now) && device.AccountSubject == account
}

// HomeLaunchURL is the Hub page a browser without a session is sent to: the
// Hub signs the launch for target and state there.
func HomeLaunchURL(root, target, state string) (string, error) {
	hub, ok := homeHubOrigin(root)
	if !ok {
		return "", errors.New("identity: no Hub origin")
	}
	return hub + HomeHubPathPrefix + "launch?" + url.Values{"target": {target}, "state": {state}}.Encode(), nil
}

// homeLaunchTargetOnHost admits an absolute https URL on exactly host.
func homeLaunchTargetOnHost(target, host string) bool {
	parsed, err := url.Parse(target)
	return err == nil && parsed.Scheme == "https" && parsed.Host == host && parsed.User == nil && parsed.Fragment == "" &&
		strings.HasPrefix(parsed.EscapedPath()+"/", "/") && len(target) <= 4<<10
}

func homeBrowserSessionMAC(key []byte, encoded string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(homeBrowserSessionDomain + encoded))
	return mac.Sum(nil)
}

type homeBrowserSessionKeyFile struct {
	Schema string `json:"schema"`
	Key    []byte `json:"key"`
}

var homeBrowserSessionKeys sync.Map

// homeBrowserSessionKey is the Gate's own session MAC key in the
// verification custody, generated on first use; it is no Owner or CA key.
func homeBrowserSessionKey(root string, create bool) ([]byte, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if cached, ok := homeBrowserSessionKeys.Load(absolute); ok {
		return cached.([]byte), nil
	}
	raw, err := readHomePrivate(absolute, homeBrowserSessionKeyPath, 1<<10)
	if errors.Is(err, fs.ErrNotExist) && create {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		encoded, _ := json.Marshal(homeBrowserSessionKeyFile{Schema: homeBrowserSessionKeySchema, Key: key})
		// No-replace: a key another process wrote first wins.
		_ = writeHomePrivate(absolute, homeBrowserSessionKeyPath, encoded, true)
		raw, err = readHomePrivate(absolute, homeBrowserSessionKeyPath, 1<<10)
	}
	if err != nil {
		return nil, err
	}
	var file homeBrowserSessionKeyFile
	if json.Unmarshal(raw, &file) != nil || file.Schema != homeBrowserSessionKeySchema || len(file.Key) != 32 {
		return nil, errors.New("identity: unreadable browser session key")
	}
	homeBrowserSessionKeys.Store(absolute, file.Key)
	return file.Key, nil
}
