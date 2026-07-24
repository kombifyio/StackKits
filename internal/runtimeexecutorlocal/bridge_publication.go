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
	bridgePublicationProviderRef      = "stackkits-service-publication-contract"
	bridgePublicationModuleRef        = "stackkits-bridge-publication-runtime"
	bridgePublicationUnitRef          = "executor-contract"
	bridgePublicationOutputRef        = "modern/federation/publication/executor-contract.json"
	bridgePublicationArtifactPrefix   = "bridge-publication-executor-contract-instance-"
	bridgePublicationHealthSourceRef  = "bridge-publication-health"
	bridgePublicationMaxArtifactBytes = 512 << 10
	bridgePublicationMaxStaleness     = 5 * time.Minute
)

type BridgePublicationApplyPolicy struct {
	PolicyDigest        string                                         `json:"policyDigest"`
	StackID             string                                         `json:"stackId"`
	SiteRef             string                                         `json:"siteRef"`
	NodeRef             string                                         `json:"nodeRef"`
	ExecutionChannelRef string                                         `json:"executionChannelRef"`
	EvaluatedAt         string                                         `json:"evaluatedAt"`
	Publications        []architecturev2renderer.BridgePublicationRule `json:"publications"`
}

type BridgePublicationExpectation = BridgePublicationApplyPolicy

// BridgePublicationRuleObservation contains bounded configuration/readback
// metadata only. It cannot carry endpoints, credentials or provider handles.
type BridgePublicationRuleObservation struct {
	ServiceRef               string                                                 `json:"serviceRef"`
	SourceSiteRef            string                                                 `json:"sourceSiteRef"`
	EdgeSiteRef              string                                                 `json:"edgeSiteRef"`
	Host                     string                                                 `json:"host"`
	Protocol                 string                                                 `json:"protocol"`
	Port                     int                                                    `json:"port"`
	Path                     string                                                 `json:"path"`
	TLSMinVersion            string                                                 `json:"tlsMinVersion"`
	AuthPolicyRef            string                                                 `json:"authPolicyRef"`
	OriginIdentityRef        string                                                 `json:"originIdentityRef"`
	RateLimitRequests        int                                                    `json:"rateLimitRequests"`
	RateLimitWindowSeconds   int                                                    `json:"rateLimitWindowSeconds"`
	ModuleRef                string                                                 `json:"moduleRef"`
	UnitRef                  string                                                 `json:"unitRef"`
	OriginNodeRefs           []string                                               `json:"originNodeRefs"`
	OriginInstanceRefs       []string                                               `json:"originInstanceRefs"`
	OriginTargets            []architecturev2renderer.BridgePublicationOriginTarget `json:"originTargets"`
	UpstreamProtocol         string                                                 `json:"upstreamProtocol"`
	TargetPort               int                                                    `json:"targetPort"`
	HealthGateRef            string                                                 `json:"healthGateRef"`
	DataBindingRef           string                                                 `json:"dataBindingRef,omitempty"`
	Authentication           string                                                 `json:"authentication"`
	Privilege                string                                                 `json:"privilege"`
	EnrolledDeviceRequired   bool                                                   `json:"enrolledDeviceRequired"`
	OwnerStepUpRequired      bool                                                   `json:"ownerStepUpRequired"`
	AllowedMethods           []string                                               `json:"allowedMethods"`
	PublicationConfigured    bool                                                   `json:"publicationConfigured"`
	DefaultClosed            bool                                                   `json:"defaultClosed"`
	OriginMTLSRequired       bool                                                   `json:"originMTLSRequired"`
	OriginIdentityBound      bool                                                   `json:"originIdentityBound"`
	TLSPolicyBound           bool                                                   `json:"tlsPolicyBound"`
	AuthenticationBound      bool                                                   `json:"authenticationBound"`
	RateLimitBound           bool                                                   `json:"rateLimitBound"`
	ConfigurationObservedAt  string                                                 `json:"configurationObservedAt"`
	VerifierPolicyObservedAt string                                                 `json:"verifierPolicyObservedAt"`
	TLSPolicyObservedAt      string                                                 `json:"tlsPolicyObservedAt"`
}

