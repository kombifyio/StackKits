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
	internalPKIProviderRef      = "stackkits-internal-pki"
	internalPKIModuleRef        = "stackkits-internal-pki-contract"
	internalPKIUnitRef          = "executor-contract"
	internalPKIOutputRef        = "home/tls/internal-pki-executor-contract.json"
	internalPKIArtifactPrefix   = "internal-pki-executor-contract-instance-"
	internalPKIHealthSourceRef  = "internal-pki-renewal-contract"
	internalPKIMaxArtifactBytes = 512 << 10
)

type InternalPKIPolicy struct {
	PolicyDigest        string
	StackID             string
	SiteRef             string
	NodeRef             string
	ExecutionChannelRef string
	EvaluatedAt         string
	Authority           architecturev2renderer.InternalPKIRuntimeAuthority
	TrustTargets        []architecturev2renderer.InternalPKIRuntimeTrustTarget
	LeafIdentities      []architecturev2renderer.InternalPKIRuntimeLeafIdentity
	ValiditySeconds     int
	RenewBeforeSeconds  int
}

type InternalPKIRootObservation struct {
	PolicyDigest         string   `json:"policyDigest"`
	Status               string   `json:"status"`
	RootFingerprint      string   `json:"rootFingerprint"`
	PublicKeyFingerprint string   `json:"publicKeyFingerprint"`
	Serial               string   `json:"serial"`
	NotBefore            string   `json:"notBefore"`
	NotAfter             string   `json:"notAfter"`
	ObservedAt           string   `json:"observedAt"`
	TrustedFingerprints  []string `json:"trustedFingerprints"`
	ContinuityValidUntil string   `json:"continuityValidUntil"`
}

type InternalPKILeafObservation struct {
	IdentityID             string   `json:"identityId"`
	SubjectRef             string   `json:"subjectRef"`
	DNSSANs                []string `json:"dnsSANs"`
	IPSANs                 []string `json:"ipSANs"`
	CA                     bool     `json:"ca"`
	CertificateFingerprint string   `json:"certificateFingerprint"`
	PublicKeyFingerprint   string   `json:"publicKeyFingerprint"`
	TrustRootFingerprint   string   `json:"trustRootFingerprint"`
	Serial                 string   `json:"serial"`
	NotBefore              string   `json:"notBefore"`
	NotAfter               string   `json:"notAfter"`
	ObservedAt             string   `json:"observedAt"`
}

type InternalPKILeafSetObservation struct {
	PolicyDigest string                       `json:"policyDigest"`
	Status       string                       `json:"status"`
	Leaves       []InternalPKILeafObservation `json:"leaves"`
}

type InternalPKITrustObservation struct {
	PolicyDigest    string                                                 `json:"policyDigest"`
	Status          string                                                 `json:"status"`
	RootFingerprint string                                                 `json:"rootFingerprint"`
	Targets         []architecturev2renderer.InternalPKIRuntimeTrustTarget `json:"targets"`
	ObservedAt      string                                                 `json:"observedAt"`
	ValidUntil      string                                                 `json:"validUntil"`
}

type InternalPKIVerifyObservation struct {
	PolicyDigest    string                                                 `json:"policyDigest"`
	Status          string                                                 `json:"status"`
	RootFingerprint string                                                 `json:"rootFingerprint"`
	Leaves          []InternalPKILeafObservation                           `json:"leaves"`
	Targets         []architecturev2renderer.InternalPKIRuntimeTrustTarget `json:"targets"`
	ObservedAt      string                                                 `json:"observedAt"`
}

// Root, leaf, trust-distribution, and verification operations are separate
// construction-time authorities. StackKits never receives their key material.
type InternalPKIRootOperations interface {
	EnsureRootAuthority(context.Context, InternalPKIPolicy) (InternalPKIRootObservation, error)
}

type InternalPKILeafOperations interface {
	IssueCompilerBoundLeaves(context.Context, InternalPKIPolicy, string) (InternalPKILeafSetObservation, error)
}

type InternalPKITrustOperations interface {
	DistributePublicTrustRoot(context.Context, InternalPKIPolicy, string) (InternalPKITrustObservation, error)
}

type InternalPKIVerifyOperations interface {
	VerifyInternalPKI(context.Context, InternalPKIPolicy, string) (InternalPKIVerifyObservation, error)
}

type InternalPKIAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type InternalPKIExecutor struct {
	identity  runtimeexecutor.ExecutorIdentity
	binding   LocalTargetBinding
	authority InternalPKIAuthority
	root      InternalPKIRootOperations
	leaf      InternalPKILeafOperations
	trust     InternalPKITrustOperations
	verify    InternalPKIVerifyOperations
	now       func() time.Time
}

func NewInternalPKIExecutor(
	identity runtimeexecutor.ExecutorIdentity,
	binding LocalTargetBinding,
	authority InternalPKIAuthority,
	root InternalPKIRootOperations,
	leaf InternalPKILeafOperations,
	trust InternalPKITrustOperations,
	verify InternalPKIVerifyOperations,
) *InternalPKIExecutor {
	return NewInternalPKIExecutorWithClock(identity, binding, authority, root, leaf, trust, verify, time.Now)
}

func NewInternalPKIExecutorWithClock(
	identity runtimeexecutor.ExecutorIdentity,
	binding LocalTargetBinding,
	authority InternalPKIAuthority,
	root InternalPKIRootOperations,
	leaf InternalPKILeafOperations,
	trust InternalPKITrustOperations,
	verify InternalPKIVerifyOperations,
	now func() time.Time,
) *InternalPKIExecutor {
	return &InternalPKIExecutor{
		identity: identity, binding: binding, authority: authority,
		root: root, leaf: leaf, trust: trust, verify: verify, now: now,
	}
}

func (e *InternalPKIExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *InternalPKIExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("internal PKI executor requires a context")
	}
	if e == nil || e.root == nil || e.leaf == nil || e.trust == nil || e.verify == nil || e.now == nil ||
		strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" ||
		strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.ModuleContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("internal PKI executor requires exact authenticated authority and separated operations")
	}
	evaluatedAt := e.now().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("internal PKI executor clock returned zero time")
	}
	target, health, policy, err := validateInternalPKIRequest(request, e.binding, e.authority, evaluatedAt)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	root, err := e.root.EnsureRootAuthority(ctx, cloneInternalPKIPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("ensure exact internal PKI root authority: %w", err)
	}
	if !validInternalPKIRootObservation(root, policy) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("root observation does not prove current material and rotation continuity")
	}
	leaves, err := e.leaf.IssueCompilerBoundLeaves(ctx, cloneInternalPKIPolicy(policy), root.RootFingerprint)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("issue exact internal PKI leaf set: %w", err)
	}
	if !validInternalPKILeaves(leaves, policy, root.RootFingerprint, "issued") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("leaf observation does not prove exact CA=false compiler identities")
	}
	trust, err := e.trust.DistributePublicTrustRoot(ctx, cloneInternalPKIPolicy(policy), root.RootFingerprint)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("distribute exact public trust root: %w", err)
	}
	if !validInternalPKITrust(trust, policy, root.RootFingerprint) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("trust observation does not prove exact public-root target distribution")
	}
	verified, err := e.verify.VerifyInternalPKI(ctx, cloneInternalPKIPolicy(policy), root.RootFingerprint)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact internal PKI postconditions: %w", err)
	}
	if !validInternalPKIVerification(verified, policy, root.RootFingerprint) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("verification observation does not prove fresh internal PKI postconditions")
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                        `json:"schemaVersion"`
		Root          InternalPKIRootObservation    `json:"root"`
		Leaves        InternalPKILeafSetObservation `json:"leaves"`
		Trust         InternalPKITrustObservation   `json:"trust"`
		Verify        InternalPKIVerifyObservation  `json:"verify"`
	}{"stackkit.internal-pki-evidence/v1", root, leaves, trust, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal internal PKI evidence: %w", err)
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:         runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://internal-pki/" + target.InstanceRef, ObservationDigest: digest,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef,
			Status:         runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://internal-pki/" + target.InstanceRef, ObservationDigest: digest,
		}},
	}, nil
}

func validateInternalPKIRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority InternalPKIAuthority, evaluatedAt time.Time) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, InternalPKIPolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 {
		return emptyTarget, emptyHealth, InternalPKIPolicy{}, errors.New("internal PKI executor requires one runtime, one health target, one artifact, and no access binding")
	}
	if !validCoreHostBootstrapDigest(request.RequestDigest) {
		return emptyTarget, emptyHealth, InternalPKIPolicy{}, errors.New("internal PKI executor requires a sealed request digest")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.InternalPKIExecutorContractRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != internalPKIModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != internalPKIProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != internalPKIModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.OwnerContractHash != authority.ModuleContractHash || target.UnitRef != internalPKIUnitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "native" ||
		target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" || target.WorkloadRef != "" ||
		target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 ||
		len(target.AccessBindingRefs) != 0 || !slices.Equal(target.SiteRefs, []string{binding.SiteRef}) ||
		!slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, InternalPKIPolicy{}, errors.New("runtime target is not the exact internal PKI authority contract")
	}
	wantInstance := internalPKIUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := internalPKIArtifactPrefix + wantInstance
	if target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, InternalPKIPolicy{}, errors.New("runtime target does not bind the exact authority-node artifact")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != internalPKIHealthSourceRef || health.ContractHash != authority.HealthContractHash ||
		health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != internalPKIModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, InternalPKIPolicy{}, errors.New("health target is not the exact internal PKI renewal postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != wantArtifactID || artifact.Kind != "native-config" || artifact.Format != "json" ||
		artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != internalPKIProviderRef ||
		artifact.ProviderContractHash != target.ProviderContractHash || artifact.ModuleRef != internalPKIModuleRef ||
		artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != internalPKIUnitRef ||
		artifact.UnitContractHash != target.UnitContractHash || artifact.InstanceRef != wantInstance ||
		artifact.OutputRef != internalPKIOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) ||
		!slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 ||
		len(artifact.Content) > internalPKIMaxArtifactBytes {
		return emptyTarget, emptyHealth, InternalPKIPolicy{}, errors.New("artifact is not the exact CUE-owned internal PKI authority instance")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, InternalPKIPolicy{}, errors.New("internal PKI artifact digest does not match immutable content")
	}
	governed, err := architecturev2renderer.ValidateInternalPKIExecutorArtifact(artifact.Content, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return emptyTarget, emptyHealth, InternalPKIPolicy{}, fmt.Errorf("validate governed internal PKI policy: %w", err)
	}
	digestInput, err := json.Marshal(struct {
		ArtifactDigest      string
		RequestDigest       string
		SiteRef             string
		NodeRef             string
		ExecutionChannelRef string
	}{artifact.Digest, request.RequestDigest, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, InternalPKIPolicy{}, fmt.Errorf("bind internal PKI policy: %w", err)
	}
	policySum := sha256.Sum256(digestInput)
	return target, health, InternalPKIPolicy{
		PolicyDigest: "sha256:" + hex.EncodeToString(policySum[:]),
		StackID:      governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, EvaluatedAt: evaluatedAt.Format(time.RFC3339Nano),
		Authority: governed.Authority, TrustTargets: governed.TrustTargets, LeafIdentities: governed.LeafIdentities,
		ValiditySeconds: governed.ValiditySeconds, RenewBeforeSeconds: governed.RenewBeforeSeconds,
	}, nil
}

func cloneInternalPKIPolicy(policy InternalPKIPolicy) InternalPKIPolicy {
	policy.Authority.KeyUsage = append([]string(nil), policy.Authority.KeyUsage...)
	policy.TrustTargets = append([]architecturev2renderer.InternalPKIRuntimeTrustTarget(nil), policy.TrustTargets...)
	policy.LeafIdentities = append([]architecturev2renderer.InternalPKIRuntimeLeafIdentity(nil), policy.LeafIdentities...)
	for index := range policy.LeafIdentities {
		policy.LeafIdentities[index].DNSSANs = append([]string(nil), policy.LeafIdentities[index].DNSSANs...)
		policy.LeafIdentities[index].IPSANs = append([]string(nil), policy.LeafIdentities[index].IPSANs...)
	}
	return policy
}

