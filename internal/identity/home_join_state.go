package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// The verification state of a joined server (homelab identity target §4,
// §12): the Authority signs, with its device credential issuer key, which
// devices and accounts are revoked and the household groups of every
// account; each joined server reads it every homeJoinedStateRefresh (target
// 60 s) with a request signed by the state-read key the Owner approved at
// join. Partition: a joined server that cannot reach the Authority keeps the
// last state it verified, so issued credentials and sessions stay valid to
// their lifetime against it; a browser session is opened only while the
// state is at most homeJoinedStateFresh old, so new sign-ins fail closed.
const (
	homeJoinedStateFile    = ".stackkit/custody/home-verifier/joined-state.jws"
	homeJoinedStateType    = "stackkit-verification-state+jwt"
	homeJoinProofType      = "stackkit-join-proof+jwt"
	homeJoinProofHeader    = "Stackkit-Join-Proof"
	homeJoinedStateRefresh = 15 * time.Second
	homeJoinedStateFresh   = HomeRevocationTarget + 30*time.Second
	homeJoinedStateIdle    = 10 * time.Minute
	homeJoinedStateMax     = 256 << 10
	homeJoinProofWindow    = 30 * time.Second
)

type homeJoinedStateClaims struct {
	jwt.Claims
	RevokedDevices    []string            `json:"revokedDevices"`
	RevokedAccounts   []string            `json:"revokedAccounts"`
	Groups            map[string][]string `json:"groups"`
	GroupsRefreshedAt int64               `json:"groupsRefreshedAt,omitempty"`
	// Servers are the homelab's current joined servers (by owner reference)
	// and the names the Owner approved for each: a joined server admits
	// another server's names into its LAN resolver only as listed here.
	Servers map[string]homeJoinedStateServer `json:"servers"`
}

// homeJoinedStateServer is one current joined server in the state: its
// Owner-approved names and LAN address.
type homeJoinedStateServer struct {
	Hostnames  []string `json:"hostnames"`
	LANAddress string   `json:"lanAddress,omitempty"`
}

type homeJoinProofClaims struct {
	jwt.Claims
	Method string `json:"htm"`
	Target string `json:"htu"`
}

// homeJoinReplay keeps every state-read proof the Authority accepted until
// it can no longer be fresh, bounded per joined server so one server cannot
// crowd out another's reads.
var homeJoinReplay = struct {
	sync.Mutex
	seen    map[string]time.Time
	perKey  map[string]int
	keyOfID map[string]string
}{seen: map[string]time.Time{}, perKey: map[string]int{}, keyOfID: map[string]string{}}

// homeJoinReplayPerServer bounds one server's remembered proofs: a server
// reads every 15 s and a proof is remembered about 50 s.
const homeJoinReplayPerServer = 64

// rememberHomeJoinProof records one proof of a joined server once.
func rememberHomeJoinProof(key, proofID string, until, now time.Time) bool {
	homeJoinReplay.Lock()
	defer homeJoinReplay.Unlock()
	for seen, expires := range homeJoinReplay.seen {
		if !now.Before(expires) {
			delete(homeJoinReplay.seen, seen)
			owner := homeJoinReplay.keyOfID[seen]
			delete(homeJoinReplay.keyOfID, seen)
			if homeJoinReplay.perKey[owner]--; homeJoinReplay.perKey[owner] <= 0 {
				delete(homeJoinReplay.perKey, owner)
			}
		}
	}
	id := key + "\x00" + proofID
	if _, seen := homeJoinReplay.seen[id]; seen || homeJoinReplay.perKey[key] >= homeJoinReplayPerServer {
		return false
	}
	homeJoinReplay.seen[id], homeJoinReplay.keyOfID[id] = until, key
	homeJoinReplay.perKey[key]++
	return true
}

// registerHomeJoinedStateRoute mounts GET HomeJoinedStatePath on the Hub: a
// joined server's signed proof in Stackkit-Join-Proof answers the current
// verification state, signed by the device credential issuer.
func registerHomeJoinedStateRoute(mux *http.ServeMux, root string) {
	mux.HandleFunc("GET "+HomeJoinedStatePath, func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		server, ok := authenticateHomeJoinProof(root, r, now)
		if !ok {
			denyHomeIdentity(w, http.StatusUnauthorized, "join_proof_refused", false)
			return
		}
		state, err := signHomeJoinedState(root, server, now)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
			return
		}
		w.Header().Set("Content-Type", "application/jwt")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, state)
	})
}

