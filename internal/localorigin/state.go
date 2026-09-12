// Package localorigin realizes the node-local mTLS publication inside the
// existing StackKits server. Only Apply-owned, signed publication state is read.
package localorigin

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const stateDirectory = ".stackkit/custody/origin-runtime"

// Backend is produced only after the existing application runtime has observed
// the exact running container, its scoped loopback mapping and HTTP readiness.
type Backend struct {
	ModuleRef   string    `json:"moduleRef"`
	UnitRef     string    `json:"unitRef"`
	InstanceRef string    `json:"instanceRef"`
	NodeRef     string    `json:"nodeRef"`
	ServiceRef  string    `json:"serviceRef"`
	TargetPort  int       `json:"targetPort"`
	Address     string    `json:"address"`
	ContainerID string    `json:"containerId"`
	ObservedAt  time.Time `json:"observedAt"`
}

type publication struct {
	Disabled       bool                                                     `json:"disabled,omitempty"`
	Policy         architecturev2renderer.BridgeOriginMTLSPublicationPolicy `json:"policy"`
	CertificatePEM string                                                   `json:"certificatePEM"`
	KeyRef         string                                                   `json:"keyRef"`
	BoundAt        time.Time                                                `json:"boundAt"`
}

type signedState struct {
	Kind      string                                  `json:"kind"`
	Payload   json.RawMessage                         `json:"payload"`
	Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func stateRef(kind, identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return stateDirectory + "/" + kind + "-" + hex.EncodeToString(digest[:]) + ".json"
}

func RecordBackend(workspaceRoot string, backend Backend) error {
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return err
	}
	if backend.NodeRef != owner.Binding.NodeRef || backend.ModuleRef == "" || backend.UnitRef == "" || backend.InstanceRef == "" || backend.ServiceRef == "" || backend.ObservedAt.IsZero() || backend.TargetPort < 1 || backend.TargetPort > 65535 || !validContainerID(backend.ContainerID) {
		return errors.New("localorigin: backend lacks exact observed local workload identity")
	}
	if err := validateLoopbackAddress(backend.Address); err != nil {
		return err
	}
	return writeState(workspaceRoot, stateRef("backend", backend.InstanceRef), "backend", backend)
}

// WithdrawBackend removes the exact signed origin backend record for the
// removed container. Missing exact state may converge. A replaced directory,
// a non-regular occupancy of the hashed path, or a newer ContainerID is left
// untouched.
func WithdrawBackend(workspaceRoot string, backend Backend) error {
	if backend.InstanceRef == "" || backend.ModuleRef == "" || backend.UnitRef == "" || backend.NodeRef == "" || backend.ServiceRef == "" || !validContainerID(backend.ContainerID) {
		return errors.New("localorigin: backend withdrawal requires exact observed container identity")
	}
	path := stateRef("backend", backend.InstanceRef)
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Close() }()
	info, err := tx.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("localorigin: backend path is not the signed regular record")
	}
	raw, readInfo, err := tx.ReadStableBounded(path, 256<<10)
	if err != nil {
		return err
	}
	if !os.SameFile(info, readInfo) || !readInfo.Mode().IsRegular() {
		return errors.New("localorigin: backend record changed during withdrawal")
	}
	var record signedState
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	signature := record.Signature
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	unsigned, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if record.Kind != "backend" {
		return errors.New("localorigin: state kind mismatch")
	}
	if err := localevidence.VerifyOwnerPolicyState(workspaceRoot, unsigned, signature); err != nil {
		return err
	}
	var existing Backend
	if err := json.Unmarshal(record.Payload, &existing); err != nil {
		return err
	}
	if existing.InstanceRef != backend.InstanceRef || existing.ModuleRef != backend.ModuleRef ||
		existing.UnitRef != backend.UnitRef || existing.NodeRef != backend.NodeRef ||
		existing.ServiceRef != backend.ServiceRef {
		return errors.New("localorigin: backend identity differs from the removed workload")
	}
	if existing.ContainerID != backend.ContainerID {
		return errors.New("localorigin: backend container was replaced; recorded identity was not withdrawn")
	}
	if err := tx.RemoveRegularFile(path, readInfo); err != nil {
		return err
	}
	_, err = tx.SyncDirectory(stateDirectory)
	return err
}

func validContainerID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	number, err := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || number < 1 || number > 65535 {
		return errors.New("localorigin: backend must be an explicit observed loopback socket")
	}
	return nil
}

