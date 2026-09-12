package localevidence

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

const basementOriginProvisionerName = "stackkits-owner-origin"
const basementOriginSignURL = "https://localhost:9000/1.0/sign"

// ErrBasementOriginProvisionerMissing preserves existing immutable runtime
// bundles: they need an explicit custody upgrade before workload issuance.
var ErrBasementOriginProvisionerMissing = errors.New("localevidence: established runtime needs the owner origin provisioner")

func basementOriginProvisioner(workspaceRoot string) (basementStepCAProvisioner, error) {
	return basementWorkloadProvisioner(workspaceRoot, basementOriginProvisionerName, "serverAuth")
}

func basementWorkloadProvisioner(workspaceRoot, name, usage string) (basementStepCAProvisioner, error) {
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return basementStepCAProvisioner{}, err
	}
	sans := `{{ toJson .SANs }}`
	if usage == "clientAuth" {
		// A JWK token with no SANs otherwise defaults to its subject as a DNS
		// SAN. Workload peers have an explicit principal, no DNS authority.
		sans = `[]`
	}
	return basementStepCAProvisioner{
		Type: "JWK", Name: name,
		Key:     map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(key.Public()), "kid": key.KeyID, "alg": "EdDSA", "use": "sig"},
		Claims:  map[string]any{"minTLSCertDuration": "5m", "maxTLSCertDuration": "24h", "defaultTLSCertDuration": "5m", "enableSSHCA": false, "disableRenewal": true},
		Options: map[string]any{"x509": map[string]any{"template": `{"subject":{{ toJson .Subject }},"sans":` + sans + `,"keyUsage":["digitalSignature"],"extKeyUsage":["` + usage + `"]}`}},
	}, nil
}

// IssueBasementOriginCertificate submits an owner-authorized CSR for the supplied
// origin identity policy to the existing
// node-local step-ca. Only the CSR's owner retains the leaf private key. Owner
// custody signs the one-time authorization token; step-ca alone signs the leaf.
// This does not enroll Cloud peers or claim an enforced mTLS publication.
func IssueBasementOriginCertificate(ctx context.Context, workspaceRoot string, policy architecturev2renderer.BridgeOriginMTLSPublicationPolicy, csrPEM []byte) ([]byte, error) {
	return issueBasementOriginCertificate(ctx, workspaceRoot, policy, csrPEM, nil)
}

func issueBasementOriginCertificate(ctx context.Context, workspaceRoot string, policy architecturev2renderer.BridgeOriginMTLSPublicationPolicy, csrPEM []byte, transport http.RoundTripper) ([]byte, error) {
	csr, err := validateBasementOriginCSR(policy, csrPEM)
	if err != nil {
		return nil, err
	}
	return issueBasementWorkloadCertificate(ctx, workspaceRoot, csr, csrPEM, policy.CredentialTTLSeconds, basementOriginProvisionerName, x509.ExtKeyUsageServerAuth, transport)
}

