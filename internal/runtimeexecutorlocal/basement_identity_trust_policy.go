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
	basementIdentityTrustProviderRef      = "stackkits-basement-identity-trust-policy"
	basementIdentityTrustModuleRef        = "stackkits-basement-identity-trust-policy-manifest"
	basementIdentityTrustUnitRef          = "policy-bundle"
	basementIdentityTrustInstanceRef      = "policy-bundle-logical"
	basementIdentityTrustArtifactRef      = "basement-identity-trust-policy"
	basementIdentityTrustOutputRef        = "local/identity/trust-policy.json"
	basementIdentityTrustHealthSourceRef  = "basement-identity-trust-enforcement"
	basementIdentityTrustMaxArtifactBytes = 256 << 10
)

type BasementIdentityTrustPolicyBinding struct {
	SiteRefs []string
	NodeRefs []string
}

type BasementIdentityTrustPolicyAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type BasementIdentityTrustRuntimePolicy struct {
	PolicyDigest string
	StackID      string
	SiteRefs     []string
	NodeRefs     []string
	Verifiers    []architecturev2renderer.BasementIdentityTrustVerifier
}

type BasementIdentityTrustApplyObservation struct {
	PolicyDigest string `json:"policyDigest"`
	Status       string `json:"status"`
}

type BasementIdentityTrustVerifyExpectation struct {
	PolicyDigest string
	StackID      string
	SiteRefs     []string
	NodeRefs     []string
	VerifierIDs  []string
	NotBefore    time.Time
}

type BasementIdentityTrustVerifyObservation struct {
	PolicyDigest           string `json:"policyDigest"`
	Status                 string `json:"status"`
	DeviceVerifierStatus   string `json:"deviceVerifierStatus"`
	HumanVerifierStatus    string `json:"humanVerifierStatus"`
	WorkloadVerifierStatus string `json:"workloadVerifierStatus"`
	ObservedAt             string `json:"observedAt"`
}

// BasementIdentityTrustPolicyOperations owns only verifier configuration and
// readback. Enrollment, issuance, signing, key bytes, credentials, endpoints,
// provider lifecycle, and generic execution are deliberately absent.
type BasementIdentityTrustPolicyOperations interface {
	EnforceDeviceSessionVerification(context.Context, BasementIdentityTrustRuntimePolicy) (BasementIdentityTrustApplyObservation, error)
	EnforceHumanSessionVerification(context.Context, BasementIdentityTrustRuntimePolicy) (BasementIdentityTrustApplyObservation, error)
	EnforceWorkloadIdentityVerification(context.Context, BasementIdentityTrustRuntimePolicy) (BasementIdentityTrustApplyObservation, error)
	VerifyBasementIdentityTrustPolicy(context.Context, BasementIdentityTrustVerifyExpectation) (BasementIdentityTrustVerifyObservation, error)
}

// BasementIdentityTrustPolicyExecutor is an isolated adapter proof. It is not
// product-registered while the CUE enforcement requirement remains unbound.
type BasementIdentityTrustPolicyExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    BasementIdentityTrustPolicyBinding
	authority  BasementIdentityTrustPolicyAuthority
	operations BasementIdentityTrustPolicyOperations
	clock      func() time.Time
}