type BridgePublicationObservation struct {
	PolicyDigest string                             `json:"policyDigest"`
	Status       string                             `json:"status"`
	EvaluatedAt  string                             `json:"evaluatedAt"`
	ObservedAt   string                             `json:"observedAt"`
	Publications []BridgePublicationRuleObservation `json:"publications"`
}

type BridgePublicationOperations interface {
	ApplyServicePublications(context.Context, BridgePublicationApplyPolicy) (BridgePublicationObservation, error)
	RemoveObsoleteServicePublications(context.Context, BridgePublicationExpectation) (BridgePublicationObservation, error)
	VerifyServicePublications(context.Context, BridgePublicationExpectation) (BridgePublicationObservation, error)
}

type BridgePublicationAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type BridgePublicationExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  BridgePublicationAuthority
	operations BridgePublicationOperations
	now        func() time.Time
}

func NewBridgePublicationExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority BridgePublicationAuthority, operations BridgePublicationOperations) *BridgePublicationExecutor {
	return NewBridgePublicationExecutorWithClock(identity, binding, authority, operations, time.Now)
}

func NewBridgePublicationExecutorWithClock(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority BridgePublicationAuthority, operations BridgePublicationOperations, now func() time.Time) *BridgePublicationExecutor {
	return &BridgePublicationExecutor{identity: identity, binding: binding, authority: authority, operations: operations, now: now}
}

func (e *BridgePublicationExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *BridgePublicationExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("publication executor requires a context")
	}
	if e == nil || e.operations == nil || e.now == nil ||
		strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" ||
		strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.ModuleContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("publication executor requires one explicit authenticated Cloud edge target binding")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed publication request: %w", err)
	}
	evaluatedAt := e.now().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("publication executor clock returned zero time")
	}
	target, health, policy, err := validateBridgePublicationRequest(request, e.binding, e.authority, evaluatedAt)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	applied, err := e.operations.ApplyServicePublications(ctx, cloneBridgePublicationPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact service publications: %w", err)
	}
	appliedAt, err := bridgePublicationCheckedAt(e.now, evaluatedAt)
	if err != nil || !validBridgePublicationObservation(applied, policy, "applied", evaluatedAt, appliedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("apply observation does not prove exact service publications")
	}
	removed, err := e.operations.RemoveObsoleteServicePublications(ctx, cloneBridgePublicationPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("remove obsolete service publications: %w", err)
	}
	removedAt, err := bridgePublicationCheckedAt(e.now, appliedAt)
	if err != nil || !validBridgePublicationObservation(removed, policy, "obsolete-removed", evaluatedAt, removedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("removal observation does not prove exact publication reconciliation")
	}
	verified, err := e.operations.VerifyServicePublications(ctx, cloneBridgePublicationPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact service publications: %w", err)
	}
	verifiedAt, err := bridgePublicationCheckedAt(e.now, removedAt)
	if err != nil || !validBridgePublicationObservation(verified, policy, "ready", evaluatedAt, verifiedAt) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("verification observation does not prove fresh service-publication policy")
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                       `json:"schemaVersion"`
		Apply         BridgePublicationObservation `json:"apply"`
		Remove        BridgePublicationObservation `json:"remove"`
		Verify        BridgePublicationObservation `json:"verify"`
	}{"stackkit.bridge-publication-evidence/v1", applied, removed, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal publication evidence: %w", err)
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:         runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://bridge-publication/" + target.InstanceRef, ObservationDigest: digest,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef,
			Status:         runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://bridge-publication/" + target.InstanceRef, ObservationDigest: digest,
		}},
	}, nil
}

func validateBridgePublicationRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority BridgePublicationAuthority, evaluatedAt time.Time) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, BridgePublicationApplyPolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 ||
		len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 {
		return emptyTarget, emptyHealth, BridgePublicationApplyPolicy{}, errors.New("publication executor requires exactly one runtime, one health target, one artifact, and no access binding")
	}
	if !validCoreHostBootstrapDigest(request.RequestDigest) {
		return emptyTarget, emptyHealth, BridgePublicationApplyPolicy{}, errors.New("publication executor requires the sealed request digest")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.BridgePublicationExecutorBundleRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != bridgePublicationModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != bridgePublicationProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != bridgePublicationModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.OwnerContractHash != authority.ModuleContractHash || target.UnitRef != bridgePublicationUnitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "native" ||
		target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" || target.WorkloadRef != "" ||
		target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 ||
		len(target.AccessBindingRefs) != 0 || !slices.Equal(target.SiteRefs, []string{binding.SiteRef}) ||
		!slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, BridgePublicationApplyPolicy{}, errors.New("runtime target is not the exact bound publication contract")
	}
	wantInstance := bridgePublicationUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := bridgePublicationArtifactPrefix + wantInstance
	wantRequirementID := bridgePublicationModuleRef + "/" + bridgePublicationUnitRef + "/" + wantInstance
	if target.RequirementID != wantRequirementID || target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, BridgePublicationApplyPolicy{}, errors.New("runtime target does not bind the exact node-local publication artifact")
	}
	health := request.HealthTargets[0]
	if health.RequirementID != bridgePublicationHealthSourceRef+"/"+wantInstance ||
		health.SourceRef != bridgePublicationHealthSourceRef || health.ContractHash != authority.HealthContractHash ||
		health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != bridgePublicationModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, BridgePublicationApplyPolicy{}, errors.New("health target is not the exact publication postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != wantArtifactID || artifact.Kind != "native-config" || artifact.Format != "json" ||
		artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != bridgePublicationProviderRef ||
		artifact.ProviderContractHash != target.ProviderContractHash || artifact.ModuleRef != bridgePublicationModuleRef ||
		artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != bridgePublicationUnitRef ||
		artifact.UnitContractHash != target.UnitContractHash || artifact.InstanceRef != wantInstance ||
		artifact.OutputRef != bridgePublicationOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) ||
		!slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 ||
		len(artifact.Content) > bridgePublicationMaxArtifactBytes {
		return emptyTarget, emptyHealth, BridgePublicationApplyPolicy{}, errors.New("artifact is not the exact CUE-owned publication instance")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, BridgePublicationApplyPolicy{}, errors.New("publication artifact digest does not match immutable content")
	}
	governed, err := architecturev2renderer.ValidateBridgePublicationExecutorArtifact(artifact.Content, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return emptyTarget, emptyHealth, BridgePublicationApplyPolicy{}, fmt.Errorf("validate governed publication policy: %w", err)
	}
	bindingBytes, err := json.Marshal(struct {
		ArtifactDigest      string `json:"artifactDigest"`
		RequestDigest       string `json:"requestDigest"`
		SiteRef             string `json:"siteRef"`
		NodeRef             string `json:"nodeRef"`
		ExecutionChannelRef string `json:"executionChannelRef"`
	}{artifact.Digest, request.RequestDigest, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, BridgePublicationApplyPolicy{}, fmt.Errorf("bind publication policy: %w", err)
	}
	policySum := sha256.Sum256(bindingBytes)
	return target, health, BridgePublicationApplyPolicy{
		PolicyDigest: "sha256:" + hex.EncodeToString(policySum[:]), StackID: governed.StackID,
		SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		EvaluatedAt:  evaluatedAt.Format(time.RFC3339Nano),
		Publications: cloneBridgePublicationRules(governed.Publications),
	}, nil
}

func cloneBridgePublicationPolicy(policy BridgePublicationApplyPolicy) BridgePublicationApplyPolicy {
	policy.Publications = cloneBridgePublicationRules(policy.Publications)
	return policy
}

