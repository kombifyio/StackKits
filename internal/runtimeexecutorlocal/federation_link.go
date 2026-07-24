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
	federationLinkProviderRef      = "stackkits-federation-link"
	federationLinkModuleRef        = "stackkits-federation-link-runtime"
	federationLinkUnitRef          = "executor-contract"
	federationLinkOutputRef        = "modern/federation/link/executor-contract.json"
	federationLinkArtifactPrefix   = "federation-link-executor-contract-instance-"
	federationLinkHealthSourceRef  = "federation-link-health"
	federationLinkMaxArtifactBytes = 256 << 10
)

// FederationLinkApplyPolicy is the exact provider-free Site/node policy passed
// to the construction-owned link implementation. The opaque fabric and
// custody references are handles into that implementation's private custody;
// no endpoint, credential, provider resource, lease, or general LAN authority
// crosses this boundary.
type FederationLinkApplyPolicy struct {
	PolicyDigest        string                                               `json:"policyDigest"`
	StackID             string                                               `json:"stackId"`
	SiteRef             string                                               `json:"siteRef"`
	NodeRef             string                                               `json:"nodeRef"`
	SiteKind            string                                               `json:"siteKind"`
	ExecutionChannelRef string                                               `json:"executionChannelRef"`
	EvaluatedAt         string                                               `json:"evaluatedAt"`
	HomeSiteRefs        []string                                             `json:"homeSiteRefs"`
	CloudSiteRefs       []string                                             `json:"cloudSiteRefs"`
	Overlay             architecturev2renderer.FederationLinkOverlayPolicy   `json:"overlay"`
	Partition           architecturev2renderer.FederationLinkPartitionPolicy `json:"partition"`
	Binding             architecturev2renderer.FederationLinkBindingPolicy   `json:"binding"`
}

type FederationLinkExpectation = FederationLinkApplyPolicy

// FederationLinkObservation is a bounded local configuration/readback
// receipt. It deliberately contains no address, endpoint, key, token,
// provider handle, lease, or transport implementation detail.
type FederationLinkObservation struct {
	PolicyDigest                    string   `json:"policyDigest"`
	Status                          string   `json:"status"`
	EvaluatedAt                     string   `json:"evaluatedAt"`
	ObservedAt                      string   `json:"observedAt"`
	ConfigurationObservedAt         string   `json:"configurationObservedAt"`
	StackID                         string   `json:"stackId"`
	SiteRef                         string   `json:"siteRef"`
	NodeRef                         string   `json:"nodeRef"`
	SiteKind                        string   `json:"siteKind"`
	ExecutionChannelRef             string   `json:"executionChannelRef"`
	BindingRef                      string   `json:"bindingRef"`
	FabricRef                       string   `json:"fabricRef"`
	CustodyAttestationRef           string   `json:"custodyAttestationRef"`
	RequirementsHash                string   `json:"requirementsHash"`
	BindingHash                     string   `json:"bindingHash"`
	BridgeContractHash              string   `json:"bridgeContractHash"`
	BindingIssuedAt                 string   `json:"bindingIssuedAt"`
	BindingValidUntil               string   `json:"bindingValidUntil"`
	HomeSiteRefs                    []string `json:"homeSiteRefs"`
	CloudSiteRefs                   []string `json:"cloudSiteRefs"`
	PeerSiteRefs                    []string `json:"peerSiteRefs"`
	OverlayContractRef              string   `json:"overlayContractRef"`
	Implementation                  string   `json:"implementation"`
	Initiation                      string   `json:"initiation"`
	TrafficMode                     string   `json:"trafficMode"`
	OnCloudLoss                     string   `json:"onCloudLoss"`
	OnLinkLoss                      string   `json:"onLinkLoss"`
	CloudEdge                       string   `json:"cloudEdge"`
	MaxStaleVerificationSeconds     int      `json:"maxStaleVerificationSeconds"`
	LocalIdentityAuthorityAvailable bool     `json:"localIdentityAuthorityAvailable"`
	DenyNewCrossSiteSessions        bool     `json:"denyNewCrossSiteSessions"`
	LocalAgentConfigured            bool     `json:"localAgentConfigured"`
	PeerAuthenticated               bool     `json:"peerAuthenticated"`
	CustodyVerified                 bool     `json:"custodyVerified"`
	InitiatesLink                   bool     `json:"initiatesLink"`
	AcceptsOnlyAuthenticatedPeers   bool     `json:"acceptsOnlyAuthenticatedPeers"`
	OutboundEstablished             bool     `json:"outboundEstablished"`
	DeclaredFlowsOnly               bool     `json:"declaredFlowsOnly"`
	DefaultDeny                     bool     `json:"defaultDeny"`
	DefaultRouteAdvertised          bool     `json:"defaultRouteAdvertised"`
	PrivateSubnetsAdvertised        bool     `json:"privateSubnetsAdvertised"`
	BroadRoutesAllowed              bool     `json:"broadRoutesAllowed"`
	GeneralLANAccess                bool     `json:"generalLANAccess"`
	InboundHomeAccessAllowed        bool     `json:"inboundHomeAccessAllowed"`
	LocalAuthorityContinues         bool     `json:"localAuthorityContinues"`
	NewCrossSiteSessionsFailClosed  bool     `json:"newCrossSiteSessionsFailClosed"`
}

