package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	// HomeEnrollmentPort is the catalog listener stackkit-server-home-enroll
	// (foundation/architecture_v2_catalog.cue): TLS 1.3 on the node site
	// address, exposure "lan".
	HomeEnrollmentPort = 9443
	// HomeEnrollmentServerName is the enrollment listener's certificate name.
	HomeEnrollmentServerName = localevidence.HomeEnrollmentServerName
	// HomeEnrollmentURIScheme prefixes the enrollment bootstrap a device scans.
	HomeEnrollmentURIScheme = "stackkit-enroll"

	homeEnrollmentMaxBodyBytes = 16 << 10
	// homeListenerCertificateBackoff spaces the signer requests after a
	// failure.
	homeListenerCertificateBackoff = time.Minute
)

// HomeEnrollmentBootstrap is what a device client needs to reach the
// Authority's LAN enrollment listener directly: its URL on the node site
// address, the server name of its certificate and the step-ca root
// fingerprint to pin. It contains no secret and grants nothing; the person's
// own passkey sign-in is the enrollment's credential.
//
// HubEnrollmentURL is the same listener under the Hub host, which its
// certificate also names: the installed Hub PWA, a browser that cannot pin a
// root or set a server name, opens it from the home network where the Hub host
// resolves to the node site address. It is empty without an identity runtime
// address.
type HomeEnrollmentBootstrap struct {
	EnrollmentURL    string `json:"enrollmentUrl"`
	HubEnrollmentURL string `json:"hubEnrollmentUrl,omitempty"`
	TLSServerName    string `json:"tlsServerName"`
	CARootSHA256     string `json:"caRootSha256"`
	URI              string `json:"uri"`
	// VPN is the VPN add-on's peer service while the Owner has it on.
	VPN *HomeVPNAnnouncement `json:"vpn,omitempty"`
}

// targets are the enrollment URLs a proof at the LAN listener may bind.
func (b HomeEnrollmentBootstrap) targets() []string {
	if b.HubEnrollmentURL == "" {
		return []string{b.EnrollmentURL}
	}
	return []string{b.EnrollmentURL, b.HubEnrollmentURL}
}

// LoadHomeEnrollmentBootstrap derives the bootstrap from the Authority's
// public record: the node site address the LAN resolver custody binds and the
// established step-ca root. It fails closed when either is missing or the
// address is not a LAN address.
func LoadHomeEnrollmentBootstrap(root string) (HomeEnrollmentBootstrap, error) {
	authority, err := homeAuthority(root)
	if err != nil || !ObservedLANAddress(authority.LANSiteAddress) {
		return HomeEnrollmentBootstrap{}, errors.New("identity: no LAN site address is bound for device enrollment")
	}
	address := authority.LANSiteAddress
	fingerprint, err := authority.CARootSHA256()
	if err != nil {
		return HomeEnrollmentBootstrap{}, err
	}
	bootstrap := HomeEnrollmentBootstrap{
		EnrollmentURL: "https://" + net.JoinHostPort(address, strconv.Itoa(HomeEnrollmentPort)) + HomeEnrollmentPath,
		TLSServerName: HomeEnrollmentServerName, CARootSHA256: fingerprint,
	}
	if hub, hubErr := authority.HubHost(); hubErr == nil {
		bootstrap.HubEnrollmentURL = "https://" + net.JoinHostPort(hub, strconv.Itoa(HomeEnrollmentPort)) + HomeEnrollmentPath
	}
	bootstrap.VPN = homeVPNAnnouncement(root)
	values := url.Values{"url": {bootstrap.EnrollmentURL}, "sni": {bootstrap.TLSServerName}, "ca": {bootstrap.CARootSHA256}}
	bootstrap.URI = HomeEnrollmentURIScheme + ":v2?" + values.Encode()
	return bootstrap, nil
}

type homeEnrollmentPeerKey struct{}

// homeSiteNetwork is the home network the LAN enrollment listener serves: the
// prefix of this host's interface that holds the node site address from
// custody, and every address of this host. Home is a direct peer inside that
// prefix that is none of this host's addresses (its bridges and the VPN
// add-on overlay included) and not in the overlay pool.
type homeSiteNetwork struct {
	prefix netip.Prefix
	local  []netip.Addr
}

