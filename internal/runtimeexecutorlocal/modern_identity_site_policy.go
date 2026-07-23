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
	modernIdentityTrustProviderRef             = "stackkits-modern-identity-trust-policy"
	modernIdentityTrustArtifactRef             = "modern-identity-trust-policy"
	modernIdentityTrustOutputRef               = "modern/identity/trust-policy.json"
	modernIdentityTrustDistributionArtifactRef = "modern-identity-verifier-distribution-policy"
	modernIdentityTrustDistributionOutputRef   = "modern/identity/verifier-distribution-policy.json"
	modernHomeIdentityModuleRef                = "stackkits-modern-home-identity-trust-policy-manifest"
	modernCloudIdentityModuleRef               = "stackkits-modern-cloud-identity-verifier-policy-manifest"
	modernHomeIdentityHealthRef                = "modern-home-identity-trust-enforcement"
	modernCloudIdentityHealthRef               = "modern-cloud-identity-verifier-enforcement"
	modernIdentitySiteUnitRef                  = "policy-bundle"
	modernIdentitySiteMaxArtifactSize          = 256 << 10
)

type ModernIdentityTrustPolicyAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type ModernIdentitySitePolicyBinding struct {
	SiteRef             string
	NodeRef             string
	ExecutionChannelRef string
}

type ModernIdentitySiteRuntimePolicy struct {
	PolicyDigest        string
	StackID             string
	Role                string
	SiteRef             string
	NodeRef             string
	ExecutionChannelRef string
	MaxStaleSeconds     int
	Verifiers           []architecturev2renderer.ModernIdentityTrustVerifier
	Distributions       []architecturev2renderer.ModernIdentityTrustDistribution
}

type ModernIdentitySiteApplyObservation struct {
	PolicyDigest string `json:"policyDigest"`
	Status       string `json:"status"`
}

type ModernIdentitySiteVerifyExpectation struct {
	PolicyDigest     string
	StackID          string
	Role             string
	SiteRef          string
	NodeRef          string
	VerifierIDs      []string
	DistributionIDs  []string
	MaxStaleSeconds  int
	ExecutionChannel string
	NotBefore        time.Time
}

type ModernIdentitySiteVerifyObservation struct {
	PolicyDigest       string `json:"policyDigest"`
	Status             string `json:"status"`
	VerifierStatus     string `json:"verifierStatus"`
	DistributionStatus string `json:"distributionStatus"`
	DirectionStatus    string `json:"directionStatus"`
	ObservedAt         string `json:"observedAt"`
}

// ModernHomeIdentityTrustPolicyOperations owns only Home-side verification and
// publication of bounded verifier references. It has no transport, endpoint,
// credential, signing-key, provider, lease, or lifecycle API.
type ModernHomeIdentityTrustPolicyOperations interface {
	VerifyHomeSessions(context.Context, ModernIdentitySiteRuntimePolicy) (ModernIdentitySiteApplyObservation, error)
	PublishRevocationStateReferences(context.Context, ModernIdentitySiteRuntimePolicy) (ModernIdentitySiteApplyObservation, error)
	PublishVerificationKeyReferences(context.Context, ModernIdentitySiteRuntimePolicy) (ModernIdentitySiteApplyObservation, error)
	EnforceOutboundOnlyDistribution(context.Context, ModernIdentitySiteRuntimePolicy) (ModernIdentitySiteApplyObservation, error)
	VerifyModernHomeIdentityPolicy(context.Context, ModernIdentitySiteVerifyExpectation) (ModernIdentitySiteVerifyObservation, error)
}

// ModernCloudIdentityVerifierPolicyOperations owns only Cloud-side application
// and verification of bounded Home verifier state. It cannot issue credentials,
// enroll devices, reverse the flow, or address the Home network.
type ModernCloudIdentityVerifierPolicyOperations interface {
	ApplyInboundRevocationStateReferences(context.Context, ModernIdentitySiteRuntimePolicy) (ModernIdentitySiteApplyObservation, error)
	ApplyInboundVerificationKeyReferences(context.Context, ModernIdentitySiteRuntimePolicy) (ModernIdentitySiteApplyObservation, error)
	VerifyCloudSessions(context.Context, ModernIdentitySiteRuntimePolicy) (ModernIdentitySiteApplyObservation, error)
	DenyReverseDistribution(context.Context, ModernIdentitySiteRuntimePolicy) (ModernIdentitySiteApplyObservation, error)
	VerifyModernCloudIdentityPolicy(context.Context, ModernIdentitySiteVerifyExpectation) (ModernIdentitySiteVerifyObservation, error)
}

type modernIdentitySiteSpec struct {
	role, moduleRef, artifactRef, outputRef, healthRef string
	contract                                           architecturev2renderer.RendererContract
	validate                                           func([]byte) (architecturev2renderer.ModernIdentityTrustEnforcementPolicy, error)
}