// authenticateHomeJoinProof admits a proof signed by the state-read key of a
// current joined server, for GET on exactly the Authority's state route, at
// most homeJoinProofWindow old and used once.
func authenticateHomeJoinProof(root string, r *http.Request, now time.Time) (homeJoinedServer, bool) {
	values := r.Header.Values(homeJoinProofHeader)
	if len(values) != 1 || len(values[0]) > homeDeviceMaxTokenBytes {
		return homeJoinedServer{}, false
	}
	token, err := jwt.ParseSigned(values[0], []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || len(token.Headers) != 1 || token.Headers[0].JSONWebKey != nil || token.Headers[0].ExtraHeaders[jose.HeaderType] != homeJoinProofType {
		return homeJoinedServer{}, false
	}
	servers, err := loadHomeJoinedServers(root)
	address, addressErr := homeIdentityAddress(root)
	if err != nil || addressErr != nil {
		return homeJoinedServer{}, false
	}
	for _, entry := range servers.Servers {
		if entry.Removed || !now.Before(entry.ExpiresAt) || entry.ClientKey.KeyID != token.Headers[0].KeyID {
			continue
		}
		var claims homeJoinProofClaims
		if token.Claims(entry.ClientKey.Key, &claims) != nil || claims.Issuer != entry.Server.NodeRef || claims.Method != http.MethodGet ||
			claims.Target != "https://"+address.ServiceHost("base")+HomeJoinedStatePath || claims.IssuedAt == nil || len(claims.ID) < 22 {
			return homeJoinedServer{}, false
		}
		issued := claims.IssuedAt.Time()
		if issued.After(now.Add(HomeClockLeeway)) || now.Sub(issued) > homeJoinProofWindow+HomeClockLeeway {
			return homeJoinedServer{}, false
		}
		if !rememberHomeJoinProof(entry.ClientKey.KeyID, claims.ID, issued.Add(homeJoinProofWindow+2*HomeClockLeeway), now) {
			return homeJoinedServer{}, false
		}
		return entry, true
	}
	return homeJoinedServer{}, false
}

// signHomeJoinedState signs the current verification state for one joined
// server: revoked registry entries (kept 24 hours, beyond every credential's
// life), removed and revoked accounts, and the household groups while their
// projection is current.
func signHomeJoinedState(root string, server homeJoinedServer, now time.Time) (string, error) {
	issuer, err := loadHomeDeviceIssuer(root)
	if err != nil {
		return "", err
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return "", err
	}
	claims := homeJoinedStateClaims{
		Claims: jwt.Claims{Issuer: issuer.issuer, Audience: jwt.Audience{server.Server.OwnerRef}, Subject: server.Server.SiteRef,
			IssuedAt: jwt.NewNumericDate(now), ID: base64.RawURLEncoding.EncodeToString(randomHomeJoinBytes())},
		RevokedDevices: []string{}, RevokedAccounts: append([]string{}, registry.RemovedAccounts...), Groups: map[string][]string{},
		Servers: map[string]homeJoinedStateServer{},
	}
	if joined, joinedErr := loadHomeJoinedServers(root); joinedErr == nil {
		for _, other := range joined.Servers {
			if !other.Removed && now.Before(other.ExpiresAt) {
				claims.Servers[other.Server.OwnerRef] = homeJoinedStateServer{Hostnames: append([]string(nil), other.Server.Hostnames...), LANAddress: other.Server.LANAddress}
			}
		}
	}
	for _, device := range registry.Devices {
		if device.Revoked {
			claims.RevokedDevices = append(claims.RevokedDevices, device.ID)
		}
	}
	// Compacted tombstones stay revoked: a joined server may still hold an
	// app pass or session of such a device.
	for _, id := range registry.RevokedKeys {
		if !slices.Contains(claims.RevokedDevices, id) {
			claims.RevokedDevices = append(claims.RevokedDevices, id)
		}
	}
	if state, stateErr := loadHomeVerifierState(root); stateErr == nil {
		for _, account := range state.Trust.RevokedSubjects {
			if !slices.Contains(claims.RevokedAccounts, account) {
				claims.RevokedAccounts = append(claims.RevokedAccounts, account)
			}
		}
	}
	if groups, groupsErr := loadHomeAccountGroups(root); groupsErr == nil && now.Sub(groups.RefreshedAt) <= homeAccountGroupsMaxAge {
		claims.Groups, claims.GroupsRefreshedAt = groups.Accounts, groups.RefreshedAt.Unix()
	}
	slices.Sort(claims.RevokedDevices)
	slices.Sort(claims.RevokedAccounts)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: issuer.private},
		(&jose.SignerOptions{}).WithType(homeJoinedStateType).WithHeader("kid", issuer.keyID))
	if err != nil {
		return "", err
	}
	return jwt.Signed(signer).Claims(claims).Serialize()
}

