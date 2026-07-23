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
	homeAccessProviderRef      = "stackkits-home-access-policy"
	homeAccessModuleRef        = "stackkits-home-access-policy-manifest"
	homeAccessUnitRef          = "policy-bundle"
	homeAccessArtifactRef      = "home-access-policy"
	homeAccessOutputRef        = "local/network/access-policy.json"
	homeAccessHealthSourceRef  = "home-access-enforcement"
	homeAccessMaxArtifactBytes = 256 << 10
)

// HomeAccessPolicyBinding is service-owned placement and execution-channel
// authority. It carries no endpoint, credential, transport configuration,
// discovery authority, provider handle, or provider lifecycle.
type HomeAccessPolicyBinding struct {
	SiteRefs            []string
	NodeRefs            []string
	ExecutionChannelRef string
}

// HomeAccessPolicyAuthority is selected from the service-owned catalog during
// adapter registration. Request bytes cannot define these hashes.
type HomeAccessPolicyAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

// HomeAccessRuntimePolicy is the complete secret-free policy handed to each
// closed enforcement operation.
type HomeAccessRuntimePolicy struct {
	PolicyDigest string
	StackID      string
	KitSlug      string
	SiteRefs     []string
	NodeRefs     []string
	Routes       []architecturev2renderer.HomeAccessEnforcementRoute
}

type HomeAccessApplyObservation struct {
	PolicyDigest string `json:"policyDigest"`
	Status       string `json:"status"`
}

type HomeAccessVerifyExpectation struct {
	PolicyDigest string
	StackID      string
	KitSlug      string
	SiteRefs     []string
	NodeRefs     []string
	RouteCount   int
	NotBefore    time.Time
}

type HomeAccessVerifyObservation struct {
	PolicyDigest           string `json:"policyDigest"`
	Status                 string `json:"status"`
	LANAccessStatus        string `json:"lanAccessStatus"`
	LocalIngressStatus     string `json:"localIngressStatus"`
	PrivilegedStepUpStatus string `json:"privilegedStepUpStatus"`
	ObservedAt             string `json:"observedAt"`
}

// HomeAccessPolicyOperations is the finite policy-enforcement capability. It
// exposes no generic command, raw firewall/router API, credential, endpoint,
// discovery, server-provider, or lifecycle operation.
type HomeAccessPolicyOperations interface {
	EnforceLANAccess(context.Context, HomeAccessRuntimePolicy) (HomeAccessApplyObservation, error)
	EnforceLocalIngress(context.Context, HomeAccessRuntimePolicy) (HomeAccessApplyObservation, error)
	EnforcePrivilegedStepUp(context.Context, HomeAccessRuntimePolicy) (HomeAccessApplyObservation, error)
	VerifyHomeAccessPolicy(context.Context, HomeAccessVerifyExpectation) (HomeAccessVerifyObservation, error)
}

// HomeAccessPolicyExecutor consumes one exact CUE-generated Home access policy.
// It remains outside product registration until an authenticated operations
// backend can enforce and read back all three responsibilities.
type HomeAccessPolicyExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    HomeAccessPolicyBinding
	authority  HomeAccessPolicyAuthority
	operations HomeAccessPolicyOperations
	clock      func() time.Time
}

