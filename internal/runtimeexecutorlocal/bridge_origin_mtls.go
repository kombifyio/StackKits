package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	bridgeOriginMTLSProviderRef      = "stackkits-service-publication-contract"
	bridgeOriginMTLSModuleRef        = "stackkits-bridge-origin-mtls-runtime"
	bridgeOriginMTLSUnitRef          = "executor-contract"
	bridgeOriginMTLSOutputRef        = "modern/federation/origin-mtls/executor-contract.json"
	bridgeOriginMTLSArtifactPrefix   = "bridge-origin-mtls-executor-contract-instance-"
	bridgeOriginMTLSHealthSourceRef  = "bridge-origin-mtls-health"
	bridgeOriginMTLSMaxArtifactBytes = 512 << 10
)

type BridgeOriginMTLSApplyPolicy struct {
	PolicyDigest        string                                                     `json:"policyDigest"`
	StackID             string                                                     `json:"stackId"`
	SiteRef             string                                                     `json:"siteRef"`
	NodeRef             string                                                     `json:"nodeRef"`
	ExecutionChannelRef string                                                     `json:"executionChannelRef"`
	EvaluatedAt         string                                                     `json:"evaluatedAt"`
	Publications        []architecturev2renderer.BridgeOriginMTLSPublicationPolicy `json:"publications"`
}

type BridgeOriginMTLSExpectation struct {
	PolicyDigest        string                                                     `json:"policyDigest"`
	StackID             string                                                     `json:"stackId"`
	SiteRef             string                                                     `json:"siteRef"`
	NodeRef             string                                                     `json:"nodeRef"`
	ExecutionChannelRef string                                                     `json:"executionChannelRef"`
	EvaluatedAt         string                                                     `json:"evaluatedAt"`
	Publications        []architecturev2renderer.BridgeOriginMTLSPublicationPolicy `json:"publications"`
}

type BridgeOriginMTLSMaterialObservation struct {
	ServiceRef                   string   `json:"serviceRef"`
	IdentityRef                  string   `json:"identityRef"`
	ModuleRef                    string   `json:"moduleRef"`
	UnitRef                      string   `json:"unitRef"`
	OriginInstanceRef            string   `json:"originInstanceRef"`
	UpstreamProtocol             string   `json:"upstreamProtocol"`
	TargetPort                   int      `json:"targetPort"`
	ServerName                   string   `json:"serverName"`
	MinimumTLSVersion            string   `json:"minimumTLSVersion"`
	MutualTLSRequired            bool     `json:"mutualTLSRequired"`
	ClientCertificateRequired    bool     `json:"clientCertificateRequired"`
	OutboundOnly                 bool     `json:"outboundOnly"`
	GeneralLANAccess             bool     `json:"generalLANAccess"`
	CredentialIssuerRef          string   `json:"credentialIssuerRef"`
	Issuer                       string   `json:"issuer"`
	Audience                     string   `json:"audience"`
	VerificationKeySetRef        string   `json:"verificationKeySetRef"`
	EdgeVerifierRef              string   `json:"edgeVerifierRef"`
	VerifierDistributionRef      string   `json:"verifierDistributionRef"`
	CertificateSubjectRef        string   `json:"certificateSubjectRef"`
	CertificateSANs              []string `json:"certificateSANs"`
	CertificateExtendedKeyUsages []string `json:"certificateExtendedKeyUsages"`
	CertificateCA                bool     `json:"certificateCA"`
	CertificateChainVerified     bool     `json:"certificateChainVerified"`
	CertificateFingerprint       string   `json:"certificateFingerprint"`
	PublicKeyFingerprint         string   `json:"publicKeyFingerprint"`
	Serial                       string   `json:"serial"`
	NotBefore                    string   `json:"notBefore"`
	NotAfter                     string   `json:"notAfter"`
	ConfigurationObservedAt      string   `json:"configurationObservedAt"`
	RevocationStateObservedAt    string   `json:"revocationStateObservedAt"`
}

// BridgeOriginMTLSObservation contains only bounded postcondition metadata.
// Certificate/private-key bytes, endpoints and credentials are forbidden.
type BridgeOriginMTLSObservation struct {
	PolicyDigest string                                `json:"policyDigest"`
	Status       string                                `json:"status"`
	EvaluatedAt  string                                `json:"evaluatedAt"`
	ObservedAt   string                                `json:"observedAt"`
	Materials    []BridgeOriginMTLSMaterialObservation `json:"materials"`
}

