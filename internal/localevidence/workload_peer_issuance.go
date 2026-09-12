package localevidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"slices"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const basementPeerProvisionerName = "stackkits-owner-workload-peer"

func basementWorkloadProvisioners(root string) ([]basementStepCAProvisioner, error) {
	origin, err := basementOriginProvisioner(root)
	if err != nil {
		return nil, err
	}
	peer, err := basementWorkloadProvisioner(root, basementPeerProvisionerName, "clientAuth")
	if err != nil {
		return nil, err
	}
	return []basementStepCAProvisioner{origin, peer}, nil
}

func requireBasementWorkloadProvisioners(root string) error {
	if err := requireBasementOriginProvisioner(root); err != nil {
		return err
	}
	return requireBasementWorkloadProvisioner(root, basementPeerProvisionerName, x509.ExtKeyUsageClientAuth)
}

// IssueWorkloadPeerCertificate is a local Owner capability, never an HTTP
// signing endpoint. The caller must obtain explicit approval for the peer and
// scope. The existing step-ca alone signs the certificate; the caller must admit
// that exact certificate separately before it can access an origin.
func IssueWorkloadPeerCertificate(ctx context.Context, root, peerRef string, csrPEM []byte, ttl int) ([]byte, error) {
	if ttl < 300 || ttl > 86400 {
		return nil, errors.New("localevidence: invalid bounded workload peer issuance")
	}
	csr, err := validateWorkloadPeerCSR(peerRef, csrPEM)
	if err != nil {
		return nil, err
	}
	return issueBasementWorkloadCertificate(ctx, root, csr, csrPEM, ttl, basementPeerProvisionerName, x509.ExtKeyUsageClientAuth, nil)
}

func validateWorkloadPeerCSR(peerRef string, csrPEM []byte) (*x509.CertificateRequest, error) {
	if !validWorkloadPeerRef(peerRef) || len(csrPEM) > 64<<10 {
		return nil, errors.New("localevidence: invalid workload peer CSR identity")
	}
	block, rest := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("localevidence: one workload peer CSR required")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != peerRef || len(csr.DNSNames)+len(csr.IPAddresses)+len(csr.URIs)+len(csr.EmailAddresses) != 0 {
		return nil, errors.New("localevidence: CSR does not prove exact workload peer identity")
	}
	return csr, nil
}

// PrepareWorkloadPeerEnrollment checks explicit key replacement and permanent
// revoked-key denial before requesting a certificate from step-ca. The returned
// commitment must still match the signed admission after issuance.
func PrepareWorkloadPeerEnrollment(root, peerRef string, csrPEM []byte, replaceKey bool) (string, error) {
	csr, err := validateWorkloadPeerCSR(peerRef, csrPEM)
	if err != nil {
		return "", err
	}
	fs, err := confinedfs.Open(root)
	if err != nil {
		return "", err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return "", err
	}
	defer tx.Close()
	record, err := readWorkloadPeer(root, tx, peerRef)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	chain, err := workloadPeerCertificates([]byte(record.Admission.CertificatePEM))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(csr.RawSubjectPublicKeyInfo)
	if slices.Contains(record.RevokedKeys, hex.EncodeToString(digest[:])) || record.Revoked && bytes.Equal(chain[0].RawSubjectPublicKeyInfo, csr.RawSubjectPublicKeyInfo) {
		return "", errors.New("localevidence: revoked workload key requires a new Cloud-held key")
	}
	if (record.Revoked || !bytes.Equal(chain[0].RawSubjectPublicKeyInfo, csr.RawSubjectPublicKeyInfo)) && !replaceKey {
		return "", errors.New("localevidence: existing workload identity requires explicit key replacement")
	}
	return workloadPeerCommitment(&record)
}
