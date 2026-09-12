package localorigin

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// Proof is read from the real listener after current publication, backend and
// peer authorization have been evaluated. It never contains private key data.
type Proof struct {
	Nonce                     string                                                   `json:"nonce"`
	Policy                    architecturev2renderer.BridgeOriginMTLSPublicationPolicy `json:"policy"`
	CertificatePEM            string                                                   `json:"certificatePEM"`
	ObservedAt                time.Time                                                `json:"observedAt"`
	TLSVersion                uint16                                                   `json:"tlsVersion"`
	ClientCertificateRequired bool                                                     `json:"clientCertificateRequired"`
	LoopbackOnly              bool                                                     `json:"loopbackOnly"`
}

func serveProof(w http.ResponseWriter, r *http.Request, root string, p publication, dial func(context.Context, Backend) (net.Conn, error)) {
	if r.Method != http.MethodGet || localevidence.VerifyOriginProbeOwner(root, r.TLS.PeerCertificates) != nil {
		http.Error(w, "owner runtime proof denied", http.StatusForbidden)
		return
	}
	nonce := r.URL.Query().Get("nonce")
	decoded, err := hex.DecodeString(nonce)
	if err != nil || len(decoded) != 32 {
		http.Error(w, "proof nonce required", http.StatusBadRequest)
		return
	}
	address, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || validateLoopbackAddress(address.String()) != nil {
		http.Error(w, "loopback listener required", http.StatusServiceUnavailable)
		return
	}
	var peerPEM string
	if err := readState(root, stateRef("publication-peer", p.Policy.ServerName), "publication-peer", &peerPEM); err != nil {
		http.Error(w, "admitted peer unavailable", http.StatusServiceUnavailable)
		return
	}
	var chain []*x509.Certificate
	for raw := []byte(peerPEM); len(raw) > 0; {
		block, rest := pem.Decode(raw)
		if block == nil {
			http.Error(w, "peer chain unavailable", http.StatusServiceUnavailable)
			return
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			http.Error(w, "peer chain unavailable", http.StatusServiceUnavailable)
			return
		}
		chain = append(chain, certificate)
		raw = rest
	}
	if _, err := localevidence.AuthorizeWorkloadPeer(root, chain, localevidence.WorkloadPeerScope{ServiceRef: p.Policy.ServiceRef, EdgeSiteRef: p.Policy.EdgeSiteRef, Audience: p.Policy.Audience}, time.Now().UTC()); err != nil {
		http.Error(w, "current peer authorization unavailable", http.StatusServiceUnavailable)
		return
	}
	backend, err := loadBackend(root, p.Policy)
	if err != nil {
		http.Error(w, "backend unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	connection, err := dial(ctx, backend)
	if err != nil {
		http.Error(w, "backend socket unavailable", http.StatusServiceUnavailable)
		return
	}
	_ = connection.Close()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Proof{Nonce: nonce, Policy: p.Policy, CertificatePEM: p.CertificatePEM, ObservedAt: time.Now().UTC(), TLSVersion: r.TLS.Version, ClientCertificateRequired: true, LoopbackOnly: true})
}

func Observe(ctx context.Context, root, serverName string) (Proof, error) {
	var address string
	var proof Proof
	if err := readState(root, stateRef("listener", "origin"), "listener", &address); err != nil {
		return proof, err
	}
	if err := validateLoopbackAddress(address); err != nil {
		return proof, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return proof, err
	}
	now := time.Now().UTC()
	raw, err := localevidence.ReadOriginProof(ctx, root, address, serverName, hex.EncodeToString(nonce))
	if err != nil {
		return proof, err
	}
	if err := json.Unmarshal(raw, &proof); err != nil {
		return proof, err
	}
	if proof.Nonce != hex.EncodeToString(nonce) || proof.Policy.ServerName != serverName || proof.ObservedAt.Before(now) || proof.ObservedAt.After(time.Now()) || proof.TLSVersion != tls.VersionTLS13 || !proof.ClientCertificateRequired || !proof.LoopbackOnly {
		return proof, errors.New("localorigin: runtime proof did not establish current mTLS state")
	}
	return proof, nil
}