type FederationLinkOperations interface {
	EstablishInterSiteLink(context.Context, FederationLinkApplyPolicy) (FederationLinkObservation, error)
	RemoveObsoleteInterSiteLink(context.Context, FederationLinkExpectation) (FederationLinkObservation, error)
	VerifyInterSiteLink(context.Context, FederationLinkExpectation) (FederationLinkObservation, error)
}

type FederationLinkAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type FederationLinkExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  FederationLinkAuthority
	operations FederationLinkOperations
	now        func() time.Time
}

func NewFederationLinkExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority FederationLinkAuthority, operations FederationLinkOperations) *FederationLinkExecutor {
	return NewFederationLinkExecutorWithClock(identity, binding, authority, operations, time.Now)
}

func NewFederationLinkExecutorWithClock(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority FederationLinkAuthority, operations FederationLinkOperations, now func() time.Time) *FederationLinkExecutor {
	return &FederationLinkExecutor{identity: identity, binding: binding, authority: authority, operations: operations, now: now}
}

func (e *FederationLinkExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *FederationLinkExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("federation link executor requires a context")
	}
	if e == nil || e.operations == nil || e.now == nil ||
		strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" ||
		strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.ModuleContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("federation link executor requires one exact authenticated Site/node target binding")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed federation link request: %w", err)
	}
	evaluatedAt := e.now().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("federation link executor clock returned zero time")
	}
	target, health, policy, err := validateFederationLinkRequest(request, e.binding, e.authority, evaluatedAt)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}

	established, err := e.operations.EstablishInterSiteLink(ctx, cloneFederationLinkPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("establish exact inter-Site link: %w", err)
	}
	establishedAt, err := federationLinkCheckedAt(e.now, evaluatedAt)
	if err != nil || !validFederationLinkObservation(established, policy, "established", evaluatedAt, establishedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("establish observation does not prove the exact federation link")
	}

	removed, err := e.operations.RemoveObsoleteInterSiteLink(ctx, cloneFederationLinkPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("remove obsolete inter-Site link state: %w", err)
	}
	removedAt, err := federationLinkCheckedAt(e.now, establishedAt)
	if err != nil || !validFederationLinkObservation(removed, policy, "obsolete-removed", evaluatedAt, removedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("removal observation does not prove exact federation link reconciliation")
	}

	verified, err := e.operations.VerifyInterSiteLink(ctx, cloneFederationLinkPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact inter-Site link: %w", err)
	}
	verifiedAt, err := federationLinkCheckedAt(e.now, removedAt)
	if err != nil || !validFederationLinkObservation(verified, policy, "ready", evaluatedAt, verifiedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("verification observation does not prove a fresh fail-closed federation link")
	}

	evidence, err := json.Marshal(struct {
		SchemaVersion string                    `json:"schemaVersion"`
		Establish     FederationLinkObservation `json:"establish"`
		Remove        FederationLinkObservation `json:"remove"`
		Verify        FederationLinkObservation `json:"verify"`
	}{"stackkit.federation-link-evidence/v1", established, removed, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal federation link evidence: %w", err)
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:         runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://federation-link/" + target.InstanceRef, ObservationDigest: digest,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef,
			Status:         runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://federation-link/" + target.InstanceRef, ObservationDigest: digest,
		}},
	}, nil
}

func validateFederationLinkRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority FederationLinkAuthority, evaluatedAt time.Time) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, FederationLinkApplyPolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 ||
		len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, errors.New("federation link executor requires exactly one runtime, one health target, one artifact, and no access binding")
	}
	if !validCoreHostBootstrapDigest(request.RequestDigest) {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, errors.New("federation link executor requires the sealed request digest")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.FederationLinkExecutorBundleRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != federationLinkModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != federationLinkProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != federationLinkModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.OwnerContractHash != authority.ModuleContractHash || target.UnitRef != federationLinkUnitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "host" ||
		target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" || target.WorkloadRef != "" ||
		target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 ||
		len(target.AccessBindingRefs) != 0 || !slices.Equal(target.SiteRefs, []string{binding.SiteRef}) ||
		!slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, errors.New("runtime target is not the exact bound federation link contract")
	}
	wantInstance := federationLinkUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := federationLinkArtifactPrefix + wantInstance
	wantRequirementID := federationLinkModuleRef + "/" + federationLinkUnitRef + "/" + wantInstance
	if target.RequirementID != wantRequirementID || target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, errors.New("runtime target does not bind the exact node-local federation link artifact")
	}
	health := request.HealthTargets[0]
	if health.RequirementID != federationLinkHealthSourceRef+"/"+wantInstance ||
		health.SourceRef != federationLinkHealthSourceRef || health.ContractHash != authority.HealthContractHash ||
		health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != federationLinkModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, errors.New("health target is not the exact federation link postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != wantArtifactID || artifact.Kind != "native-config" || artifact.Format != "json" ||
		artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != federationLinkProviderRef ||
		artifact.ProviderContractHash != target.ProviderContractHash || artifact.ModuleRef != federationLinkModuleRef ||
		artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != federationLinkUnitRef ||
		artifact.UnitContractHash != target.UnitContractHash || artifact.InstanceRef != wantInstance ||
		artifact.OutputRef != federationLinkOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) ||
		!slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 ||
		len(artifact.Content) > federationLinkMaxArtifactBytes {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, errors.New("artifact is not the exact CUE-owned federation link instance")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, errors.New("federation link artifact digest does not match immutable content")
	}
	governed, err := architecturev2renderer.ValidateFederationLinkExecutorArtifact(
		artifact.Content, binding.SiteRef, binding.NodeRef, evaluatedAt,
	)
	if err != nil {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, fmt.Errorf("validate governed federation link policy: %w", err)
	}
	issuedAt, err := exactFederationLinkTime(governed.Binding.IssuedAt)
	if err != nil || evaluatedAt.Before(issuedAt) {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, errors.New("federation link binding is not yet valid")
	}
	validUntil, err := exactFederationLinkTime(governed.Binding.ValidUntil)
	if err != nil || !evaluatedAt.Before(validUntil) {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, errors.New("federation link binding is expired")
	}
	digestInput, err := json.Marshal(struct {
		ArtifactDigest      string `json:"artifactDigest"`
		RequestDigest       string `json:"requestDigest"`
		SiteRef             string `json:"siteRef"`
		NodeRef             string `json:"nodeRef"`
		ExecutionChannelRef string `json:"executionChannelRef"`
		BindingHash         string `json:"bindingHash"`
	}{artifact.Digest, request.RequestDigest, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef, governed.Binding.BindingHash})
	if err != nil {
		return emptyTarget, emptyHealth, FederationLinkApplyPolicy{}, fmt.Errorf("bind federation link policy: %w", err)
	}
	policySum := sha256.Sum256(digestInput)
	return target, health, FederationLinkApplyPolicy{
		PolicyDigest: "sha256:" + hex.EncodeToString(policySum[:]), StackID: governed.StackID,
		SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, SiteKind: governed.SiteKind,
		ExecutionChannelRef: binding.ExecutionChannelRef, EvaluatedAt: evaluatedAt.Format(time.RFC3339Nano),
		HomeSiteRefs:  append([]string(nil), governed.HomeSiteRefs...),
		CloudSiteRefs: append([]string(nil), governed.CloudSiteRefs...),
		Overlay:       cloneFederationLinkOverlay(governed.Overlay),
		Partition:     governed.Partition,
		Binding:       governed.Binding,
	}, nil
}

func cloneFederationLinkPolicy(policy FederationLinkApplyPolicy) FederationLinkApplyPolicy {
	policy.HomeSiteRefs = append([]string(nil), policy.HomeSiteRefs...)
	policy.CloudSiteRefs = append([]string(nil), policy.CloudSiteRefs...)
	policy.Overlay = cloneFederationLinkOverlay(policy.Overlay)
	return policy
}

