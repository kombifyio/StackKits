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
	cloudPublicEdgeProviderRef       = "stackkits-cloud-public-edge"
	cloudPublicEdgeModuleRef         = "stackkits-cloud-public-edge-runtime"
	cloudPublicEdgeUnitRef           = "executor-contract"
	cloudPublicEdgeOutputRef         = "cloud/public-edge/executor-contract.json"
	cloudPublicEdgeArtifactPrefix    = "cloud-public-edge-executor-contract-instance-"
	cloudPublicEdgeHealthSourceRef   = "cloud-public-edge-health"
	cloudPublicEdgeMaxArtifactBytes  = 512 << 10
	cloudPublicEdgeMaxObservationAge = 5 * time.Minute
)

type CloudPublicEdgeApplyPolicy struct {
	PolicyDigest        string                                        `json:"policyDigest"`
	RequestDigest       string                                        `json:"requestDigest"`
	ArtifactDigest      string                                        `json:"artifactDigest"`
	StateDigest         string                                        `json:"stateDigest"`
	EvaluatedAt         string                                        `json:"evaluatedAt"`
	StackID             string                                        `json:"stackId"`
	SiteRef             string                                        `json:"siteRef"`
	NodeRef             string                                        `json:"nodeRef"`
	ExecutionChannelRef string                                        `json:"executionChannelRef"`
	NetworkMode         string                                        `json:"networkMode"`
	TransportSubnet     string                                        `json:"transportSubnet"`
	IPv6                bool                                          `json:"ipv6"`
	TLSMinVersion       string                                        `json:"tlsMinVersion"`
	ParentRulesetRef    string                                        `json:"parentRulesetRef"`
	DelegatedChainRef   string                                        `json:"delegatedChainRef"`
	Routes              []architecturev2renderer.CloudPublicEdgeRoute `json:"routes"`
}

type CloudPublicEdgeExpectation struct {
	PolicyDigest        string   `json:"policyDigest"`
	RequestDigest       string   `json:"requestDigest"`
	ArtifactDigest      string   `json:"artifactDigest"`
	StateDigest         string   `json:"stateDigest"`
	EvaluatedAt         string   `json:"evaluatedAt"`
	StackID             string   `json:"stackId"`
	SiteRef             string   `json:"siteRef"`
	NodeRef             string   `json:"nodeRef"`
	ExecutionChannelRef string   `json:"executionChannelRef"`
	ParentRulesetRef    string   `json:"parentRulesetRef"`
	DelegatedChainRef   string   `json:"delegatedChainRef"`
	RouteRefs           []string `json:"routeRefs"`
	BackendPoolRefs     []string `json:"backendPoolRefs"`
	HealthGateRefs      []string `json:"healthGateRefs"`
}

type CloudPublicEdgeObservation struct {
	Operation           string   `json:"operation"`
	PolicyDigest        string   `json:"policyDigest"`
	RequestDigest       string   `json:"requestDigest"`
	ArtifactDigest      string   `json:"artifactDigest"`
	StateDigest         string   `json:"stateDigest"`
	StackID             string   `json:"stackId"`
	SiteRef             string   `json:"siteRef"`
	NodeRef             string   `json:"nodeRef"`
	ExecutionChannelRef string   `json:"executionChannelRef"`
	EvaluatedAt         string   `json:"evaluatedAt"`
	ObservedAt          string   `json:"observedAt"`
	ParentRulesetRef    string   `json:"parentRulesetRef"`
	DelegatedChainRef   string   `json:"delegatedChainRef"`
	Status              string   `json:"status"`
	RouteRefs           []string `json:"routeRefs"`
	BackendPoolRefs     []string `json:"backendPoolRefs"`
	HealthGateRefs      []string `json:"healthGateRefs"`
	DefaultClosed       bool     `json:"defaultClosed"`
	UnauthorizedRoutes  int      `json:"unauthorizedRoutes"`
}

type CloudPublicEdgeEvidence struct {
	SchemaVersion  string                     `json:"schemaVersion"`
	RequestDigest  string                     `json:"requestDigest"`
	ArtifactDigest string                     `json:"artifactDigest"`
	PolicyDigest   string                     `json:"policyDigest"`
	StateDigest    string                     `json:"stateDigest"`
	EvaluatedAt    string                     `json:"evaluatedAt"`
	Apply          CloudPublicEdgeObservation `json:"apply"`
	Reconcile      CloudPublicEdgeObservation `json:"reconcile"`
	Verify         CloudPublicEdgeObservation `json:"verify"`
}

type CloudPublicEdgeEvidenceReceipt struct {
	EvidenceDigest string `json:"evidenceDigest"`
	CommittedAt    string `json:"committedAt"`
}