// Bind issues the server certificate through the existing step-ca and installs
// the supplied publication. Callers must validate its executor artifact before
// granting this local capability. It cannot select an arbitrary URL:
// the backend must already be attested by the application runtime producer.
func Bind(ctx context.Context, workspaceRoot string, policy architecturev2renderer.BridgeOriginMTLSPublicationPolicy) error {
	if policy.ServerName == "" || policy.IdentityRef == "" || policy.UpstreamProtocol != "http" || policy.MinimumTLSVersion != "TLS1.3" || policy.RevocationMaxStalenessSeconds < 1 || policy.RevocationMaxStalenessSeconds > 86400 {
		return errors.New("localorigin: unsupported origin publication policy")
	}
	if _, err := loadBackend(workspaceRoot, policy); err != nil {
		return err
	}
	var existing publication
	if err := readState(workspaceRoot, stateRef("publication", policy.ServerName), "publication", &existing); err == nil && !existing.Disabled {
		_, certificate, loadErr := loadPublication(workspaceRoot, policy.ServerName)
		var invalid x509.CertificateInvalidError
		if loadErr != nil && !(errors.As(loadErr, &invalid) && invalid.Reason == x509.Expired) {
			return loadErr
		}
		if loadErr == nil && existing.Policy == policy {
			leaf, err := x509.ParseCertificate(certificate.Certificate[0])
			if err != nil {
				return err
			}
			if leaf.NotAfter.After(time.Now().Add(time.Minute)) {
				return nil
			}
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: policy.IdentityRef}, DNSNames: []string{policy.ServerName}}, key)
	if err != nil {
		return err
	}
	cert, err := localevidence.IssueBasementOriginCertificate(ctx, workspaceRoot, policy, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	digest := sha256.Sum256(key.Public().(ed25519.PublicKey))
	keyRef := stateDirectory + "/key-" + hex.EncodeToString(digest[:]) + ".pem"
	if err := writePrivate(workspaceRoot, keyRef, keyPEM); err != nil {
		return err
	}
	return writeState(workspaceRoot, stateRef("publication", policy.ServerName), "publication", publication{Policy: policy, CertificatePEM: string(cert), KeyRef: keyRef, BoundAt: time.Now().UTC()})
}

func loadBackend(workspaceRoot string, policy architecturev2renderer.BridgeOriginMTLSPublicationPolicy) (Backend, error) {
	var backend Backend
	if err := readState(workspaceRoot, stateRef("backend", policy.OriginInstanceRef), "backend", &backend); err != nil {
		return backend, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return backend, err
	}
	if backend.InstanceRef != policy.OriginInstanceRef || backend.ModuleRef != policy.ModuleRef || backend.UnitRef != policy.UnitRef || backend.NodeRef != owner.Binding.NodeRef || backend.ServiceRef != policy.ServiceRef || backend.TargetPort != policy.TargetPort || !validContainerID(backend.ContainerID) {
		return backend, errors.New("localorigin: observed application backend differs from publication")
	}
	return backend, validateLoopbackAddress(backend.Address)
}

func loadPublication(workspaceRoot, serverName string) (publication, tls.Certificate, error) {
	var p publication
	if err := readState(workspaceRoot, stateRef("publication", serverName), "publication", &p); err != nil {
		return p, tls.Certificate{}, err
	}
	if p.Disabled || p.Policy.ServerName != serverName || p.Policy.MinimumTLSVersion != "TLS1.3" {
		return p, tls.Certificate{}, errors.New("localorigin: publication identity mismatch")
	}
	key, err := readPrivate(workspaceRoot, p.KeyRef, 16<<10)
	if err != nil {
		return p, tls.Certificate{}, err
	}
	certificate, err := tls.X509KeyPair([]byte(p.CertificatePEM), key)
	if err != nil {
		return p, certificate, err
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return p, certificate, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return p, certificate, err
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(owner.StepCARootCertificatePEM))
	intermediates := x509.NewCertPool()
	for _, der := range certificate.Certificate[1:] {
		item, err := x509.ParseCertificate(der)
		if err != nil {
			return p, certificate, err
		}
		intermediates.AddCert(item)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: serverName, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return p, certificate, err
	}
	return p, certificate, nil
}

func writeState(workspaceRoot, path, kind string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	record := signedState{Kind: kind, Payload: payload}
	unsigned, err := json.Marshal(record)
	if err != nil {
		return err
	}
	record.Signature, err = localevidence.SignOwnerPolicyState(workspaceRoot, unsigned)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writePrivate(workspaceRoot, path, raw)
}

func readState(workspaceRoot, path, kind string, value any) error {
	raw, err := readPrivate(workspaceRoot, path, 256<<10)
	if err != nil {
		return err
	}
	var record signedState
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	signature := record.Signature
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	unsigned, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if record.Kind != kind {
		return errors.New("localorigin: state kind mismatch")
	}
	if err := localevidence.VerifyOwnerPolicyState(workspaceRoot, unsigned, signature); err != nil {
		return err
	}
	return json.Unmarshal(record.Payload, value)
}

func readPrivate(workspaceRoot, path string, limit int64) ([]byte, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer tx.Close()
	raw, info, err := tx.ReadStableBounded(path, limit)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("localorigin: runtime state is not owner-only")
	}
	return raw, nil
}

func writePrivate(workspaceRoot, path string, raw []byte) error {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	if err := tx.MkdirAll(filepath.ToSlash(filepath.Dir(path)), 0700); err != nil {
		return err
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(path, raw)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("localorigin: state write was not durable")
	}
	_, err = tx.SyncDirectory(filepath.ToSlash(filepath.Dir(path)))
	return err
}