func randomHomeJoinBytes() []byte {
	raw := make([]byte, 18)
	_, _ = rand.Read(raw)
	return raw
}

// verifyHomeJoinedState verifies a state the Authority signed for this
// joined server with the issuer key of the installed bundle.
func verifyHomeJoinedState(bundle HomeTrustBundle, raw string, now time.Time) (homeJoinedStateClaims, error) {
	var claims homeJoinedStateClaims
	if len(raw) == 0 || len(raw) > homeJoinedStateMax {
		return claims, ErrHomeAccessDenied
	}
	token, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || len(token.Headers) != 1 || token.Headers[0].JSONWebKey != nil || token.Headers[0].KeyID != bundle.DeviceIssuer.KeyID ||
		token.Headers[0].ExtraHeaders[jose.HeaderType] != homeJoinedStateType {
		return claims, ErrHomeAccessDenied
	}
	public, ok := bundle.DeviceIssuer.Key.Key.(ed25519.PublicKey)
	if !ok || token.Claims(public, &claims) != nil || claims.Issuer != bundle.DeviceIssuer.Issuer || claims.IssuedAt == nil ||
		len(claims.Audience) != 1 || claims.Audience[0] != bundle.Server.OwnerRef || claims.Subject != bundle.Server.SiteRef ||
		claims.IssuedAt.Time().After(now.Add(HomeClockLeeway)) {
		return claims, ErrHomeAccessDenied
	}
	return claims, nil
}

// RefreshHomeJoinedState reads the Authority's verification state once and
// keeps it when it verifies and is not older than the one held. It writes the
// household groups of the state into the projection the Access Gate reads.
func RefreshHomeJoinedState(ctx context.Context, root string, now time.Time) error {
	bundle, err := LoadHomeJoinedTrust(root)
	if err != nil {
		return err
	}
	request, err := newHomeJoinedStateRequest(ctx, root, bundle, now)
	if err != nil {
		return err
	}
	response, err := homeJoinedStateClient(bundle).Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, homeJoinedStateMax+1))
	if err != nil || response.StatusCode != http.StatusOK {
		return errors.New("identity: the Authority refused or did not answer the verification state read")
	}
	return acceptHomeJoinedState(root, bundle, raw)
}

// newHomeJoinedStateRequest is one state read, signed by the state-read key
// for GET on exactly the bundle's state route.
func newHomeJoinedStateRequest(ctx context.Context, root string, bundle HomeTrustBundle, now time.Time) (*http.Request, error) {
	key, err := loadHomeJoinKey(root, homeJoinClientKeyPath, "state-read")
	if err != nil {
		return nil, err
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("identity: unreadable state-read key")
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: private},
		(&jose.SignerOptions{}).WithType(homeJoinProofType).WithHeader("kid", bundle.ClientKeyThumbprint))
	if err != nil {
		return nil, err
	}
	proof, err := jwt.Signed(signer).Claims(homeJoinProofClaims{
		Claims: jwt.Claims{Issuer: bundle.Server.NodeRef, IssuedAt: jwt.NewNumericDate(now), ID: base64.RawURLEncoding.EncodeToString(randomHomeJoinBytes())},
		Method: http.MethodGet, Target: bundle.VerificationState,
	}).Serialize()
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, bundle.VerificationState, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(homeJoinProofHeader, proof)
	return request, nil
}

// acceptHomeJoinedState keeps a state that verifies under the bundle's issuer
// key and is not older than the one held.
func acceptHomeJoinedState(root string, bundle HomeTrustBundle, raw []byte) error {
	claims, err := verifyHomeJoinedState(bundle, string(raw), time.Now())
	if err != nil {
		return err
	}
	if prior, priorErr := loadHomeJoinedState(root, bundle); priorErr == nil && claims.IssuedAt.Time().Before(prior.IssuedAt.Time()) {
		return errors.New("identity: the Authority answered an older verification state")
	}
	if err := writeHomePrivate(root, homeJoinedStateFile, raw, false); err != nil {
		return err
	}
	if claims.GroupsRefreshedAt > 0 {
		groups, _ := json.Marshal(homeAccountGroupsRecord{Schema: homeAccountGroupsSchema, RefreshedAt: time.Unix(claims.GroupsRefreshedAt, 0).UTC(), Accounts: claims.Groups})
		if err := writeHomePrivate(root, homeAccountGroupsPath, groups, false); err != nil {
			return err
		}
	}
	invalidateHomeVerifier(root)
	return nil
}

