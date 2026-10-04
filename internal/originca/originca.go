// Package originca holds the node side of Cloudflare Origin CA certificates
// for managed kombify.me origins (ADR-0047).
//
// The node generates the key pair and hands out only a CSR. Techstack has the
// certificate issued and delivers the signed certificate, which is public. The
// private key is written to owner custody and never leaves the node: no
// result, log or receipt in this package carries it.
package originca

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	SchemaVersion = "stackkit.origin-certificate/v1"
	// ManagedZone is the only zone the Origin CA certificate covers.
	ManagedZone = "kombify.me"

	PhaseRequest = "request"
	PhaseInstall = "install"

	// ContainerDir is where the router sees the live directory.
	ContainerDir = "/origin-tls"

	maxHosts        = 50
	maxCertificate  = 64 << 10
	customRoot      = "public-tls-origin"
	liveDirName     = "live"
	pendingDirName  = "pending"
	pendingKeyName  = "pending.key"
	pendingMetaName = "pending.json"
	stateName       = "state.json"
	dynamicName     = "dynamic.yaml"
)

//go:embed origin_ca_rsa_root.pem
var rsaRootPEM []byte

//go:embed origin_ca_ecc_root.pem
var eccRootPEM []byte

// RootPool returns the Cloudflare Origin CA roots published at
// developers.cloudflare.com/ssl/static. Origin CA certificates are not
// browser trust; callers use this pool for node-vantage probes of managed
// routes instead of system trust.
func RootPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(rsaRootPEM)
	pool.AppendCertsFromPEM(eccRootPEM)
	return pool
}

// RootPEM returns the concatenated root certificates.
func RootPEM() []byte { return append(bytes.Clone(rsaRootPEM), eccRootPEM...) }

// CustodyDir is the owner-custody directory of the origin certificate.
func CustodyDir(workspace string) string {
	return filepath.Join(workspace, ".stackkit", "custody", customRoot)
}

// LiveDir is the directory the router mounts read-only.
func LiveDir(workspace string) string { return filepath.Join(CustodyDir(workspace), liveDirName) }

// RequestResult is the public result of the request phase.
type RequestResult struct {
	SchemaVersion string   `json:"schemaVersion"`
	Phase         string   `json:"phase"`
	Hosts         []string `json:"hosts"`
	KeyAlgorithm  string   `json:"keyAlgorithm"`
	CSRPEM        string   `json:"csrPem"`
	CSRSHA256     string   `json:"csrSha256"`
}

// InstallResult is the public result of the install phase. It carries the
// certificate identity only, never key material.
type InstallResult struct {
	SchemaVersion     string   `json:"schemaVersion"`
	Phase             string   `json:"phase"`
	Hosts             []string `json:"hosts"`
	SerialHex         string   `json:"serialHex"`
	NotBefore         string   `json:"notBefore"`
	NotAfter          string   `json:"notAfter"`
	CertificateSHA256 string   `json:"certificateSha256"`
}

type pendingMeta struct {
	Hosts     []string `json:"hosts"`
	CSRSHA256 string   `json:"csrSha256"`
}

type state struct {
	Hosts             []string `json:"hosts"`
	SerialHex         string   `json:"serialHex"`
	NotAfter          string   `json:"notAfter"`
	CertificateSHA256 string   `json:"certificateSha256"`
}

// Installer binds the node operations to one workspace and a trust pool.
type Installer struct {
	Workspace string
	Roots     *x509.CertPool
	Now       func() time.Time
}

func (i Installer) now() time.Time {
	if i.Now != nil {
		return i.Now().UTC()
	}
	return time.Now().UTC()
}

func (i Installer) roots() *x509.CertPool {
	if i.Roots != nil {
		return i.Roots
	}
	return RootPool()
}

// NormalizeHosts validates and sorts the managed hostnames.
func NormalizeHosts(hosts []string) ([]string, error) {
	if len(hosts) == 0 || len(hosts) > maxHosts {
		return nil, fmt.Errorf("origin certificate needs 1 to %d hostnames", maxHosts)
	}
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if !validManagedHost(host) {
			return nil, fmt.Errorf("hostname %q is not a managed %s hostname", host, ManagedZone)
		}
		if !slices.Contains(out, host) {
			out = append(out, host)
		}
	}
	slices.Sort(out)
	return out, nil
}

