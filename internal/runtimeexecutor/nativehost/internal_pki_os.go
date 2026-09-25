package nativehost

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"slices"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// osInternalPKIOperations observes the existing Basement step-ca/Traefik owner.
// It never creates a CA or leaf private key, and never claims remote trust.
type osInternalPKIOperations struct {
	workspaceRoot string
	issuanceWait  time.Duration
	retryInterval time.Duration
}

func NewOSInternalPKIOperations(workspaceRoot string) (*osInternalPKIOperations, error) {
	root, err := ownerWorkspaceRoot(workspaceRoot, "local Basement internal PKI")
	if err != nil {
		return nil, err
	}
	return &osInternalPKIOperations{workspaceRoot: root, issuanceWait: certificateIssuanceWait, retryInterval: certificateIssuanceInterval}, nil
}

func (o *osInternalPKIOperations) root(ctx context.Context, policy InternalPKIPolicy) (*x509.Certificate, error) {
	if ctx == nil {
		return nil, errors.New("internal PKI requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if policy.Authority.SiteRef != policy.SiteRef || policy.Authority.NodeRef != policy.NodeRef {
		return nil, errors.New("internal PKI authority is not this local node")
	}
	for _, target := range policy.TrustTargets {
		if target.SiteRef != policy.SiteRef || target.NodeRef != policy.NodeRef {
			return nil, errors.New("internal PKI remote trust distribution requires its remote owner")
		}
	}
	// Loading verifies the signed index and every mounted runtime custody file,
	// including the exact public root already distributed to this node.
	if _, err := localevidence.LoadBasementRuntimeCustody(o.workspaceRoot); err != nil {
		return nil, err
	}
	owner, err := localevidence.LoadOwnerCustody(o.workspaceRoot)
	if err != nil {
		return nil, err
	}
	if owner.Binding.SiteRef != policy.SiteRef || owner.Binding.NodeRef != policy.NodeRef || owner.Binding.ChannelRef != policy.ExecutionChannelRef {
		return nil, errors.New("internal PKI owner custody differs from the executing target")
	}
	block, _ := pem.Decode([]byte(owner.StepCARootCertificatePEM))
	if block == nil {
		return nil, errors.New("internal PKI owner root is unavailable")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if _, ok := cert.PublicKey.(ed25519.PublicKey); !ok || cert.MaxPathLenZero || cert.MaxPathLen != -1 {
		return nil, errors.New("internal PKI root differs from existing owner CA constraints")
	}
	if !cert.IsCA || cert.CheckSignatureFrom(cert) != nil {
		return nil, errors.New("internal PKI owner root is not a self-signed CA")
	}
	return cert, nil
}

func internalPKIFingerprint(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (o *osInternalPKIOperations) EnsureRootAuthority(ctx context.Context, policy InternalPKIPolicy) (InternalPKIRootObservation, error) {
	root, err := o.root(ctx, policy)
	if err != nil {
		return InternalPKIRootObservation{}, err
	}
	fingerprint := internalPKIFingerprint(root.Raw)
	return InternalPKIRootObservation{PolicyDigest: policy.PolicyDigest, Status: "ready", RootFingerprint: fingerprint,
		PublicKeyFingerprint: internalPKIFingerprint(root.RawSubjectPublicKeyInfo), Serial: root.SerialNumber.String(),
		NotBefore: root.NotBefore.UTC().Format(time.RFC3339Nano), NotAfter: root.NotAfter.UTC().Format(time.RFC3339Nano),
		ObservedAt: policy.EvaluatedAt, TrustedFingerprints: []string{fingerprint}, ContinuityValidUntil: root.NotAfter.UTC().Format(time.RFC3339Nano)}, nil
}

func (o *osInternalPKIOperations) IssueCompilerBoundLeaves(ctx context.Context, policy InternalPKIPolicy, fingerprint string) (InternalPKILeafSetObservation, error) {
	leaves, err := o.observeLeaves(ctx, policy, fingerprint)
	if err != nil {
		return InternalPKILeafSetObservation{}, err
	}
	return InternalPKILeafSetObservation{PolicyDigest: policy.PolicyDigest, Status: "issued", Leaves: leaves}, nil
}

func (o *osInternalPKIOperations) observeLeaves(ctx context.Context, policy InternalPKIPolicy, fingerprint string) ([]InternalPKILeafObservation, error) {
	root, err := o.root(ctx, policy)
	if err != nil {
		return nil, err
	}
	if internalPKIFingerprint(root.Raw) != fingerprint {
		return nil, errors.New("internal PKI root changed")
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	leaves := make([]InternalPKILeafObservation, 0, len(policy.LeafIdentities))
	for _, identity := range policy.LeafIdentities {
		if identity.SiteRef != policy.SiteRef || identity.NodeRef != policy.NodeRef || len(identity.DNSSANs) == 0 {
			return nil, errors.New("internal PKI leaf is outside the local DNS ingress owner")
		}
		// Traefik's existing step-ca ACME resolver owns issuance and renewal. The
		// loopback dial prevents DNS from redirecting this local authority probe;
		// regular TLS verification still binds the exact SNI and custodied root.
		var cert *x509.Certificate
		err := waitForCertificateIssuance(ctx, o.issuanceWait, o.retryInterval, func() error {
			probed, probeErr := probeInternalPKICertificate(ctx, roots, identity, "127.0.0.1:443")
			if probeErr != nil {
				return probeErr
			}
			cert = probed
			return nil
		})
		if err != nil {
			return nil, err
		}
		dns := append([]string(nil), cert.DNSNames...)
		slices.Sort(dns)
		ips := make([]string, 0, len(cert.IPAddresses))
		for _, ip := range cert.IPAddresses {
			ips = append(ips, ip.String())
		}
		slices.Sort(ips)
		leaves = append(leaves, InternalPKILeafObservation{IdentityID: identity.ID, SubjectRef: identity.SubjectRef, DNSSANs: dns, IPSANs: ips,
			CertificateFingerprint: internalPKIFingerprint(cert.Raw), PublicKeyFingerprint: internalPKIFingerprint(cert.RawSubjectPublicKeyInfo), TrustRootFingerprint: fingerprint,
			Serial: cert.SerialNumber.String(), NotBefore: cert.NotBefore.UTC().Format(time.RFC3339Nano), NotAfter: cert.NotAfter.UTC().Format(time.RFC3339Nano), ObservedAt: policy.EvaluatedAt})
	}
	return leaves, nil
}

func (o *osInternalPKIOperations) DistributePublicTrustRoot(ctx context.Context, policy InternalPKIPolicy, fingerprint string) (InternalPKITrustObservation, error) {
	root, err := o.root(ctx, policy)
	if err != nil {
		return InternalPKITrustObservation{}, err
	}
	if internalPKIFingerprint(root.Raw) != fingerprint {
		return InternalPKITrustObservation{}, errors.New("internal PKI distributed root differs from authority")
	}
	// A successful regular TLS handshake proves actual product consumption of
	// this root; this is not an operating-system or external-client trust claim.
	if _, err := o.observeLeaves(ctx, policy, fingerprint); err != nil {
		return InternalPKITrustObservation{}, err
	}
	return InternalPKITrustObservation{PolicyDigest: policy.PolicyDigest, Status: "distributed", RootFingerprint: fingerprint,
		Targets: append([]architecturev2renderer.InternalPKIRuntimeTrustTarget(nil), policy.TrustTargets...), ObservedAt: policy.EvaluatedAt, ValidUntil: root.NotAfter.UTC().Format(time.RFC3339Nano)}, nil
}

func (o *osInternalPKIOperations) VerifyInternalPKI(ctx context.Context, policy InternalPKIPolicy, fingerprint string) (InternalPKIVerifyObservation, error) {
	leaves, err := o.observeLeaves(ctx, policy, fingerprint)
	if err != nil {
		return InternalPKIVerifyObservation{}, err
	}
	return InternalPKIVerifyObservation{PolicyDigest: policy.PolicyDigest, Status: "ready", RootFingerprint: fingerprint,
		Leaves: leaves, Targets: append([]architecturev2renderer.InternalPKIRuntimeTrustTarget(nil), policy.TrustTargets...), ObservedAt: policy.EvaluatedAt}, nil
}

func internalPKISecureLeafKey(key any) bool {
	switch public := key.(type) {
	case *rsa.PublicKey:
		return public.N.BitLen() >= 2048
	case *ecdsa.PublicKey:
		return public.Curve.Params().BitSize >= 256
	case ed25519.PublicKey:
		return len(public) == ed25519.PublicKeySize
	default:
		return false
	}
}

// Only the native owner chooses address; tests use a real isolated TLS socket.
func probeInternalPKICertificate(ctx context.Context, roots *x509.CertPool, identity architecturev2renderer.InternalPKIRuntimeLeafIdentity, address string) (*x509.Certificate, error) {
	if len(identity.DNSSANs) == 0 {
		return nil, errors.New("internal PKI ingress requires compiler DNS SANs")
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 30 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: identity.DNSSANs[0]}}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("observe compiler-bound internal PKI ingress: %w", err)
	}
	state := connection.(*tls.Conn).ConnectionState()
	_ = connection.Close()
	if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return nil, errors.New("internal PKI ingress has no verified certificate chain")
	}
	cert := state.PeerCertificates[0]
	if !internalPKISecureLeafKey(cert.PublicKey) {
		return nil, errors.New("internal PKI ingress public key does not meet the owner security policy")
	}
	dns := append([]string(nil), cert.DNSNames...)
	slices.Sort(dns)
	ips := make([]string, 0, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		ips = append(ips, ip.String())
	}
	slices.Sort(ips)
	if cert.IsCA || !slices.Equal(dns, identity.DNSSANs) || !slices.Equal(ips, identity.IPSANs) {
		return nil, errors.New("internal PKI ingress certificate differs from compiler SANs")
	}

	return cert, nil
}
