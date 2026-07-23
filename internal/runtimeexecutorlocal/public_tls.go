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
	publicTLSProviderRef      = "stackkits-public-tls"
	publicTLSModuleRef        = "stackkits-public-tls-contract"
	publicTLSUnitRef          = "executor-contract"
	publicTLSOutputRef        = "cloud/tls/executor-contract.json"
	publicTLSArtifactPrefix   = "public-tls-executor-contract-instance-"
	publicTLSHealthSourceRef  = "public-tls-renewal-contract"
	publicTLSMaxArtifactBytes = 512 << 10
)

// PublicTLSApplyPolicy is the exact credential-free policy passed to an
// authenticated Cloud TLS implementation. The implementation owns ACME
// credentials and logical material-slot custody outside StackKits.
type PublicTLSApplyPolicy struct {
	PolicyDigest        string                                         `json:"policyDigest"`
	StackID             string                                         `json:"stackId"`
	SiteRef             string                                         `json:"siteRef"`
	NodeRef             string                                         `json:"nodeRef"`
	ExecutionChannelRef string                                         `json:"executionChannelRef"`
	EvaluatedAt         string                                         `json:"evaluatedAt"`
	Profile             architecturev2renderer.PublicTLSRuntimeProfile `json:"profile"`
	Issuer              architecturev2renderer.PublicTLSRuntimeIssuer  `json:"issuer"`
	Routes              []architecturev2renderer.PublicTLSRuntimeRoute `json:"routes"`
}

type PublicTLSExpectation struct {
	PolicyDigest        string   `json:"policyDigest"`
	StackID             string   `json:"stackId"`
	SiteRef             string   `json:"siteRef"`
	NodeRef             string   `json:"nodeRef"`
	ExecutionChannelRef string   `json:"executionChannelRef"`
	EvaluatedAt         string   `json:"evaluatedAt"`
	ValiditySeconds     int      `json:"validitySeconds"`
	RenewBeforeSeconds  int      `json:"renewBeforeSeconds"`
	RouteRefs           []string `json:"routeRefs"`
	MaterialSlotIDs     []string `json:"materialSlotIds"`
}

// PublicTLSObservation proves only postconditions and logical custody. It
// cannot carry certificate, private-key, account-key, credential, endpoint, or
// provider resource bytes.
type PublicTLSObservation struct {
	PolicyDigest    string   `json:"policyDigest"`
	Status          string   `json:"status"`
	EvaluatedAt     string   `json:"evaluatedAt"`
	ValidUntil      string   `json:"validUntil"`
	RouteRefs       []string `json:"routeRefs"`
	MaterialSlotIDs []string `json:"materialSlotIds"`
}

type PublicTLSOperations interface {
	MaterializePublicTLS(context.Context, PublicTLSApplyPolicy) (PublicTLSObservation, error)
	RenewPublicTLS(context.Context, PublicTLSExpectation) (PublicTLSObservation, error)
	VerifyPublicTLS(context.Context, PublicTLSExpectation) (PublicTLSObservation, error)
}

type PublicTLSAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type PublicTLSExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  PublicTLSAuthority
	operations PublicTLSOperations
	now        func() time.Time
}

func NewPublicTLSExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority PublicTLSAuthority, operations PublicTLSOperations) *PublicTLSExecutor {
	return NewPublicTLSExecutorWithClock(identity, binding, authority, operations, time.Now)
}

func NewPublicTLSExecutorWithClock(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority PublicTLSAuthority, operations PublicTLSOperations, now func() time.Time) *PublicTLSExecutor {
	return &PublicTLSExecutor{identity: identity, binding: binding, authority: authority, operations: operations, now: now}
}

func (e *PublicTLSExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *PublicTLSExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("public TLS executor requires a context")
	}
	if e == nil || e.operations == nil || e.now == nil ||
		strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("public TLS executor requires one explicit authenticated Cloud target binding")
	}
	evaluatedAt := e.now().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("public TLS executor clock returned zero time")
	}
	target, health, policy, expectation, err := validatePublicTLSRequest(request, e.binding, e.authority, evaluatedAt)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	materialized, err := e.operations.MaterializePublicTLS(ctx, clonePublicTLSApplyPolicy(policy))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("materialize exact public TLS policy: %w", err)
	}
	if !validPublicTLSObservation(materialized, expectation, "materialized") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("materialization observation does not prove the exact public TLS policy")
	}
	renewed, err := e.operations.RenewPublicTLS(ctx, clonePublicTLSExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("renew exact public TLS policy: %w", err)
	}
	if !validPublicTLSObservation(renewed, expectation, "renewed") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("renewal observation does not prove fresh public TLS material")
	}
	verified, err := e.operations.VerifyPublicTLS(ctx, clonePublicTLSExpectation(expectation))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact public TLS policy: %w", err)
	}
	if !validPublicTLSObservation(verified, expectation, "ready") {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("verification observation does not prove fresh public TLS termination")
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string               `json:"schemaVersion"`
		Materialize   PublicTLSObservation `json:"materialize"`
		Renew         PublicTLSObservation `json:"renew"`
		Verify        PublicTLSObservation `json:"verify"`
	}{"stackkit.public-tls-evidence/v1", materialized, renewed, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal public TLS evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:         runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://public-tls/" + target.InstanceRef, ObservationDigest: digestString,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef,
			Status:         runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://public-tls/" + target.InstanceRef, ObservationDigest: digestString,
		}},
	}, nil
}

func validatePublicTLSRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority PublicTLSAuthority, evaluatedAt time.Time) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, PublicTLSApplyPolicy, PublicTLSExpectation, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 {
		return emptyTarget, emptyHealth, PublicTLSApplyPolicy{}, PublicTLSExpectation{}, errors.New("public TLS executor requires exactly one runtime, one health target, one artifact, and no access binding")
	}
	if !validCoreHostBootstrapDigest(request.RequestDigest) {
		return emptyTarget, emptyHealth, PublicTLSApplyPolicy{}, PublicTLSExpectation{}, errors.New("public TLS executor requires the sealed request digest")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.PublicTLSExecutorContractRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != publicTLSModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != publicTLSProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != publicTLSModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.OwnerContractHash != authority.ModuleContractHash || target.UnitRef != publicTLSUnitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "native" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.WorkloadRef != "" || target.ImageRef != "" ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, PublicTLSApplyPolicy{}, PublicTLSExpectation{}, errors.New("runtime target is not the exact bound public TLS contract")
	}
	wantInstance := publicTLSUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := publicTLSArtifactPrefix + wantInstance
	if target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, PublicTLSApplyPolicy{}, PublicTLSExpectation{}, errors.New("runtime target does not bind the exact node-local public TLS artifact")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != publicTLSHealthSourceRef || health.ContractHash != authority.HealthContractHash ||
		health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != publicTLSModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, PublicTLSApplyPolicy{}, PublicTLSExpectation{}, errors.New("health target is not the exact public TLS renewal postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != wantArtifactID || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != publicTLSProviderRef ||
		artifact.ProviderContractHash != target.ProviderContractHash || artifact.ModuleRef != publicTLSModuleRef ||
		artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != publicTLSUnitRef ||
		artifact.UnitContractHash != target.UnitContractHash || artifact.InstanceRef != wantInstance ||
		artifact.OutputRef != publicTLSOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) ||
		!slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 || len(artifact.Content) > publicTLSMaxArtifactBytes {
		return emptyTarget, emptyHealth, PublicTLSApplyPolicy{}, PublicTLSExpectation{}, errors.New("artifact is not the exact CUE-owned public TLS instance")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, PublicTLSApplyPolicy{}, PublicTLSExpectation{}, errors.New("public TLS artifact digest does not match its immutable content")
	}
	governed, err := architecturev2renderer.ValidatePublicTLSExecutorArtifact(artifact.Content, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return emptyTarget, emptyHealth, PublicTLSApplyPolicy{}, PublicTLSExpectation{}, fmt.Errorf("validate governed public TLS policy: %w", err)
	}
	policyDigestInput, err := json.Marshal(struct {
		ArtifactDigest      string `json:"artifactDigest"`
		RequestDigest       string `json:"requestDigest"`
		SiteRef             string `json:"siteRef"`
		NodeRef             string `json:"nodeRef"`
		ExecutionChannelRef string `json:"executionChannelRef"`
	}{artifact.Digest, request.RequestDigest, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, PublicTLSApplyPolicy{}, PublicTLSExpectation{}, fmt.Errorf("bind public TLS policy: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	policyDigest := "sha256:" + hex.EncodeToString(policyDigestBytes[:])
	routeRefs := make([]string, len(governed.Routes))
	for index, route := range governed.Routes {
		routeRefs[index] = route.ID
	}
	slotIDs := make([]string, len(governed.Issuer.MaterialSlots))
	for index, slot := range governed.Issuer.MaterialSlots {
		slotIDs[index] = slot.ID
	}
	evaluatedAtText := evaluatedAt.Format(time.RFC3339Nano)
	policy := PublicTLSApplyPolicy{
		PolicyDigest: policyDigest, StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, EvaluatedAt: evaluatedAtText,
		Profile: governed.Profile, Issuer: governed.Issuer, Routes: governed.Routes,
	}
	expectation := PublicTLSExpectation{
		PolicyDigest: policyDigest, StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, EvaluatedAt: evaluatedAtText,
		ValiditySeconds: governed.Issuer.ValiditySeconds, RenewBeforeSeconds: governed.Issuer.RenewBeforeSeconds,
		RouteRefs: routeRefs, MaterialSlotIDs: slotIDs,
	}
	return target, health, policy, expectation, nil
}

func clonePublicTLSApplyPolicy(policy PublicTLSApplyPolicy) PublicTLSApplyPolicy {
	policy.Issuer.MaterialSlots = append([]architecturev2renderer.PublicTLSRuntimeMaterialSlot(nil), policy.Issuer.MaterialSlots...)
	policy.Routes = append([]architecturev2renderer.PublicTLSRuntimeRoute(nil), policy.Routes...)
	return policy
}

func clonePublicTLSExpectation(expectation PublicTLSExpectation) PublicTLSExpectation {
	expectation.RouteRefs = append([]string(nil), expectation.RouteRefs...)
	expectation.MaterialSlotIDs = append([]string(nil), expectation.MaterialSlotIDs...)
	return expectation
}

func validPublicTLSObservation(observation PublicTLSObservation, expectation PublicTLSExpectation, status string) bool {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.Status != status ||
		observation.EvaluatedAt != expectation.EvaluatedAt ||
		!slices.Equal(observation.RouteRefs, expectation.RouteRefs) ||
		!slices.Equal(observation.MaterialSlotIDs, expectation.MaterialSlotIDs) {
		return false
	}
	evaluatedAt, err := time.Parse(time.RFC3339Nano, expectation.EvaluatedAt)
	if err != nil {
		return false
	}
	validUntil, err := time.Parse(time.RFC3339Nano, observation.ValidUntil)
	if err != nil || expectation.ValiditySeconds <= expectation.RenewBeforeSeconds {
		return false
	}
	return validUntil.After(evaluatedAt.Add(time.Duration(expectation.RenewBeforeSeconds)*time.Second)) &&
		!validUntil.After(evaluatedAt.Add(time.Duration(expectation.ValiditySeconds)*time.Second))
}

var _ runtimeexecutor.Executor = (*PublicTLSExecutor)(nil)