func NewBasementIdentityTrustPolicyExecutor(identity runtimeexecutor.ExecutorIdentity, binding BasementIdentityTrustPolicyBinding, authority BasementIdentityTrustPolicyAuthority, operations BasementIdentityTrustPolicyOperations) *BasementIdentityTrustPolicyExecutor {
	return &BasementIdentityTrustPolicyExecutor{
		identity: identity,
		binding: BasementIdentityTrustPolicyBinding{
			SiteRefs: append([]string(nil), binding.SiteRefs...),
			NodeRefs: append([]string(nil), binding.NodeRefs...),
		},
		authority: authority, operations: operations,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

func (e *BasementIdentityTrustPolicyExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	return e.identity
}

func (e *BasementIdentityTrustPolicyExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement identity-trust executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || !validExactRefSet(e.binding.SiteRefs) || !validExactRefSet(e.binding.NodeRefs) ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement identity-trust executor requires exact catalog authority, Home placement, and authenticated operations")
	}
	target, health, policy, err := validateBasementIdentityTrustPolicyRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	startedAt := e.clock().UTC()
	if startedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement identity-trust executor clock returned zero time")
	}
	device, err := e.operations.EnforceDeviceSessionVerification(ctx, cloneBasementIdentityTrustRuntimePolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("enforce Basement device-session verification: %w", err)
	}
	if !validBasementIdentityTrustApplyObservation(device, policy.PolicyDigest) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("device verifier observation does not prove the exact policy")
	}
	human, err := e.operations.EnforceHumanSessionVerification(ctx, cloneBasementIdentityTrustRuntimePolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("enforce Basement human-session verification: %w", err)
	}
	if !validBasementIdentityTrustApplyObservation(human, policy.PolicyDigest) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("human verifier observation does not prove the exact policy")
	}
	workload, err := e.operations.EnforceWorkloadIdentityVerification(ctx, cloneBasementIdentityTrustRuntimePolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("enforce Basement workload-identity verification: %w", err)
	}
	if !validBasementIdentityTrustApplyObservation(workload, policy.PolicyDigest) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("workload verifier observation does not prove the exact policy")
	}
	expectation := BasementIdentityTrustVerifyExpectation{
		PolicyDigest: policy.PolicyDigest, StackID: policy.StackID,
		SiteRefs: append([]string(nil), policy.SiteRefs...), NodeRefs: append([]string(nil), policy.NodeRefs...),
		NotBefore: startedAt,
	}
	for _, verifier := range policy.Verifiers {
		expectation.VerifierIDs = append(expectation.VerifierIDs, verifier.ID)
	}
	verified, err := e.operations.VerifyBasementIdentityTrustPolicy(ctx, cloneBasementIdentityTrustVerifyExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify Basement identity-trust policy: %w", err)
	}
	if err := validateBasementIdentityTrustVerifyObservation(verified, expectation, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                                 `json:"schemaVersion"`
		Device        BasementIdentityTrustApplyObservation  `json:"device"`
		Human         BasementIdentityTrustApplyObservation  `json:"human"`
		Workload      BasementIdentityTrustApplyObservation  `json:"workload"`
		Verify        BasementIdentityTrustVerifyObservation `json:"verify"`
	}{"stackkit.basement-identity-trust-enforcement-evidence/v1", device, human, workload, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Basement identity-trust evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://basement-identity-trust-policy/" + target.InstanceRef, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://basement-identity-trust-policy/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

func validateBasementIdentityTrustPolicyRequest(request runtimeexecutor.ExecutionRequest, binding BasementIdentityTrustPolicyBinding, authority BasementIdentityTrustPolicyAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, BasementIdentityTrustRuntimePolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.Artifacts) != 1 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, BasementIdentityTrustRuntimePolicy{}, errors.New("Basement identity-trust executor requires exactly one runtime, one health target, one artifact, and no external access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.BasementIdentityTrustPolicyRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != basementIdentityTrustModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != basementIdentityTrustProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != basementIdentityTrustModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != basementIdentityTrustUnitRef || target.UnitContractHash != contract.ContractHash || target.InstanceRef != basementIdentityTrustInstanceRef ||
		target.RuntimeKind != "native" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" || target.ExecutionChannelRef != "" || target.WorkloadRef != "" || target.ImageRef != "" ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 || !slices.Equal(target.SiteRefs, binding.SiteRefs) ||
		!slices.Equal(target.NodeRefs, binding.NodeRefs) || !slices.Equal(target.ArtifactRefs, []string{basementIdentityTrustArtifactRef}) {
		return emptyTarget, emptyHealth, BasementIdentityTrustRuntimePolicy{}, errors.New("runtime target is not the exact bound Basement identity-trust contract")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != basementIdentityTrustHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != basementIdentityTrustModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, BasementIdentityTrustRuntimePolicy{}, errors.New("health target is not the exact Basement identity-trust enforcement postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != basementIdentityTrustArtifactRef || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" ||
		artifact.OwnerRef != basementIdentityTrustInstanceRef || artifact.OwnerContractHash != contract.ContractHash || artifact.ProviderRef != basementIdentityTrustProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
		artifact.ModuleRef != basementIdentityTrustModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash || artifact.UnitRef != basementIdentityTrustUnitRef || artifact.UnitContractHash != contract.ContractHash ||
		artifact.InstanceRef != basementIdentityTrustInstanceRef || artifact.OutputRef != basementIdentityTrustOutputRef || !slices.Equal(artifact.SiteRefs, binding.SiteRefs) || !slices.Equal(artifact.NodeRefs, binding.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > basementIdentityTrustMaxArtifactBytes {
		return emptyTarget, emptyHealth, BasementIdentityTrustRuntimePolicy{}, errors.New("artifact is not the exact CUE-owned Basement identity-trust policy")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, BasementIdentityTrustRuntimePolicy{}, errors.New("Basement identity-trust artifact digest does not match its immutable content")
	}
	projection, err := architecturev2renderer.ValidateBasementIdentityTrustPolicyArtifact(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, BasementIdentityTrustRuntimePolicy{}, fmt.Errorf("validate governed Basement identity-trust policy: %w", err)
	}
	if !slices.Equal(projection.SiteRefs, binding.SiteRefs) || !basementIdentityTrustVerifiersWithinBinding(projection.Verifiers, binding.SiteRefs) {
		return emptyTarget, emptyHealth, BasementIdentityTrustRuntimePolicy{}, errors.New("Basement identity-trust policy does not bind the exact authorized Home placement")
	}
	policyDigestInput, err := json.Marshal(struct {
		ArtifactDigest       string   `json:"artifactDigest"`
		ProviderContractHash string   `json:"providerContractHash"`
		ModuleContractHash   string   `json:"moduleContractHash"`
		HealthContractHash   string   `json:"healthContractHash"`
		SiteRefs             []string `json:"siteRefs"`
		NodeRefs             []string `json:"nodeRefs"`
	}{artifact.Digest, authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, binding.SiteRefs, binding.NodeRefs})
	if err != nil {
		return emptyTarget, emptyHealth, BasementIdentityTrustRuntimePolicy{}, fmt.Errorf("bind Basement identity-trust authority: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	return target, health, BasementIdentityTrustRuntimePolicy{
		PolicyDigest: "sha256:" + hex.EncodeToString(policyDigestBytes[:]), StackID: projection.StackID,
		SiteRefs: append([]string(nil), projection.SiteRefs...), NodeRefs: append([]string(nil), binding.NodeRefs...),
		Verifiers: cloneBasementIdentityTrustVerifiers(projection.Verifiers),
	}, nil
}

func basementIdentityTrustVerifiersWithinBinding(verifiers []architecturev2renderer.BasementIdentityTrustVerifier, siteRefs []string) bool {
	for _, verifier := range verifiers {
		if !slices.Equal(verifier.SiteRefs, siteRefs) {
			return false
		}
	}
	return true
}

func validBasementIdentityTrustApplyObservation(observation BasementIdentityTrustApplyObservation, policyDigest string) bool {
	return observation.PolicyDigest == policyDigest && observation.Status == "enforced"
}

func validateBasementIdentityTrustVerifyObservation(observation BasementIdentityTrustVerifyObservation, expectation BasementIdentityTrustVerifyExpectation, completedAt time.Time) error {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != "ready" || observation.DeviceVerifierStatus != "enforced" ||
		observation.HumanVerifierStatus != "enforced" || observation.WorkloadVerifierStatus != "enforced" {
		return errors.New("verification does not prove the exact enforced Basement identity-trust policy")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Location() != time.UTC || observedAt.Format(time.RFC3339Nano) != observation.ObservedAt || observedAt.Before(expectation.NotBefore) || observedAt.After(completedAt) {
		return errors.New("Basement identity-trust verification is not fresh at the exact invocation boundary")
	}
	return nil
}

func cloneBasementIdentityTrustRuntimePolicy(policy BasementIdentityTrustRuntimePolicy) BasementIdentityTrustRuntimePolicy {
	policy.SiteRefs = append([]string(nil), policy.SiteRefs...)
	policy.NodeRefs = append([]string(nil), policy.NodeRefs...)
	policy.Verifiers = cloneBasementIdentityTrustVerifiers(policy.Verifiers)
	return policy
}

func cloneBasementIdentityTrustVerifiers(verifiers []architecturev2renderer.BasementIdentityTrustVerifier) []architecturev2renderer.BasementIdentityTrustVerifier {
	cloned := append([]architecturev2renderer.BasementIdentityTrustVerifier(nil), verifiers...)
	for index := range cloned {
		cloned[index].Audiences = append([]string(nil), cloned[index].Audiences...)
		cloned[index].SiteRefs = append([]string(nil), cloned[index].SiteRefs...)
	}
	return cloned
}

func cloneBasementIdentityTrustVerifyExpectation(expectation BasementIdentityTrustVerifyExpectation) BasementIdentityTrustVerifyExpectation {
	expectation.SiteRefs = append([]string(nil), expectation.SiteRefs...)
	expectation.NodeRefs = append([]string(nil), expectation.NodeRefs...)
	expectation.VerifierIDs = append([]string(nil), expectation.VerifierIDs...)
	return expectation
}

var _ runtimeexecutor.Executor = (*BasementIdentityTrustPolicyExecutor)(nil)