func cloneFederationLinkOverlay(overlay architecturev2renderer.FederationLinkOverlayPolicy) architecturev2renderer.FederationLinkOverlayPolicy {
	overlay.PeerSiteRefs = append([]string(nil), overlay.PeerSiteRefs...)
	return overlay
}

func exactFederationLinkTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Format(time.RFC3339Nano) != value || parsed.Location() != time.UTC {
		return time.Time{}, errors.New("timestamp is not canonical UTC RFC3339Nano")
	}
	return parsed, nil
}

func federationLinkCheckedAt(now func() time.Time, notBefore time.Time) (time.Time, error) {
	checkedAt := now().UTC()
	if checkedAt.IsZero() || checkedAt.Before(notBefore) {
		return time.Time{}, errors.New("federation link executor clock moved backwards or returned zero time")
	}
	return checkedAt, nil
}

func validFederationLinkObservation(observation FederationLinkObservation, expectation FederationLinkExpectation, status string, evaluatedAt, checkedAt time.Time) bool {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != status ||
		observation.EvaluatedAt != expectation.EvaluatedAt ||
		observation.StackID != expectation.StackID || observation.SiteRef != expectation.SiteRef ||
		observation.NodeRef != expectation.NodeRef || observation.SiteKind != expectation.SiteKind ||
		observation.ExecutionChannelRef != expectation.ExecutionChannelRef ||
		observation.BindingRef != expectation.Binding.BindingRef ||
		observation.FabricRef != expectation.Binding.FabricRef ||
		observation.CustodyAttestationRef != expectation.Binding.CustodyAttestationRef ||
		observation.RequirementsHash != expectation.Binding.RequirementsHash ||
		observation.BindingHash != expectation.Binding.BindingHash ||
		observation.BridgeContractHash != expectation.Binding.BridgeContractHash ||
		observation.BindingIssuedAt != expectation.Binding.IssuedAt ||
		observation.BindingValidUntil != expectation.Binding.ValidUntil ||
		!slices.Equal(observation.HomeSiteRefs, expectation.HomeSiteRefs) ||
		!slices.Equal(observation.CloudSiteRefs, expectation.CloudSiteRefs) ||
		!slices.Equal(observation.PeerSiteRefs, expectation.Overlay.PeerSiteRefs) ||
		observation.OverlayContractRef != expectation.Overlay.ContractRef ||
		observation.Implementation != expectation.Overlay.Implementation ||
		observation.Initiation != expectation.Overlay.Initiation ||
		observation.TrafficMode != expectation.Overlay.TrafficMode ||
		observation.OnCloudLoss != expectation.Partition.OnCloudLoss ||
		observation.OnLinkLoss != expectation.Partition.OnLinkLoss ||
		observation.CloudEdge != expectation.Partition.CloudEdge ||
		observation.MaxStaleVerificationSeconds != expectation.Partition.MaxStaleVerificationSeconds ||
		observation.LocalIdentityAuthorityAvailable != expectation.Partition.LocalIdentityAuthorityAvailable ||
		observation.DenyNewCrossSiteSessions != expectation.Partition.DenyNewCrossSiteSessions ||
		!observation.LocalAgentConfigured || !observation.PeerAuthenticated || !observation.CustodyVerified ||
		!observation.AcceptsOnlyAuthenticatedPeers || !observation.OutboundEstablished ||
		!observation.DeclaredFlowsOnly || !observation.DefaultDeny ||
		observation.DefaultRouteAdvertised || observation.PrivateSubnetsAdvertised ||
		observation.BroadRoutesAllowed || observation.GeneralLANAccess ||
		observation.InboundHomeAccessAllowed || !observation.LocalAuthorityContinues ||
		!observation.NewCrossSiteSessionsFailClosed ||
		observation.InitiatesLink != (expectation.SiteKind == "home") {
		return false
	}
	observedAt, err := exactFederationLinkTime(observation.ObservedAt)
	if err != nil || observedAt.Before(evaluatedAt) || observedAt.After(checkedAt) {
		return false
	}
	configuredAt, err := exactFederationLinkTime(observation.ConfigurationObservedAt)
	if err != nil || configuredAt.Before(evaluatedAt) || configuredAt.After(observedAt) {
		return false
	}
	validUntil, err := exactFederationLinkTime(expectation.Binding.ValidUntil)
	return err == nil && checkedAt.Before(validUntil)
}

var _ runtimeexecutor.Executor = (*FederationLinkExecutor)(nil)
