package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
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
	// HomePairingURIScheme prefixes the pairing payload shown as code and QR.
	HomePairingURIScheme = "stackkit-enroll"

	homeEnrollmentMaxBodyBytes = 16 << 10
)

// HomeEnrollmentBootstrap is the trust bootstrap a device needs before it
// sends its CSR: where the LAN enrollment listener is, which server name its
// certificate carries, and the step-ca root fingerprint to pin. It contains no
// secret; the pairing code is the only credential in the payload.
type HomeEnrollmentBootstrap struct {
	EnrollmentURL string `json:"enrollmentUrl"`
	TLSServerName string `json:"tlsServerName"`
	CARootSHA256  string `json:"caRootSha256"`
}

// LoadHomeEnrollmentBootstrap derives the bootstrap from owner custody: the
// node site address the LAN resolver custody already binds, and the
// established step-ca root. It fails closed when either is missing or the
// address is not a LAN address.
func LoadHomeEnrollmentBootstrap(root string) (HomeEnrollmentBootstrap, error) {
	address, err := localevidence.BasementLANDNSResolverAddress(root)
	if err != nil || !ObservedLANAddress(address) || net.ParseIP(address).IsLoopback() {
		return HomeEnrollmentBootstrap{}, errors.New("identity: no LAN site address is bound for device enrollment")
	}
	fingerprint, err := localevidence.HomeEnrollmentCARootSHA256(root)
	if err != nil {
		return HomeEnrollmentBootstrap{}, err
	}
	return HomeEnrollmentBootstrap{
		EnrollmentURL: "https://" + net.JoinHostPort(address, strconv.Itoa(HomeEnrollmentPort)) + HomeEnrollmentPath,
		TLSServerName: HomeEnrollmentServerName, CARootSHA256: fingerprint,
	}, nil
}

// PairingURI is the payload the step-up page renders as text and QR code.
func (b HomeEnrollmentBootstrap) PairingURI(pairing HomePairing) string {
	values := url.Values{"code": {pairing.Code}, "url": {b.EnrollmentURL}, "sni": {b.TLSServerName}, "ca": {b.CARootSHA256}}
	return HomePairingURIScheme + ":v1?" + values.Encode()
}

type homeEnrollmentPeerKey struct{}

// NewHomeEnrollmentServer is the dedicated LAN enrollment listener. It serves
// only POST HomeEnrollmentPath over TLS 1.3 with a step-ca issued server
// certificate. Admission uses the transport peer address of the accepted
// connection; forwarded headers are never read. The same handler mounted on
// any other listener refuses every request.
func NewHomeEnrollmentServer(root, listen string, issue HomeDeviceIssuer, certificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) (*http.Server, error) {
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return nil, errors.New("identity: enrollment listener needs host:port")
	}
	if issue == nil || certificate == nil {
		return nil, errors.New("identity: enrollment listener needs a device provisioner and a server certificate")
	}
	mux := http.NewServeMux()
	mux.Handle("POST "+HomeEnrollmentPath, newHomeEnrollmentHandler(root, issue, time.Now))
	return &http.Server{
		Addr: listen, Handler: mux,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10,
		TLSConfig:      &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: certificate},
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, homeEnrollmentPeerKey{}, conn.RemoteAddr().String())
		},
	}, nil
}

// HomeEnrollmentHandler is the enrollment route for any listener other than
// NewHomeEnrollmentServer. It refuses every request: an enrollment must arrive
// on the dedicated LAN listener.
func HomeEnrollmentHandler(root string) http.Handler {
	return newHomeEnrollmentHandler(root, nil, time.Now)
}

func newHomeEnrollmentHandler(root string, issue HomeDeviceIssuer, now func() time.Time) http.Handler {
	absolute, rootErr := filepath.Abs(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, onListener := r.Context().Value(homeEnrollmentPeerKey{}).(string)
		if !onListener || r.TLS == nil || issue == nil {
			denyHomeEnrollment(w, http.StatusForbidden, "enrollment_listener_required")
			return
		}
		if rootErr != nil {
			denyHomeEnrollment(w, http.StatusServiceUnavailable, "workspace_unavailable")
			return
		}
		var request struct {
			Code string `json:"code"`
			CSR  string `json:"csr"`
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, homeEnrollmentMaxBodyBytes+1))
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err != nil || len(raw) > homeEnrollmentMaxBodyBytes || decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
			denyHomeEnrollment(w, http.StatusBadRequest, "invalid_enrollment_request")
			return
		}
		device, chain, err := EnrollHomeDeviceWithPairing(r.Context(), absolute,
			HomeEnrollmentRequest{Code: request.Code, CSRPEM: []byte(request.CSR), LAN: ObservedLANAddress(peer)}, issue, now())
		switch {
		case errors.Is(err, ErrHomeDeviceRevoked):
			denyHomeEnrollment(w, http.StatusForbidden, "device_key_revoked")
			return
		case errors.Is(err, ErrHomeDeviceConflict):
			denyHomeEnrollment(w, http.StatusConflict, "device_name_in_use")
			return
		case errors.Is(err, ErrHomePairingDenied):
			denyHomeEnrollment(w, http.StatusForbidden, "pairing_denied")
			return
		case err != nil:
			denyHomeEnrollment(w, http.StatusServiceUnavailable, "device_provisioner_unavailable")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"device": device, "certificateChain": string(chain)})
	})
}

func denyHomeEnrollment(w http.ResponseWriter, status int, reason string) {
	guidance := map[string]string{
		"enrollment_listener_required":   "Enroll from the home network through the enrollment address in the pairing payload.",
		"invalid_enrollment_request":     "Send one JSON object with the pairing code and a PEM certificate request.",
		"pairing_denied":                 "The pairing code is unknown, expired or already used, or the device is outside the home network. Ask the Owner for a new code.",
		"device_key_revoked":             "This device key was revoked. Generate a new key and ask the Owner for a new code.",
		"device_name_in_use":             "Another enrolled device uses this name. Choose a different device name.",
		"device_provisioner_unavailable": "The node-local certificate authority did not issue a certificate. The pairing code is spent; ask the Owner for a new code.",
		"workspace_unavailable":          "The StackKits workspace is unavailable on this node.",
	}[reason]
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error_code": "home_enrollment_denied", "reason_code": reason, "retryable": false, "user_guidance": guidance,
	})
}

// HomeEnrollmentCertificateSource issues the listener certificate through
// step-ca on first use and renews it an hour before expiry. Without step-ca or
// owner custody the TLS handshake fails, so the listener stays closed.
func HomeEnrollmentCertificateSource(root string) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	var mu sync.Mutex
	var current *tls.Certificate
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()
		if current != nil && time.Until(current.Leaf.NotAfter) > time.Hour {
			return current, nil
		}
		ctx, cancel := context.WithTimeout(hello.Context(), 30*time.Second)
		defer cancel()
		issued, err := localevidence.IssueHomeEnrollmentServerCertificate(ctx, root)
		if err != nil {
			return nil, err
		}
		current = &issued
		return current, nil
	}
}
