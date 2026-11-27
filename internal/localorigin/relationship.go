package localorigin

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// RenewPeer requests a short-lived certificate renewal for an eligible workload
// peer using its local CSR/key custody and installs the newly issued leaf only
// after verifying it against the pinned Home root fingerprint. An issuer or
// eligibility failure leaves any currently installed credential untouched.
func RenewPeer(ctx context.Context, root, serverName, peerRef, workloadID, nonce string, issuer localevidence.WorkloadCertificateIssuer, now time.Time) (PeerCredential, error) {
	var result PeerCredential
	if !localevidence.ValidWorkloadPeerRef(peerRef) {
		return result, ErrPeerSelection
	}
	p, _, err := loadPublication(root, serverName)
	if err != nil {
		return result, err
	}
	if p.Policy.ServerName != serverName {
		return result, errors.New("localorigin: publication server name mismatch")
	}
	request, err := RequestPeerWithRotation(root, peerRef, false)
	if err != nil {
		return result, err
	}
	return renewPeerWithRequest(ctx, root, p.Policy.ServerName, p.Policy.ServiceRef, p.Policy.EdgeSiteRef, p.Policy.Audience, request, workloadID, nonce, issuer, now)
}

// RecoverPeer stages a replacement Cloud-held key without mutating the active
// key custody, binds its public key in Owner custody (refusing permanently
// revoked keys), renews and verifies the certificate, and only commits the new
// key + credential once verification and installation succeed.
func RecoverPeer(ctx context.Context, root, serverName, peerRef, workloadID, nonce string, issuer localevidence.WorkloadCertificateIssuer, now time.Time) (PeerCredential, error) {
	if !localevidence.ValidWorkloadPeerRef(peerRef) {
		return PeerCredential{}, ErrPeerSelection
	}
	p, _, err := loadPublication(root, serverName)
	if err != nil {
		return PeerCredential{}, err
	}
	if p.Policy.ServerName != serverName {
		return PeerCredential{}, errors.New("localorigin: publication server name mismatch")
	}

	staged, err := stagePeerKeyRotation(root, peerRef)
	if err != nil {
		return PeerCredential{}, err
	}
	block, _ := pem.Decode([]byte(staged.Request.CSRPEM))
	if block == nil {
		return PeerCredential{}, errors.New("localorigin: staged peer CSR missing")
	}
	fingerprint, err := csrSubjectKeyFingerprint(block.Bytes)
	if err != nil {
		return PeerCredential{}, err
	}
	if _, err := localevidence.RecoverWorkloadRelationshipKey(root, peerRef, workloadID, fingerprint, now); err != nil {
		return PeerCredential{}, err
	}
	renewed, err := localevidence.RenewWorkloadRelationship(ctx, root, localevidence.WorkloadRenewalRequest{
		PeerRef:     peerRef,
		WorkloadID:  workloadID,
		ServiceRef:  p.Policy.ServiceRef,
		EdgeSiteRef: p.Policy.EdgeSiteRef,
		Audience:    p.Policy.Audience,
		ServerName:  p.Policy.ServerName,
		CSRPEM:      []byte(staged.Request.CSRPEM),
		Nonce:       nonce,
	}, issuer, now)
	if err != nil {
		return PeerCredential{}, err
	}
	if err := commitPeerKeyRotation(root, peerRef, staged); err != nil {
		return PeerCredential{}, err
	}
	return installRenewedPeerCredential(root, p.Policy.ServerName, peerRef, renewed.CertificatePEM)
}

func renewPeerWithRequest(ctx context.Context, root, serverName, serviceRef, edgeSiteRef, audience string, request PeerRequest, workloadID, nonce string, issuer localevidence.WorkloadCertificateIssuer, now time.Time) (PeerCredential, error) {
	renewed, err := localevidence.RenewWorkloadRelationship(ctx, root, localevidence.WorkloadRenewalRequest{
		PeerRef:     request.PeerRef,
		WorkloadID:  workloadID,
		ServiceRef:  serviceRef,
		EdgeSiteRef: edgeSiteRef,
		Audience:    audience,
		ServerName:  serverName,
		CSRPEM:      []byte(request.CSRPEM),
		Nonce:       nonce,
	}, issuer, now)
	if err != nil {
		return PeerCredential{}, err
	}
	return installRenewedPeerCredential(root, serverName, request.PeerRef, renewed.CertificatePEM)
}

func installRenewedPeerCredential(root, serverName, peerRef, certificatePEM string) (PeerCredential, error) {
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return PeerCredential{}, err
	}
	block, _ := pem.Decode([]byte(owner.StepCARootCertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return PeerCredential{}, errors.New("localorigin: owner root certificate missing")
	}
	rootSum := sha256.Sum256(block.Bytes)
	rootFingerprint := "sha256:" + hex.EncodeToString(rootSum[:])
	credential := PeerCredential{
		PeerRef:            peerRef,
		ServerName:         serverName,
		CertificatePEM:     certificatePEM,
		RootCertificatePEM: owner.StepCARootCertificatePEM,
	}
	if err := InstallPeer(root, credential, rootFingerprint); err != nil {
		return PeerCredential{}, err
	}
	if err := writeState(root, stateRef("publication-peer", serverName), "publication-peer", certificatePEM); err != nil {
		return PeerCredential{}, err
	}
	return credential, nil
}

func stagePeerKeyRotation(root, peerRef string) (peerKey, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return peerKey{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: peerRef}}, key)
	if err != nil {
		return peerKey{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return peerKey{}, err
	}
	staged := peerKey{
		Request:       PeerRequest{PeerRef: peerRef, CSRPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))},
		PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	}
	if err := writeState(root, stateRef("peer-key-staged", peerRef), "peer-key", staged); err != nil {
		return peerKey{}, err
	}
	return staged, nil
}

func commitPeerKeyRotation(root, peerRef string, staged peerKey) error {
	var existing peerKey
	path := stateRef("peer-key", peerRef)
	if err := readState(root, path, "peer-key", &existing); err == nil {
		if err := archivePeerKey(root, existing); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeState(root, path, "peer-key", staged)
}

func csrSubjectKeyFingerprint(csrDER []byte) (string, error) {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return "", err
	}
	if err := csr.CheckSignature(); err != nil {
		return "", err
	}
	sum := sha256.Sum256(csr.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]), nil
}

// ObserveRelationship returns the read-only Guard D qualification evidence for
// the named peer relationship from existing local authority custody.
func ObserveRelationship(root, peerRef, authorityVersion, guardVersion string, now time.Time) (localevidence.WorkloadRelationshipEvidence, error) {
	return localevidence.ObserveWorkloadRelationship(root, peerRef, authorityVersion, guardVersion, now)
}
