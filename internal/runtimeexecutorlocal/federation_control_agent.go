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
	federationControlAgentProviderRef     = "stackkits-federation-control-agent"
	federationControlAgentModuleRef       = "stackkits-federation-control-agent-runtime"
	federationControlAgentUnitRef         = "executor-contract"
	federationControlAgentOutputRef       = "modern/federation/control-agent/executor-contract.json"
	federationControlAgentArtifactPrefix  = "federation-control-agent-executor-contract-instance-"
	federationControlAgentHealthSourceRef = "federation-control-agent-health"
	federationControlAgentMaxArtifactSize = 256 << 10
)

// FederationControlAgentApplyPolicy is the exact material-free outbound
// control policy handed to a service-owned Operations implementation. It has
// no endpoint, credential, tunnel, provider, lease, or LAN authority.
type FederationControlAgentApplyPolicy struct {
	PolicyDigest        string                                                 `json:"policyDigest"`
	StackID             string                                                 `json:"stackId"`
	SiteRef             string                                                 `json:"siteRef"`
	NodeRef             string                                                 `json:"nodeRef"`
	SiteKind            string                                                 `json:"siteKind"`
	ExecutionChannelRef string                                                 `json:"executionChannelRef"`
	EvaluatedAt         string                                                 `json:"evaluatedAt"`
	ContractHash        string                                                 `json:"contractHash"`
	Actions             []architecturev2renderer.FederationControlAgentAction  `json:"actions"`
	Partition           architecturev2renderer.FederationControlAgentPartition `json:"partition"`
}

type FederationControlAgentExpectation = FederationControlAgentApplyPolicy

// FederationControlAgentObservation is a bounded configuration/readback
// receipt. The Operations owner retains all transport and credential custody.
type FederationControlAgentObservation struct {
	PolicyDigest                    string                                                `json:"policyDigest"`
	Status                          string                                                `json:"status"`
	EvaluatedAt                     string                                                `json:"evaluatedAt"`
	ObservedAt                      string                                                `json:"observedAt"`
	ConfigurationObservedAt         string                                                `json:"configurationObservedAt"`
	StackID                         string                                                `json:"stackId"`
	SiteRef                         string                                                `json:"siteRef"`
	NodeRef                         string                                                `json:"nodeRef"`
	SiteKind                        string                                                `json:"siteKind"`
	ExecutionChannelRef             string                                                `json:"executionChannelRef"`
	ContractHash                    string                                                `json:"contractHash"`
	Actions                         []architecturev2renderer.FederationControlAgentAction `json:"actions"`
	OnCloudLoss                     string                                                `json:"onCloudLoss"`
	OnLinkLoss                      string                                                `json:"onLinkLoss"`
	CloudEdge                       string                                                `json:"cloudEdge"`
	MaxStaleVerificationSeconds     int                                                   `json:"maxStaleVerificationSeconds"`
	LocalIdentityAuthorityAvailable bool                                                  `json:"localIdentityAuthorityAvailable"`
	DenyNewCrossSiteSessions        bool                                                  `json:"denyNewCrossSiteSessions"`
	OutboundOnly                    bool                                                  `json:"outboundOnly"`
	InboundCloudToHomeAllowed       bool                                                  `json:"inboundCloudToHomeAllowed"`
	GeneralLANAccess                bool                                                  `json:"generalLANAccess"`
	LocalAuthorityContinues         bool                                                  `json:"localAuthorityContinues"`
	NewCrossSiteSessionsFailClosed  bool                                                  `json:"newCrossSiteSessionsFailClosed"`
}

type FederationControlAgentOperations interface {
	BindOutboundControlAgent(context.Context, FederationControlAgentApplyPolicy) (FederationControlAgentObservation, error)
	RemoveObsoleteOutboundControlAgent(context.Context, FederationControlAgentExpectation) (FederationControlAgentObservation, error)
	VerifyOutboundControlAgent(context.Context, FederationControlAgentExpectation) (FederationControlAgentObservation, error)
}

type FederationControlAgentAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type FederationControlAgentExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  FederationControlAgentAuthority
	operations FederationControlAgentOperations
	now        func() time.Time
}

func NewFederationControlAgentExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority FederationControlAgentAuthority, operations FederationControlAgentOperations) *FederationControlAgentExecutor {
	return NewFederationControlAgentExecutorWithClock(identity, binding, authority, operations, time.Now)
}

func NewFederationControlAgentExecutorWithClock(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority FederationControlAgentAuthority, operations FederationControlAgentOperations, now func() time.Time) *FederationControlAgentExecutor {
	return &FederationControlAgentExecutor{identity: identity, binding: binding, authority: authority, operations: operations, now: now}
}