func issueBasementWorkloadCertificate(ctx context.Context, workspaceRoot string, csr *x509.CertificateRequest, csrPEM []byte, ttl int, provisioner string, usage x509.ExtKeyUsage, transport http.RoundTripper) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("localevidence: origin issuance requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := LoadBasementRuntimeCustody(workspaceRoot); err != nil {
		return nil, err
	}
	if err := requireBasementWorkloadProvisioner(workspaceRoot, provisioner, usage); err != nil {
		return nil, err
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return nil, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(owner.StepCARootCertificatePEM)) {
		return nil, errors.New("localevidence: origin issuance has no established trust root")
	}
	now := time.Now().UTC().Truncate(time.Second)
	token, err := basementWorkloadToken(key, csr, now, provisioner)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"csr": string(csrPEM), "ott": token, "notBefore": now.Format(time.RFC3339), "notAfter": now.Add(time.Duration(ttl) * time.Second).Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	if transport == nil {
		t := basementOriginHTTPTransport(roots)
		defer t.CloseIdleConnections()
		transport = t
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, basementOriginSignURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("localevidence: local step-ca issuance transport failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("localevidence: local step-ca rejected origin issuance (HTTP %d)", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("localevidence: unreadable or oversized step-ca certificate response")
	}
	var result struct {
		Certificate string   `json:"crt"`
		CA          string   `json:"ca"`
		Chain       []string `json:"certChain"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("localevidence: malformed step-ca certificate response")
	}
	leaf, err := parseCertificatePEM(result.Certificate, "origin leaf")
	if err != nil {
		return nil, err
	}
	intermediates := x509.NewCertPool()
	chain := append([]string{result.CA}, result.Chain...)
	for _, certificate := range chain {
		intermediates.AppendCertsFromPEM([]byte(certificate))
	}
	verifiedChains, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: time.Now().UTC(), KeyUsages: []x509.ExtKeyUsage{usage}})
	if err != nil {
		return nil, errors.New("localevidence: step-ca origin leaf does not chain to established authority")
	}
	if !bytes.Equal(leaf.RawSubjectPublicKeyInfo, csr.RawSubjectPublicKeyInfo) || leaf.Subject.CommonName != csr.Subject.CommonName ||
		!slices.Equal(leaf.DNSNames, csr.DNSNames) || len(leaf.IPAddresses)+len(leaf.URIs)+len(leaf.EmailAddresses) != 0 ||
		leaf.IsCA || !slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{usage}) || len(leaf.UnknownExtKeyUsage) != 0 ||
		leaf.KeyUsage != x509.KeyUsageDigitalSignature || leaf.NotBefore.Before(now) ||
		leaf.NotAfter.Sub(leaf.NotBefore) > time.Duration(ttl)*time.Second || !leaf.NotAfter.After(time.Now().Add(time.Minute)) {
		return nil, errors.New("localevidence: step-ca origin leaf widened or mismatched the requested identity")
	}
	var verifiedPEM bytes.Buffer
	for _, certificate := range verifiedChains[0] {
		if err := pem.Encode(&verifiedPEM, &pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}); err != nil {
			return nil, err
		}
	}
	return verifiedPEM.Bytes(), nil
}

func basementOriginHTTPTransport(roots *x509.CertPool) *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", "127.0.0.1:9000")
	}}
}

// VerifyBasementOriginProvisioner proves that the running, owner-rooted step-ca
// has loaded the owner public signing key before Apply completes an upgrade.
func VerifyBasementOriginProvisioner(ctx context.Context, workspaceRoot string) error {
	if ctx == nil {
		return errors.New("localevidence: provisioner verification requires a context")
	}
	if _, err := LoadBasementRuntimeCustody(workspaceRoot); err != nil {
		return err
	}
	if err := requireBasementWorkloadProvisioners(workspaceRoot); err != nil {
		return err
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return err
	}
	expected, err := basementWorkloadProvisioners(workspaceRoot)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(owner.StepCARootCertificatePEM)) {
		return errors.New("localevidence: missing provisioner trust root")
	}
	transport := basementOriginHTTPTransport(roots)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://localhost:9000/provisioners", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("localevidence: observe local step-ca provisioners: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("localevidence: step-ca provisioner readback returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return errors.New("localevidence: unreadable provisioner readback")
	}
	var result struct {
		Provisioners []basementStepCAProvisioner `json:"provisioners"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return errors.New("localevidence: invalid provisioner readback")
	}
	for _, wanted := range expected {
		found := false
		for _, provisioner := range result.Provisioners {
			if provisioner.Type == wanted.Type && provisioner.Name == wanted.Name && provisioner.Key["kid"] == wanted.Key["kid"] && provisioner.Key["x"] == wanted.Key["x"] && provisioner.Key["kty"] == "OKP" && provisioner.Key["crv"] == "Ed25519" {
				found = true
			}
		}
		if !found {
			return errors.New("localevidence: running step-ca has not loaded an owner workload provisioner")
		}
	}
	return nil
}

func validateBasementOriginCSR(policy architecturev2renderer.BridgeOriginMTLSPublicationPolicy, raw []byte) (*x509.CertificateRequest, error) {
	if policy.IdentityRef == "" || policy.IdentityRef != strings.TrimSpace(policy.IdentityRef) || !validBasementRuntimeDomain(policy.ServerName) ||
		policy.MinimumTLSVersion != "TLS1.3" || policy.CredentialTTLSeconds < 300 || policy.CredentialTTLSeconds > 86400 {
		return nil, errors.New("localevidence: origin issuance requires the bounded workload identity policy")
	}
	if len(raw) > 1<<20 {
		return nil, errors.New("localevidence: oversized origin CSR")
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("localevidence: origin issuance requires one CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != policy.IdentityRef ||
		!slices.Equal(csr.DNSNames, []string{policy.ServerName}) || len(csr.IPAddresses)+len(csr.URIs)+len(csr.EmailAddresses) != 0 {
		return nil, errors.New("localevidence: origin CSR does not prove the exact requested identity")
	}
	return csr, nil
}

func requireBasementOriginProvisioner(workspaceRoot string) error {
	return requireBasementWorkloadProvisioner(workspaceRoot, basementOriginProvisionerName, x509.ExtKeyUsageServerAuth)
}

func requireBasementWorkloadProvisioner(workspaceRoot, name string, usage x509.ExtKeyUsage) error {
	eku := "serverAuth"
	if usage == x509.ExtKeyUsageClientAuth {
		eku = "clientAuth"
	}
	expected, err := basementWorkloadProvisioner(workspaceRoot, name, eku)
	if err != nil {
		return err
	}
	path, err := confinedCustodyPath(workspaceRoot, filepath.ToSlash(filepath.Join(basementRuntimeCustodyRelDir, "step-ca/config/ca.json")))
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config basementStepCAConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	for _, provisioner := range config.Authority.Provisioners {
		if reflect.DeepEqual(provisioner, expected) {
			return nil
		}
	}
	return ErrBasementOriginProvisionerMissing
}

func basementWorkloadToken(key OwnerKey, csr *x509.CertificateRequest, now time.Time, provisioner string) (string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	fingerprint := sha256.Sum256(csr.Raw)
	header, _ := json.Marshal(map[string]string{"alg": "EdDSA", "kid": key.KeyID, "typ": "JWT"})
	claims, err := json.Marshal(map[string]any{"iss": provisioner, "sub": csr.Subject.CommonName, "aud": []string{basementOriginSignURL}, "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": base64.RawURLEncoding.EncodeToString(nonce), "sans": csr.DNSNames, "cnf": map[string]string{"x5rt#S256": base64.RawURLEncoding.EncodeToString(fingerprint[:])}})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	return payload + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key.private, []byte(payload))), nil
}