type modernIdentitySiteOperation struct {
	name string
	run  func(context.Context, ModernIdentitySiteRuntimePolicy) (ModernIdentitySiteApplyObservation, error)
}

type modernIdentitySitePolicyExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    ModernIdentitySitePolicyBinding
	authority  ModernIdentityTrustPolicyAuthority
	spec       modernIdentitySiteSpec
	operations []modernIdentitySiteOperation
	verify     func(context.Context, ModernIdentitySiteVerifyExpectation) (ModernIdentitySiteVerifyObservation, error)
	clock      func() time.Time
}

type ModernHomeIdentityTrustPolicyExecutor struct {
	*modernIdentitySitePolicyExecutor
}
type ModernCloudIdentityVerifierPolicyExecutor struct {
	*modernIdentitySitePolicyExecutor
}

func NewModernHomeIdentityTrustPolicyExecutor(identity runtimeexecutor.ExecutorIdentity, binding ModernIdentitySitePolicyBinding, authority ModernIdentityTrustPolicyAuthority, operations ModernHomeIdentityTrustPolicyOperations) *ModernHomeIdentityTrustPolicyExecutor {
	common := newModernIdentitySitePolicyExecutor(identity, binding, authority, modernIdentitySiteSpec{
		role: "home", moduleRef: modernHomeIdentityModuleRef, artifactRef: modernIdentityTrustArtifactRef, outputRef: modernIdentityTrustOutputRef,
		healthRef: modernHomeIdentityHealthRef, contract: architecturev2renderer.ModernHomeIdentityTrustPolicyRendererContract(), validate: architecturev2renderer.ValidateModernHomeIdentityTrustPolicyArtifact,
	})
	if operations != nil {
		common.operations = []modernIdentitySiteOperation{
			{"verify Home sessions", operations.VerifyHomeSessions},
			{"publish revocation-state references", operations.PublishRevocationStateReferences},
			{"publish verification-key references", operations.PublishVerificationKeyReferences},
			{"enforce outbound-only distribution", operations.EnforceOutboundOnlyDistribution},
		}
		common.verify = operations.VerifyModernHomeIdentityPolicy
	}
	return &ModernHomeIdentityTrustPolicyExecutor{common}
}

func NewModernCloudIdentityVerifierPolicyExecutor(identity runtimeexecutor.ExecutorIdentity, binding ModernIdentitySitePolicyBinding, authority ModernIdentityTrustPolicyAuthority, operations ModernCloudIdentityVerifierPolicyOperations) *ModernCloudIdentityVerifierPolicyExecutor {
	common := newModernIdentitySitePolicyExecutor(identity, binding, authority, modernIdentitySiteSpec{
		role: "cloud", moduleRef: modernCloudIdentityModuleRef, artifactRef: modernIdentityTrustDistributionArtifactRef, outputRef: modernIdentityTrustDistributionOutputRef,
		healthRef: modernCloudIdentityHealthRef, contract: architecturev2renderer.ModernCloudIdentityVerifierPolicyRendererContract(), validate: architecturev2renderer.ValidateModernCloudIdentityVerifierPolicyArtifact,
	})
	if operations != nil {
		common.operations = []modernIdentitySiteOperation{
			{"apply inbound revocation-state references", operations.ApplyInboundRevocationStateReferences},
			{"apply inbound verification-key references", operations.ApplyInboundVerificationKeyReferences},
			{"verify Cloud sessions", operations.VerifyCloudSessions},
			{"deny reverse distribution", operations.DenyReverseDistribution},
		}
		common.verify = operations.VerifyModernCloudIdentityPolicy
	}
	return &ModernCloudIdentityVerifierPolicyExecutor{common}
}

func newModernIdentitySitePolicyExecutor(identity runtimeexecutor.ExecutorIdentity, binding ModernIdentitySitePolicyBinding, authority ModernIdentityTrustPolicyAuthority, spec modernIdentitySiteSpec) *modernIdentitySitePolicyExecutor {
	return &modernIdentitySitePolicyExecutor{identity: identity, binding: binding, authority: authority, spec: spec, clock: func() time.Time { return time.Now().UTC() }}
}

func (e *modernIdentitySitePolicyExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	return e.identity
}

func (e *modernIdentitySitePolicyExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Modern identity Site executor requires a context")
	}
	if e == nil || e.clock == nil || e.verify == nil || len(e.operations) != 4 || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Modern identity Site executor requires exact catalog authority, node placement, execution channel, and authenticated operations")
	}
	target, health, policy, err := validateModernIdentitySitePolicyRequest(request, e.binding, e.authority, e.spec)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	startedAt := e.clock().UTC()
	if startedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Modern identity Site executor clock returned zero time")
	}
	observations := make([]ModernIdentitySiteApplyObservation, 0, len(e.operations))
	for _, operation := range e.operations {
		observation, err := operation.run(ctx, cloneModernIdentitySiteRuntimePolicy(policy))
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s: %w", operation.name, err)
		}
		if observation.PolicyDigest != policy.PolicyDigest || observation.Status != "enforced" {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s observation does not prove the exact policy", operation.name)
		}
		observations = append(observations, observation)
	}
	expectation := ModernIdentitySiteVerifyExpectation{
		PolicyDigest: policy.PolicyDigest, StackID: policy.StackID, Role: policy.Role, SiteRef: policy.SiteRef, NodeRef: policy.NodeRef,
		MaxStaleSeconds: policy.MaxStaleSeconds, ExecutionChannel: policy.ExecutionChannelRef, NotBefore: startedAt,
	}
	for _, verifier := range policy.Verifiers {
		expectation.VerifierIDs = append(expectation.VerifierIDs, verifier.ID)
	}
	for _, distribution := range policy.Distributions {
		expectation.DistributionIDs = append(expectation.DistributionIDs, distribution.ID)
	}
	verified, err := e.verify(ctx, cloneModernIdentitySiteVerifyExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify Modern %s identity policy: %w", e.spec.role, err)
	}
	if err := validateModernIdentitySiteVerifyObservation(verified, expectation, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                               `json:"schemaVersion"`
		Role          string                               `json:"role"`
		Operations    []ModernIdentitySiteApplyObservation `json:"operations"`
		Verify        ModernIdentitySiteVerifyObservation  `json:"verify"`
	}{"stackkit.modern-identity-site-enforcement-evidence/v1", e.spec.role, observations, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Modern identity Site evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://modern-identity/" + e.spec.role + "/" + target.InstanceRef, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://modern-identity/" + e.spec.role + "/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

func validateModernIdentitySitePolicyRequest(request runtimeexecutor.ExecutionRequest, binding ModernIdentitySitePolicyBinding, authority ModernIdentityTrustPolicyAuthority, spec modernIdentitySiteSpec) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, ModernIdentitySiteRuntimePolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.Artifacts) != 1 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, ModernIdentitySiteRuntimePolicy{}, errors.New("Modern identity Site executor requires exactly one runtime target, health target, artifact, and no access binding")
	}
	instanceRef := modernIdentitySiteUnitRef + "-node-" + binding.NodeRef
	artifactRef := spec.artifactRef + "-instance-" + instanceRef
	target := request.RuntimeTargets[0]
	if target.OwnerKind != "module" || target.OwnerRef != spec.moduleRef || target.OwnerVersion != "" || target.ProviderRef != modernIdentityTrustProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != spec.moduleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash || target.UnitRef != modernIdentitySiteUnitRef ||
		target.UnitContractHash != spec.contract.ContractHash || target.InstanceRef != instanceRef || target.RuntimeKind != "native" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || !slices.Equal(target.ArtifactRefs, []string{artifactRef}) {
		return emptyTarget, emptyHealth, ModernIdentitySiteRuntimePolicy{}, errors.New("runtime target is not the exact node-local Modern identity Site contract")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != spec.healthRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" || health.TargetRef != spec.moduleRef ||
		health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, ModernIdentitySiteRuntimePolicy{}, errors.New("health target is not the exact Modern identity Site postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != artifactRef || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" || artifact.OwnerRef != instanceRef ||
		artifact.OwnerContractHash != spec.contract.ContractHash || artifact.ProviderRef != modernIdentityTrustProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash || artifact.ModuleRef != spec.moduleRef ||
		artifact.ModuleContractHash != authority.ModuleContractHash || artifact.UnitRef != modernIdentitySiteUnitRef || artifact.UnitContractHash != spec.contract.ContractHash || artifact.InstanceRef != instanceRef || artifact.OutputRef != spec.outputRef ||
		!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 || len(artifact.Content) > modernIdentitySiteMaxArtifactSize {
		return emptyTarget, emptyHealth, ModernIdentitySiteRuntimePolicy{}, errors.New("artifact is not the exact CUE-owned Modern identity Site policy")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, ModernIdentitySiteRuntimePolicy{}, errors.New("Modern identity Site artifact digest does not match its immutable content")
	}
	projection, err := spec.validate(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, ModernIdentitySiteRuntimePolicy{}, fmt.Errorf("validate governed Modern %s identity policy: %w", spec.role, err)
	}
	policy, err := narrowModernIdentitySitePolicy(projection, spec.role, binding)
	if err != nil {
		return emptyTarget, emptyHealth, ModernIdentitySiteRuntimePolicy{}, err
	}
	bound, err := json.Marshal(struct {
		ArtifactDigest, ProviderHash, ModuleHash, HealthHash, SiteRef, NodeRef, ExecutionChannel string
	}{artifact.Digest, authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, ModernIdentitySiteRuntimePolicy{}, fmt.Errorf("bind Modern identity Site authority: %w", err)
	}
	policyDigest := sha256.Sum256(bound)
	policy.PolicyDigest = "sha256:" + hex.EncodeToString(policyDigest[:])
	return target, health, policy, nil
}

func narrowModernIdentitySitePolicy(projection architecturev2renderer.ModernIdentityTrustEnforcementPolicy, role string, binding ModernIdentitySitePolicyBinding) (ModernIdentitySiteRuntimePolicy, error) {
	governedSites := projection.HomeSiteRefs
	maxStale := 0
	if role == "cloud" {
		governedSites = projection.CloudSiteRefs
		maxStale = projection.MaxStaleSeconds
	}
	if !slices.Contains(governedSites, binding.SiteRef) {
		return ModernIdentitySiteRuntimePolicy{}, errors.New("Modern identity policy does not authorize the bound Site role")
	}
	policy := ModernIdentitySiteRuntimePolicy{StackID: projection.StackID, Role: role, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef, MaxStaleSeconds: maxStale}
	for _, verifier := range projection.Verifiers {
		if verifier.SiteKind == role && slices.Contains(verifier.SiteRefs, binding.SiteRef) {
			copy := verifier
			copy.SiteRefs = []string{binding.SiteRef}
			copy.Audiences = append([]string(nil), verifier.Audiences...)
			policy.Verifiers = append(policy.Verifiers, copy)
		}
	}
	for _, distribution := range projection.Distributions {
		refs := distribution.FromSiteRefs
		if role == "cloud" {
			refs = distribution.ToSiteRefs
		}
		if slices.Contains(refs, binding.SiteRef) {
			copy := distribution
			copy.FromSiteRefs = append([]string(nil), distribution.FromSiteRefs...)
			copy.ToSiteRefs = append([]string(nil), distribution.ToSiteRefs...)
			copy.Materials = append([]string(nil), distribution.Materials...)
			policy.Distributions = append(policy.Distributions, copy)
		}
	}
	if len(policy.Verifiers) == 0 || len(policy.Distributions) == 0 {
		return ModernIdentitySiteRuntimePolicy{}, errors.New("Modern identity Site projection lacks verifier or distribution closure")
	}
	return policy, nil
}

func validateModernIdentitySiteVerifyObservation(observation ModernIdentitySiteVerifyObservation, expectation ModernIdentitySiteVerifyExpectation, completedAt time.Time) error {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != "ready" || observation.VerifierStatus != "enforced" || observation.DistributionStatus != "enforced" || observation.DirectionStatus != "enforced" {
		return errors.New("verification does not prove the exact Modern identity Site policy")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Location() != time.UTC || observedAt.Format(time.RFC3339Nano) != observation.ObservedAt || observedAt.Before(expectation.NotBefore) || observedAt.After(completedAt) {
		return errors.New("Modern identity Site verification is not fresh at the exact invocation boundary")
	}
	return nil
}

func cloneModernIdentitySiteRuntimePolicy(policy ModernIdentitySiteRuntimePolicy) ModernIdentitySiteRuntimePolicy {
	policy.Verifiers = append([]architecturev2renderer.ModernIdentityTrustVerifier(nil), policy.Verifiers...)
	for index := range policy.Verifiers {
		policy.Verifiers[index].Audiences = append([]string(nil), policy.Verifiers[index].Audiences...)
		policy.Verifiers[index].SiteRefs = append([]string(nil), policy.Verifiers[index].SiteRefs...)
	}
	policy.Distributions = append([]architecturev2renderer.ModernIdentityTrustDistribution(nil), policy.Distributions...)
	for index := range policy.Distributions {
		policy.Distributions[index].FromSiteRefs = append([]string(nil), policy.Distributions[index].FromSiteRefs...)
		policy.Distributions[index].ToSiteRefs = append([]string(nil), policy.Distributions[index].ToSiteRefs...)
		policy.Distributions[index].Materials = append([]string(nil), policy.Distributions[index].Materials...)
	}
	return policy
}

func cloneModernIdentitySiteVerifyExpectation(expectation ModernIdentitySiteVerifyExpectation) ModernIdentitySiteVerifyExpectation {
	expectation.VerifierIDs = append([]string(nil), expectation.VerifierIDs...)
	expectation.DistributionIDs = append([]string(nil), expectation.DistributionIDs...)
	return expectation
}

var (
	_ runtimeexecutor.Executor = (*ModernHomeIdentityTrustPolicyExecutor)(nil)
	_ runtimeexecutor.Executor = (*ModernCloudIdentityVerifierPolicyExecutor)(nil)
)
