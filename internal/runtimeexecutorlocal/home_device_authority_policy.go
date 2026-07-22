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
	homeDeviceAuthorityProviderRef      = "stackkits-home-device-authority"
	homeDeviceAuthorityModuleRef        = "stackkits-home-device-authority-policy-manifest"
	homeDeviceAuthorityUnitRef          = "policy-bundle"
	homeDeviceAuthorityInstanceRef      = "policy-bundle-logical"
	homeDeviceAuthorityArtifactRef      = "home-device-authority-policy"
	homeDeviceAuthorityOutputRef        = "local/identity/device-authority-policy.json"
	homeDeviceAuthorityHealthSourceRef  = "home-device-authority-enforcement"
	homeDeviceAuthorityMaxArtifactBytes = 256 << 10
)

type HomeDeviceAuthorityPolicyBinding struct {
	SiteRefs []string
	NodeRefs []string
}

type HomeDeviceAuthorityPolicyAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type HomeDeviceAuthorityRuntimePolicy struct {
	PolicyDigest string
	Policy       architecturev2renderer.HomeDeviceAuthorityEnforcementPolicy
	NodeRefs     []string
}

type HomeDeviceAuthorityApplyObservation struct {
	PolicyDigest string `json:"policyDigest"`
	Status       string `json:"status"`
}

type HomeDeviceAuthorityVerifyExpectation struct {
	PolicyDigest string
	StackID      string
	SiteRefs     []string
	NodeRefs     []string
	IssuerID     string
	NotBefore    time.Time
}

type HomeDeviceAuthorityVerifyObservation struct {
	PolicyDigest     string `json:"policyDigest"`
	Status           string `json:"status"`
	EnrollmentStatus string `json:"enrollmentStatus"`
	IssuerStatus     string `json:"issuerStatus"`
	RevocationStatus string `json:"revocationStatus"`
	ObservedAt       string `json:"observedAt"`
}

// HomeDeviceAuthorityPolicyOperations configures only the device authority
// policy. It does not enroll a particular device, mint a credential, carry key
// bytes or credentials, expose an endpoint, or own network/provider lifecycle.
type HomeDeviceAuthorityPolicyOperations interface {
	ConfigureDeviceEnrollment(context.Context, HomeDeviceAuthorityRuntimePolicy) (HomeDeviceAuthorityApplyObservation, error)
	ConfigureDeviceCredentialIssuer(context.Context, HomeDeviceAuthorityRuntimePolicy) (HomeDeviceAuthorityApplyObservation, error)
	ConfigureDeviceCredentialRevocation(context.Context, HomeDeviceAuthorityRuntimePolicy) (HomeDeviceAuthorityApplyObservation, error)
	VerifyHomeDeviceAuthorityPolicy(context.Context, HomeDeviceAuthorityVerifyExpectation) (HomeDeviceAuthorityVerifyObservation, error)
}

type HomeDeviceAuthorityPolicyExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    HomeDeviceAuthorityPolicyBinding
	authority  HomeDeviceAuthorityPolicyAuthority
	operations HomeDeviceAuthorityPolicyOperations
	clock      func() time.Time
}

func NewHomeDeviceAuthorityPolicyExecutor(identity runtimeexecutor.ExecutorIdentity, binding HomeDeviceAuthorityPolicyBinding, authority HomeDeviceAuthorityPolicyAuthority, operations HomeDeviceAuthorityPolicyOperations) *HomeDeviceAuthorityPolicyExecutor {
	return &HomeDeviceAuthorityPolicyExecutor{
		identity:  identity,
		binding:   HomeDeviceAuthorityPolicyBinding{SiteRefs: append([]string(nil), binding.SiteRefs...), NodeRefs: append([]string(nil), binding.NodeRefs...)},
		authority: authority, operations: operations, clock: func() time.Time { return time.Now().UTC() },
	}
}

func (e *HomeDeviceAuthorityPolicyExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	return e.identity
}

func (e *HomeDeviceAuthorityPolicyExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home device-authority executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || !validExactRefSet(e.binding.SiteRefs) || !validExactRefSet(e.binding.NodeRefs) ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home device-authority executor requires exact catalog authority, Home placement, and authenticated operations")
	}
	target, health, policy, err := validateHomeDeviceAuthorityPolicyRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	startedAt := e.clock().UTC()
	if startedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home device-authority executor clock returned zero time")
	}
	type operation struct {
		name string
		run  func(context.Context, HomeDeviceAuthorityRuntimePolicy) (HomeDeviceAuthorityApplyObservation, error)
	}
	operations := []operation{
		{"configure device enrollment", e.operations.ConfigureDeviceEnrollment},
		{"configure device credential issuer", e.operations.ConfigureDeviceCredentialIssuer},
		{"configure device credential revocation", e.operations.ConfigureDeviceCredentialRevocation},
	}
	observations := make([]HomeDeviceAuthorityApplyObservation, 0, len(operations))
	for _, operation := range operations {
		observation, err := operation.run(ctx, cloneHomeDeviceAuthorityRuntimePolicy(policy))
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s: %w", operation.name, err)
		}
		if observation.PolicyDigest != policy.PolicyDigest || observation.Status != "enforced" {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s observation does not prove the exact policy", operation.name)
		}
		observations = append(observations, observation)
	}
	expectation := HomeDeviceAuthorityVerifyExpectation{
		PolicyDigest: policy.PolicyDigest, StackID: policy.Policy.StackID, SiteRefs: append([]string(nil), policy.Policy.SiteRefs...),
		NodeRefs: append([]string(nil), policy.NodeRefs...), IssuerID: policy.Policy.Issuer.ID, NotBefore: startedAt,
	}
	verified, err := e.operations.VerifyHomeDeviceAuthorityPolicy(ctx, cloneHomeDeviceAuthorityVerifyExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify Home device-authority policy: %w", err)
	}
	if err := validateHomeDeviceAuthorityVerifyObservation(verified, expectation, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                                `json:"schemaVersion"`
		Operations    []HomeDeviceAuthorityApplyObservation `json:"operations"`
		Verify        HomeDeviceAuthorityVerifyObservation  `json:"verify"`
	}{"stackkit.home-device-authority-enforcement-evidence/v1", observations, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Home device-authority evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://home-device-authority-policy/" + target.InstanceRef, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://home-device-authority-policy/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

func validateHomeDeviceAuthorityPolicyRequest(request runtimeexecutor.ExecutionRequest, binding HomeDeviceAuthorityPolicyBinding, authority HomeDeviceAuthorityPolicyAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, HomeDeviceAuthorityRuntimePolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.Artifacts) != 1 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, HomeDeviceAuthorityRuntimePolicy{}, errors.New("Home device-authority executor requires exactly one runtime, one health target, one artifact, and no external access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.HomeDeviceAuthorityPolicyRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != homeDeviceAuthorityModuleRef || target.OwnerVersion != "" || target.ProviderRef != homeDeviceAuthorityProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != homeDeviceAuthorityModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != homeDeviceAuthorityUnitRef || target.UnitContractHash != contract.ContractHash || target.InstanceRef != homeDeviceAuthorityInstanceRef || target.RuntimeKind != "native" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.ExecutionChannelRef != "" || target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, binding.SiteRefs) || !slices.Equal(target.NodeRefs, binding.NodeRefs) || !slices.Equal(target.ArtifactRefs, []string{homeDeviceAuthorityArtifactRef}) {
		return emptyTarget, emptyHealth, HomeDeviceAuthorityRuntimePolicy{}, errors.New("runtime target is not the exact bound Home device-authority contract")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != homeDeviceAuthorityHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != homeDeviceAuthorityModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, HomeDeviceAuthorityRuntimePolicy{}, errors.New("health target is not the exact Home device-authority enforcement postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != homeDeviceAuthorityArtifactRef || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" ||
		artifact.OwnerRef != homeDeviceAuthorityInstanceRef || artifact.OwnerContractHash != contract.ContractHash || artifact.ProviderRef != homeDeviceAuthorityProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
		artifact.ModuleRef != homeDeviceAuthorityModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash || artifact.UnitRef != homeDeviceAuthorityUnitRef || artifact.UnitContractHash != contract.ContractHash ||
		artifact.InstanceRef != homeDeviceAuthorityInstanceRef || artifact.OutputRef != homeDeviceAuthorityOutputRef || !slices.Equal(artifact.SiteRefs, binding.SiteRefs) || !slices.Equal(artifact.NodeRefs, binding.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > homeDeviceAuthorityMaxArtifactBytes {
		return emptyTarget, emptyHealth, HomeDeviceAuthorityRuntimePolicy{}, errors.New("artifact is not the exact CUE-owned Home device-authority policy")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, HomeDeviceAuthorityRuntimePolicy{}, errors.New("Home device-authority artifact digest does not match its immutable content")
	}
	projection, err := architecturev2renderer.ValidateHomeDeviceAuthorityPolicyArtifact(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, HomeDeviceAuthorityRuntimePolicy{}, fmt.Errorf("validate governed Home device-authority policy: %w", err)
	}
	if !slices.Equal(projection.SiteRefs, binding.SiteRefs) || !slices.Equal(projection.Issuer.SiteRefs, binding.SiteRefs) || projection.Issuer.EnrollmentMode != "local-only" || projection.Issuer.EnrollmentExposure != "lan" || !projection.Issuer.ProofOfPossessionRequired {
		return emptyTarget, emptyHealth, HomeDeviceAuthorityRuntimePolicy{}, errors.New("Home device-authority policy does not bind exact LAN-local possession-bound enrollment")
	}
	policyDigestInput, err := json.Marshal(struct {
		ArtifactDigest string   `json:"artifactDigest"`
		ProviderHash   string   `json:"providerHash"`
		ModuleHash     string   `json:"moduleHash"`
		HealthHash     string   `json:"healthHash"`
		SiteRefs       []string `json:"siteRefs"`
		NodeRefs       []string `json:"nodeRefs"`
	}{artifact.Digest, authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, binding.SiteRefs, binding.NodeRefs})
	if err != nil {
		return emptyTarget, emptyHealth, HomeDeviceAuthorityRuntimePolicy{}, fmt.Errorf("bind Home device-authority authority: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	return target, health, HomeDeviceAuthorityRuntimePolicy{
		PolicyDigest: "sha256:" + hex.EncodeToString(policyDigestBytes[:]), Policy: cloneHomeDeviceAuthorityPolicy(projection), NodeRefs: append([]string(nil), binding.NodeRefs...),
	}, nil
}

func validateHomeDeviceAuthorityVerifyObservation(observation HomeDeviceAuthorityVerifyObservation, expectation HomeDeviceAuthorityVerifyExpectation, completedAt time.Time) error {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != "ready" || observation.EnrollmentStatus != "enforced" || observation.IssuerStatus != "enforced" || observation.RevocationStatus != "enforced" {
		return errors.New("verification does not prove the exact enforced Home device-authority policy")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Location() != time.UTC || observedAt.Format(time.RFC3339Nano) != observation.ObservedAt || observedAt.Before(expectation.NotBefore) || observedAt.After(completedAt) {
		return errors.New("Home device-authority verification is not fresh at the exact invocation boundary")
	}
	return nil
}

func cloneHomeDeviceAuthorityRuntimePolicy(policy HomeDeviceAuthorityRuntimePolicy) HomeDeviceAuthorityRuntimePolicy {
	policy.Policy = cloneHomeDeviceAuthorityPolicy(policy.Policy)
	policy.NodeRefs = append([]string(nil), policy.NodeRefs...)
	return policy
}

func cloneHomeDeviceAuthorityPolicy(policy architecturev2renderer.HomeDeviceAuthorityEnforcementPolicy) architecturev2renderer.HomeDeviceAuthorityEnforcementPolicy {
	policy.SiteRefs = append([]string(nil), policy.SiteRefs...)
	policy.Issuer.Audiences = append([]string(nil), policy.Issuer.Audiences...)
	policy.Issuer.SiteRefs = append([]string(nil), policy.Issuer.SiteRefs...)
	return policy
}

func cloneHomeDeviceAuthorityVerifyExpectation(expectation HomeDeviceAuthorityVerifyExpectation) HomeDeviceAuthorityVerifyExpectation {
	expectation.SiteRefs = append([]string(nil), expectation.SiteRefs...)
	expectation.NodeRefs = append([]string(nil), expectation.NodeRefs...)
	return expectation
}

var _ runtimeexecutor.Executor = (*HomeDeviceAuthorityPolicyExecutor)(nil)