func cloneBridgePublicationRules(rules []architecturev2renderer.BridgePublicationRule) []architecturev2renderer.BridgePublicationRule {
	cloned := append([]architecturev2renderer.BridgePublicationRule(nil), rules...)
	for index := range cloned {
		cloned[index].OriginNodeRefs = append([]string(nil), cloned[index].OriginNodeRefs...)
		cloned[index].OriginInstanceRefs = append([]string(nil), cloned[index].OriginInstanceRefs...)
		cloned[index].OriginTargets = append([]architecturev2renderer.BridgePublicationOriginTarget(nil), cloned[index].OriginTargets...)
		cloned[index].AllowedMethods = append([]string(nil), cloned[index].AllowedMethods...)
	}
	return cloned
}

func bridgePublicationCheckedAt(now func() time.Time, notBefore time.Time) (time.Time, error) {
	checkedAt := now().UTC()
	if checkedAt.IsZero() || checkedAt.Before(notBefore) {
		return time.Time{}, errors.New("publication executor clock moved backwards or returned zero time")
	}
	return checkedAt, nil
}

func validBridgePublicationObservation(observation BridgePublicationObservation, expectation BridgePublicationExpectation, status string, evaluatedAt, checkedAt time.Time) bool {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != status ||
		observation.EvaluatedAt != expectation.EvaluatedAt ||
		len(observation.Publications) != len(expectation.Publications) {
		return false
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Format(time.RFC3339Nano) != observation.ObservedAt ||
		observedAt.Before(evaluatedAt) || observedAt.After(checkedAt) {
		return false
	}
	for index, actual := range observation.Publications {
		want := expectation.Publications[index]
		if actual.ServiceRef != want.ServiceRef || actual.SourceSiteRef != want.SourceSiteRef ||
			actual.EdgeSiteRef != want.EdgeSiteRef || actual.Host != want.Host ||
			actual.Protocol != want.Protocol || actual.Port != want.Port || actual.Path != want.Path ||
			actual.TLSMinVersion != want.TLSMinVersion || actual.AuthPolicyRef != want.AuthPolicyRef ||
			actual.OriginIdentityRef != want.OriginIdentityRef ||
			actual.RateLimitRequests != want.RateLimitRequests ||
			actual.RateLimitWindowSeconds != want.RateLimitWindowSeconds ||
			actual.ModuleRef != want.ModuleRef || actual.UnitRef != want.UnitRef ||
			!slices.Equal(actual.OriginNodeRefs, want.OriginNodeRefs) ||
			!slices.Equal(actual.OriginInstanceRefs, want.OriginInstanceRefs) ||
			!slices.Equal(actual.OriginTargets, want.OriginTargets) ||
			actual.UpstreamProtocol != want.UpstreamProtocol || actual.TargetPort != want.TargetPort ||
			actual.HealthGateRef != want.HealthGateRef || actual.DataBindingRef != want.DataBindingRef ||
			actual.Authentication != want.Authentication || actual.Privilege != want.Privilege ||
			actual.EnrolledDeviceRequired != want.EnrolledDeviceRequired ||
			actual.OwnerStepUpRequired != want.OwnerStepUpRequired ||
			!slices.Equal(actual.AllowedMethods, want.AllowedMethods) ||
			!actual.PublicationConfigured || !actual.DefaultClosed || !actual.OriginMTLSRequired ||
			!actual.OriginIdentityBound || !actual.TLSPolicyBound ||
			!actual.AuthenticationBound || !actual.RateLimitBound {
			return false
		}
		for _, raw := range []string{actual.ConfigurationObservedAt, actual.VerifierPolicyObservedAt, actual.TLSPolicyObservedAt} {
			timestamp, parseErr := time.Parse(time.RFC3339Nano, raw)
			if parseErr != nil || timestamp.Format(time.RFC3339Nano) != raw ||
				timestamp.Before(evaluatedAt) || timestamp.After(observedAt) ||
				checkedAt.Sub(timestamp) > bridgePublicationMaxStaleness {
				return false
			}
		}
	}
	return true
}

var _ runtimeexecutor.Executor = (*BridgePublicationExecutor)(nil)