// CloudPublicEdgeOperations is owned by an authenticated Cloud host channel.
// It exposes exact edge-policy reconciliation and durable evidence custody,
// never provider resources, DNS mutation, certificate issuance or secrets.
type CloudPublicEdgeOperations interface {
	ApplyPublicEdge(context.Context, CloudPublicEdgeApplyPolicy) (CloudPublicEdgeObservation, error)
	RemoveObsoletePublicEdge(context.Context, CloudPublicEdgeExpectation) (CloudPublicEdgeObservation, error)
	VerifyPublicEdge(context.Context, CloudPublicEdgeExpectation) (CloudPublicEdgeObservation, error)
	CommitEvidence(context.Context, CloudPublicEdgeEvidence) (CloudPublicEdgeEvidenceReceipt, error)
}

type CloudPublicEdgeAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type CloudPublicEdgeExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  CloudPublicEdgeAuthority
	operations CloudPublicEdgeOperations
	clock      func() time.Time
}

func NewCloudPublicEdgeExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudPublicEdgeAuthority, operations CloudPublicEdgeOperations) *CloudPublicEdgeExecutor {
	return NewCloudPublicEdgeExecutorWithClock(identity, binding, authority, operations, time.Now)
}

func NewCloudPublicEdgeExecutorWithClock(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudPublicEdgeAuthority, operations CloudPublicEdgeOperations, now func() time.Time) *CloudPublicEdgeExecutor {
	return &CloudPublicEdgeExecutor{identity: identity, binding: binding, authority: authority, operations: operations, clock: now}
}