func NewHomeAccessPolicyExecutor(identity runtimeexecutor.ExecutorIdentity, binding HomeAccessPolicyBinding, authority HomeAccessPolicyAuthority, operations HomeAccessPolicyOperations) *HomeAccessPolicyExecutor {
	return &HomeAccessPolicyExecutor{
		identity: identity,
		binding: HomeAccessPolicyBinding{
			SiteRefs:            append([]string(nil), binding.SiteRefs...),
			NodeRefs:            append([]string(nil), binding.NodeRefs...),
			ExecutionChannelRef: binding.ExecutionChannelRef,
		},
		authority: authority, operations: operations,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

func (e *HomeAccessPolicyExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *HomeAccessPolicyExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home access policy executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || len(e.binding.SiteRefs) != 1 || len(e.binding.NodeRefs) != 1 ||
		!validExactRefSet(e.binding.SiteRefs) || !validExactRefSet(e.binding.NodeRefs) || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home access policy executor requires exact catalog authority, placement, and authenticated operations")
	}
	target, health, policy, err := validateHomeAccessPolicyRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	startedAt := e.clock().UTC()
	if startedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home access policy executor clock returned zero time")
	}
	lan, err := e.operations.EnforceLANAccess(ctx, cloneHomeAccessRuntimePolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("enforce exact Home LAN access policy: %w", err)
	}
	if !validHomeAccessApplyObservation(lan, policy.PolicyDigest) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("LAN access observation does not prove the exact enforced policy")
	}
	ingress, err := e.operations.EnforceLocalIngress(ctx, cloneHomeAccessRuntimePolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("enforce exact Home local ingress policy: %w", err)
	}
	if !validHomeAccessApplyObservation(ingress, policy.PolicyDigest) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("local ingress observation does not prove the exact enforced policy")
	}
	stepUp, err := e.operations.EnforcePrivilegedStepUp(ctx, cloneHomeAccessRuntimePolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("enforce exact Home privileged step-up policy: %w", err)
	}
	if !validHomeAccessApplyObservation(stepUp, policy.PolicyDigest) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("privileged step-up observation does not prove the exact enforced policy")
	}
	expectation := HomeAccessVerifyExpectation{
		PolicyDigest: policy.PolicyDigest, StackID: policy.StackID, KitSlug: policy.KitSlug,
		SiteRefs: append([]string(nil), policy.SiteRefs...), NodeRefs: append([]string(nil), policy.NodeRefs...),
		RouteCount: len(policy.Routes), NotBefore: startedAt,
	}
	verified, err := e.operations.VerifyHomeAccessPolicy(ctx, cloneHomeAccessVerifyExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact Home access policy: %w", err)
	}
	completedAt := e.clock().UTC()
	if err := validateHomeAccessVerifyObservation(verified, expectation, completedAt); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                      `json:"schemaVersion"`
		LANAccess     HomeAccessApplyObservation  `json:"lanAccess"`
		LocalIngress  HomeAccessApplyObservation  `json:"localIngress"`
		StepUp        HomeAccessApplyObservation  `json:"privilegedStepUp"`
		Verify        HomeAccessVerifyObservation `json:"verify"`
	}{"stackkit.home-access-enforcement-evidence/v1", lan, ingress, stepUp, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Home access enforcement evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://home-access-policy/" + target.InstanceRef, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://home-access-policy/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

func validateHomeAccessPolicyRequest(request runtimeexecutor.ExecutionRequest, binding HomeAccessPolicyBinding, authority HomeAccessPolicyAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, HomeAccessRuntimePolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.Artifacts) != 1 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, HomeAccessRuntimePolicy{}, errors.New("Home access policy executor requires exactly one runtime, one health target, one artifact, and no external access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.HomeAccessPolicyRendererContract()
	expectedInstanceRef := homeAccessUnitRef + "-node-" + binding.NodeRefs[0]
	expectedArtifactRef := homeAccessArtifactRef + "-instance-" + expectedInstanceRef
	if target.OwnerKind != "module" || target.OwnerRef != homeAccessModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != homeAccessProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != homeAccessModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != homeAccessUnitRef || target.UnitContractHash != contract.ContractHash || target.InstanceRef != expectedInstanceRef ||
		target.RuntimeKind != "native" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" || target.ExecutionChannelRef != binding.ExecutionChannelRef ||
		target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, binding.SiteRefs) || !slices.Equal(target.NodeRefs, binding.NodeRefs) || !slices.Equal(target.ArtifactRefs, []string{expectedArtifactRef}) {
		return emptyTarget, emptyHealth, HomeAccessRuntimePolicy{}, errors.New("runtime target is not the exact bound Home access policy contract")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != homeAccessHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != homeAccessModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, HomeAccessRuntimePolicy{}, errors.New("health target is not the exact Home access enforcement postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != expectedArtifactRef || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != expectedInstanceRef || artifact.OwnerContractHash != contract.ContractHash ||
		artifact.ProviderRef != homeAccessProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
		artifact.ModuleRef != homeAccessModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash || artifact.UnitRef != homeAccessUnitRef || artifact.UnitContractHash != contract.ContractHash ||
		artifact.InstanceRef != expectedInstanceRef || artifact.OutputRef != homeAccessOutputRef || !slices.Equal(artifact.SiteRefs, binding.SiteRefs) || !slices.Equal(artifact.NodeRefs, binding.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > homeAccessMaxArtifactBytes {
		return emptyTarget, emptyHealth, HomeAccessRuntimePolicy{}, errors.New("artifact is not the exact CUE-owned Home access policy")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, HomeAccessRuntimePolicy{}, errors.New("Home access policy artifact digest does not match its immutable content")
	}
	projection, err := architecturev2renderer.ValidateHomeAccessPolicyArtifact(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, HomeAccessRuntimePolicy{}, fmt.Errorf("validate governed Home access policy: %w", err)
	}
	localRoutes, err := homeAccessRoutesForBinding(projection.Routes, binding)
	if !slices.Equal(projection.SiteRefs, binding.SiteRefs) || err != nil {
		return emptyTarget, emptyHealth, HomeAccessRuntimePolicy{}, errors.New("Home access policy does not bind the exact authorized Home placement")
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
		return emptyTarget, emptyHealth, HomeAccessRuntimePolicy{}, fmt.Errorf("bind Home access policy authority: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	policy := HomeAccessRuntimePolicy{
		PolicyDigest: "sha256:" + hex.EncodeToString(policyDigestBytes[:]), StackID: projection.StackID, KitSlug: projection.KitSlug,
		SiteRefs: append([]string(nil), projection.SiteRefs...), NodeRefs: append([]string(nil), binding.NodeRefs...), Routes: localRoutes,
	}
	return target, health, policy, nil
}

func validExactRefSet(refs []string) bool {
	if len(refs) == 0 || !slices.IsSorted(refs) {
		return false
	}
	for index, ref := range refs {
		if strings.TrimSpace(ref) == "" || ref != strings.TrimSpace(ref) || index > 0 && refs[index-1] == ref {
			return false
		}
	}
	return true
}

func homeAccessRoutesForBinding(routes []architecturev2renderer.HomeAccessEnforcementRoute, binding HomeAccessPolicyBinding) ([]architecturev2renderer.HomeAccessEnforcementRoute, error) {
	local := make([]architecturev2renderer.HomeAccessEnforcementRoute, 0, len(routes))
	for _, route := range routes {
		if !slices.Contains(binding.SiteRefs, route.OriginSiteRef) {
			return nil, errors.New("route origin exceeds the bound Home Site")
		}
		if !slices.Contains(route.OriginNodeRefs, binding.NodeRefs[0]) {
			continue
		}
		copy := route
		copy.OriginNodeRefs = append([]string(nil), binding.NodeRefs...)
		local = append(local, copy)
	}
	return local, nil
}

func validHomeAccessApplyObservation(observation HomeAccessApplyObservation, policyDigest string) bool {
	return observation.PolicyDigest == policyDigest && observation.Status == "enforced"
}

func validateHomeAccessVerifyObservation(observation HomeAccessVerifyObservation, expectation HomeAccessVerifyExpectation, completedAt time.Time) error {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != "ready" || observation.LANAccessStatus != "enforced" ||
		observation.LocalIngressStatus != "enforced" || observation.PrivilegedStepUpStatus != "enforced" {
		return errors.New("verification does not prove the exact enforced Home access policy")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Location() != time.UTC || observedAt.Format(time.RFC3339Nano) != observation.ObservedAt ||
		observedAt.Before(expectation.NotBefore) || observedAt.After(completedAt) {
		return errors.New("Home access verification is not fresh at the exact invocation boundary")
	}
	return nil
}

func cloneHomeAccessRuntimePolicy(policy HomeAccessRuntimePolicy) HomeAccessRuntimePolicy {
	policy.SiteRefs = append([]string(nil), policy.SiteRefs...)
	policy.NodeRefs = append([]string(nil), policy.NodeRefs...)
	policy.Routes = cloneHomeAccessRoutes(policy.Routes)
	return policy
}

func cloneHomeAccessRoutes(routes []architecturev2renderer.HomeAccessEnforcementRoute) []architecturev2renderer.HomeAccessEnforcementRoute {
	cloned := append([]architecturev2renderer.HomeAccessEnforcementRoute(nil), routes...)
	for index := range cloned {
		cloned[index].OriginNodeRefs = append([]string(nil), cloned[index].OriginNodeRefs...)
		cloned[index].AllowedSiteRefs = append([]string(nil), cloned[index].AllowedSiteRefs...)
		cloned[index].AllowedMethods = append([]string(nil), cloned[index].AllowedMethods...)
	}
	return cloned
}

func cloneHomeAccessVerifyExpectation(expectation HomeAccessVerifyExpectation) HomeAccessVerifyExpectation {
	expectation.SiteRefs = append([]string(nil), expectation.SiteRefs...)
	expectation.NodeRefs = append([]string(nil), expectation.NodeRefs...)
	return expectation
}

var _ runtimeexecutor.Executor = (*HomeAccessPolicyExecutor)(nil)
