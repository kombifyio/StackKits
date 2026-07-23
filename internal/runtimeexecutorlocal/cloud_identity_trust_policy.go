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
	cloudIdentityTrustProviderRef      = "stackkits-cloud-identity-trust-policy"
	cloudIdentityTrustModuleRef        = "stackkits-cloud-identity-trust-policy-manifest"
	cloudIdentityTrustUnitRef          = "policy-bundle"
	cloudIdentityTrustArtifactRef      = "cloud-identity-trust-policy"
	cloudIdentityTrustOutputRef        = "cloud/identity/trust-policy.json"
	cloudIdentityTrustHealthSourceRef  = "cloud-identity-trust-enforcement"
	cloudIdentityTrustMaxArtifactBytes = 256 << 10
)

type CloudIdentityTrustPolicyBinding struct {
	SiteRefs            []string
	NodeRefs            []string
	ExecutionChannelRef string
}

type CloudIdentityTrustPolicyAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type CloudIdentityTrustRuntimePolicy struct {
	PolicyDigest string
	StackID      string
	SiteRefs     []string
	NodeRefs     []string
	Issuers      []architecturev2renderer.CloudIdentityTrustIssuer
	Verifiers    []architecturev2renderer.CloudIdentityTrustVerifier
}

type CloudIdentityTrustApplyObservation struct {
	PolicyDigest string `json:"policyDigest"`
	Status       string `json:"status"`
}

type CloudIdentityTrustVerifyExpectation struct {
	PolicyDigest string
	StackID      string
	SiteRefs     []string
	NodeRefs     []string
	IssuerIDs    []string
	VerifierIDs  []string
	NotBefore    time.Time
}

type CloudIdentityTrustVerifyObservation struct {
	PolicyDigest           string `json:"policyDigest"`
	Status                 string `json:"status"`
	HumanIssuerStatus      string `json:"humanIssuerStatus"`
	WorkloadIssuerStatus   string `json:"workloadIssuerStatus"`
	DeviceVerifierStatus   string `json:"deviceVerifierStatus"`
	HumanVerifierStatus    string `json:"humanVerifierStatus"`
	WorkloadVerifierStatus string `json:"workloadVerifierStatus"`
	ObservedAt             string `json:"observedAt"`
}

// CloudIdentityTrustPolicyOperations is the exact Cloud trust capability. It
// cannot enroll or issue device credentials and exposes no generic signing,
// key, credential, endpoint, provider, network, or lifecycle API.
type CloudIdentityTrustPolicyOperations interface {
	ConfigureHumanCredentialIssuer(context.Context, CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error)
	ConfigureWorkloadCredentialIssuer(context.Context, CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error)
	EnforceDeviceSessionVerification(context.Context, CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error)
	EnforceHumanSessionVerification(context.Context, CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error)
	EnforceWorkloadIdentityVerification(context.Context, CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error)
	VerifyCloudIdentityTrustPolicy(context.Context, CloudIdentityTrustVerifyExpectation) (CloudIdentityTrustVerifyObservation, error)
}

// CloudIdentityTrustPolicyExecutor is isolated from product registration until
// an authenticated backend and the matching CUE owner transition exist.
type CloudIdentityTrustPolicyExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    CloudIdentityTrustPolicyBinding
	authority  CloudIdentityTrustPolicyAuthority
	operations CloudIdentityTrustPolicyOperations
	clock      func() time.Time
}

func NewCloudIdentityTrustPolicyExecutor(identity runtimeexecutor.ExecutorIdentity, binding CloudIdentityTrustPolicyBinding, authority CloudIdentityTrustPolicyAuthority, operations CloudIdentityTrustPolicyOperations) *CloudIdentityTrustPolicyExecutor {
	return &CloudIdentityTrustPolicyExecutor{
		identity: identity,
		binding: CloudIdentityTrustPolicyBinding{
			SiteRefs: append([]string(nil), binding.SiteRefs...), NodeRefs: append([]string(nil), binding.NodeRefs...), ExecutionChannelRef: binding.ExecutionChannelRef,
		},
		authority: authority, operations: operations, clock: func() time.Time { return time.Now().UTC() },
	}
}

func (e *CloudIdentityTrustPolicyExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	return e.identity
}

func (e *CloudIdentityTrustPolicyExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud identity-trust executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || len(e.binding.SiteRefs) != 1 || len(e.binding.NodeRefs) != 1 ||
		!validExactRefSet(e.binding.SiteRefs) || !validExactRefSet(e.binding.NodeRefs) || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud identity-trust executor requires exact catalog authority, Cloud placement, and authenticated operations")
	}
	target, health, policy, err := validateCloudIdentityTrustPolicyRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	startedAt := e.clock().UTC()
	if startedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud identity-trust executor clock returned zero time")
	}
	type operation struct {
		name string
		run  func(context.Context, CloudIdentityTrustRuntimePolicy) (CloudIdentityTrustApplyObservation, error)
	}
	operations := []operation{
		{"configure human credential issuer", e.operations.ConfigureHumanCredentialIssuer},
		{"configure workload credential issuer", e.operations.ConfigureWorkloadCredentialIssuer},
		{"enforce device-session verification", e.operations.EnforceDeviceSessionVerification},
		{"enforce human-session verification", e.operations.EnforceHumanSessionVerification},
		{"enforce workload-identity verification", e.operations.EnforceWorkloadIdentityVerification},
	}
	observations := make([]CloudIdentityTrustApplyObservation, 0, len(operations))
	for _, operation := range operations {
		observation, err := operation.run(ctx, cloneCloudIdentityTrustRuntimePolicy(policy))
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s: %w", operation.name, err)
		}
		if observation.PolicyDigest != policy.PolicyDigest || observation.Status != "enforced" {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s observation does not prove the exact policy", operation.name)
		}
		observations = append(observations, observation)
	}
	expectation := CloudIdentityTrustVerifyExpectation{
		PolicyDigest: policy.PolicyDigest, StackID: policy.StackID, SiteRefs: append([]string(nil), policy.SiteRefs...),
		NodeRefs: append([]string(nil), policy.NodeRefs...), NotBefore: startedAt,
	}
	for _, issuer := range policy.Issuers {
		expectation.IssuerIDs = append(expectation.IssuerIDs, issuer.ID)
	}
	for _, verifier := range policy.Verifiers {
		expectation.VerifierIDs = append(expectation.VerifierIDs, verifier.ID)
	}
	verified, err := e.operations.VerifyCloudIdentityTrustPolicy(ctx, cloneCloudIdentityTrustVerifyExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify Cloud identity-trust policy: %w", err)
	}
	if err := validateCloudIdentityTrustVerifyObservation(verified, expectation, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                               `json:"schemaVersion"`
		Operations    []CloudIdentityTrustApplyObservation `json:"operations"`
		Verify        CloudIdentityTrustVerifyObservation  `json:"verify"`
	}{"stackkit.cloud-identity-trust-enforcement-evidence/v1", observations, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Cloud identity-trust evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://cloud-identity-trust-policy/" + target.InstanceRef, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://cloud-identity-trust-policy/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

func validateCloudIdentityTrustPolicyRequest(request runtimeexecutor.ExecutionRequest, binding CloudIdentityTrustPolicyBinding, authority CloudIdentityTrustPolicyAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, CloudIdentityTrustRuntimePolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.Artifacts) != 1 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, CloudIdentityTrustRuntimePolicy{}, errors.New("Cloud identity-trust executor requires exactly one runtime, one health target, one artifact, and no external access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.CloudIdentityTrustPolicyRendererContract()
	expectedInstanceRef := cloudIdentityTrustUnitRef + "-node-" + binding.NodeRefs[0]
	expectedArtifactRef := cloudIdentityTrustArtifactRef + "-instance-" + expectedInstanceRef
	if target.OwnerKind != "module" || target.OwnerRef != cloudIdentityTrustModuleRef || target.OwnerVersion != "" || target.ProviderRef != cloudIdentityTrustProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != cloudIdentityTrustModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != cloudIdentityTrustUnitRef || target.UnitContractHash != contract.ContractHash || target.InstanceRef != expectedInstanceRef || target.RuntimeKind != "native" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.ExecutionChannelRef != binding.ExecutionChannelRef || target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, binding.SiteRefs) || !slices.Equal(target.NodeRefs, binding.NodeRefs) || !slices.Equal(target.ArtifactRefs, []string{expectedArtifactRef}) {
		return emptyTarget, emptyHealth, CloudIdentityTrustRuntimePolicy{}, errors.New("runtime target is not the exact bound Cloud identity-trust contract")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != cloudIdentityTrustHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != cloudIdentityTrustModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, CloudIdentityTrustRuntimePolicy{}, errors.New("health target is not the exact Cloud identity-trust enforcement postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != expectedArtifactRef || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" ||
		artifact.OwnerRef != expectedInstanceRef || artifact.OwnerContractHash != contract.ContractHash || artifact.ProviderRef != cloudIdentityTrustProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
		artifact.ModuleRef != cloudIdentityTrustModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash || artifact.UnitRef != cloudIdentityTrustUnitRef || artifact.UnitContractHash != contract.ContractHash ||
		artifact.InstanceRef != expectedInstanceRef || artifact.OutputRef != cloudIdentityTrustOutputRef || !slices.Equal(artifact.SiteRefs, binding.SiteRefs) || !slices.Equal(artifact.NodeRefs, binding.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > cloudIdentityTrustMaxArtifactBytes {
		return emptyTarget, emptyHealth, CloudIdentityTrustRuntimePolicy{}, errors.New("artifact is not the exact CUE-owned Cloud identity-trust policy")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, CloudIdentityTrustRuntimePolicy{}, errors.New("Cloud identity-trust artifact digest does not match its immutable content")
	}
	projection, err := architecturev2renderer.ValidateCloudIdentityTrustPolicyArtifact(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, CloudIdentityTrustRuntimePolicy{}, fmt.Errorf("validate governed Cloud identity-trust policy: %w", err)
	}
	if projection.SiteRef != binding.SiteRefs[0] {
		return emptyTarget, emptyHealth, CloudIdentityTrustRuntimePolicy{}, errors.New("Cloud identity-trust policy does not bind the exact authorized Cloud placement")
	}
	policyDigestInput, err := json.Marshal(struct {
		ArtifactDigest       string   `json:"artifactDigest"`
		ProviderContractHash string   `json:"providerContractHash"`
		ModuleContractHash   string   `json:"moduleContractHash"`
		HealthContractHash   string   `json:"healthContractHash"`
		SiteRefs             []string `json:"siteRefs"`
		NodeRefs             []string `json:"nodeRefs"`
		ExecutionChannelRef  string   `json:"executionChannelRef"`
	}{artifact.Digest, authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, binding.SiteRefs, binding.NodeRefs, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, CloudIdentityTrustRuntimePolicy{}, fmt.Errorf("bind Cloud identity-trust authority: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	return target, health, CloudIdentityTrustRuntimePolicy{
		PolicyDigest: "sha256:" + hex.EncodeToString(policyDigestBytes[:]), StackID: projection.StackID,
		SiteRefs: []string{projection.SiteRef}, NodeRefs: append([]string(nil), binding.NodeRefs...),
		Issuers: cloneCloudIdentityTrustIssuers(projection.Issuers), Verifiers: cloneCloudIdentityTrustVerifiers(projection.Verifiers),
	}, nil
}

func validateCloudIdentityTrustVerifyObservation(observation CloudIdentityTrustVerifyObservation, expectation CloudIdentityTrustVerifyExpectation, completedAt time.Time) error {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != "ready" || observation.HumanIssuerStatus != "enforced" || observation.WorkloadIssuerStatus != "enforced" ||
		observation.DeviceVerifierStatus != "enforced" || observation.HumanVerifierStatus != "enforced" || observation.WorkloadVerifierStatus != "enforced" {
		return errors.New("verification does not prove the exact enforced Cloud identity-trust policy")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Location() != time.UTC || observedAt.Format(time.RFC3339Nano) != observation.ObservedAt || observedAt.Before(expectation.NotBefore) || observedAt.After(completedAt) {
		return errors.New("Cloud identity-trust verification is not fresh at the exact invocation boundary")
	}
	return nil
}

func cloneCloudIdentityTrustRuntimePolicy(policy CloudIdentityTrustRuntimePolicy) CloudIdentityTrustRuntimePolicy {
	policy.SiteRefs = append([]string(nil), policy.SiteRefs...)
	policy.NodeRefs = append([]string(nil), policy.NodeRefs...)
	policy.Issuers = cloneCloudIdentityTrustIssuers(policy.Issuers)
	policy.Verifiers = cloneCloudIdentityTrustVerifiers(policy.Verifiers)
	return policy
}

func cloneCloudIdentityTrustIssuers(issuers []architecturev2renderer.CloudIdentityTrustIssuer) []architecturev2renderer.CloudIdentityTrustIssuer {
	cloned := append([]architecturev2renderer.CloudIdentityTrustIssuer(nil), issuers...)
	for index := range cloned {
		cloned[index].Audiences = append([]string(nil), cloned[index].Audiences...)
	}
	return cloned
}

func cloneCloudIdentityTrustVerifiers(verifiers []architecturev2renderer.CloudIdentityTrustVerifier) []architecturev2renderer.CloudIdentityTrustVerifier {
	cloned := append([]architecturev2renderer.CloudIdentityTrustVerifier(nil), verifiers...)
	for index := range cloned {
		cloned[index].Audiences = append([]string(nil), cloned[index].Audiences...)
	}
	return cloned
}

func cloneCloudIdentityTrustVerifyExpectation(expectation CloudIdentityTrustVerifyExpectation) CloudIdentityTrustVerifyExpectation {
	expectation.SiteRefs = append([]string(nil), expectation.SiteRefs...)
	expectation.NodeRefs = append([]string(nil), expectation.NodeRefs...)
	expectation.IssuerIDs = append([]string(nil), expectation.IssuerIDs...)
	expectation.VerifierIDs = append([]string(nil), expectation.VerifierIDs...)
	return expectation
}

var _ runtimeexecutor.Executor = (*CloudIdentityTrustPolicyExecutor)(nil)
