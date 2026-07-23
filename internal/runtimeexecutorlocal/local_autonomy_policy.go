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
	localAutonomyProviderRef      = "stackkits-local-autonomy-policy"
	localAutonomyModuleRef        = "stackkits-local-autonomy-policy-manifest"
	localAutonomyUnitRef          = "policy-bundle"
	localAutonomyArtifactRef      = "local-autonomy-policy"
	localAutonomyOutputRef        = "local/autonomy/policy.json"
	localAutonomyHealthSourceRef  = "local-autonomy-enforcement"
	localAutonomyMaxArtifactBytes = 256 << 10
)

type LocalAutonomyPolicyBinding struct {
	HomeSiteRefs        []string
	NodeRefs            []string
	ExecutionChannelRef string
}

type LocalAutonomyPolicyAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type LocalAutonomyRuntimePolicy struct {
	PolicyDigest string
	Policy       architecturev2renderer.LocalAutonomyEnforcementPolicy
	NodeRefs     []string
}

type LocalAutonomyApplyObservation struct {
	PolicyDigest string `json:"policyDigest"`
	Status       string `json:"status"`
}

type LocalAutonomyVerifyExpectation struct {
	PolicyDigest    string
	StackID         string
	KitSlug         string
	HomeSiteRefs    []string
	CloudSiteRefs   []string
	ControlMembers  []string
	OnLinkLoss      string
	OnCloudLoss     string
	CloudEdge       string
	DenyCrossSite   bool
	MaxStaleSeconds int
	NotBefore       time.Time
}

type LocalAutonomyVerifyObservation struct {
	PolicyDigest           string `json:"policyDigest"`
	Status                 string `json:"status"`
	CrossSiteSessionStatus string `json:"crossSiteSessionStatus"`
	LinkLossStatus         string `json:"linkLossStatus"`
	LocalControlStatus     string `json:"localControlStatus"`
	ObservedAt             string `json:"observedAt"`
}

// LocalAutonomyPolicyOperations is intentionally limited to the three exact
// enforcement responsibilities declared by CUE plus their readback. It owns no
// generic network, credential, provider, endpoint, tunnel, or lifecycle API.
type LocalAutonomyPolicyOperations interface {
	DenyForbiddenCrossSiteSessions(context.Context, LocalAutonomyRuntimePolicy) (LocalAutonomyApplyObservation, error)
	EnforceLinkLossPolicy(context.Context, LocalAutonomyRuntimePolicy) (LocalAutonomyApplyObservation, error)
	PreserveLocalControl(context.Context, LocalAutonomyRuntimePolicy) (LocalAutonomyApplyObservation, error)
	VerifyLocalAutonomyPolicy(context.Context, LocalAutonomyVerifyExpectation) (LocalAutonomyVerifyObservation, error)
}

// LocalAutonomyPolicyExecutor is an isolated adapter proof. Product Apply stays
// blocked until an authenticated implementation of LocalAutonomyPolicyOperations
// is registered together with the corresponding CUE owner transition.
type LocalAutonomyPolicyExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalAutonomyPolicyBinding
	authority  LocalAutonomyPolicyAuthority
	operations LocalAutonomyPolicyOperations
	clock      func() time.Time
}

func NewLocalAutonomyPolicyExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalAutonomyPolicyBinding, authority LocalAutonomyPolicyAuthority, operations LocalAutonomyPolicyOperations) *LocalAutonomyPolicyExecutor {
	return &LocalAutonomyPolicyExecutor{
		identity: identity,
		binding: LocalAutonomyPolicyBinding{
			HomeSiteRefs:        append([]string(nil), binding.HomeSiteRefs...),
			NodeRefs:            append([]string(nil), binding.NodeRefs...),
			ExecutionChannelRef: binding.ExecutionChannelRef,
		},
		authority: authority, operations: operations,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

func (e *LocalAutonomyPolicyExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *LocalAutonomyPolicyExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("local-autonomy executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || len(e.binding.HomeSiteRefs) != 1 || len(e.binding.NodeRefs) != 1 ||
		!validExactRefSet(e.binding.HomeSiteRefs) || !validExactRefSet(e.binding.NodeRefs) || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("local-autonomy executor requires exact catalog authority, Home control placement, and authenticated operations")
	}
	target, health, policy, err := validateLocalAutonomyPolicyRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	startedAt := e.clock().UTC()
	if startedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("local-autonomy executor clock returned zero time")
	}
	crossSite, err := e.operations.DenyForbiddenCrossSiteSessions(ctx, cloneLocalAutonomyRuntimePolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("deny forbidden cross-Site sessions: %w", err)
	}
	if !validLocalAutonomyApplyObservation(crossSite, policy.PolicyDigest, "enforced") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("cross-Site observation does not prove the exact deny policy")
	}
	linkLoss, err := e.operations.EnforceLinkLossPolicy(ctx, cloneLocalAutonomyRuntimePolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("enforce exact link-loss policy: %w", err)
	}
	if !validLocalAutonomyApplyObservation(linkLoss, policy.PolicyDigest, "enforced") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("link-loss observation does not prove the exact policy")
	}
	localControl, err := e.operations.PreserveLocalControl(ctx, cloneLocalAutonomyRuntimePolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("preserve exact Home local control: %w", err)
	}
	if !validLocalAutonomyApplyObservation(localControl, policy.PolicyDigest, "preserved") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("local-control observation does not prove the exact preserved policy")
	}
	expectation := LocalAutonomyVerifyExpectation{
		PolicyDigest: policy.PolicyDigest, StackID: policy.Policy.StackID, KitSlug: policy.Policy.KitSlug,
		HomeSiteRefs: append([]string(nil), policy.Policy.HomeSiteRefs...), CloudSiteRefs: append([]string(nil), policy.Policy.CloudSiteRefs...),
		ControlMembers: append([]string(nil), policy.Policy.ControlMembers...), OnLinkLoss: policy.Policy.OnLinkLoss,
		OnCloudLoss: policy.Policy.OnCloudLoss, CloudEdge: policy.Policy.CloudEdge, DenyCrossSite: policy.Policy.DenyNewCrossSiteSessions,
		MaxStaleSeconds: policy.Policy.MaxStaleVerificationSeconds, NotBefore: startedAt,
	}
	verified, err := e.operations.VerifyLocalAutonomyPolicy(ctx, cloneLocalAutonomyVerifyExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact local-autonomy policy: %w", err)
	}
	if err := validateLocalAutonomyVerifyObservation(verified, expectation, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                         `json:"schemaVersion"`
		CrossSite     LocalAutonomyApplyObservation  `json:"crossSiteSessions"`
		LinkLoss      LocalAutonomyApplyObservation  `json:"linkLoss"`
		LocalControl  LocalAutonomyApplyObservation  `json:"localControl"`
		Verify        LocalAutonomyVerifyObservation `json:"verify"`
	}{"stackkit.local-autonomy-enforcement-evidence/v1", crossSite, linkLoss, localControl, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal local-autonomy enforcement evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://local-autonomy-policy/" + target.InstanceRef, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://local-autonomy-policy/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

func validateLocalAutonomyPolicyRequest(request runtimeexecutor.ExecutionRequest, binding LocalAutonomyPolicyBinding, authority LocalAutonomyPolicyAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, LocalAutonomyRuntimePolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.Artifacts) != 1 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, LocalAutonomyRuntimePolicy{}, errors.New("local-autonomy executor requires exactly one runtime, one health target, one artifact, and no external access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.LocalAutonomyPolicyRendererContract()
	expectedInstanceRef := localAutonomyUnitRef + "-node-" + binding.NodeRefs[0]
	expectedArtifactRef := localAutonomyArtifactRef + "-instance-" + expectedInstanceRef
	if target.OwnerKind != "module" || target.OwnerRef != localAutonomyModuleRef || target.OwnerVersion != "" || target.ProviderRef != localAutonomyProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != localAutonomyModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != localAutonomyUnitRef || target.UnitContractHash != contract.ContractHash || target.InstanceRef != expectedInstanceRef ||
		target.RuntimeKind != "native" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" || target.ExecutionChannelRef != binding.ExecutionChannelRef || target.WorkloadRef != "" || target.ImageRef != "" ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 || !slices.Equal(target.SiteRefs, binding.HomeSiteRefs) ||
		!slices.Equal(target.NodeRefs, binding.NodeRefs) || !slices.Equal(target.ArtifactRefs, []string{expectedArtifactRef}) {
		return emptyTarget, emptyHealth, LocalAutonomyRuntimePolicy{}, errors.New("runtime target is not the exact bound local-autonomy policy contract")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != localAutonomyHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != localAutonomyModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, LocalAutonomyRuntimePolicy{}, errors.New("health target is not the exact local-autonomy enforcement postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != expectedArtifactRef || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" ||
		artifact.OwnerRef != expectedInstanceRef || artifact.OwnerContractHash != contract.ContractHash || artifact.ProviderRef != localAutonomyProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
		artifact.ModuleRef != localAutonomyModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash || artifact.UnitRef != localAutonomyUnitRef || artifact.UnitContractHash != contract.ContractHash ||
		artifact.InstanceRef != expectedInstanceRef || artifact.OutputRef != localAutonomyOutputRef || !slices.Equal(artifact.SiteRefs, binding.HomeSiteRefs) || !slices.Equal(artifact.NodeRefs, binding.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > localAutonomyMaxArtifactBytes {
		return emptyTarget, emptyHealth, LocalAutonomyRuntimePolicy{}, errors.New("artifact is not the exact CUE-owned local-autonomy policy")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, LocalAutonomyRuntimePolicy{}, errors.New("local-autonomy artifact digest does not match its immutable content")
	}
	projection, err := architecturev2renderer.ValidateLocalAutonomyPolicyArtifact(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, LocalAutonomyRuntimePolicy{}, fmt.Errorf("validate governed local-autonomy policy: %w", err)
	}
	if !slices.Equal(projection.HomeSiteRefs, binding.HomeSiteRefs) || projection.AuthoritySiteRef != binding.HomeSiteRefs[0] || !slices.Contains(projection.ControlMembers, binding.NodeRefs[0]) ||
		projection.HumanAuthoritySiteRef != projection.AuthoritySiteRef || projection.DeviceAuthoritySiteRef != projection.AuthoritySiteRef || !projection.LocalIdentityAuthorityAvailable || !projection.DenyNewCrossSiteSessions {
		return emptyTarget, emptyHealth, LocalAutonomyRuntimePolicy{}, errors.New("local-autonomy policy does not bind the exact Home control authority")
	}
	policyDigestInput, err := json.Marshal(struct {
		ArtifactDigest       string   `json:"artifactDigest"`
		ProviderContractHash string   `json:"providerContractHash"`
		ModuleContractHash   string   `json:"moduleContractHash"`
		HealthContractHash   string   `json:"healthContractHash"`
		HomeSiteRefs         []string `json:"homeSiteRefs"`
		NodeRefs             []string `json:"nodeRefs"`
		ExecutionChannelRef  string   `json:"executionChannelRef"`
	}{artifact.Digest, authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, binding.HomeSiteRefs, binding.NodeRefs, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, LocalAutonomyRuntimePolicy{}, fmt.Errorf("bind local-autonomy policy authority: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	return target, health, LocalAutonomyRuntimePolicy{
		PolicyDigest: "sha256:" + hex.EncodeToString(policyDigestBytes[:]), Policy: cloneLocalAutonomyEnforcementPolicy(projection), NodeRefs: append([]string(nil), binding.NodeRefs...),
	}, nil
}

func validLocalAutonomyApplyObservation(observation LocalAutonomyApplyObservation, digest, status string) bool {
	return observation.PolicyDigest == digest && observation.Status == status
}

func validateLocalAutonomyVerifyObservation(observation LocalAutonomyVerifyObservation, expectation LocalAutonomyVerifyExpectation, completedAt time.Time) error {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != "ready" || observation.CrossSiteSessionStatus != "denied" ||
		observation.LinkLossStatus != "enforced" || observation.LocalControlStatus != "preserved" {
		return errors.New("verification does not prove the exact enforced local-autonomy policy")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Location() != time.UTC || observedAt.Format(time.RFC3339Nano) != observation.ObservedAt ||
		observedAt.Before(expectation.NotBefore) || observedAt.After(completedAt) {
		return errors.New("local-autonomy verification is not fresh at the exact invocation boundary")
	}
	return nil
}

func cloneLocalAutonomyRuntimePolicy(policy LocalAutonomyRuntimePolicy) LocalAutonomyRuntimePolicy {
	policy.Policy = cloneLocalAutonomyEnforcementPolicy(policy.Policy)
	policy.NodeRefs = append([]string(nil), policy.NodeRefs...)
	return policy
}

func cloneLocalAutonomyEnforcementPolicy(policy architecturev2renderer.LocalAutonomyEnforcementPolicy) architecturev2renderer.LocalAutonomyEnforcementPolicy {
	policy.HomeSiteRefs = append([]string(nil), policy.HomeSiteRefs...)
	policy.CloudSiteRefs = append([]string(nil), policy.CloudSiteRefs...)
	policy.ControlMembers = append([]string(nil), policy.ControlMembers...)
	policy.EdgeVerifierSiteRefs = append([]string(nil), policy.EdgeVerifierSiteRefs...)
	policy.DataBindings = append([]architecturev2renderer.LocalAutonomyEnforcementDataBinding(nil), policy.DataBindings...)
	for index := range policy.DataBindings {
		policy.DataBindings[index].Classes = append([]string(nil), policy.DataBindings[index].Classes...)
		policy.DataBindings[index].ReplicaSiteRefs = append([]string(nil), policy.DataBindings[index].ReplicaSiteRefs...)
		policy.DataBindings[index].AllowedClasses = append([]string(nil), policy.DataBindings[index].AllowedClasses...)
	}
	return policy
}

func cloneLocalAutonomyVerifyExpectation(expectation LocalAutonomyVerifyExpectation) LocalAutonomyVerifyExpectation {
	expectation.HomeSiteRefs = append([]string(nil), expectation.HomeSiteRefs...)
	expectation.CloudSiteRefs = append([]string(nil), expectation.CloudSiteRefs...)
	expectation.ControlMembers = append([]string(nil), expectation.ControlMembers...)
	return expectation
}

var _ runtimeexecutor.Executor = (*LocalAutonomyPolicyExecutor)(nil)
