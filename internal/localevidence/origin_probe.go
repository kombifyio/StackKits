package localevidence

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const OriginProofPath = "/.well-known/stackkit-origin-proof"

// VerifyOriginProbeOwner accepts only actual TLS possession of the currently
// custodied Owner certificate. It grants a read-only runtime proof, no workload
// access, management operation, or human passkey assertion.
func VerifyOriginProbeOwner(root string, chain []*x509.Certificate) error {
	owner, err := LoadOwnerCustody(root)
	if err != nil {
		return err
	}
	expected, err := parseCertificatePEM(owner.OwnerCertificatePEM, "owner")
	if err != nil {
		return err
	}
	if len(chain) == 0 || !bytes.Equal(chain[0].Raw, expected.Raw) {
		return errors.New("localevidence: runtime probe requires current Owner TLS possession")
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(owner.StepCARootCertificatePEM))
	_, err = chain[0].Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return err
}

// ReadOriginProof keeps the Owner private key inside its custody package. The
// only request is a bounded read-only probe to an explicit loopback listener.
func ReadOriginProof(ctx context.Context, root, address, serverName, nonce string) ([]byte, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, errors.New("localevidence: origin probe must stay on loopback")
	}
	owner, err := LoadOwnerCustody(root)
	if err != nil {
		return nil, err
	}
	key, err := LoadOwnerKey(root)
	if err != nil {
		return nil, err
	}
	cert, err := parseCertificatePEM(owner.OwnerCertificatePEM, "owner")
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(owner.StepCARootCertificatePEM))
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, ServerName: serverName, RootCAs: roots, Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key.private}}}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := url.URL{Scheme: "https", Host: serverName, Path: OriginProofPath, RawQuery: url.Values{"nonce": {nonce}}.Encode()}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		return nil, errors.New("localevidence: live origin proof unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 {
		return nil, errors.New("localevidence: unreadable origin proof")
	}
	// The handler may race a certificate rotation after this connection's
	// handshake. Only the leaf observed on the wire may become evidence.
	var proof struct {
		CertificatePEM string `json:"certificatePEM"`
	}
	if err := json.Unmarshal(raw, &proof); err != nil {
		return nil, err
	}
	leaf, _ := pem.Decode([]byte(proof.CertificatePEM))
	if leaf == nil || leaf.Type != "CERTIFICATE" || len(response.TLS.PeerCertificates) == 0 || !bytes.Equal(leaf.Bytes, response.TLS.PeerCertificates[0].Raw) {
		return nil, errors.New("localevidence: origin proof leaf differs from TLS handshake")
	}
	return raw, nil
}