func (n homeSiteNetwork) admits(peer string) bool {
	parsed, err := netip.ParseAddrPort(peer)
	if err != nil {
		return false
	}
	address := parsed.Addr().Unmap()
	return n.prefix.IsValid() && address.IsValid() && !address.IsLoopback() && !address.IsUnspecified() && n.prefix.Contains(address) &&
		!slices.Contains(n.local, address) && !homeOverlayPool.Contains(address)
}

// loadHomeSiteNetwork finds the site network for the node site address in
// custody: from this process's own interfaces when it runs in the host's
// network namespace, otherwise from the site network the host recorded at
// Apply (RecordHomeSiteNetwork) for the listener the Basement core publishes
// on the site address only. Without either, the listener attests no home.
func loadHomeSiteNetwork(root string) (homeSiteNetwork, []string, error) {
	bootstrap, err := LoadHomeEnrollmentBootstrap(root)
	if err != nil {
		return homeSiteNetwork{}, nil, err
	}
	authority, err := homeAuthority(root)
	if err != nil {
		return homeSiteNetwork{}, nil, err
	}
	siteAddress, err := netip.ParseAddr(authority.LANSiteAddress)
	if err != nil {
		return homeSiteNetwork{}, nil, err
	}
	network, err := interfaceHomeSiteNetwork(siteAddress.Unmap())
	if err != nil {
		return homeSiteNetwork{}, nil, err
	}
	if !network.prefix.IsValid() {
		recorded, recordErr := loadRecordedHomeSiteNetwork(root, siteAddress.Unmap())
		if recordErr != nil {
			return homeSiteNetwork{}, nil, errors.New("identity: the LAN enrollment listener does not run on the site network")
		}
		// The listener's own namespace addresses (a container bridge) are
		// never home either.
		recorded.local = append(recorded.local, network.local...)
		network = recorded
	}
	return network, bootstrap.targets(), nil
}

// interfaceHomeSiteNetwork reads this namespace's addresses and, when one of
// them is the site address, the prefix of its interface.
func interfaceHomeSiteNetwork(siteAddress netip.Addr) (homeSiteNetwork, error) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return homeSiteNetwork{}, err
	}
	network := homeSiteNetwork{}
	for _, entry := range addresses {
		ipNet, ok := entry.(*net.IPNet)
		if !ok {
			continue
		}
		address, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok {
			continue
		}
		address = address.Unmap()
		network.local = append(network.local, address)
		if address == siteAddress {
			ones, _ := ipNet.Mask.Size()
			network.prefix = netip.PrefixFrom(address, ones).Masked()
		}
	}
	// A default gateway relays connections with its own LAN address.
	network.local = append(network.local, homeDefaultGateways()...)
	return network, nil
}

// NewHomeEnrollmentServer is the Authority's dedicated LAN enrollment
// listener, the only attestation of home: a submission it accepts directly
// from a peer on the site network (see homeSiteNetwork) is enrolled at once.
// It must run in the host's network namespace on the node site address. It
// serves only POST HomeEnrollmentPath over TLS 1.3 with a step-ca issued
// server certificate, binds every proof to the enrollment URL of the node's
// own custody, never the client's Host, and reads no forwarded header.
func NewHomeEnrollmentServer(root, listen string, certificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) (*http.Server, error) {
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return nil, errors.New("identity: enrollment listener needs host:port")
	}
	if certificate == nil {
		return nil, errors.New("identity: enrollment listener needs a server certificate")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("POST "+HomeEnrollmentPath, newHomeEnrollmentHandler(absolute, true, func() (homeSiteNetwork, []string, error) { return loadHomeSiteNetwork(absolute) }))
	mux.HandleFunc("OPTIONS "+HomeEnrollmentPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	server := &http.Server{
		Addr: listen, Handler: homeHubCORS(absolute, mux),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10,
		TLSConfig:      &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: certificate},
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, homeEnrollmentPeerKey{}, conn.RemoteAddr().String())
		},
	}
	return server, nil
}

// HomeEnrollmentHandler is the enrollment route of every other listener (the
// node API behind the reach paths). A submission there is always away and
// waits for its confirmation, whatever address, header or name it carries.
func HomeEnrollmentHandler(root string) http.Handler {
	return newHomeEnrollmentHandler(root, false, nil)
}

