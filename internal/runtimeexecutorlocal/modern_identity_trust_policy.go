package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	modernIdentityTrustProviderRef             = "stackkits-modern-identity-trust-policy"
	modernIdentityTrustModuleRef               = "stackkits-modern-identity-trust-policy-manifest"
	modernIdentityTrustUnitRef                 = "policy-bundle"
	modernIdentityTrustInstanceRef             = "policy-bundle-logical"
	modernIdentityTrustArtifactRef             = "modern-identity-trust-policy"
	modernIdentityTrustOutputRef               = "modern/identity/trust-policy.json"
	modernIdentityTrustDistributionArtifactRef = "modern-identity-verifier-distribution-policy"
	modernIdentityTrustDistributionOutputRef   = "modern/identity/verifier-distribution-policy.json"
	modernIdentityTrustHealthSourceRef         = "modern-identity-trust-enforcement"
	modernIdentityTrustMaxArtifactBytes        = 256 << 10
)

type ModernIdentityTrustPolicyBinding struct {
	HomeSiteRefs  []string
	CloudSiteRefs []string
	NodeRefs      []string
}

type ModernIdentityTrustPolicyAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type ModernIdentityTrustRuntimePolicy struct {
	PolicyDigest string
	Policy       architecturev2renderer.ModernIdentityTrustEnforcementPolicy
	NodeRefs     []string
}

type ModernIdentityTrustApplyObservation struct {
	PolicyDigest string `json:"policyDigest"`
	Status       string `json:"status"`
}

type ModernIdentityTrustVerifyExpectation struct {
	PolicyDigest    string
	StackID         string
	HomeSiteRefs    []string
	CloudSiteRefs   []string
	NodeRefs        []string
	VerifierIDs     []string
	DistributionIDs []string
	MaxStaleSeconds int
	NotBefore       time.Time
}

type ModernIdentityTrustVerifyObservation struct {
	PolicyDigest                   string `json:"policyDigest"`
	Status                         string `json:"status"`
	RevocationDistributionStatus   string `json:"revocationDistributionStatus"`
	KeyReferenceDistributionStatus string `json:"keyReferenceDistributionStatus"`
	OneWayDistributionStatus       string `json:"oneWayDistributionStatus"`
	CloudVerifierStatus            string `json:"cloudVerifierStatus"`
	HomeVerifierStatus             string `json:"homeVerifierStatus"`
	ObservedAt                     string `json:"observedAt"`
}

// ModernIdentityTrustPolicyOperations owns only the five exact enforcement
// responsibilities. It exposes no signing/enrollment, key bytes, credentials,
// endpoints, transport, generic LAN/federation, provider, or lifecycle API.
type ModernIdentityTrustPolicyOperations interface {
	DistributeRevocationState(context.Context, ModernIdentityTrustRuntimePolicy) (ModernIdentityTrustApplyObservation, error)
	DistributeVerificationKeyReferences(context.Context, ModernIdentityTrustRuntimePolicy) (ModernIdentityTrustApplyObservation, error)
	EnforceOneWayDistribution(context.Context, ModernIdentityTrustRuntimePolicy) (ModernIdentityTrustApplyObservation, error)
	EnforceCloudSessionVerification(context.Context, ModernIdentityTrustRuntimePolicy) (ModernIdentityTrustApplyObservation, error)
	EnforceHomeSessionVerification(context.Context, ModernIdentityTrustRuntimePolicy) (ModernIdentityTrustApplyObservation, error)
	VerifyModernIdentityTrustPolicy(context.Context, ModernIdentityTrustVerifyExpectation) (ModernIdentityTrustVerifyObservation, error)
}

type ModernIdentityTrustPolicyExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    ModernIdentityTrustPolicyBinding
	authority  ModernIdentityTrustPolicyAuthority
	operations ModernIdentityTrustPolicyOperations
	clock      func() time.Time
}

func NewModernIdentityTrustPolicyExecutor(identity runtimeexecutor.ExecutorIdentity, binding ModernIdentityTrustPolicyBinding, authority ModernIdentityTrustPolicyAuthority, operations ModernIdentityTrustPolicyOperations) *ModernIdentityTrustPolicyExecutor {
	return &ModernIdentityTrustPolicyExecutor{
		identity: identity,
		binding: ModernIdentityTrustPolicyBinding{
			HomeSiteRefs: append([]string(nil), binding.HomeSiteRefs...), CloudSiteRefs: append([]string(nil), binding.CloudSiteRefs...), NodeRefs: append([]string(nil), binding.NodeRefs...),
		},
		authority: authority, operations: operations, clock: func() time.Time { return time.Now().UTC() },
	}
}

func (e *ModernIdentityTrustPolicyExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	return e.identity
}

func (e *ModernIdentityTrustPolicyExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Modern identity-trust executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || !validExactRefSet(e.binding.HomeSiteRefs) || !validExactRefSet(e.binding.CloudSiteRefs) || !validExactRefSet(e.binding.NodeRefs) ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Modern identity-trust executor requires exact catalog authority, federated placement, and authenticated operations")
	}
	target, health, policy, err := validateModernIdentityTrustPolicyRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	startedAt := e.clock().UTC()
	if startedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Modern identity-trust executor clock returned zero time")
	}
	type operation struct {
		name string
		run  func(context.Context, ModernIdentityTrustRuntimePolicy) (ModernIdentityTrustApplyObservation, error)
	}
	operations := []operation{
		{"distribute revocation state", e.operations.DistributeRevocationState},
		{"distribute verification-key references", e.operations.DistributeVerificationKeyReferences},
		{"enforce one-way distribution", e.operations.EnforceOneWayDistribution},
		{"enforce Cloud session verification", e.operations.EnforceCloudSessionVerification},
		{"enforce Home session verification", e.operations.EnforceHomeSessionVerification},
	}
	observations := make([]ModernIdentityTrustApplyObservation, 0, len(operations))
	for _, operation := range operations {
		observation, err := operation.run(ctx, cloneModernIdentityTrustRuntimePolicy(policy))
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s: %w", operation.name, err)
		}
		if observation.PolicyDigest != policy.PolicyDigest || observation.Status != "enforced" {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s observation does not prove the exact policy", operation.name)
		}
		observations = append(observations, observation)
	}
	expectation := ModernIdentityTrustVerifyExpectation{
		PolicyDigest: policy.PolicyDigest, StackID: policy.Policy.StackID,
		HomeSiteRefs: append([]string(nil), policy.Policy.HomeSiteRefs...), CloudSiteRefs: append([]string(nil), policy.Policy.CloudSiteRefs...),
		NodeRefs: append([]string(nil), policy.NodeRefs...), MaxStaleSeconds: policy.Policy.MaxStaleSeconds, NotBefore: startedAt,
	}
	for _, verifier := range policy.Policy.Verifiers {
		expectation.VerifierIDs = append(expectation.VerifierIDs, verifier.ID)
	}
	for _, distribution := range policy.Policy.Distributions {
		expectation.DistributionIDs = append(expectation.DistributionIDs, distribution.ID)
	}
	verified, err := e.operations.VerifyModernIdentityTrustPolicy(ctx, cloneModernIdentityTrustVerifyExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify Modern identity-trust policy: %w", err)
	}
	if err := validateModernIdentityTrustVerifyObservation(verified, expectation, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                                `json:"schemaVersion"`
		Operations    []ModernIdentityTrustApplyObservation `json:"operations"`
		Verify        ModernIdentityTrustVerifyObservation  `json:"verify"`
	}{"stackkit.modern-identity-trust-enforcement-evidence/v1", observations, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Modern identity-trust evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://modern-identity-trust-policy/" + target.InstanceRef, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://modern-identity-trust-policy/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

func validateModernIdentityTrustPolicyRequest(request runtimeexecutor.ExecutionRequest, binding ModernIdentityTrustPolicyBinding, authority ModernIdentityTrustPolicyAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, ModernIdentityTrustRuntimePolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.Artifacts) != 2 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, ModernIdentityTrustRuntimePolicy{}, errors.New("Modern identity-trust executor requires exactly one runtime, one health target, two artifacts, and no external access binding")
	}
	siteRefs := append(append([]string(nil), binding.CloudSiteRefs...), binding.HomeSiteRefs...)
	slices.Sort(siteRefs)
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.ModernIdentityTrustPolicyRendererContract()
	artifactRefs := []string{modernIdentityTrustArtifactRef, modernIdentityTrustDistributionArtifactRef}
	if target.OwnerKind != "module" || target.OwnerRef != modernIdentityTrustModuleRef || target.OwnerVersion != "" || target.ProviderRef != modernIdentityTrustProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != modernIdentityTrustModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != modernIdentityTrustUnitRef || target.UnitContractHash != contract.ContractHash || target.InstanceRef != modernIdentityTrustInstanceRef || target.RuntimeKind != "native" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.ExecutionChannelRef != "" || target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, siteRefs) || !slices.Equal(target.NodeRefs, binding.NodeRefs) || !slices.Equal(target.ArtifactRefs, artifactRefs) {
		return emptyTarget, emptyHealth, ModernIdentityTrustRuntimePolicy{}, errors.New("runtime target is not the exact bound Modern identity-trust contract")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != modernIdentityTrustHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != modernIdentityTrustModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, ModernIdentityTrustRuntimePolicy{}, errors.New("health target is not the exact Modern identity-trust enforcement postcondition")
	}
	contents := make(map[string][]byte, 2)
	digests := make(map[string]string, 2)
	expectedOutputs := map[string]string{modernIdentityTrustArtifactRef: modernIdentityTrustOutputRef, modernIdentityTrustDistributionArtifactRef: modernIdentityTrustDistributionOutputRef}
	for _, artifact := range request.Artifacts {
		outputRef, exists := expectedOutputs[artifact.ID]
		if !exists || contents[artifact.ID] != nil || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" ||
			artifact.OwnerRef != modernIdentityTrustInstanceRef || artifact.OwnerContractHash != contract.ContractHash || artifact.ProviderRef != modernIdentityTrustProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
			artifact.ModuleRef != modernIdentityTrustModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash || artifact.UnitRef != modernIdentityTrustUnitRef || artifact.UnitContractHash != contract.ContractHash ||
			artifact.InstanceRef != modernIdentityTrustInstanceRef || artifact.OutputRef != outputRef || !slices.Equal(artifact.SiteRefs, siteRefs) || !slices.Equal(artifact.NodeRefs, binding.NodeRefs) ||
			len(artifact.Content) == 0 || len(artifact.Content) > modernIdentityTrustMaxArtifactBytes {
			return emptyTarget, emptyHealth, ModernIdentityTrustRuntimePolicy{}, errors.New("artifact set is not the exact CUE-owned Modern identity-trust policy pair")
		}
		digest := sha256.Sum256(artifact.Content)
		if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
			return emptyTarget, emptyHealth, ModernIdentityTrustRuntimePolicy{}, errors.New("Modern identity-trust artifact digest does not match its immutable content")
		}
		contents[artifact.ID], digests[artifact.ID] = artifact.Content, artifact.Digest
	}
	projection, err := architecturev2renderer.ValidateModernIdentityTrustPolicyArtifacts(contents[modernIdentityTrustArtifactRef], contents[modernIdentityTrustDistributionArtifactRef])
	if err != nil {
		return emptyTarget, emptyHealth, ModernIdentityTrustRuntimePolicy{}, fmt.Errorf("validate governed Modern identity-trust policy pair: %w", err)
	}
	if !slices.Equal(projection.HomeSiteRefs, binding.HomeSiteRefs) || !slices.Equal(projection.CloudSiteRefs, binding.CloudSiteRefs) {
		return emptyTarget, emptyHealth, ModernIdentityTrustRuntimePolicy{}, errors.New("Modern identity-trust policy does not bind the exact Home and Cloud Sites")
	}
	policyDigestInput, err := json.Marshal(struct {
		TrustArtifactDigest string   `json:"trustArtifactDigest"`
		DistributionDigest  string   `json:"distributionDigest"`
		ProviderHash        string   `json:"providerHash"`
		ModuleHash          string   `json:"moduleHash"`
		HealthHash          string   `json:"healthHash"`
		SiteRefs            []string `json:"siteRefs"`
		NodeRefs            []string `json:"nodeRefs"`
	}{digests[modernIdentityTrustArtifactRef], digests[modernIdentityTrustDistributionArtifactRef], authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, siteRefs, binding.NodeRefs})
	if err != nil {
		return emptyTarget, emptyHealth, ModernIdentityTrustRuntimePolicy{}, fmt.Errorf("bind Modern identity-trust authority: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	return target, health, ModernIdentityTrustRuntimePolicy{PolicyDigest: "sha256:" + hex.EncodeToString(policyDigestBytes[:]), Policy: cloneModernIdentityTrustPolicy(projection), NodeRefs: append([]string(nil), binding.NodeRefs...)}, nil
}

func validateModernIdentityTrustVerifyObservation(observation ModernIdentityTrustVerifyObservation, expectation ModernIdentityTrustVerifyExpectation, completedAt time.Time) error {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != "ready" || observation.RevocationDistributionStatus != "enforced" || observation.KeyReferenceDistributionStatus != "enforced" ||
		observation.OneWayDistributionStatus != "enforced" || observation.CloudVerifierStatus != "enforced" || observation.HomeVerifierStatus != "enforced" {
		return errors.New("verification does not prove the exact enforced Modern identity-trust policy")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Location() != time.UTC || observedAt.Format(time.RFC3339Nano) != observation.ObservedAt || observedAt.Before(expectation.NotBefore) || observedAt.After(completedAt) {
		return errors.New("Modern identity-trust verification is not fresh at the exact invocation boundary")
	}
	return nil
}

func cloneModernIdentityTrustRuntimePolicy(policy ModernIdentityTrustRuntimePolicy) ModernIdentityTrustRuntimePolicy {
	policy.Policy = cloneModernIdentityTrustPolicy(policy.Policy)
	policy.NodeRefs = append([]string(nil), policy.NodeRefs...)
	return policy
}

func cloneModernIdentityTrustPolicy(policy architecturev2renderer.ModernIdentityTrustEnforcementPolicy) architecturev2renderer.ModernIdentityTrustEnforcementPolicy {
	policy.HomeSiteRefs = append([]string(nil), policy.HomeSiteRefs...)
	policy.CloudSiteRefs = append([]string(nil), policy.CloudSiteRefs...)
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

func cloneModernIdentityTrustVerifyExpectation(expectation ModernIdentityTrustVerifyExpectation) ModernIdentityTrustVerifyExpectation {
	expectation.HomeSiteRefs = append([]string(nil), expectation.HomeSiteRefs...)
	expectation.CloudSiteRefs = append([]string(nil), expectation.CloudSiteRefs...)
	expectation.NodeRefs = append([]string(nil), expectation.NodeRefs...)
	expectation.VerifierIDs = append([]string(nil), expectation.VerifierIDs...)
	expectation.DistributionIDs = append([]string(nil), expectation.DistributionIDs...)
	return expectation
}

var _ runtimeexecutor.Executor = (*ModernIdentityTrustPolicyExecutor)(nil)