func validManagedHost(host string) bool {
	name := strings.TrimPrefix(host, "*.")
	if !strings.HasSuffix(name, "."+ManagedZone) || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

// Request generates a fresh ECDSA P-256 key pair in custody and returns the
// CSR. A repeated request replaces the pending key; the live certificate keeps
// serving until Install swaps it.
func (i Installer) Request(hosts []string) (RequestResult, error) {
	hosts, err := NormalizeHosts(hosts)
	if err != nil {
		return RequestResult{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return RequestResult{}, fmt.Errorf("generate origin key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: hosts[0]},
		DNSNames: hosts,
	}, key)
	if err != nil {
		return RequestResult{}, fmt.Errorf("create origin CSR: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return RequestResult{}, fmt.Errorf("encode origin key: %w", err)
	}
	pendingDir := filepath.Join(CustodyDir(i.Workspace), pendingDirName)
	if err := os.MkdirAll(pendingDir, 0o700); err != nil {
		return RequestResult{}, fmt.Errorf("create origin custody: %w", err)
	}
	if err := os.MkdirAll(LiveDir(i.Workspace), 0o700); err != nil {
		return RequestResult{}, fmt.Errorf("create origin live directory: %w", err)
	}
	sum := sha256.Sum256(csrDER)
	meta := pendingMeta{Hosts: hosts, CSRSHA256: "sha256:" + hex.EncodeToString(sum[:])}
	metaRaw, _ := json.Marshal(meta)
	if err := writeAtomic(filepath.Join(pendingDir, pendingKeyName), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return RequestResult{}, err
	}
	if err := writeAtomic(filepath.Join(pendingDir, pendingMetaName), metaRaw, 0o600); err != nil {
		return RequestResult{}, err
	}
	return RequestResult{
		SchemaVersion: SchemaVersion, Phase: PhaseRequest, Hosts: hosts, KeyAlgorithm: "ecdsa-p256",
		CSRPEM:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
		CSRSHA256: meta.CSRSHA256,
	}, nil
}

// Install verifies the delivered certificate against the pending key, the
// requested hostnames and the Origin CA roots, then publishes it atomically
// to the router's live directory.
func (i Installer) Install(certificatePEM []byte) (InstallResult, error) {
	if len(certificatePEM) == 0 || len(certificatePEM) > maxCertificate {
		return InstallResult{}, errors.New("origin certificate is empty or too large")
	}
	pendingDir := filepath.Join(CustodyDir(i.Workspace), pendingDirName)
	var meta pendingMeta
	metaRaw, err := os.ReadFile(filepath.Join(pendingDir, pendingMetaName)) // #nosec G304 -- fixed custody path
	if err != nil || json.Unmarshal(metaRaw, &meta) != nil {
		return InstallResult{}, errors.New("no pending origin certificate request on this node")
	}
	keyPEM, err := os.ReadFile(filepath.Join(pendingDir, pendingKeyName)) // #nosec G304 -- fixed custody path
	if err != nil {
		return InstallResult{}, errors.New("pending origin key is unavailable")
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return InstallResult{}, errors.New("pending origin key is corrupt")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return InstallResult{}, errors.New("pending origin key is corrupt")
	}
	var chain []*x509.Certificate
	rest := certificatePEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return InstallResult{}, errors.New("delivered origin certificate must contain certificates only")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return InstallResult{}, fmt.Errorf("parse origin certificate: %w", err)
		}
		chain = append(chain, cert)
	}
	if len(chain) == 0 {
		return InstallResult{}, errors.New("delivered origin certificate is not PEM")
	}
	leaf := chain[0]
	public, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !public.Equal(&key.PublicKey) {
		return InstallResult{}, errors.New("delivered certificate does not match the key generated on this node")
	}
	now := i.now()
	for _, host := range meta.Hosts {
		if !slices.Contains(leaf.DNSNames, host) {
			return InstallResult{}, fmt.Errorf("delivered certificate does not cover %q", host)
		}
	}
	intermediates := x509.NewCertPool()
	for _, cert := range chain[1:] {
		intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: i.roots(), Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return InstallResult{}, fmt.Errorf("delivered certificate is not a valid Cloudflare Origin CA certificate: %w", err)
	}
	sum := sha256.Sum256(leaf.Raw)
	digest := hex.EncodeToString(sum[:])
	liveDir := LiveDir(i.Workspace)
	if err := os.MkdirAll(liveDir, 0o700); err != nil {
		return InstallResult{}, fmt.Errorf("create origin live directory: %w", err)
	}
	certName, keyName := "origin-"+digest[:16]+".crt", "origin-"+digest[:16]+".key"
	if err := writeAtomic(filepath.Join(liveDir, certName), certificatePEM, 0o644); err != nil {
		return InstallResult{}, err
	}
	if err := writeAtomic(filepath.Join(liveDir, keyName), keyPEM, 0o600); err != nil {
		return InstallResult{}, err
	}
	dynamic := fmt.Sprintf("tls:\n  certificates:\n    - certFile: %s/%s\n      keyFile: %s/%s\n  stores:\n    default:\n      defaultCertificate:\n        certFile: %s/%s\n        keyFile: %s/%s\n",
		ContainerDir, certName, ContainerDir, keyName, ContainerDir, certName, ContainerDir, keyName)
	if err := writeAtomic(filepath.Join(liveDir, dynamicName), []byte(dynamic), 0o644); err != nil {
		return InstallResult{}, err
	}
	stateRaw, _ := json.Marshal(state{Hosts: meta.Hosts, SerialHex: leaf.SerialNumber.Text(16), NotAfter: leaf.NotAfter.UTC().Format(time.RFC3339), CertificateSHA256: "sha256:" + digest})
	if err := writeAtomic(filepath.Join(CustodyDir(i.Workspace), stateName), stateRaw, 0o600); err != nil {
		return InstallResult{}, err
	}
	_ = os.Remove(filepath.Join(pendingDir, pendingKeyName))
	_ = os.Remove(filepath.Join(pendingDir, pendingMetaName))
	removeStale(liveDir, certName, keyName)
	return InstallResult{
		SchemaVersion: SchemaVersion, Phase: PhaseInstall, Hosts: meta.Hosts, SerialHex: leaf.SerialNumber.Text(16),
		NotBefore: leaf.NotBefore.UTC().Format(time.RFC3339), NotAfter: leaf.NotAfter.UTC().Format(time.RFC3339),
		CertificateSHA256: "sha256:" + digest,
	}, nil
}

func removeStale(liveDir string, keep ...string) {
	entries, err := os.ReadDir(liveDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "origin-") && !slices.Contains(keep, name) {
			_ = os.Remove(filepath.Join(liveDir, name))
		}
	}
}

