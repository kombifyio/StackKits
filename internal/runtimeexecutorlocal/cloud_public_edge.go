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

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	cloudPublicEdgeProviderRef      = "stackkits-cloud-public-edge"
	cloudPublicEdgeModuleRef        = "stackkits-cloud-public-edge-runtime"
	cloudPublicEdgeUnitRef          = "executor-contract"
	cloudPublicEdgeOutputRef        = "cloud/public-edge/executor-contract.json"
	cloudPublicEdgeArtifactPrefix   = "cloud-public-edge-executor-contract-instance-"
	cloudPublicEdgeHealthSourceRef  = "cloud-public-edge-contract"
	cloudPublicEdgeMaxArtifactBytes = 512 << 10
)

type CloudPublicEdgeApplyPolicy struct {
	PolicyDigest        string                                        `json:"policyDigest"`
	StackID             string                                        `json:"stackId"`
	SiteRef             string                                        `json:"siteRef"`
	NodeRef             string                                        `json:"nodeRef"`
	ExecutionChannelRef string                                        `json:"executionChannelRef"`
	NetworkMode         string                                        `json:"networkMode"`
	TransportSubnet     string                                        `json:"transportSubnet"`
	IPv6                bool                                          `json:"ipv6"`
	TLSMinVersion       string                                        `json:"tlsMinVersion"`
	Routes              []architecturev2renderer.CloudPublicEdgeRoute `json:"routes"`
}

type CloudPublicEdgeExpectation struct {
	PolicyDigest        string   `json:"policyDigest"`
	StackID             string   `json:"stackId"`
	SiteRef             string   `json:"siteRef"`
	NodeRef             string   `json:"nodeRef"`
	ExecutionChannelRef string   `json:"executionChannelRef"`
	RouteRefs           []string `json:"routeRefs"`
}

type CloudPublicEdgeObservation struct {
	PolicyDigest string   `json:"policyDigest"`
	Status       string   `json:"status"`
	RouteRefs    []string `json:"routeRefs"`
}

// CloudPublicEdgeOperations is owned by an authenticated Cloud host channel.
// It exposes only exact edge-policy reconciliation, never generic proxy
// commands, provider resources, DNS mutation, certificate issuance or secrets.
type CloudPublicEdgeOperations interface {
	ApplyPublicEdge(context.Context, CloudPublicEdgeApplyPolicy) (CloudPublicEdgeObservation, error)
	RemoveObsoletePublicEdge(context.Context, CloudPublicEdgeExpectation) (CloudPublicEdgeObservation, error)
	VerifyPublicEdge(context.Context, CloudPublicEdgeExpectation) (CloudPublicEdgeObservation, error)
}

type CloudPublicEdgeAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

// CloudPublicEdgeExecutor remains isolated behind an explicit Product factory
// that requires a real authenticated host operations implementation.
type CloudPublicEdgeExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  CloudPublicEdgeAuthority
	operations CloudPublicEdgeOperations
}

func NewCloudPublicEdgeExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudPublicEdgeAuthority, operations CloudPublicEdgeOperations) *CloudPublicEdgeExecutor {
	return &CloudPublicEdgeExecutor{identity: identity, binding: binding, authority: authority, operations: operations}
}

func (e *CloudPublicEdgeExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *CloudPublicEdgeExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud public-edge executor requires a context")
	}
	if e == nil || e.operations == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud public-edge executor requires one explicit authenticated Cloud target binding")
	}
	target, health, policy, expectation, err := validateCloudPublicEdgeRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	applyPolicy, err := cloneCloudPublicEdgePolicy(policy)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	applyObservation, err := e.operations.ApplyPublicEdge(ctx, applyPolicy)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Cloud public-edge policy: %w", err)
	}
	if !validCloudPublicEdgeObservation(applyObservation, expectation, "applied") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("apply observation does not prove the exact Cloud public-edge policy")
	}
	removeObservation, err := e.operations.RemoveObsoletePublicEdge(ctx, defensiveCloudPublicEdgeExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("remove obsolete Cloud public-edge routes: %w", err)
	}
	if !validCloudPublicEdgeObservation(removeObservation, expectation, "reconciled") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("reconcile observation does not prove exact removal of obsolete public-edge routes")
	}
	verifyObservation, err := e.operations.VerifyPublicEdge(ctx, defensiveCloudPublicEdgeExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact Cloud public-edge policy: %w", err)
	}
	if !validCloudPublicEdgeObservation(verifyObservation, expectation, "ready") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("fresh verification does not prove the exact Cloud public-edge policy")
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                     `json:"schemaVersion"`
		Apply         CloudPublicEdgeObservation `json:"apply"`
		Reconcile     CloudPublicEdgeObservation `json:"reconcile"`
		Verify        CloudPublicEdgeObservation `json:"verify"`
	}{"stackkit.cloud-public-edge-evidence/v1", applyObservation, removeObservation, verifyObservation})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Cloud public-edge evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://cloud-public-edge/" + target.InstanceRef, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://cloud-public-edge/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

func validateCloudPublicEdgeRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority CloudPublicEdgeAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, CloudPublicEdgeApplyPolicy, CloudPublicEdgeExpectation, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 {
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
	if target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, errors.New("runtime target does not bind the exact node-local Cloud public-edge artifact")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != cloudPublicEdgeHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != cloudPublicEdgeModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
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
	policyDigestInput, err := json.Marshal(struct {
		ArtifactDigest      string `json:"artifactDigest"`
		SiteRef             string `json:"siteRef"`
		NodeRef             string `json:"nodeRef"`
		ExecutionChannelRef string `json:"executionChannelRef"`
	}{artifact.Digest, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, CloudPublicEdgeApplyPolicy{}, CloudPublicEdgeExpectation{}, fmt.Errorf("bind Cloud public-edge policy: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	policyDigest := "sha256:" + hex.EncodeToString(policyDigestBytes[:])
	routeRefs := make([]string, len(governed.Routes))
	for index := range governed.Routes {
		routeRefs[index] = governed.Routes[index].ID
	}
	policy := CloudPublicEdgeApplyPolicy{PolicyDigest: policyDigest, StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef, NetworkMode: governed.NetworkMode, TransportSubnet: governed.TransportSubnet, IPv6: governed.IPv6, TLSMinVersion: governed.TLSMinVersion, Routes: governed.Routes}
	expectation := CloudPublicEdgeExpectation{PolicyDigest: policyDigest, StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef, RouteRefs: routeRefs}
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

func defensiveCloudPublicEdgeExpectation(expectation CloudPublicEdgeExpectation) CloudPublicEdgeExpectation {
	expectation.RouteRefs = append([]string(nil), expectation.RouteRefs...)
	return expectation
}

func validCloudPublicEdgeObservation(observation CloudPublicEdgeObservation, expectation CloudPublicEdgeExpectation, status string) bool {
	return observation.PolicyDigest == expectation.PolicyDigest && observation.Status == status && slices.Equal(observation.RouteRefs, expectation.RouteRefs)
}

var _ runtimeexecutor.Executor = (*CloudPublicEdgeExecutor)(nil)