func newHomeEnrollmentHandler(root string, lanListener bool, site func() (homeSiteNetwork, []string, error)) http.Handler {
	absolute, rootErr := filepath.Abs(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rootErr != nil {
			denyHomeEnrollment(w, http.StatusServiceUnavailable, "workspace_unavailable")
			return
		}
		submission := HomeEnrollmentSubmission{}
		if lanListener {
			peer, onListener := r.Context().Value(homeEnrollmentPeerKey{}).(string)
			if !onListener || r.TLS == nil {
				denyHomeEnrollment(w, http.StatusForbidden, "enrollment_listener_required")
				return
			}
			// Home is the direct connection to this listener from the site
			// network. A loopback peer is a process on the server itself (a
			// connector or proxy), a bridge, overlay or own address is this
			// host, and a public peer is not on the home network.
			if !ObservedLANAddress(peer) {
				denyHomeEnrollment(w, http.StatusForbidden, "not_at_home")
				return
			}
			network, targets, err := site()
			if err != nil {
				denyHomeEnrollment(w, http.StatusServiceUnavailable, "enrollment_unavailable")
				return
			}
			if !network.admits(peer) {
				denyHomeEnrollment(w, http.StatusForbidden, "not_at_home")
				return
			}
			submission.Home, submission.Targets = true, targets
		}
		var body struct {
			Name    string `json:"name"`
			Custody string `json:"custody"`
			Person  string `json:"person"`
		}
		proofs := r.Header.Values("DPoP")
		if len(proofs) != 1 || !decodeHomeJSON(r.Body, homeEnrollmentMaxBodyBytes, &body, false) {
			denyHomeEnrollment(w, http.StatusBadRequest, "invalid_enrollment_request")
			return
		}
		submission.Name, submission.Custody, submission.Person, submission.Proof = body.Name, body.Custody, body.Person, proofs[0]
		verifier, err := HomeVerifierFor(absolute)
		if err != nil {
			denyHomeEnrollment(w, http.StatusServiceUnavailable, "workspace_unavailable")
			return
		}
		outcome, err := verifier.Enroll(submission)
		switch {
		case errors.Is(err, ErrHomeEnrollmentUnavailable):
			denyHomeEnrollment(w, http.StatusServiceUnavailable, "enrollment_unavailable")
			return
		case errors.Is(err, ErrHomeEnrollmentRateLimited):
			denyHomeEnrollment(w, http.StatusTooManyRequests, "rate_limited")
			return
		case errors.Is(err, ErrHomeDeviceCapReached):
			denyHomeEnrollment(w, http.StatusConflict, "device_cap_reached")
			return
		case errors.Is(err, ErrHomeDeviceRevoked):
			denyHomeEnrollment(w, http.StatusForbidden, "device_key_revoked")
			return
		case errors.Is(err, ErrHomeDeviceConflict):
			denyHomeEnrollment(w, http.StatusConflict, "device_conflict")
			return
		case errors.Is(err, ErrHomeRegistryFull):
			denyHomeEnrollment(w, http.StatusInsufficientStorage, "registry_full")
			return
		case err != nil:
			denyHomeEnrollment(w, http.StatusUnauthorized, "enrollment_denied")
			return
		}
		status := http.StatusAccepted
		if outcome.Credential != nil {
			status = http.StatusCreated
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(outcome)
	})
}

func denyHomeEnrollment(w http.ResponseWriter, status int, reason string) {
	guidance := map[string]string{
		"enrollment_listener_required": "Enroll from the home network through the enrollment address of the bootstrap, or over the usual address to ask for a confirmation.",
		"not_at_home":                  "This connection does not come directly from a device on the home network. Enroll over the usual address instead; your own enrolled device or the Owner confirms it.",
		"registry_full":                "The device registry is full. Ask the Owner to revoke devices that are no longer used.",
		"invalid_enrollment_request":   "Send one DPoP proof signed by the device key and one JSON object with name, custody and person (the passkey sign-in ID token).",
		"enrollment_denied":            "The proof, the passkey sign-in or this server's identity trust is not current. Sign in with your passkey again and send a new proof.",
		"enrollment_unavailable":       "This server enrolls no devices: it has no applied device enrollment policy, no LAN enrollment address, or its enrollment listener is not on the site network.",
		"rate_limited":                 "Too many enrollment attempts for this account. Try again later.",
		"device_cap_reached":           "This account has reached its device limit. Revoke a device you no longer use first.",
		"device_key_revoked":           "This device key was revoked. Generate a new key and enroll again.",
		"device_conflict":              "This device key is already enrolled or waiting for confirmation, or another device of this account uses this name.",
		"workspace_unavailable":        "The StackKits workspace is unavailable on this node.",
	}[reason]
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error_code": "home_enrollment_denied", "reason_code": reason, "retryable": status == http.StatusTooManyRequests, "user_guidance": guidance,
	})
}

