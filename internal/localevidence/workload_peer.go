package localevidence

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const workloadPeerDirectory = ".stackkit/custody/workload-peers"
const workloadPeerSignatureDomain = "stackkit.owner-workload-peer-admission/v1\x00"

// WorkloadPeerAdmission is an explicit Home-owner approval of one CA-issued
// client certificate for one publication. It neither issues a certificate nor
// grants access to identity administration or any other service.
type WorkloadPeerAdmission struct {
	PeerRef        string    `json:"peerRef"`
	ServiceRef     string    `json:"serviceRef"`
	EdgeSiteRef    string    `json:"edgeSiteRef"`
	Audience       string    `json:"audience"`
	CertificatePEM string    `json:"certificatePEM"`
	ValidUntil     time.Time `json:"validUntil"`
}

type WorkloadPeerScope struct {
	ServiceRef  string `json:"serviceRef"`
	EdgeSiteRef string `json:"edgeSiteRef"`
	Audience    string `json:"audience"`
}

type workloadPeerRecord struct {
	Revision                 uint64                    `json:"revision"`
	Admission                WorkloadPeerAdmission     `json:"admission"`
	Revoked                  bool                      `json:"revoked"`
	RetiredCertificates      []string                  `json:"retiredCertificates,omitempty"`
	RetiredCertificateExpiry map[string]time.Time      `json:"retiredCertificateExpiry,omitempty"`
	RevokedKeys              []string                  `json:"revokedKeys,omitempty"`
	Signature                OwnerPolicyStateSignature `json:"signature"`
}

// AdmitWorkloadPeer must be called by an authenticated local owner operation.
// The CA chain, client-only usage and exact subject are checked before approval.
func AdmitWorkloadPeer(workspaceRoot string, admission WorkloadPeerAdmission) error {
	return admitWorkloadPeer(workspaceRoot, admission, nil)
}

func admitWorkloadPeer(workspaceRoot string, admission WorkloadPeerAdmission, expectedState *string) error {
	now := time.Now().UTC()
	if !validWorkloadPeerRef(admission.PeerRef) || !validWorkloadPeerRef(admission.ServiceRef) || !validWorkloadPeerRef(admission.EdgeSiteRef) || strings.TrimSpace(admission.Audience) == "" || len(admission.Audience) > 512 {
		return errors.New("localevidence: workload peer requires exact identity and publication scope")
	}
	chain, err := workloadPeerCertificates([]byte(admission.CertificatePEM))
	if err != nil {
		return err
	}
	if err := verifyWorkloadPeerCertificate(workspaceRoot, chain, admission.PeerRef, now); err != nil {
		return err
	}
	if !admission.ValidUntil.After(now) || admission.ValidUntil.After(chain[0].NotAfter) {
		return errors.New("localevidence: workload peer approval must expire within its certificate lifetime")
	}
	return mutateWorkloadPeer(workspaceRoot, admission.PeerRef, expectedState, func(previous *workloadPeerRecord) (workloadPeerRecord, error) {
		record := workloadPeerRecord{Admission: admission}
		if previous != nil {
			record.RetiredCertificates = append([]string(nil), previous.RetiredCertificates...)
			record.RetiredCertificateExpiry = make(map[string]time.Time)
			for fingerprint, expiry := range previous.RetiredCertificateExpiry {
				if expiry.After(now) {
					record.RetiredCertificateExpiry[fingerprint] = expiry
				}
			}
			record.RevokedKeys = append([]string(nil), previous.RevokedKeys...)
			old, err := workloadPeerCertificates([]byte(previous.Admission.CertificatePEM))
			if err != nil {
				return record, err
			}
			fingerprint := workloadPeerFingerprint(chain[0])
			if previous.Revoked && !slices.Contains(record.RevokedKeys, workloadPeerKeyFingerprint(old[0])) {
				record.RevokedKeys = append(record.RevokedKeys, workloadPeerKeyFingerprint(old[0]))
			}
			if slices.Contains(record.RevokedKeys, workloadPeerKeyFingerprint(chain[0])) || slices.Contains(record.RetiredCertificates, fingerprint) || record.RetiredCertificateExpiry[fingerprint].After(now) {
				return record, errors.New("localevidence: a revoked certificate cannot be readmitted")
			}
			if !bytes.Equal(old[0].Raw, chain[0].Raw) && old[0].NotAfter.After(now) {
				record.RetiredCertificateExpiry[workloadPeerFingerprint(old[0])] = old[0].NotAfter
			}
		}
		return record, nil
	})
}

