package commands

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The VPN add-on peer service runs on the Authority's host beside the tool's
// custody (never in the core container). An enrolled device asks it for its
// peer with its device credential and a DPoP proof right after its
// enrollment returned the first credential, joins with the tool's client and
// ends the enrollment with a probe through the VPN (homelab identity target
// §6.2). The reconciler beside it keeps the tool's live peers equal to the
// active devices of the registry.
const (
	vpnPeerServiceMaxBody = 16 << 10
	vpnIssueInterval      = 10 * time.Second
	vpnProbeInterval      = 2 * time.Second
	vpnReconcilePoll      = 2 * time.Second
	vpnReconcileFull      = 30 * time.Second
	// An issue holds the peer ledger while the tool answers; it is bounded,
	// one at a time, and spaced so the reconciler always gets the ledger in
	// between and a revocation is never held up.
	vpnIssueTimeout = 20 * time.Second
	vpnIssueSpacing = 2 * time.Second
)

type vpnPeerService struct {
	root   string
	host   vpnHost
	driver vpnDriver

	mu        sync.Mutex
	lastCall  map[string]time.Time
	issueMu   sync.Mutex
	lastIssue time.Time
	guardMu   sync.Mutex
}

func newVPNPeerService(h vpnHost, driver vpnDriver) *vpnPeerService {
	return &vpnPeerService{root: h.root, host: h, driver: driver, lastCall: map[string]time.Time{}}
}

// allow rate-limits one kind of call per device.
func (s *vpnPeerService) allow(kind, device string, interval time.Duration, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := kind + "\x00" + device
	if last, seen := s.lastCall[key]; seen && now.Sub(last) < interval {
		return false
	}
	s.lastCall[key] = now
	return true
}

func (s *vpnPeerService) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+identity.HomeVPNInfoPath, s.serveInfo)
	mux.HandleFunc("POST "+identity.HomeVPNPeerPath, s.servePeer)
	mux.HandleFunc("POST "+identity.HomeVPNConfirmPath, s.serveConfirm)
	if control := s.driver.control(); control != nil {
		mux.Handle("/", control)
	}
	return mux
}

func (s *vpnPeerService) authenticate(w http.ResponseWriter, r *http.Request) (identity.HomeAuthentication, bool) {
	auth, err := identity.AuthenticateHomeVPNRequest(s.root, r, s.host.peerServiceOrigins())
	if err != nil {
		writeVPNError(w, http.StatusUnauthorized, "current_device_proof_unavailable", "Send the device credential (Authorization: DPoP) and a fresh DPoP proof for exactly this method and URL.")
		return identity.HomeAuthentication{}, false
	}
	return auth, true
}

func (s *vpnPeerService) serveInfo(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if !s.allow("info", auth.Device, vpnProbeInterval, time.Now()) {
		writeVPNError(w, http.StatusTooManyRequests, "rate_limited", "Ask again in a moment.")
		return
	}
	info := map[string]any{"tool": s.host.selection.Tool, "peerPath": identity.HomeVPNPeerPath, "confirmUrl": s.host.probeURL()}
	if s.host.selection.Tool != identity.HomeVPNToolWireGuard {
		if peer, err := identity.HomeVPNPeerOf(s.root, auth.Device, s.host.selection.Tool); err == nil {
			info["peer"] = map[string]any{"state": peer.State, "addresses": peer.Addresses}
		}
	} else if address, err := s.wireguardAddress(auth.Device); err == nil {
		info["peer"] = map[string]any{"state": identity.HomeVPNPeerIssued, "addresses": []string{address}}
	}
	writeVPNJSON(w, http.StatusOK, info)
}