// homeJoinedStateClient trusts the bundle's root beside the system roots and
// dials the Authority's LAN address for its Hub host when the bundle names
// one, so the read needs no zone; it falls back to DNS.
func homeJoinedStateClient(bundle HomeTrustBundle) *http.Client {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM([]byte(bundle.RootCertificatePEM))
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if bundle.AuthorityLANAddress != "" {
				_, port, _ := net.SplitHostPort(address)
				if conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(bundle.AuthorityLANAddress, port)); err == nil {
					return conn, nil
				}
			}
			return dialer.DialContext(ctx, network, address)
		},
		ResponseHeaderTimeout: 10 * time.Second, MaxIdleConns: 2,
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func loadHomeJoinedState(root string, bundle HomeTrustBundle) (homeJoinedStateClaims, error) {
	raw, err := readHomePrivate(root, homeJoinedStateFile, homeJoinedStateMax)
	if err != nil {
		return homeJoinedStateClaims{}, err
	}
	return verifyHomeJoinedState(bundle, string(raw), time.Now())
}

// loadHomeJoinedSnapshot is a joined server's verification snapshot: the
// Authority's device credential issuer from the bundle and the last state it
// verified. Without a state it fails, so nothing verifies before the first
// read.
func loadHomeJoinedSnapshot(root string, bundle HomeTrustBundle) (homeDeviceSnapshot, error) {
	// The issuer key is trusted to the bundle's expiry and not after it.
	if !time.Now().Before(bundle.ExpiresAt) {
		return homeDeviceSnapshot{}, ErrHomeJoinRefused
	}
	claims, err := loadHomeJoinedState(root, bundle)
	if err != nil {
		return homeDeviceSnapshot{}, err
	}
	public, _ := bundle.DeviceIssuer.Key.Key.(ed25519.PublicKey)
	snapshot := homeDeviceSnapshot{
		issuer: homeDeviceIssuerPublic{issuer: bundle.DeviceIssuer.Issuer, audience: bundle.DeviceIssuer.Audience, keyID: bundle.DeviceIssuer.KeyID, key: public},
		joined: true, stateAt: claims.IssuedAt.Time().UTC(),
		revokedDevices: map[string]bool{}, revokedAccounts: map[string]bool{},
	}
	for _, id := range claims.RevokedDevices {
		snapshot.revokedDevices[id] = true
	}
	for _, account := range append(claims.RevokedAccounts, bundle.RevokedSubjects...) {
		snapshot.revokedAccounts[account] = true
	}
	return snapshot, nil
}

// KeepHomeJoinedStateCurrent reads the Authority's verification state every
// homeJoinedStateRefresh on a joined server until ctx ends. A server that has
// not joined is checked again every homeJoinedStateIdle without a warning.
func KeepHomeJoinedStateCurrent(ctx context.Context, root string) {
	delay, failing := time.Duration(0), false
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if !homeJoined(root) {
			delay = homeJoinedStateIdle
			continue
		}
		read, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := RefreshHomeJoinedState(read, root, time.Now())
		cancel()
		switch {
		case err != nil && !failing:
			slog.Warn("the Authority's verification state was not read; issued credentials stay valid to their lifetime, new browser sessions fail closed", "error", err)
			failing = true
		case err == nil && failing:
			slog.Info("the Authority's verification state is current again")
			failing = false
		}
		delay = homeJoinedStateRefresh
	}
}

func removeHomePrivate(root, path string) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(absolute, filepath.FromSlash(path)))
}

// HomeJoinedStatus is a joined server's view of its Authority.
type HomeJoinedStatus struct {
	AuthoritySiteRef  string     `json:"authoritySiteRef"`
	RootFingerprint   string     `json:"rootFingerprint"`
	Hostnames         []string   `json:"hostnames"`
	ExpiresAt         time.Time  `json:"expiresAt"`
	VerificationState string     `json:"verificationState"`
	StateAt           *time.Time `json:"stateAt,omitempty"`
	// Current is true while new browser sessions open here.
	Current bool `json:"current"`
}

// LoadHomeJoinedStatus reports the installed trust and the age of the last
// verified state.
func LoadHomeJoinedStatus(root string, now time.Time) (HomeJoinedStatus, error) {
	bundle, err := LoadHomeJoinedTrust(root)
	if err != nil {
		return HomeJoinedStatus{}, err
	}
	fingerprint, _ := HomeTrustBundleRootFingerprint(bundle)
	status := HomeJoinedStatus{AuthoritySiteRef: bundle.AuthoritySiteRef, RootFingerprint: fingerprint, Hostnames: bundle.Server.Hostnames,
		ExpiresAt: bundle.ExpiresAt, VerificationState: bundle.VerificationState}
	if claims, stateErr := loadHomeJoinedState(root, bundle); stateErr == nil {
		at := claims.IssuedAt.Time().UTC()
		status.StateAt, status.Current = &at, now.Sub(at) <= homeJoinedStateFresh
	}
	return status, nil
}