type BridgeOriginMTLSOperations interface {
	BindOriginMTLS(context.Context, BridgeOriginMTLSApplyPolicy) (BridgeOriginMTLSObservation, error)
	RemoveObsoleteOriginMTLS(context.Context, BridgeOriginMTLSExpectation) (BridgeOriginMTLSObservation, error)
	VerifyOriginMTLS(context.Context, BridgeOriginMTLSExpectation) (BridgeOriginMTLSObservation, error)
}

type BridgeOriginMTLSAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type BridgeOriginMTLSExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  BridgeOriginMTLSAuthority
	operations BridgeOriginMTLSOperations
	now        func() time.Time
}

func NewBridgeOriginMTLSExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority BridgeOriginMTLSAuthority, operations BridgeOriginMTLSOperations) *BridgeOriginMTLSExecutor {
	return NewBridgeOriginMTLSExecutorWithClock(identity, binding, authority, operations, time.Now)
}

func NewBridgeOriginMTLSExecutorWithClock(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority BridgeOriginMTLSAuthority, operations BridgeOriginMTLSOperations, now func() time.Time) *BridgeOriginMTLSExecutor {
	return &BridgeOriginMTLSExecutor{identity: identity, binding: binding, authority: authority, operations: operations, now: now}
}

func (e *BridgeOriginMTLSExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *BridgeOriginMTLSExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("origin mTLS executor requires a context")
	}
	if e == nil || e.operations == nil || e.now == nil ||
		strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("origin mTLS executor requires one explicit authenticated Home target binding")
	}
	evaluatedAt := e.now().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("origin mTLS executor clock returned zero time")
	}
	target, health, policy, expectation, err := validateBridgeOriginMTLSRequest(request, e.binding, e.authority, evaluatedAt)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	bound, err := e.operations.BindOriginMTLS(ctx, cloneBridgeOriginMTLSApplyPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("bind exact origin mTLS policy: %w", err)
	}
	bindCheckedAt, err := bridgeOriginMTLSCheckedAt(e.now, evaluatedAt)
	if err != nil || !validBridgeOriginMTLSObservation(bound, expectation, "bound", evaluatedAt, bindCheckedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("bind observation does not prove the exact origin mTLS policy")
	}
	removed, err := e.operations.RemoveObsoleteOriginMTLS(ctx, cloneBridgeOriginMTLSExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("remove obsolete origin mTLS policy: %w", err)
	}
	removeCheckedAt, err := bridgeOriginMTLSCheckedAt(e.now, bindCheckedAt)
	if err != nil || !validBridgeOriginMTLSObservation(removed, expectation, "obsolete-removed", evaluatedAt, removeCheckedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("removal observation does not prove exact origin mTLS reconciliation")
	}
	verified, err := e.operations.VerifyOriginMTLS(ctx, cloneBridgeOriginMTLSExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact origin mTLS policy: %w", err)
	}
	verifyCheckedAt, err := bridgeOriginMTLSCheckedAt(e.now, removeCheckedAt)
	if err != nil || !validBridgeOriginMTLSObservation(verified, expectation, "ready", evaluatedAt, verifyCheckedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("verification observation does not prove fresh origin mTLS termination")
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                      `json:"schemaVersion"`
		Bind          BridgeOriginMTLSObservation `json:"bind"`
		Remove        BridgeOriginMTLSObservation `json:"remove"`
		Verify        BridgeOriginMTLSObservation `json:"verify"`
	}{"stackkit.bridge-origin-mtls-evidence/v1", bound, removed, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal origin mTLS evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:         runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://bridge-origin-mtls/" + target.InstanceRef, ObservationDigest: digestString,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef,
			Status:         runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://bridge-origin-mtls/" + target.InstanceRef, ObservationDigest: digestString,
		}},
	}, nil
}

func validateBridgeOriginMTLSRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority BridgeOriginMTLSAuthority, evaluatedAt time.Time) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, BridgeOriginMTLSApplyPolicy, BridgeOriginMTLSExpectation, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 {
		return emptyTarget, emptyHealth, BridgeOriginMTLSApplyPolicy{}, BridgeOriginMTLSExpectation{}, errors.New("origin mTLS executor requires exactly one runtime, one health target, one artifact, and no access binding")
	}
	if !validCoreHostBootstrapDigest(request.RequestDigest) {
		return emptyTarget, emptyHealth, BridgeOriginMTLSApplyPolicy{}, BridgeOriginMTLSExpectation{}, errors.New("origin mTLS executor requires the sealed request digest")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.BridgeOriginMTLSExecutorBundleRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != bridgeOriginMTLSModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != bridgeOriginMTLSProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != bridgeOriginMTLSModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.OwnerContractHash != authority.ModuleContractHash || target.UnitRef != bridgeOriginMTLSUnitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "native" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.WorkloadRef != "" || target.ImageRef != "" ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, BridgeOriginMTLSApplyPolicy{}, BridgeOriginMTLSExpectation{}, errors.New("runtime target is not the exact bound origin mTLS contract")
	}
	wantInstance := bridgeOriginMTLSUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := bridgeOriginMTLSArtifactPrefix + wantInstance
	wantRequirementID := bridgeOriginMTLSModuleRef + "/" + bridgeOriginMTLSUnitRef + "/" + wantInstance
	if target.RequirementID != wantRequirementID || target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, BridgeOriginMTLSApplyPolicy{}, BridgeOriginMTLSExpectation{}, errors.New("runtime target does not bind the exact node-local origin mTLS artifact")
	}
	health := request.HealthTargets[0]
	if health.RequirementID != bridgeOriginMTLSHealthSourceRef+"/"+wantInstance ||
		health.SourceRef != bridgeOriginMTLSHealthSourceRef || health.ContractHash != authority.HealthContractHash ||
		health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != bridgeOriginMTLSModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, BridgeOriginMTLSApplyPolicy{}, BridgeOriginMTLSExpectation{}, errors.New("health target is not the exact origin mTLS postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != wantArtifactID || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != bridgeOriginMTLSProviderRef ||
		artifact.ProviderContractHash != target.ProviderContractHash || artifact.ModuleRef != bridgeOriginMTLSModuleRef ||
		artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != bridgeOriginMTLSUnitRef ||
		artifact.UnitContractHash != target.UnitContractHash || artifact.InstanceRef != wantInstance ||
		artifact.OutputRef != bridgeOriginMTLSOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) ||
		!slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 || len(artifact.Content) > bridgeOriginMTLSMaxArtifactBytes {
		return emptyTarget, emptyHealth, BridgeOriginMTLSApplyPolicy{}, BridgeOriginMTLSExpectation{}, errors.New("artifact is not the exact CUE-owned origin mTLS instance")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, BridgeOriginMTLSApplyPolicy{}, BridgeOriginMTLSExpectation{}, errors.New("origin mTLS artifact digest does not match its immutable content")
	}
	governed, err := architecturev2renderer.ValidateBridgeOriginMTLSExecutorArtifact(artifact.Content, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return emptyTarget, emptyHealth, BridgeOriginMTLSApplyPolicy{}, BridgeOriginMTLSExpectation{}, fmt.Errorf("validate governed origin mTLS policy: %w", err)
	}
	policyDigestInput, err := json.Marshal(struct {
		ArtifactDigest      string `json:"artifactDigest"`
		RequestDigest       string `json:"requestDigest"`
		SiteRef             string `json:"siteRef"`
		NodeRef             string `json:"nodeRef"`
		ExecutionChannelRef string `json:"executionChannelRef"`
	}{artifact.Digest, request.RequestDigest, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, BridgeOriginMTLSApplyPolicy{}, BridgeOriginMTLSExpectation{}, fmt.Errorf("bind origin mTLS policy: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	policyDigest := "sha256:" + hex.EncodeToString(policyDigestBytes[:])
	evaluatedAtText := evaluatedAt.Format(time.RFC3339Nano)
	publications := append([]architecturev2renderer.BridgeOriginMTLSPublicationPolicy(nil), governed.Publications...)
	policy := BridgeOriginMTLSApplyPolicy{
		PolicyDigest: policyDigest, StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, EvaluatedAt: evaluatedAtText, Publications: publications,
	}
	expectation := BridgeOriginMTLSExpectation{
		PolicyDigest: policyDigest, StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, EvaluatedAt: evaluatedAtText,
		Publications: append([]architecturev2renderer.BridgeOriginMTLSPublicationPolicy(nil), publications...),
	}
	return target, health, policy, expectation, nil
}

func cloneBridgeOriginMTLSApplyPolicy(policy BridgeOriginMTLSApplyPolicy) BridgeOriginMTLSApplyPolicy {
	policy.Publications = append([]architecturev2renderer.BridgeOriginMTLSPublicationPolicy(nil), policy.Publications...)
	return policy
}

func cloneBridgeOriginMTLSExpectation(expectation BridgeOriginMTLSExpectation) BridgeOriginMTLSExpectation {
	expectation.Publications = append([]architecturev2renderer.BridgeOriginMTLSPublicationPolicy(nil), expectation.Publications...)
	return expectation
}

func bridgeOriginMTLSCheckedAt(now func() time.Time, notBefore time.Time) (time.Time, error) {
	checkedAt := now().UTC()
	if checkedAt.IsZero() || checkedAt.Before(notBefore) {
		return time.Time{}, errors.New("origin mTLS executor clock moved backwards or returned zero time")
	}
	return checkedAt, nil
}

func validBridgeOriginMTLSObservation(observation BridgeOriginMTLSObservation, expectation BridgeOriginMTLSExpectation, status string, evaluatedAt, checkedAt time.Time) bool {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != status ||
		observation.EvaluatedAt != expectation.EvaluatedAt || len(observation.Materials) != len(expectation.Publications) {
		return false
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Format(time.RFC3339Nano) != observation.ObservedAt ||
		observedAt.Before(evaluatedAt) || observedAt.After(checkedAt) {
		return false
	}
	seenFingerprints := make(map[string]struct{}, len(observation.Materials))
	seenPublicKeys := make(map[string]struct{}, len(observation.Materials))
	seenSerials := make(map[string]struct{}, len(observation.Materials))
	for index, material := range observation.Materials {
		publication := expectation.Publications[index]
		if material.ServiceRef != publication.ServiceRef || material.IdentityRef != publication.IdentityRef ||
			material.ModuleRef != publication.ModuleRef || material.UnitRef != publication.UnitRef ||
			material.OriginInstanceRef != publication.OriginInstanceRef ||
			material.UpstreamProtocol != publication.UpstreamProtocol || material.TargetPort != publication.TargetPort ||
			material.ServerName != publication.ServerName ||
			material.MinimumTLSVersion != "TLS1.3" ||
			!material.MutualTLSRequired || !material.ClientCertificateRequired ||
			!material.OutboundOnly || material.GeneralLANAccess ||
			material.CredentialIssuerRef != publication.CredentialIssuerRef || material.Issuer != publication.Issuer ||
			material.Audience != publication.Audience || material.VerificationKeySetRef != publication.VerificationKeySetRef ||
			material.EdgeVerifierRef != publication.EdgeVerifierRef ||
			material.VerifierDistributionRef != publication.VerifierDistributionRef ||
			material.CertificateSubjectRef != publication.IdentityRef ||
			!slices.Equal(material.CertificateSANs, []string{publication.ServerName}) ||
			!slices.Equal(material.CertificateExtendedKeyUsages, []string{"server-auth"}) ||
			material.CertificateCA || !material.CertificateChainVerified ||
			!validCoreHostBootstrapDigest(material.CertificateFingerprint) ||
			!validCoreHostBootstrapDigest(material.PublicKeyFingerprint) ||
			material.Serial == "" || material.Serial != strings.TrimSpace(material.Serial) || len(material.Serial) > 128 {
			return false
		}
		if _, duplicate := seenFingerprints[material.CertificateFingerprint]; duplicate {
			return false
		}
		seenFingerprints[material.CertificateFingerprint] = struct{}{}
		if _, duplicate := seenPublicKeys[material.PublicKeyFingerprint]; duplicate {
			return false
		}
		seenPublicKeys[material.PublicKeyFingerprint] = struct{}{}
		if _, duplicate := seenSerials[material.Serial]; duplicate {
			return false
		}
		seenSerials[material.Serial] = struct{}{}
		notBefore, beforeErr := time.Parse(time.RFC3339Nano, material.NotBefore)
		notAfter, afterErr := time.Parse(time.RFC3339Nano, material.NotAfter)
		configObservedAt, configErr := time.Parse(time.RFC3339Nano, material.ConfigurationObservedAt)
		revocationObservedAt, revocationErr := time.Parse(time.RFC3339Nano, material.RevocationStateObservedAt)
		if beforeErr != nil || afterErr != nil || configErr != nil || revocationErr != nil ||
			notBefore.Format(time.RFC3339Nano) != material.NotBefore || notAfter.Format(time.RFC3339Nano) != material.NotAfter ||
			configObservedAt.Format(time.RFC3339Nano) != material.ConfigurationObservedAt ||
			revocationObservedAt.Format(time.RFC3339Nano) != material.RevocationStateObservedAt ||
			notBefore.After(observedAt) || !notAfter.After(checkedAt.Add(time.Minute)) ||
			!notAfter.After(notBefore) ||
			notAfter.Sub(notBefore) > time.Duration(publication.CredentialTTLSeconds)*time.Second ||
			configObservedAt.After(observedAt) || checkedAt.Sub(configObservedAt) > time.Duration(publication.VerifierMaxStalenessSeconds)*time.Second ||
			revocationObservedAt.After(observedAt) ||
			checkedAt.Sub(revocationObservedAt) > time.Duration(publication.RevocationMaxStalenessSeconds)*time.Second {
			return false
		}
	}
	return true
}

var _ runtimeexecutor.Executor = (*BridgeOriginMTLSExecutor)(nil)