// HomeEnrollmentCertificateSource obtains the listener certificate on first
// use and renews it an hour before expiry. The key is generated here and
// never leaves the process; the host-side signer, which alone holds the Owner
// custody, has step-ca sign the request for exactly the listener's names. The
// chain is checked against the Authority's step-ca root before it is served.
// Without the signer the TLS handshake fails, so the listener stays closed.
func HomeEnrollmentCertificateSource(root string, signer HomeAuthoritySigner) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	var mu sync.Mutex
	var current *tls.Certificate
	var failedAt time.Time
	var failure error
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()
		if current != nil && time.Until(current.Leaf.NotAfter) > time.Hour {
			return current, nil
		}
		if signer == nil {
			return nil, errors.New("identity: no host-side identity signer; the enrollment listener has no certificate")
		}
		// After a failure, handshakes wait a minute instead of each asking
		// the signer, so clients cannot exhaust its hourly budget.
		if failure != nil && time.Since(failedAt) < homeListenerCertificateBackoff {
			if current != nil && time.Now().Before(current.Leaf.NotAfter) {
				return current, nil
			}
			return nil, failure
		}
		ctx, cancel := context.WithTimeout(hello.Context(), 30*time.Second)
		defer cancel()
		issued, err := issueHomeEnrollmentListenerCertificate(ctx, root, signer)
		if err != nil {
			failedAt, failure = time.Now(), err
			if current != nil && time.Now().Before(current.Leaf.NotAfter) {
				return current, nil
			}
			return nil, err
		}
		current, failure = &issued, nil
		return current, nil
	}
}

// issueHomeEnrollmentListenerCertificate generates the listener key, sends
// only its certificate request to the signer and verifies what comes back.
func issueHomeEnrollmentListenerCertificate(ctx context.Context, root string, signer HomeAuthoritySigner) (tls.Certificate, error) {
	authority, err := homeAuthority(root)
	if err != nil {
		return tls.Certificate{}, err
	}
	hub, _ := authority.HubHost()
	names := localevidence.HomeEnrollmentListenerNames(hub)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: HomeEnrollmentServerName}, DNSNames: names}, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	chainPEM, err := signer.IssueListenerCertificate(ctx, csr)
	if err != nil {
		return tls.Certificate{}, err
	}
	certificate := tls.Certificate{PrivateKey: key}
	for rest := chainPEM; ; {
		var block *pem.Block
		if block, rest = pem.Decode(rest); block == nil {
			break
		}
		certificate.Certificate = append(certificate.Certificate, block.Bytes)
	}
	if len(certificate.Certificate) == 0 {
		return tls.Certificate{}, errors.New("identity: the signer returned no listener certificate")
	}
	if certificate.Leaf, err = x509.ParseCertificate(certificate.Certificate[0]); err != nil {
		return tls.Certificate{}, err
	}
	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(authority.StepCARootCertificatePEM)) {
		return tls.Certificate{}, errors.New("identity: no established step-ca root")
	}
	for _, der := range certificate.Certificate[1:] {
		if parsed, parseErr := x509.ParseCertificate(der); parseErr == nil {
			intermediates.AddCert(parsed)
		}
	}
	leaf := certificate.Leaf
	public, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !public.Equal(&key.PublicKey) || !slices.Equal(leaf.DNSNames, names) {
		return tls.Certificate{}, errors.New("identity: the signer returned a certificate for another key or name")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return tls.Certificate{}, errors.New("identity: the listener certificate does not chain to the Authority's root")
	}
	return certificate, nil
}
