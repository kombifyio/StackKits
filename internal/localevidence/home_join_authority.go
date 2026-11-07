package localevidence

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/kombifyio/stackkits/contracts/applyevidence"
)

// A further server joins the homelab's one Authority (homelab identity target
// §4, §14.1). The Authority's Owner custody signs the join: its step-ca root
// issues the joining server's intermediate CA and its Owner key signs the
// trust bundle the joining server installs. The joining server verifies both
// against the root whose fingerprint the Owner confirms there; the Owner
// certificate the root issued at setup binds the Owner key to that root.
const (
	homeTrustBundleDomain = "stackkit.homelab-trust-bundle/v1\x00"

	// HomeJoinIntermediateLifetime is the lifetime of a further server's
	// intermediate CA.
	HomeJoinIntermediateLifetime = 90 * 24 * time.Hour
	homeJoinIntermediateBackdate = 5 * time.Minute
)

// HomeJoinIssuerMaterial is the public half of the Owner custody a trust
// bundle carries: the root and the Owner certificate it issued.
type HomeJoinIssuerMaterial struct {
	OwnerRef            string
	KeyID               string
	SiteRef             string
	RootCertificatePEM  string
	OwnerCertificatePEM string
}

// LoadHomeJoinIssuerMaterial reads the root and Owner certificate of this
// workspace's Owner custody (the Authority's host only).
func LoadHomeJoinIssuerMaterial(workspaceRoot string) (HomeJoinIssuerMaterial, error) {
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return HomeJoinIssuerMaterial{}, err
	}
	return HomeJoinIssuerMaterial{
		OwnerRef: owner.OwnerRef, KeyID: owner.KeyID, SiteRef: owner.Binding.SiteRef,
		RootCertificatePEM: owner.StepCARootCertificatePEM, OwnerCertificatePEM: owner.OwnerCertificatePEM,
	}, nil
}

// SignHomeTrustBundle signs a trust bundle's canonical bytes with the Owner
// key of this workspace (the Authority's host only).
func SignHomeTrustBundle(workspaceRoot string, canonical []byte) (OwnerPolicyStateSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(workspaceRoot, canonical, homeTrustBundleDomain, "homelab trust bundle")
	return OwnerPolicyStateSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, err
}

// ErrHomeTrustBundleRefused is the one answer to a trust bundle that is not
// signed by the Owner of the confirmed root.
var ErrHomeTrustBundleRefused = errors.New("localevidence: the homelab trust bundle is not signed by the Owner of this root")

// ParseHomeJoinRoot parses a self-signed root CA certificate.
func ParseHomeJoinRoot(rootPEM string) (*x509.Certificate, error) {
	root, err := parseCertificatePEM(rootPEM, "homelab root")
	if err != nil || !root.IsCA || !root.BasicConstraintsValid || root.CheckSignatureFrom(root) != nil {
		return nil, ErrHomeTrustBundleRefused
	}
	return root, nil
}

// VerifyHomeTrustBundle verifies a trust bundle's Owner signature on a
// server that holds no custody of that Owner: the Owner certificate must be
// issued by root (the root whose fingerprint the Owner confirmed), be current,
// name ownerRef, carry the Ed25519 key of keyID and have made the signature.
func VerifyHomeTrustBundle(root *x509.Certificate, ownerCertificatePEM, ownerRef string, canonical []byte, signature OwnerPolicyStateSignature, now time.Time) error {
	if root == nil || !root.IsCA || root.CheckSignatureFrom(root) != nil {
		return ErrHomeTrustBundleRefused
	}
	owner, err := parseCertificatePEM(ownerCertificatePEM, "owner")
	if err != nil || owner.CheckSignatureFrom(root) != nil || owner.IsCA || owner.Subject.CommonName != ownerRef ||
		now.Before(owner.NotBefore) || !now.Before(owner.NotAfter) {
		return ErrHomeTrustBundleRefused
	}
	public, ok := owner.PublicKey.(ed25519.PublicKey)
	if !ok || len(public) != ed25519.PublicKeySize {
		return ErrHomeTrustBundleRefused
	}
	if !verifyPinnedOwnerSignature(homeTrustBundleDomain, canonical, signature, ownerRef, applyevidence.ProducerKeyID(public), public) {
		return ErrHomeTrustBundleRefused
	}
	return nil
}

// HomeJoinIntermediateRequest is what the Authority issues an intermediate
// CA for: the joining server's public key and the DNS subtrees it may name.
type HomeJoinIntermediateRequest struct {
	CommonName string
	PublicKey  crypto.PublicKey
	// Permitted are the server's hostnames, each a DNS subtree.
	Permitted []string
	// Excluded are the identity names only the Authority serves.
	Excluded []string
}

// IssueHomeJoinIntermediate signs a further server's intermediate CA with
// the step-ca root key of this workspace (the Authority's host only): a P-256
// key, the permitted DNS subtrees of the server's hostnames, every IP range
// and the identity names excluded, path length 0, 90 days. It returns DER.
func IssueHomeJoinIntermediate(workspaceRoot string, request HomeJoinIntermediateRequest, now time.Time) ([]byte, error) {
	public, ok := request.PublicKey.(*ecdsa.PublicKey)
	if !ok || public.Curve != elliptic.P256() || len(request.Permitted) == 0 || len(request.Excluded) == 0 || request.CommonName == "" {
		return nil, errors.New("localevidence: a join intermediate needs a P-256 key, permitted hostnames and excluded identity names")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return nil, err
	}
	root, err := parseCertificatePEM(owner.StepCARootCertificatePEM, "step-ca root")
	if err != nil {
		return nil, err
	}
	rootKey, err := loadStepCARootKey(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("localevidence: load the step-ca root key for the join intermediate: %w", err)
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, err
	}
	notBefore := now.UTC().Add(-homeJoinIntermediateBackdate).Truncate(time.Second)
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: request.CommonName},
		NotBefore: notBefore, NotAfter: notBefore.Add(homeJoinIntermediateBackdate + HomeJoinIntermediateLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true, MaxPathLen: 0, MaxPathLenZero: true,
		// Server authentication only: no client certificate chains to it, so a
		// certificate without DNS names cannot pass as a workload peer.
		ExtKeyUsage:                 []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         append([]string(nil), request.Permitted...),
		ExcludedDNSDomains:          append([]string(nil), request.Excluded...),
		ExcludedIPRanges:            HomeJoinExcludedIPRanges(),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, root, public, rootKey)
	if err != nil {
		return nil, fmt.Errorf("localevidence: issue the join intermediate: %w", err)
	}
	return der, nil
}

// HomeJoinExcludedIPRanges is every IPv4 and IPv6 address: a further server's
// intermediate never names an address.
func HomeJoinExcludedIPRanges() []*net.IPNet {
	return []*net.IPNet{
		{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)},
		{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)},
	}
}

// HomeJoinRootFingerprint is the lowercase hex SHA-256 of a root certificate
// (DER): the value the device enrollment bootstrap pins and the Owner
// confirms on a joining server.
func HomeJoinRootFingerprint(root *x509.Certificate) string {
	sum := sha256.Sum256(root.Raw)
	return hex.EncodeToString(sum[:])
}