func workloadPeerFingerprint(cert *x509.Certificate) string {
	digest := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(digest[:])
}

func workloadPeerKeyFingerprint(cert *x509.Certificate) string {
	digest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(digest[:])
}

// RevokeWorkloadPeer persists an owner-signed denial. Origin authorization reads
// this file for every request, including requests on established TLS sessions.
func RevokeWorkloadPeer(workspaceRoot, peerRef string) error {
	return revokeWorkloadPeer(workspaceRoot, peerRef, nil)
}

func revokeWorkloadPeer(workspaceRoot, peerRef string, expectedState *string) error {
	return mutateWorkloadPeer(workspaceRoot, peerRef, expectedState, func(previous *workloadPeerRecord) (workloadPeerRecord, error) {
		if previous == nil {
			return workloadPeerRecord{}, errors.New("localevidence: workload peer is not admitted")
		}
		previous.Revoked = true
		chain, err := workloadPeerCertificates([]byte(previous.Admission.CertificatePEM))
		if err != nil {
			return workloadPeerRecord{}, err
		}
		fingerprint := workloadPeerKeyFingerprint(chain[0])
		if !slices.Contains(previous.RevokedKeys, fingerprint) {
			previous.RevokedKeys = append(previous.RevokedKeys, fingerprint)
		}
		return *previous, nil
	})
}

// AuthorizeWorkloadPeer consumes certificates from a real TLS connection, not a
// forwarded header. The caller must obtain proof of private-key possession via
// TLS client authentication. No cached admission or revocation snapshot is used.
func AuthorizeWorkloadPeer(workspaceRoot string, chain []*x509.Certificate, scope WorkloadPeerScope, now time.Time) (string, error) {
	if len(chain) == 0 || now.IsZero() {
		return "", errors.New("localevidence: workload peer TLS proof is absent")
	}
	peerRef := chain[0].Subject.CommonName
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return "", err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return "", err
	}
	defer tx.Close()
	record, err := readWorkloadPeer(workspaceRoot, tx, peerRef)
	if err != nil {
		return "", err
	}
	if record.Revoked || !record.Admission.ValidUntil.After(now) || record.Admission.ServiceRef != scope.ServiceRef || record.Admission.EdgeSiteRef != scope.EdgeSiteRef || record.Admission.Audience != scope.Audience {
		return "", errors.New("localevidence: workload peer is revoked, expired or outside its approved publication")
	}
	admitted, err := workloadPeerCertificates([]byte(record.Admission.CertificatePEM))
	if err != nil {
		return "", err
	}
	if !bytes.Equal(admitted[0].Raw, chain[0].Raw) {
		return "", errors.New("localevidence: workload peer certificate differs from owner approval")
	}
	if err := verifyWorkloadPeerCertificate(workspaceRoot, chain, peerRef, now); err != nil {
		return "", err
	}
	return peerRef, nil
}

func validWorkloadPeerRef(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:/", r)) {
			return false
		}
	}
	return true
}

// ValidWorkloadPeerRef is shared by CSR production and Home admission so an
// unusable identity never causes private-key custody to be created.
func ValidWorkloadPeerRef(value string) bool { return validWorkloadPeerRef(value) }

func workloadPeerPath(peerRef string) (string, error) {
	if !validWorkloadPeerRef(peerRef) {
		return "", errors.New("localevidence: invalid workload peer reference")
	}
	digest := sha256.Sum256([]byte(peerRef))
	return workloadPeerDirectory + "/" + hex.EncodeToString(digest[:]) + ".json", nil
}

func workloadPeerCertificates(raw []byte) ([]*x509.Certificate, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return nil, errors.New("localevidence: invalid workload peer certificate size")
	}
	var chain []*x509.Certificate
	for len(bytes.TrimSpace(raw)) > 0 {
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, errors.New("localevidence: malformed workload peer certificate chain")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		chain = append(chain, certificate)
		raw = rest
	}
	return chain, nil
}