// Serves reports whether an unexpired installed origin certificate covers
// host.
func Serves(workspace, host string, now time.Time) bool {
	current, ok := loadState(workspace)
	if !ok {
		return false
	}
	notAfter, err := time.Parse(time.RFC3339, current.NotAfter)
	return err == nil && notAfter.After(now) && covers(current, host)
}

// Covers reports whether an installed origin certificate, expired or not,
// covers host. Route rendering uses it: a managed route stays on the origin
// certificate path after expiry and never falls back to an ACME order.
func Covers(workspace, host string) bool {
	current, ok := loadState(workspace)
	return ok && covers(current, host)
}

func loadState(workspace string) (state, bool) {
	raw, err := os.ReadFile(filepath.Join(CustodyDir(workspace), stateName)) // #nosec G304 -- fixed custody path
	if err != nil {
		return state{}, false
	}
	var current state
	return current, json.Unmarshal(raw, &current) == nil
}

func covers(current state, host string) bool {
	host = strings.ToLower(host)
	for _, covered := range current.Hosts {
		if covered == host {
			return true
		}
		if strings.HasPrefix(covered, "*.") && strings.Count(host, ".") == strings.Count(covered, ".") && strings.HasSuffix(host, covered[1:]) {
			return true
		}
	}
	return false
}

func writeAtomic(path string, content []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("write origin custody: %w", err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write origin custody: %w", err)
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write origin custody: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("write origin custody: %w", err)
	}
	return os.Rename(name, path)
}