func (e *CloudPublicEdgeExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *CloudPublicEdgeExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud public-edge executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud public-edge executor requires one explicit authenticated Cloud target binding")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed Cloud public-edge request: %w", err)
	}
	target, health, policy, expectation, err := validateCloudPublicEdgeRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evaluatedAt := e.clock().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud public-edge executor clock returned zero time")
	}
	policy.RequestDigest, policy.EvaluatedAt = request.RequestDigest, evaluatedAt.Format(time.RFC3339Nano)
	expectation.RequestDigest, expectation.EvaluatedAt = request.RequestDigest, policy.EvaluatedAt

	applyPolicy, err := cloneCloudPublicEdgePolicy(policy)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	applyObservation, err := e.operations.ApplyPublicEdge(ctx, applyPolicy)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Cloud public-edge policy: %w", err)
	}
	applyAt, err := validateCloudPublicEdgeObservation(applyObservation, expectation, "apply-public-edge", "applied", false, evaluatedAt, evaluatedAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	reconcileObservation, err := e.operations.RemoveObsoletePublicEdge(ctx, cloneCloudPublicEdgeExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("remove obsolete Cloud public-edge routes: %w", err)
	}
	reconcileAt, err := validateCloudPublicEdgeObservation(reconcileObservation, expectation, "remove-obsolete-public-edge", "reconciled", false, evaluatedAt, applyAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	verifyObservation, err := e.operations.VerifyPublicEdge(ctx, cloneCloudPublicEdgeExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact Cloud public-edge policy: %w", err)
	}
	verifyAt, err := validateCloudPublicEdgeObservation(verifyObservation, expectation, "verify-public-edge", "ready", true, evaluatedAt, reconcileAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidenceRecord := CloudPublicEdgeEvidence{
		SchemaVersion: "stackkit.cloud-public-edge-evidence/v2", RequestDigest: request.RequestDigest,
		ArtifactDigest: expectation.ArtifactDigest, PolicyDigest: expectation.PolicyDigest, StateDigest: expectation.StateDigest,
		EvaluatedAt: expectation.EvaluatedAt, Apply: applyObservation, Reconcile: reconcileObservation, Verify: verifyObservation,
	}
	evidence, err := json.Marshal(evidenceRecord)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Cloud public-edge evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	receipt, err := e.operations.CommitEvidence(ctx, cloneCloudPublicEdgeEvidence(evidenceRecord))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("commit exact Cloud public-edge evidence: %w", err)
	}
	if _, err := validateCloudPublicEdgeReceipt(receipt, digestString, evaluatedAt, verifyAt, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	refDigest := strings.TrimPrefix(digestString, "sha256:")
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://cloud-public-edge/" + refDigest, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://cloud-public-edge/" + refDigest, ObservationDigest: digestString}},
	}, nil
}

func validateCloudPublicEdgeRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority CloudPublicEdgeAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, CloudPublicEdgeApplyPolicy, CloudPublicEdgeExpectation, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if !validCoreHostBootstrapDigest(request.RequestDigest) || len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, errors.New("Cloud public-edge executor requires exactly one runtime, one health target, one artifact, and no access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.CloudPublicEdgeExecutorBundleRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != cloudPublicEdgeModuleRef || target.OwnerVersion != "" || target.ProviderRef != cloudPublicEdgeProviderRef ||
		target.ProviderContractHash != authority.ProviderContractHash || target.ModuleRef != cloudPublicEdgeModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != cloudPublicEdgeUnitRef || target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" ||
		target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, errors.New("runtime target is not the exact bound Cloud public-edge contract")
	}
	wantInstance := cloudPublicEdgeUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := cloudPublicEdgeArtifactPrefix + wantInstance
	wantRequirementID := cloudPublicEdgeModuleRef + "/" + cloudPublicEdgeUnitRef + "/" + wantInstance
	if target.RequirementID != wantRequirementID || target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, errors.New("runtime target does not bind the exact node-local Cloud public-edge artifact")
	}
	health := request.HealthTargets[0]
	wantHealthRequirementID := "module-" + cloudPublicEdgeModuleRef + "-" + cloudPublicEdgeHealthSourceRef + "-node-" + binding.NodeRef
	if health.RequirementID != wantHealthRequirementID || health.SourceRef != cloudPublicEdgeHealthSourceRef || health.ContractHash != authority.HealthContractHash ||
		health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" || health.TargetRef != cloudPublicEdgeModuleRef ||
		health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, errors.New("health target is not the exact Cloud public-edge postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != wantArtifactID || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != cloudPublicEdgeProviderRef || artifact.ProviderContractHash != target.ProviderContractHash ||
		artifact.ModuleRef != cloudPublicEdgeModuleRef || artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != cloudPublicEdgeUnitRef || artifact.UnitContractHash != target.UnitContractHash ||
		artifact.InstanceRef != wantInstance || artifact.OutputRef != cloudPublicEdgeOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > cloudPublicEdgeMaxArtifactBytes {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, errors.New("artifact is not the exact CUE-owned Cloud public-edge instance")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, errors.New("Cloud public-edge artifact digest does not match its immutable content")
	}
	governed, err := architecturev2renderer.ValidateCloudPublicEdgeExecutorArtifact(artifact.Content, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, fmt.Errorf("validate governed Cloud public-edge policy: %w", err)
	}
	policyDigest, err := digestCloudPublicEdge(struct {
		ArtifactDigest, RequestDigest, ProviderContractHash, ModuleContractHash, HealthContractHash string
		SiteRef, NodeRef, ExecutionChannelRef                                                       string
	}{artifact.Digest, request.RequestDigest, authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, fmt.Errorf("bind Cloud public-edge policy: %w", err)
	}
	parentRulesetRef := cloudHostFirewallRulesetRef(binding.SiteRef, binding.NodeRef)
	delegatedChainRef := cloudPublicEdgeChainRef(binding.SiteRef, binding.NodeRef)
	stateDigest, err := digestCloudPublicEdge(struct {
		StackID, SiteRef, NodeRef, NetworkMode, TransportSubnet, TLSMinVersion, ParentRulesetRef, DelegatedChainRef string
		IPv6                                                                                                        bool
		Routes                                                                                                      []architecturev2renderer.CloudPublicEdgeRoute
	}{governed.StackID, binding.SiteRef, binding.NodeRef, governed.NetworkMode, governed.TransportSubnet, governed.TLSMinVersion, parentRulesetRef, delegatedChainRef, governed.IPv6, governed.Routes})
	if err != nil {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, fmt.Errorf("digest Cloud public-edge state: %w", err)
	}
	routeRefs, backendPoolRefs, healthGateRefs := cloudPublicEdgeRefs(governed.Routes)
	policy := CloudPublicEdgeApplyPolicy{
		PolicyDigest: policyDigest, ArtifactDigest: artifact.Digest, StateDigest: stateDigest,
		StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		NetworkMode: governed.NetworkMode, TransportSubnet: governed.TransportSubnet, IPv6: governed.IPv6, TLSMinVersion: governed.TLSMinVersion,
		ParentRulesetRef: parentRulesetRef, DelegatedChainRef: delegatedChainRef, Routes: governed.Routes,
	}
	expectation := CloudPublicEdgeExpectation{
		PolicyDigest: policyDigest, ArtifactDigest: artifact.Digest, StateDigest: stateDigest, StackID: governed.StackID,
		SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		ParentRulesetRef: parentRulesetRef, DelegatedChainRef: delegatedChainRef,
		RouteRefs: routeRefs, BackendPoolRefs: backendPoolRefs, HealthGateRefs: healthGateRefs,
	}
	return target, health, policy, expectation, nil
}

func cloneCloudPublicEdgePolicy(policy CloudPublicEdgeApplyPolicy) (CloudPublicEdgeApplyPolicy, error) {
	raw, err := json.Marshal(policy)
	if err != nil {
		return CloudPublicEdgeApplyPolicy{}, fmt.Errorf("clone Cloud public-edge policy: %w", err)
	}
	var clone CloudPublicEdgeApplyPolicy
	if err := json.Unmarshal(raw, &clone); err != nil {
		return CloudPublicEdgeApplyPolicy{}, fmt.Errorf("clone Cloud public-edge policy: %w", err)
	}
	return clone, nil
}

func cloneCloudPublicEdgeExpectation(expectation CloudPublicEdgeExpectation) CloudPublicEdgeExpectation {
	expectation.RouteRefs = append([]string(nil), expectation.RouteRefs...)
	expectation.BackendPoolRefs = append([]string(nil), expectation.BackendPoolRefs...)
	expectation.HealthGateRefs = append([]string(nil), expectation.HealthGateRefs...)
	return expectation
}

func cloneCloudPublicEdgeEvidence(evidence CloudPublicEdgeEvidence) CloudPublicEdgeEvidence {
	evidence.Apply = cloneCloudPublicEdgeObservation(evidence.Apply)
	evidence.Reconcile = cloneCloudPublicEdgeObservation(evidence.Reconcile)
	evidence.Verify = cloneCloudPublicEdgeObservation(evidence.Verify)
	return evidence
}

func cloneCloudPublicEdgeObservation(observation CloudPublicEdgeObservation) CloudPublicEdgeObservation {
	observation.RouteRefs = append([]string(nil), observation.RouteRefs...)
	observation.BackendPoolRefs = append([]string(nil), observation.BackendPoolRefs...)
	observation.HealthGateRefs = append([]string(nil), observation.HealthGateRefs...)
	return observation
}

func cloudPublicEdgeRefs(routes []architecturev2renderer.CloudPublicEdgeRoute) ([]string, []string, []string) {
	routeRefs, backendRefs, healthRefs := make([]string, 0, len(routes)), make([]string, 0, len(routes)), make([]string, 0, len(routes))
	for _, route := range routes {
		routeRefs = append(routeRefs, route.ID)
		backendRefs = append(backendRefs, route.BackendPoolRef)
		healthRefs = append(healthRefs, route.HealthGateRef)
	}
	slices.Sort(routeRefs)
	slices.Sort(backendRefs)
	slices.Sort(healthRefs)
	return routeRefs, backendRefs, healthRefs
}

func digestCloudPublicEdge(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateCloudPublicEdgeObservation(observation CloudPublicEdgeObservation, expectation CloudPublicEdgeExpectation, operation, status string, requireClosed bool, evaluatedAt, notBefore, observedNow time.Time) (time.Time, error) {
	if observation.Operation != operation || observation.PolicyDigest != expectation.PolicyDigest || observation.RequestDigest != expectation.RequestDigest ||
		observation.ArtifactDigest != expectation.ArtifactDigest || observation.StateDigest != expectation.StateDigest || observation.EvaluatedAt != expectation.EvaluatedAt ||
		observation.StackID != expectation.StackID || observation.SiteRef != expectation.SiteRef || observation.NodeRef != expectation.NodeRef ||
		observation.ExecutionChannelRef != expectation.ExecutionChannelRef ||
		observation.ParentRulesetRef != expectation.ParentRulesetRef || observation.DelegatedChainRef != expectation.DelegatedChainRef ||
		observation.Status != status || !slices.Equal(observation.RouteRefs, expectation.RouteRefs) ||
		!slices.Equal(observation.BackendPoolRefs, expectation.BackendPoolRefs) || !slices.Equal(observation.HealthGateRefs, expectation.HealthGateRefs) ||
		(requireClosed && (!observation.DefaultClosed || observation.UnauthorizedRoutes != 0)) {
		return time.Time{}, fmt.Errorf("%s observation does not prove the exact Cloud public-edge state", operation)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Before(evaluatedAt) || observedAt.Before(notBefore) || observedAt.After(observedNow) || observedNow.Sub(observedAt) > cloudPublicEdgeMaxObservationAge {
		return time.Time{}, fmt.Errorf("%s observation is stale, future-dated, or non-monotonic", operation)
	}
	return observedAt, nil
}

func validateCloudPublicEdgeReceipt(receipt CloudPublicEdgeEvidenceReceipt, digest string, evaluatedAt, notBefore, observedNow time.Time) (time.Time, error) {
	committedAt, err := time.Parse(time.RFC3339Nano, receipt.CommittedAt)
	if receipt.EvidenceDigest != digest || err != nil || committedAt.Before(evaluatedAt) || committedAt.Before(notBefore) ||
		committedAt.After(observedNow) || observedNow.Sub(committedAt) > cloudPublicEdgeMaxObservationAge {
		return time.Time{}, errors.New("Cloud public-edge evidence custody receipt is unbound, stale, future-dated, or non-monotonic")
	}
	return committedAt, nil
}

var _ runtimeexecutor.Executor = (*CloudPublicEdgeExecutor)(nil)
