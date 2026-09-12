package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localorigin"
)

type osBridgeOriginMTLSOperations struct{ root string }

func NewOSBridgeOriginMTLSOperations(root string) BridgeOriginMTLSOperations {
	return &osBridgeOriginMTLSOperations{root: root}
}

func (o *osBridgeOriginMTLSOperations) checkTarget(site, node, channel string) error {
	owner, err := localevidence.LoadOwnerCustody(o.root)
	if err != nil {
		return err
	}
	if owner.Binding.SiteRef != site || owner.Binding.NodeRef != node || owner.Binding.ChannelRef != channel {
		return errors.New("origin mTLS: target differs from current local custody")
	}
	return nil
}

func (o *osBridgeOriginMTLSOperations) BindOriginMTLS(ctx context.Context, p BridgeOriginMTLSApplyPolicy) (BridgeOriginMTLSObservation, error) {
	if err := o.checkTarget(p.SiteRef, p.NodeRef, p.ExecutionChannelRef); err != nil {
		return BridgeOriginMTLSObservation{}, err
	}
	// Withdraw omitted routes before issuance or readiness can fail. A failed
	// replacement must not keep a previously authorized publication serving.
	names := make([]string, 0, len(p.Publications))
	for _, publication := range p.Publications {
		names = append(names, publication.ServerName)
	}
	if err := localorigin.RemoveObsolete(o.root, names); err != nil {
		return BridgeOriginMTLSObservation{}, err
	}
	for _, publication := range p.Publications {
		if err := localorigin.Bind(ctx, o.root, publication); err != nil {
			return BridgeOriginMTLSObservation{}, err
		}
	}
	return o.observe(ctx, BridgeOriginMTLSExpectation(p), "bound")
}

func (o *osBridgeOriginMTLSOperations) RemoveObsoleteOriginMTLS(ctx context.Context, p BridgeOriginMTLSExpectation) (BridgeOriginMTLSObservation, error) {
	if err := o.checkTarget(p.SiteRef, p.NodeRef, p.ExecutionChannelRef); err != nil {
		return BridgeOriginMTLSObservation{}, err
	}
	names := make([]string, 0, len(p.Publications))
	for _, publication := range p.Publications {
		names = append(names, publication.ServerName)
	}
	if err := localorigin.RemoveObsolete(o.root, names); err != nil {
		return BridgeOriginMTLSObservation{}, err
	}
	return o.observe(ctx, p, "obsolete-removed")
}

func (o *osBridgeOriginMTLSOperations) VerifyOriginMTLS(ctx context.Context, p BridgeOriginMTLSExpectation) (BridgeOriginMTLSObservation, error) {
	return o.observe(ctx, p, "ready")
}

func (o *osBridgeOriginMTLSOperations) observe(ctx context.Context, p BridgeOriginMTLSExpectation, status string) (BridgeOriginMTLSObservation, error) {
	result := BridgeOriginMTLSObservation{PolicyDigest: p.PolicyDigest, Status: status, EvaluatedAt: p.EvaluatedAt}
	if err := o.checkTarget(p.SiteRef, p.NodeRef, p.ExecutionChannelRef); err != nil {
		return result, err
	}
	for _, wanted := range p.Publications {
		proof, err := localorigin.Observe(ctx, o.root, wanted.ServerName)
		if err != nil {
			return result, err
		}
		if proof.Policy != wanted {
			return result, errors.New("origin mTLS: live configuration differs from request")
		}
		block, _ := pem.Decode([]byte(proof.CertificatePEM))
		if block == nil {
			return result, errors.New("origin mTLS: live leaf absent")
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return result, err
		}
		if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
			return result, errors.New("origin mTLS: live leaf usage is not server-only")
		}
		certDigest, keyDigest := sha256.Sum256(leaf.Raw), sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		actual := proof.Policy
		result.Materials = append(result.Materials, BridgeOriginMTLSMaterialObservation{
			ServiceRef: actual.ServiceRef, IdentityRef: actual.IdentityRef, ModuleRef: actual.ModuleRef, UnitRef: actual.UnitRef, OriginInstanceRef: actual.OriginInstanceRef,
			UpstreamProtocol: actual.UpstreamProtocol, TargetPort: actual.TargetPort, ServerName: actual.ServerName, MinimumTLSVersion: actual.MinimumTLSVersion,
			MutualTLSRequired: proof.ClientCertificateRequired, ClientCertificateRequired: proof.ClientCertificateRequired, OutboundOnly: proof.LoopbackOnly, GeneralLANAccess: !proof.LoopbackOnly,
			CredentialIssuerRef: actual.CredentialIssuerRef, Issuer: actual.Issuer, Audience: actual.Audience, VerificationKeySetRef: actual.VerificationKeySetRef, EdgeVerifierRef: actual.EdgeVerifierRef, VerifierDistributionRef: actual.VerifierDistributionRef,
			CertificateSubjectRef: leaf.Subject.CommonName, CertificateSANs: leaf.DNSNames, CertificateExtendedKeyUsages: []string{"server-auth"}, CertificateCA: leaf.IsCA, CertificateChainVerified: true,
			CertificateFingerprint: "sha256:" + hex.EncodeToString(certDigest[:]), PublicKeyFingerprint: "sha256:" + hex.EncodeToString(keyDigest[:]), Serial: leaf.SerialNumber.String(), NotBefore: leaf.NotBefore.UTC().Format(time.RFC3339Nano), NotAfter: leaf.NotAfter.UTC().Format(time.RFC3339Nano),
			ConfigurationObservedAt: proof.ObservedAt.Format(time.RFC3339Nano), RevocationStateObservedAt: proof.ObservedAt.Format(time.RFC3339Nano),
		})
	}
	result.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return result, nil
}