// servePeer gives the calling device its peer of the selected tool.
func (s *vpnPeerService) servePeer(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if !s.allow("issue", auth.Device, vpnIssueInterval, now) {
		writeVPNError(w, http.StatusTooManyRequests, "rate_limited", "This device asked for a peer a moment ago; use the one it received.")
		return
	}
	var body struct {
		PublicKey string `json:"publicKey"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, vpnPeerServiceMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeVPNError(w, http.StatusBadRequest, "invalid_request", "Send one JSON object; WireGuard takes {\"publicKey\": \"<the device's own WireGuard public key>\"}.")
		return
	}
	if s.host.selection.Tool == identity.HomeVPNToolWireGuard {
		ctx, cancel := context.WithTimeout(r.Context(), 2*vpnToolTimeout)
		defer cancel()
		s.serveWireGuardPeer(ctx, w, auth, body.PublicKey, now)
		return
	}
	if body.PublicKey != "" {
		writeVPNError(w, http.StatusBadRequest, "invalid_request", "This tool takes no public key; its client creates its own.")
		return
	}
	s.issueMu.Lock()
	defer s.issueMu.Unlock()
	if wait := vpnIssueSpacing - time.Since(s.lastIssue); wait > 0 {
		time.Sleep(wait)
	}
	defer func() { s.lastIssue = time.Now() }()
	ctx, cancel := context.WithTimeout(r.Context(), vpnIssueTimeout)
	defer cancel()
	now = time.Now().UTC()
	var answer map[string]any
	peer, err := identity.IssueHomeVPNPeer(s.root, auth.Device, s.host.selection.Tool, now, func() (identity.HomeVPNPeer, error) {
		issued, material, issueErr := s.driver.issue(ctx, auth.Device)
		answer = material
		return issued, issueErr
	})
	switch {
	case errors.Is(err, identity.ErrHomeVPNDeviceInactive):
		writeVPNError(w, http.StatusForbidden, "device_inactive", "Only an enrolled, unrevoked device receives a VPN peer; a pending enrollment waits for its confirmation.")
		return
	case err != nil:
		slog.Error("vpn peer issue failed", "tool", s.host.selection.Tool, "error", err)
		writeVPNError(w, http.StatusBadGateway, "tool_unavailable", "The VPN tool could not issue a peer now; try again shortly.")
		return
	}
	answer["tool"], answer["state"], answer["probe"] = s.host.selection.Tool, peer.State, s.host.probeURL()
	if dns := s.host.dnsHint(); dns != nil {
		answer["dns"] = dns
	}
	slog.Info("vpn peer issued", "tool", s.host.selection.Tool, "device", auth.Device, "keyRef", peer.KeyRef, "nodeRef", peer.NodeRef)
	writeVPNJSON(w, http.StatusCreated, answer)
}

func (s *vpnPeerService) serveWireGuardPeer(ctx context.Context, w http.ResponseWriter, auth identity.HomeAuthentication, publicKey string, now time.Time) {
	peer, err := identity.BindHomeOverlayPeer(s.root, auth.Device, publicKey, now)
	switch {
	case errors.Is(err, identity.ErrHomeOverlayDeviceInactive):
		writeVPNError(w, http.StatusForbidden, "device_inactive", "Only an enrolled, unrevoked device receives a VPN peer; a pending enrollment waits for its confirmation.")
		return
	case errors.Is(err, identity.ErrHomeOverlayKeyInvalid), errors.Is(err, identity.ErrHomeOverlayKeyInUse):
		writeVPNError(w, http.StatusBadRequest, "invalid_public_key", "Generate a WireGuard key pair on this device and send only its public key.")
		return
	case err != nil:
		writeVPNError(w, http.StatusServiceUnavailable, "peer_registry_unavailable", "The peer registry is busy or full; try again shortly.")
		return
	}
	if _, err := ensureHomeOverlayInterface(ctx, s.root, now); err != nil {
		slog.Error("vpn wireguard resync failed", "error", err)
		writeVPNError(w, http.StatusServiceUnavailable, "interface_unavailable", "The peer is registered but the interface could not be updated; the next resync applies it.")
		return
	}
	node, err := identity.EnsureHomeOverlayNodeKey(s.root)
	if err != nil {
		writeVPNError(w, http.StatusServiceUnavailable, "interface_unavailable", "The node key is not in custody.")
		return
	}
	allowed := []string{node.Address + "/32", s.host.site.String() + "/32"}
	config := "[Interface]\n# PrivateKey = the private key this device generated and keeps\nAddress = " + peer.Address + "/32\n"
	answer := map[string]any{
		"tool": identity.HomeVPNToolWireGuard, "address": peer.Address + "/32", "allowedIPs": allowed, "probe": s.host.probeURL(),
		"node": map[string]any{"publicKey": node.PublicKey, "endpoint": s.host.selection.Endpoint, "address": node.Address, "persistentKeepalive": node.KeepaliveSeconds},
	}
	if dns := s.host.dnsHint(); dns != nil {
		answer["dns"] = dns
		config += "DNS = " + s.host.site.String() + "\n"
	}
	config += "\n[Peer]\nPublicKey = " + node.PublicKey + "\nEndpoint = " + s.host.selection.Endpoint + "\nAllowedIPs = " + strings.Join(allowed, ", ") +
		"\nPersistentKeepalive = " + fmt.Sprint(node.KeepaliveSeconds) + "\n"
	answer["config"] = config
	slog.Info("vpn peer issued", "tool", "wireguard", "device", auth.Device, "address", peer.Address)
	writeVPNJSON(w, http.StatusCreated, answer)
}

// serveConfirm is the device's probe through the VPN: it must arrive from
// the device's own VPN address, and binds a coordinated node to the device.
func (s *vpnPeerService) serveConfirm(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if !s.allow("confirm", auth.Device, vpnProbeInterval, time.Now()) {
		writeVPNError(w, http.StatusTooManyRequests, "rate_limited", "Probe again in a moment.")
		return
	}
	remote, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		writeVPNError(w, http.StatusBadRequest, "invalid_request", "Unreadable peer address.")
		return
	}
	source := remote.Addr().Unmap()
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(r.Context(), vpnToolTimeout)
	defer cancel()
	switch s.host.selection.Tool {
	case identity.HomeVPNToolWireGuard:
		address, err := s.wireguardAddress(auth.Device)
		if err != nil || address != source.String() {
			writeVPNError(w, http.StatusConflict, "not_through_vpn", "Send the probe through the VPN to the node site address.")
			return
		}
		writeVPNJSON(w, http.StatusOK, map[string]any{"status": "connected", "tool": "wireguard", "address": address})
	case identity.HomeVPNToolPangolin:
		// Newt reaches the site address from this host; the client was bound
		// to the device when it was created.
		peer, err := identity.HomeVPNPeerOf(s.root, auth.Device, identity.HomeVPNToolPangolin)
		if err != nil || peer.State != identity.HomeVPNPeerBound || !s.driver.probeFrom(ctx, source) {
			writeVPNError(w, http.StatusConflict, "not_through_vpn", "Send the probe through the Pangolin client to the node site address.")
			return
		}
		writeVPNJSON(w, http.StatusOK, map[string]any{"status": "connected", "tool": "pangolin", "client": peer.NodeRef})
	default:
		live, err := s.driver.live(ctx)
		if err != nil {
			writeVPNError(w, http.StatusBadGateway, "tool_unavailable", "The VPN tool is unreachable now; try again shortly.")
			return
		}
		var node *identity.HomeVPNLiveNode
		for index := range live {
			for _, address := range live[index].Addresses {
				if candidate, parseErr := netip.ParseAddr(address); parseErr == nil && candidate == source {
					node = &live[index]
				}
			}
		}
		if node == nil {
			writeVPNError(w, http.StatusConflict, "not_through_vpn", "Send the probe through the VPN to the node site address.")
			return
		}
		peer, err := identity.BindHomeVPNPeer(s.root, auth.Device, s.host.selection.Tool, *node, now)
		if err != nil {
			writeVPNError(w, http.StatusConflict, "peer_not_yours", "This VPN node was not joined with this device's join key; ask for a new peer.")
			return
		}
		// The bound address passes the guard from now on.
		s.guardMu.Lock()
		guardErr := s.driver.guard(ctx)
		s.guardMu.Unlock()
		if guardErr != nil {
			slog.Error("vpn guard update failed", "tool", s.host.selection.Tool, "error", guardErr)
			writeVPNError(w, http.StatusServiceUnavailable, "guard_unavailable", "The peer is bound but the host could not admit it yet; probe again shortly.")
			return
		}
		slog.Info("vpn peer bound", "tool", s.host.selection.Tool, "device", auth.Device, "node", peer.NodeRef)
		writeVPNJSON(w, http.StatusOK, map[string]any{"status": "connected", "tool": s.host.selection.Tool, "node": peer.NodeRef, "addresses": peer.Addresses})
	}
}

// wireguardAddress is the device's overlay address as the rendered interface
// configuration carries it, which holds active devices only.
func (s *vpnPeerService) wireguardAddress(deviceID string) (string, error) {
	config, err := identity.RenderHomeOverlayConfig(s.root, time.Now())
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(config), "\n")
	for index, line := range lines {
		if !strings.HasPrefix(line, "# device ") || !strings.HasSuffix(line, "("+deviceID+")") {
			continue
		}
		for _, next := range lines[index+1:] {
			if value, found := strings.CutPrefix(next, "AllowedIPs = "); found {
				return strings.TrimSuffix(value, "/32"), nil
			}
			if strings.HasPrefix(next, "# device ") {
				break
			}
		}
	}
	return "", identity.ErrHomeVPNNoPeer
}

// reconcile applies the registry to the tool once: a WireGuard resync, or the
// removal of every coordinated node and join key no active device holds.
func (s *vpnPeerService) reconcile(ctx context.Context) (identity.HomeVPNRemovals, error) {
	if s.host.selection.Tool == identity.HomeVPNToolWireGuard {
		_, err := ensureHomeOverlayInterface(ctx, s.root, time.Now())
		return identity.HomeVPNRemovals{}, err
	}
	removals, err := reconcileVPNPeers(ctx, s.root, s.host.selection.Tool, s.driver)
	// The guard follows the ledger on every pass (and after a failed one), so
	// a revoked device's address is gone from it even while its node's
	// deletion is retried, and a reboot cannot leave it stale.
	s.guardMu.Lock()
	guardErr := s.driver.guard(ctx)
	s.guardMu.Unlock()
	return removals, errors.Join(err, guardErr)
}

func reconcileVPNPeers(ctx context.Context, root, tool string, driver vpnDriver) (identity.HomeVPNRemovals, error) {
	live, err := driver.live(ctx)
	if err != nil {
		return identity.HomeVPNRemovals{}, err
	}
	removals, err := identity.ReconcileHomeVPNPeers(root, tool, live, time.Now().UTC())
	if err != nil {
		return removals, err
	}
	if len(removals.Nodes) == 0 && len(removals.Keys) == 0 && len(removals.Settled) == 0 {
		return removals, nil
	}
	if err := driver.remove(ctx, removals); err != nil {
		return removals, err
	}
	for _, node := range removals.Nodes {
		slog.Info("vpn peer removed", "tool", tool, "node", node.Ref)
	}
	return removals, identity.ForgetHomeVPNPeers(root, removals.Settled, time.Now().UTC())
}

// runReconciler reconciles at once on every change of the device registry,
// the peer ledger or the overlay peers (polled), and at least every 30 s for
// expiries, well inside the 60 s revocation target.
func (s *vpnPeerService) runReconciler(ctx context.Context) error {
	files := []string{identity.HomeDeviceRegistryFile, identity.HomeOverlayPeerRegistryFile, identity.HomeVPNCustodyDir + "/peers.json"}
	signature := func() string {
		var parts []string
		for _, file := range files {
			if info, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(file))); err == nil {
				parts = append(parts, fmt.Sprint(info.ModTime().UnixNano(), info.Size()))
			}
		}
		return strings.Join(parts, "|")
	}
	last, lastFull := "", time.Time{}
	ticker := time.NewTicker(vpnReconcilePoll)
	defer ticker.Stop()
	for {
		// The server moved (stackkit network re-bound it to a new LAN
		// address): the tool is re-applied for the new site address and the
		// service restarts with it.
		if site, err := vpnSiteAddress(s.root); err == nil && site != s.host.site {
			slog.Info("vpn site address moved; re-applying the tool", "from", s.host.site, "to", site)
			if err := s.followSiteMove(ctx); err != nil {
				slog.Error("vpn re-apply after the site move failed; retrying", "error", err)
			} else {
				return errVPNSiteMoved
			}
		}
		current := signature()
		if current != last || time.Since(lastFull) >= vpnReconcileFull {
			if _, err := s.reconcile(ctx); err != nil {
				slog.Error("vpn reconcile failed; retrying", "tool", s.host.selection.Tool, "error", err)
			} else {
				last, lastFull = signature(), time.Now()
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// errVPNSiteMoved ends the service after the tool followed a site move, so
// systemd starts it again with the new site address and origins.
var errVPNSiteMoved = errors.New("VPN add-on: the site address moved; restarting the peer service for it")

// followSiteMove re-applies the tool for the site address in custody: the
// WireGuard guard and interface, the node's single host route, the Headscale
// policy and the Pangolin site resource. Devices receive the new route from
// the tool (Headscale, Tailscale, Pangolin) or with their next peer request
// (WireGuard).
func (s *vpnPeerService) followSiteMove(ctx context.Context) error {
	host, err := loadVPNHost(s.root)
	if err != nil {
		return err
	}
	driver, err := newVPNDriver(host)
	if err != nil {
		return err
	}
	if err := driver.up(ctx); err != nil {
		return err
	}
	_, err = setVPNPublicListeners(ctx, host.selection.Tool)
	return err
}

// vpnPeerServiceCertificate issues the peer service's certificate through
// step-ca on first use and renews it an hour before expiry.
func vpnPeerServiceCertificate(ctx context.Context, h vpnHost) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	var mu sync.Mutex
	var current *tls.Certificate
	var lastAttempt time.Time
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()
		if current != nil && time.Until(current.Leaf.NotAfter) > time.Hour {
			return current, nil
		}
		// A failed renewal is retried at most once a minute; the current
		// certificate serves until it actually expires.
		if time.Since(lastAttempt) < time.Minute {
			if current != nil && time.Now().Before(current.Leaf.NotAfter) {
				return current, nil
			}
			return nil, errors.New("VPN add-on: the peer service certificate is being renewed")
		}
		lastAttempt = time.Now()
		issueCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		issued, err := localevidence.IssueHomeVPNPeerServiceCertificate(issueCtx, h.root, h.endpointHost())
		if err != nil {
			slog.Error("vpn peer service certificate", "error", err)
			if current != nil && time.Now().Before(current.Leaf.NotAfter) {
				return current, nil
			}
			return nil, err
		}
		current = &issued
		return current, nil
	}
}

func serveVPNPeerService(ctx context.Context, h vpnHost, driver vpnDriver) error {
	// The verifier refuses proofs dated before it started; it starts with
	// the service, not lazily at the first request.
	if _, err := identity.HomeVerifierFor(h.root); err != nil {
		return err
	}
	service := newVPNPeerService(h, driver)
	server := &http.Server{
		Addr: ":" + fmt.Sprint(identity.HomeVPNPeerServicePort), Handler: service.handler(),
		// No read or write deadline: the Headscale control and DERP
		// connections it carries are long-lived upgrades.
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: vpnPeerServiceCertificate(ctx, h)},
		// HTTP/1.1 only: the control protocol upgrades the connection.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}
	// The reconciler is the revocation path for coordinated tools: it runs
	// whatever happens to the listener, which is retried on its own.
	go func() {
		for ctx.Err() == nil {
			slog.Info("vpn peer service listening", "tool", h.selection.Tool, "port", identity.HomeVPNPeerServicePort, "origins", h.peerServiceOrigins())
			err := server.ListenAndServeTLS("", "")
			if errors.Is(err, http.ErrServerClosed) {
				return
			}
			slog.Error("vpn peer service listener failed; retrying in 30 s (the reconciler keeps running)", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	return service.runReconciler(ctx)
}

func writeVPNJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeVPNError(w http.ResponseWriter, status int, reason, guidance string) {
	writeVPNJSON(w, status, map[string]any{"error_code": "vpn_peer_denied", "reason_code": reason, "user_guidance": guidance})
}

func x509SystemPoolWith(pem []byte) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("VPN add-on: the CA file holds no certificate")
	}
	return pool, nil
}

func tlsConfigWithRoots(pool *x509.CertPool) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
}

// vpnCARootSHA256 is the hex SHA-256 of the step-ca root a native client
// pins before it trusts the peer service.
func vpnCARootSHA256(root string) string {
	authority, err := localevidence.CurrentHomeAuthority(root)
	if err != nil {
		return ""
	}
	sum, err := authority.CARootSHA256()
	if err != nil {
		return ""
	}
	return sum
}