func (e *FederationControlAgentExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	return e.identity
}

func (e *FederationControlAgentExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("federation control-agent executor requires a context")
	}
	if e == nil || e.operations == nil || e.now == nil || strings.TrimSpace(e.binding.SiteRef) == "" ||
		strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("federation control-agent executor requires one exact authenticated Site/node target binding")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed federation control-agent request: %w", err)
	}
	evaluatedAt := e.now().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("federation control-agent executor clock returned zero time")
	}
	target, health, policy, err := validateFederationControlAgentRequest(request, e.binding, e.authority, evaluatedAt)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}

	bound, err := e.operations.BindOutboundControlAgent(ctx, cloneFederationControlAgentPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("bind exact outbound control-agent: %w", err)
	}
	boundAt, err := federationControlAgentCheckedAt(e.now, evaluatedAt)
	if err != nil || !validFederationControlAgentObservation(bound, policy, "bound", evaluatedAt, boundAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("bind observation does not prove the exact outbound control-agent")
	}
	removed, err := e.operations.RemoveObsoleteOutboundControlAgent(ctx, cloneFederationControlAgentPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("remove obsolete outbound control-agent state: %w", err)
	}
	removedAt, err := federationControlAgentCheckedAt(e.now, boundAt)
	if err != nil || !validFederationControlAgentObservation(removed, policy, "obsolete-removed", evaluatedAt, removedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("removal observation does not prove exact outbound control-agent reconciliation")
	}
	verified, err := e.operations.VerifyOutboundControlAgent(ctx, cloneFederationControlAgentPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact outbound control-agent: %w", err)
	}
	verifiedAt, err := federationControlAgentCheckedAt(e.now, removedAt)
	if err != nil || !validFederationControlAgentObservation(verified, policy, "ready", evaluatedAt, verifiedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("verification observation does not prove a fresh fail-closed outbound control-agent")
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                            `json:"schemaVersion"`
		Bind          FederationControlAgentObservation `json:"bind"`
		Remove        FederationControlAgentObservation `json:"remove"`
		Verify        FederationControlAgentObservation `json:"verify"`
	}{"stackkit.federation-control-agent-evidence/v1", bound, removed, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal federation control-agent evidence: %w", err)
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return runtimeexecutor.ExecutionOutcome{Runtime: []runtimeexecutor.RuntimeOutcome{{
		RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied,
		ObservationRef: "runtime-observation://federation-control-agent/" + target.InstanceRef, ObservationDigest: digest,
	}}, Health: []runtimeexecutor.HealthOutcome{{
		RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy,
		ObservationRef: "health-observation://federation-control-agent/" + target.InstanceRef, ObservationDigest: digest,
	}}}, nil
}

func validateFederationControlAgentRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority FederationControlAgentAuthority, evaluatedAt time.Time) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, FederationControlAgentApplyPolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 || !validCoreHostBootstrapDigest(request.RequestDigest) {
		return emptyTarget, emptyHealth, FederationControlAgentApplyPolicy{}, errors.New("federation control-agent executor requires one sealed runtime, health target, artifact, and no access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.FederationControlAgentExecutorBundleRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != federationControlAgentModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != federationControlAgentProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != federationControlAgentModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.OwnerContractHash != authority.ModuleContractHash || target.UnitRef != federationControlAgentUnitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 ||
		len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 || !slices.Equal(target.SiteRefs, []string{binding.SiteRef}) ||
		!slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, FederationControlAgentApplyPolicy{}, errors.New("runtime target is not the exact bound federation control-agent contract")
	}
	instance := federationControlAgentUnitRef + "-node-" + binding.NodeRef
	artifactID := federationControlAgentArtifactPrefix + instance
	if target.RequirementID != federationControlAgentModuleRef+"/"+federationControlAgentUnitRef+"/"+instance || target.InstanceRef != instance || target.ArtifactRefs[0] != artifactID {
		return emptyTarget, emptyHealth, FederationControlAgentApplyPolicy{}, errors.New("runtime target does not bind the exact node-local federation control-agent artifact")
	}
	health := request.HealthTargets[0]
	if health.RequirementID != federationControlAgentHealthSourceRef+"/"+instance || health.SourceRef != federationControlAgentHealthSourceRef ||
		health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" ||
		health.TargetKind != "module" || health.TargetRef != federationControlAgentModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, FederationControlAgentApplyPolicy{}, errors.New("health target is not the exact federation control-agent postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != artifactID || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != instance || artifact.OwnerContractHash != target.UnitContractHash ||
		artifact.ProviderRef != federationControlAgentProviderRef || artifact.ProviderContractHash != target.ProviderContractHash ||
		artifact.ModuleRef != federationControlAgentModuleRef || artifact.ModuleContractHash != target.ModuleContractHash ||
		artifact.UnitRef != federationControlAgentUnitRef || artifact.UnitContractHash != target.UnitContractHash || artifact.InstanceRef != instance ||
		artifact.OutputRef != federationControlAgentOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > federationControlAgentMaxArtifactSize {
		return emptyTarget, emptyHealth, FederationControlAgentApplyPolicy{}, errors.New("artifact is not the exact CUE-owned federation control-agent instance")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, FederationControlAgentApplyPolicy{}, errors.New("federation control-agent artifact digest does not match immutable content")
	}
	governed, err := architecturev2renderer.ValidateFederationControlAgentExecutorArtifact(artifact.Content, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return emptyTarget, emptyHealth, FederationControlAgentApplyPolicy{}, fmt.Errorf("validate governed federation control-agent policy: %w", err)
	}
	if governed.ContractHash != authority.ProviderContractHash {
		return emptyTarget, emptyHealth, FederationControlAgentApplyPolicy{}, errors.New("federation control-agent policy does not bind the exact provider contract")
	}
	digestInput, err := json.Marshal(struct {
		ArtifactDigest      string `json:"artifactDigest"`
		RequestDigest       string `json:"requestDigest"`
		SiteRef             string `json:"siteRef"`
		NodeRef             string `json:"nodeRef"`
		ExecutionChannelRef string `json:"executionChannelRef"`
		ContractHash        string `json:"contractHash"`
	}{artifact.Digest, request.RequestDigest, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef, governed.ContractHash})
	if err != nil {
		return emptyTarget, emptyHealth, FederationControlAgentApplyPolicy{}, fmt.Errorf("bind federation control-agent policy: %w", err)
	}
	policySum := sha256.Sum256(digestInput)
	return target, health, FederationControlAgentApplyPolicy{PolicyDigest: "sha256:" + hex.EncodeToString(policySum[:]), StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, SiteKind: governed.SiteKind, ExecutionChannelRef: binding.ExecutionChannelRef, EvaluatedAt: evaluatedAt.Format(time.RFC3339Nano), ContractHash: governed.ContractHash, Actions: append([]architecturev2renderer.FederationControlAgentAction(nil), governed.Actions...), Partition: governed.Partition}, nil
}

func cloneFederationControlAgentPolicy(policy FederationControlAgentApplyPolicy) FederationControlAgentApplyPolicy {
	policy.Actions = append([]architecturev2renderer.FederationControlAgentAction(nil), policy.Actions...)
	return policy
}

func federationControlAgentCheckedAt(now func() time.Time, notBefore time.Time) (time.Time, error) {
	checked := now().UTC()
	if checked.IsZero() || checked.Before(notBefore) {
		return time.Time{}, errors.New("federation control-agent executor clock moved backwards or returned zero time")
	}
	return checked, nil
}

func validFederationControlAgentObservation(observation FederationControlAgentObservation, expectation FederationControlAgentExpectation, status string, evaluatedAt, checkedAt time.Time) bool {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != status || observation.EvaluatedAt != expectation.EvaluatedAt || observation.StackID != expectation.StackID || observation.SiteRef != expectation.SiteRef || observation.NodeRef != expectation.NodeRef || observation.SiteKind != expectation.SiteKind || observation.ExecutionChannelRef != expectation.ExecutionChannelRef || observation.ContractHash != expectation.ContractHash || !slices.EqualFunc(observation.Actions, expectation.Actions, func(a, b architecturev2renderer.FederationControlAgentAction) bool { return a == b }) || observation.OnCloudLoss != expectation.Partition.OnCloudLoss || observation.OnLinkLoss != expectation.Partition.OnLinkLoss || observation.CloudEdge != expectation.Partition.CloudEdge || observation.MaxStaleVerificationSeconds != expectation.Partition.MaxStaleVerificationSeconds || observation.LocalIdentityAuthorityAvailable != expectation.Partition.LocalIdentityAuthorityAvailable || observation.DenyNewCrossSiteSessions != expectation.Partition.DenyNewCrossSiteSessions || !observation.OutboundOnly || observation.InboundCloudToHomeAllowed || observation.GeneralLANAccess || !observation.LocalAuthorityContinues || !observation.NewCrossSiteSessionsFailClosed {
		return false
	}
	observed, err := exactFederationLinkTime(observation.ObservedAt)
	if err != nil || observed.Before(evaluatedAt) || observed.After(checkedAt) {
		return false
	}
	configured, err := exactFederationLinkTime(observation.ConfigurationObservedAt)
	return err == nil && !configured.Before(evaluatedAt) && !configured.After(observed)
}

var _ runtimeexecutor.Executor = (*FederationControlAgentExecutor)(nil)