func verifyWorkloadPeerCertificate(workspaceRoot string, chain []*x509.Certificate, peerRef string, now time.Time) error {
	if len(chain) == 0 {
		return errors.New("localevidence: workload peer certificate is absent")
	}
	leaf := chain[0]
	if leaf.IsCA || leaf.Subject.CommonName != peerRef || len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || len(leaf.UnknownExtKeyUsage) > 0 || leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		return errors.New("localevidence: workload peer certificate must be exact client-only identity")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(owner.StepCARootCertificatePEM)) {
		return errors.New("localevidence: owner trust root is unavailable")
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range chain[1:] {
		intermediates.AddCert(certificate)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return errors.New("localevidence: workload peer does not chain to current Home owner")
	}
	return nil
}

func readWorkloadPeer(workspaceRoot string, tx *confinedfs.Transaction, peerRef string) (workloadPeerRecord, error) {
	var record workloadPeerRecord
	path, err := workloadPeerPath(peerRef)
	if err != nil {
		return record, err
	}
	raw, _, err := tx.ReadStableBounded(path, 128<<10)
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, err
	}
	signature := record.Signature
	record.Signature = OwnerPolicyStateSignature{}
	canonical, err := json.Marshal(record)
	record.Signature = signature
	if err != nil {
		return record, err
	}
	if err := verifyOwnerRestore(workspaceRoot, canonical, workloadPeerSignatureDomain, signature.OwnerRef, signature.KeyID, signature.Value, "workload peer admission"); err != nil {
		return record, err
	}
	if record.Admission.PeerRef != peerRef {
		return record, errors.New("localevidence: workload peer record identity mismatch")
	}
	return record, nil
}

func mutateWorkloadPeer(workspaceRoot, peerRef string, expectedState *string, change func(*workloadPeerRecord) (workloadPeerRecord, error)) error {
	path, err := workloadPeerPath(peerRef)
	if err != nil {
		return err
	}
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
	lock, err := tx.TryAcquireOutputLock(workloadPeerDirectory)
	if err != nil {
		return err
	}
	defer lock.Release()
	old, err := readWorkloadPeer(workspaceRoot, tx, peerRef)
	var previous *workloadPeerRecord
	if err == nil {
		previous = &old
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if expectedState != nil {
		actual, err := workloadPeerCommitment(previous)
		if err != nil {
			return err
		}
		if actual != *expectedState {
			return errors.New("localevidence: owner approval refers to superseded peer state")
		}
	}
	record, err := change(previous)
	if err != nil {
		return err
	}
	record.Revision = 1
	if previous != nil {
		if previous.Revision == ^uint64(0) {
			return errors.New("localevidence: workload peer revision exhausted")
		}
		record.Revision = previous.Revision + 1
	}
	record.Signature = OwnerPolicyStateSignature{}
	canonical, err := json.Marshal(record)
	if err != nil {
		return err
	}
	value, ownerRef, keyID, err := signOwnerRestore(workspaceRoot, canonical, workloadPeerSignatureDomain, "workload peer admission")
	if err != nil {
		return err
	}
	record.Signature = OwnerPolicyStateSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	limit := 120 << 10 // Preserve space for a subsequent irrevocable key denial.
	if record.Revoked {
		limit = 128 << 10
	}
	if len(encoded) > limit {
		return errors.New("localevidence: workload peer custody exceeds bounded readback size")
	}
	absolute, err := confinedCustodyPath(workspaceRoot, path)
	if err != nil {
		return err
	}
	if err := writePrivateJSON(absolute, record); err != nil {
		return fmt.Errorf("persist workload peer approval: %w", err)
	}
	_, err = tx.SyncDirectory(workloadPeerDirectory)
	return err
}

func workloadPeerCommitment(record *workloadPeerRecord) (string, error) {
	if record == nil {
		return "", nil
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func currentWorkloadPeerCommitment(workspaceRoot, peerRef string) (string, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return "", err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return "", err
	}
	defer tx.Close()
	record, err := readWorkloadPeer(workspaceRoot, tx, peerRef)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return workloadPeerCommitment(&record)
}
