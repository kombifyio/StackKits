package localorigin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// PeerRequest contains only public proof of a Cloud-held key. PeerRef is an
// explicit local Owner choice; it is never inferred from a site or hostname.
type PeerRequest struct {
	PeerRef string `json:"peerRef"`
	CSRPEM  string `json:"csrPEM"`
}

var ErrPeerSelection = errors.New("localorigin: CSR identity differs from explicit Owner peer selection")

type peerKey struct {
	Request       PeerRequest `json:"request"`
	PrivateKeyPEM string      `json:"privateKeyPEM"`
}

// PeerCredential is returned after Home has issued and admitted the exact leaf.
// Only certificate material crosses nodes; the private key stays at Cloud.
type PeerCredential struct {
	PeerRef            string `json:"peerRef"`
	ServerName         string `json:"serverName"`
	CertificatePEM     string `json:"certificatePEM"`
	RootCertificatePEM string `json:"rootCertificatePEM"`
}

func RequestPeer(root, peerRef string) (PeerRequest, error) {
	return RequestPeerWithRotation(root, peerRef, false)
}

func RequestPeerWithRotation(root, peerRef string, rotate bool) (PeerRequest, error) {
	if !localevidence.ValidWorkloadPeerRef(peerRef) {
		return PeerRequest{}, errors.New("localorigin: explicit workload peer reference required")
	}
	var existing peerKey
	path := stateRef("peer-key", peerRef)
	if err := readState(root, path, "peer-key", &existing); err == nil {
		if existing.Request.PeerRef != peerRef {
			return PeerRequest{}, errors.New("localorigin: peer key identity mismatch")
		}
		if !rotate {
			return existing.Request, nil
		}
		// Preserve the active generation until a newly issued certificate is
		// installed. Rotation must not destroy the currently usable credential.
		if err := archivePeerKey(root, existing); err != nil {
			return PeerRequest{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return PeerRequest{}, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return PeerRequest{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: peerRef}}, key)
	if err != nil {
		return PeerRequest{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return PeerRequest{}, err
	}
	request := PeerRequest{PeerRef: peerRef, CSRPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))}
	err = writeState(root, path, "peer-key", peerKey{Request: request, PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	return request, err
}

func peerKeyGenerationRef(peerRef string, publicKey []byte) string {
	digest := sha256.Sum256(publicKey)
	return stateRef("peer-key-generation", peerRef+":"+hex.EncodeToString(digest[:]))
}

func archivePeerKey(root string, key peerKey) error {
	block, _ := pem.Decode([]byte(key.Request.CSRPEM))
	if block == nil {
		return errors.New("localorigin: existing peer CSR absent")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return errors.New("localorigin: existing peer CSR invalid")
	}
	return writeState(root, peerKeyGenerationRef(key.Request.PeerRef, csr.RawSubjectPublicKeyInfo), "peer-key", key)
}

// EnrollPeer is called by the explicitly approved local Owner lifecycle. It
// binds the CSR to an installed origin publication, then uses the existing
// signed, current-state-bound admission operation after step-ca issuance.
func EnrollPeer(ctx context.Context, root, serverName, selectedPeer string, request PeerRequest, replaceKey bool) (PeerCredential, error) {
	var result PeerCredential
	if !localevidence.ValidWorkloadPeerRef(selectedPeer) || selectedPeer != request.PeerRef {
		return result, ErrPeerSelection
	}
	previous, err := localevidence.PrepareWorkloadPeerEnrollment(root, selectedPeer, []byte(request.CSRPEM), replaceKey)
	if err != nil {
		return result, err
	}
	p, _, err := loadPublication(root, serverName)
	if err != nil {
		return result, err
	}
	certificate, err := localevidence.IssueWorkloadPeerCertificate(ctx, root, request.PeerRef, []byte(request.CSRPEM), p.Policy.CredentialTTLSeconds)
	if err != nil {
		return result, err
	}
	block, _ := pem.Decode(certificate)
	if block == nil {
		return result, errors.New("localorigin: issuer returned no leaf")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	operation, err := localevidence.SignWorkloadPeerOperation(root, localevidence.WorkloadPeerOperation{
		Operation: "admit", IssuedAt: now, ExpiresAt: now.Add(time.Minute),
		Admission: &localevidence.WorkloadPeerAdmission{PeerRef: request.PeerRef, ServiceRef: p.Policy.ServiceRef, EdgeSiteRef: p.Policy.EdgeSiteRef, Audience: p.Policy.Audience, CertificatePEM: string(certificate), ValidUntil: leaf.NotAfter},
	})
	if err != nil {
		return result, err
	}
	if operation.PreviousState != previous {
		return result, errors.New("localorigin: peer admission changed during issuance; obtain fresh Owner approval")
	}
	if err := localevidence.ApplyWorkloadPeerOperation(root, operation); err != nil {
		return result, err
	}
	if err := writeState(root, stateRef("publication-peer", serverName), "publication-peer", string(certificate)); err != nil {
		return result, err
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return result, err
	}
	return PeerCredential{PeerRef: request.PeerRef, ServerName: serverName, CertificatePEM: string(certificate), RootCertificatePEM: owner.StepCARootCertificatePEM}, nil
}

// InstallPeer requires the Home root fingerprint through a separate approved
// channel. A certificate response cannot bootstrap its own trust anchor.
func InstallPeer(root string, credential PeerCredential, rootFingerprint string) error {
	_, err := peerTLSCertificate(root, credential, rootFingerprint)
	if err != nil {
		return err
	}
	return writeState(root, stateRef("peer-credential", credential.PeerRef), "peer-credential", credential)
}

// ProbePeer exercises the installed Cloud credential over the separately
// established federation loopback socket. The credential selects the sole
// origin identity; redirects cannot forward it to a different destination.
func ProbePeer(ctx context.Context, root, peerRef, address string) (int, error) {
	return ProbePeerOrigin(ctx, root, peerRef, address, "")
}

// ProbePeerOrigin additionally pins the expected origin identity supplied by
// an adopted external fabric. It reuses the same local credential custody.
func ProbePeerOrigin(ctx context.Context, root, peerRef, address, serverName string) (int, error) {
	if err := validateLoopbackAddress(address); err != nil {
		return 0, err
	}
	var credential PeerCredential
	if err := readState(root, stateRef("peer-credential", peerRef), "peer-credential", &credential); err != nil {
		return 0, err
	}
	if serverName != "" && credential.ServerName != serverName {
		return 0, errors.New("localorigin: installed peer belongs to another origin")
	}
	block, _ := pem.Decode([]byte(credential.RootCertificatePEM))
	if block == nil {
		return 0, errors.New("localorigin: installed root missing")
	}
	digest := sha256.Sum256(block.Bytes)
	certificate, err := peerTLSCertificate(root, credential, "sha256:"+hex.EncodeToString(digest[:]))
	if err != nil {
		return 0, err
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(credential.RootCertificatePEM))
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, ServerName: credential.ServerName, RootCAs: roots, Certificates: []tls.Certificate{certificate}}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := url.URL{Scheme: "https", Host: credential.ServerName, Path: "/"}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return response.StatusCode, nil
}

func peerTLSCertificate(root string, credential PeerCredential, rootFingerprint string) (tls.Certificate, error) {
	var key peerKey
	if err := readState(root, stateRef("peer-key", credential.PeerRef), "peer-key", &key); err != nil {
		return tls.Certificate{}, err
	}
	block, rest := pem.Decode([]byte(credential.RootCertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return tls.Certificate{}, errors.New("localorigin: one pinned Home root required")
	}
	digest := sha256.Sum256(block.Bytes)
	if rootFingerprint != "sha256:"+hex.EncodeToString(digest[:]) {
		return tls.Certificate{}, errors.New("localorigin: Home root fingerprint differs from owner approval")
	}
	rootCA, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !rootCA.IsCA || rootCA.CheckSignatureFrom(rootCA) != nil {
		return tls.Certificate{}, errors.New("localorigin: invalid Home root")
	}
	certificate, err := tls.X509KeyPair([]byte(credential.CertificatePEM), []byte(key.PrivateKeyPEM))
	if err != nil {
		leafBlock, _ := pem.Decode([]byte(credential.CertificatePEM))
		if leafBlock == nil {
			return certificate, err
		}
		leaf, parseErr := x509.ParseCertificate(leafBlock.Bytes)
		if parseErr != nil {
			return certificate, parseErr
		}
		if readErr := readState(root, peerKeyGenerationRef(credential.PeerRef, leaf.RawSubjectPublicKeyInfo), "peer-key", &key); readErr != nil {
			return certificate, readErr
		}
		certificate, err = tls.X509KeyPair([]byte(credential.CertificatePEM), []byte(key.PrivateKeyPEM))
		if err != nil {
			return certificate, err
		}
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return certificate, err
	}
	if leaf.Subject.CommonName != credential.PeerRef || leaf.IsCA || len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || len(leaf.UnknownExtKeyUsage) != 0 || leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		return certificate, errors.New("localorigin: exact client-only credential required")
	}
	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(rootCA)
	for _, der := range certificate.Certificate[1:] {
		ca, err := x509.ParseCertificate(der)
		if err != nil {
			return certificate, err
		}
		intermediates.AddCert(ca)
	}
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return certificate, err
}