func validInternalPKIRootObservation(observation InternalPKIRootObservation, policy InternalPKIPolicy) bool {
	if observation.PolicyDigest != policy.PolicyDigest || observation.Status != "ready" ||
		!validCoreHostBootstrapDigest(observation.RootFingerprint) ||
		!validCoreHostBootstrapDigest(observation.PublicKeyFingerprint) ||
		strings.TrimSpace(observation.Serial) == "" || observation.ObservedAt != policy.EvaluatedAt ||
		!slices.Contains(observation.TrustedFingerprints, observation.RootFingerprint) {
		return false
	}
	notBefore, beforeErr := time.Parse(time.RFC3339Nano, observation.NotBefore)
	notAfter, afterErr := time.Parse(time.RFC3339Nano, observation.NotAfter)
	evaluatedAt, evaluatedErr := time.Parse(time.RFC3339Nano, policy.EvaluatedAt)
	continuityUntil, continuityErr := time.Parse(time.RFC3339Nano, observation.ContinuityValidUntil)
	return beforeErr == nil && afterErr == nil && evaluatedErr == nil && continuityErr == nil &&
		!notBefore.After(evaluatedAt) && notAfter.After(evaluatedAt.Add(time.Duration(policy.RenewBeforeSeconds)*time.Second)) &&
		!notAfter.After(evaluatedAt.Add(time.Duration(policy.ValiditySeconds)*time.Second)) &&
		continuityUntil.After(evaluatedAt.Add(time.Duration(policy.RenewBeforeSeconds)*time.Second))
}

func validInternalPKILeaves(observation InternalPKILeafSetObservation, policy InternalPKIPolicy, rootFingerprint, status string) bool {
	if observation.PolicyDigest != policy.PolicyDigest || observation.Status != status ||
		len(observation.Leaves) != len(policy.LeafIdentities) {
		return false
	}
	for index, identity := range policy.LeafIdentities {
		leaf := observation.Leaves[index]
		if leaf.IdentityID != identity.ID || leaf.SubjectRef != identity.SubjectRef ||
			!slices.Equal(leaf.DNSSANs, identity.DNSSANs) || !slices.Equal(leaf.IPSANs, identity.IPSANs) ||
			leaf.CA || leaf.TrustRootFingerprint != rootFingerprint ||
			!validCoreHostBootstrapDigest(leaf.CertificateFingerprint) ||
			!validCoreHostBootstrapDigest(leaf.PublicKeyFingerprint) || strings.TrimSpace(leaf.Serial) == "" ||
			leaf.ObservedAt != policy.EvaluatedAt {
			return false
		}
		notBefore, beforeErr := time.Parse(time.RFC3339Nano, leaf.NotBefore)
		notAfter, afterErr := time.Parse(time.RFC3339Nano, leaf.NotAfter)
		evaluatedAt, evaluatedErr := time.Parse(time.RFC3339Nano, policy.EvaluatedAt)
		if beforeErr != nil || afterErr != nil || evaluatedErr != nil || notBefore.After(evaluatedAt) ||
			!notAfter.After(evaluatedAt.Add(time.Duration(policy.RenewBeforeSeconds)*time.Second)) ||
			notAfter.After(evaluatedAt.Add(time.Duration(policy.ValiditySeconds)*time.Second)) {
			return false
		}
	}
	return true
}

func validInternalPKITrust(observation InternalPKITrustObservation, policy InternalPKIPolicy, rootFingerprint string) bool {
	if observation.PolicyDigest != policy.PolicyDigest || observation.Status != "distributed" ||
		observation.RootFingerprint != rootFingerprint || observation.ObservedAt != policy.EvaluatedAt ||
		!slices.Equal(observation.Targets, policy.TrustTargets) {
		return false
	}
	evaluatedAt, evaluatedErr := time.Parse(time.RFC3339Nano, policy.EvaluatedAt)
	validUntil, validErr := time.Parse(time.RFC3339Nano, observation.ValidUntil)
	return evaluatedErr == nil && validErr == nil &&
		validUntil.After(evaluatedAt.Add(time.Duration(policy.RenewBeforeSeconds)*time.Second))
}

func validInternalPKIVerification(observation InternalPKIVerifyObservation, policy InternalPKIPolicy, rootFingerprint string) bool {
	return observation.PolicyDigest == policy.PolicyDigest && observation.Status == "ready" &&
		observation.RootFingerprint == rootFingerprint && observation.ObservedAt == policy.EvaluatedAt &&
		slices.Equal(observation.Targets, policy.TrustTargets) &&
		validInternalPKILeaves(InternalPKILeafSetObservation{
			PolicyDigest: observation.PolicyDigest, Status: "verified", Leaves: observation.Leaves,
		}, policy, rootFingerprint, "verified")
}

var _ runtimeexecutor.Executor = (*InternalPKIExecutor)(nil)
