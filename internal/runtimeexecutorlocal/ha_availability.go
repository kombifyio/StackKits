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
	haAvailabilityUnitRef          = "executor-contract"
	haAvailabilityMaxArtifactBytes = 256 << 10
	haAvailabilityMaxReadbackAge   = 5 * time.Minute
)

type haAvailabilityProfile struct {
	providerRef string
	moduleRef   string
	kitSlug     string
	mode        string
	healthRef   string
}

var haAvailabilityProfiles = map[string]haAvailabilityProfile{
	"stackkits-ha-basement-warm-runtime":   {"stackkits-ha-basement-warm", "stackkits-ha-basement-warm-runtime", "basement-kit", "warm-standby", "ha-basement-warm-contract"},
	"stackkits-ha-basement-quorum-runtime": {"stackkits-ha-basement-quorum", "stackkits-ha-basement-quorum-runtime", "basement-kit", "quorum", "ha-basement-quorum-contract"},
	"stackkits-ha-cloud-warm-runtime":      {"stackkits-ha-cloud-warm", "stackkits-ha-cloud-warm-runtime", "cloud-kit", "warm-standby", "ha-cloud-warm-contract"},
	"stackkits-ha-cloud-quorum-runtime":    {"stackkits-ha-cloud-quorum", "stackkits-ha-cloud-quorum-runtime", "cloud-kit", "quorum", "ha-cloud-quorum-contract"},
	"stackkits-ha-modern-warm-runtime":     {"stackkits-ha-modern-warm", "stackkits-ha-modern-warm-runtime", "modern-homelab", "warm-standby", "ha-modern-warm-contract"},
	"stackkits-ha-modern-quorum-runtime":   {"stackkits-ha-modern-quorum", "stackkits-ha-modern-quorum-runtime", "modern-homelab", "quorum", "ha-modern-quorum-contract"},
}

// HAAvailabilityApplyPolicy is the complete secret-free decision handed to
// one authenticated member-local implementation. Provider APIs, credentials,
// endpoints, transport selection, general LAN, and failover authority are not
// representable here.
type HAAvailabilityApplyPolicy struct {
	PolicyDigest        string                                        `json:"policyDigest"`
	RequestDigest       string                                        `json:"requestDigest"`
	ArtifactDigest      string                                        `json:"artifactDigest"`
	StateDigest         string                                        `json:"stateDigest"`
	EvaluatedAt         string                                        `json:"evaluatedAt"`
	StackID             string                                        `json:"stackId"`
	KitSlug             string                                        `json:"kitSlug"`
	ModuleRef           string                                        `json:"moduleRef"`
	SiteRef             string                                        `json:"siteRef"`
	NodeRef             string                                        `json:"nodeRef"`
	ExecutionChannelRef string                                        `json:"executionChannelRef"`
	Policy              architecturev2renderer.HAAvailabilityPolicy   `json:"policy"`
	FailureModel        architecturev2renderer.HAFailureModel         `json:"failureModel"`
	Members             []architecturev2renderer.HAAvailabilityMember `json:"members"`
}

type HAAvailabilityExpectation = HAAvailabilityApplyPolicy

// HAAvailabilityMemberReadback proves the exact compiler-selected member and
// failure-domain set. No replacement or discovered member can enter evidence.
type HAAvailabilityMemberReadback struct {
	NodeRef       string `json:"nodeRef"`
	SiteRef       string `json:"siteRef"`
	FailureDomain string `json:"failureDomain"`
	Ready         bool   `json:"ready"`
}

// HAAvailabilityObservation is the closed apply/remove/verify evidence shape.
// The negative authority flags make accidental provider, WAN-quorum, general
// LAN, or independent failover ownership observable and rejectable.
type HAAvailabilityObservation struct {
	Operation           string                         `json:"operation"`
	Status              string                         `json:"status"`
	PolicyDigest        string                         `json:"policyDigest"`
	RequestDigest       string                         `json:"requestDigest"`
	ArtifactDigest      string                         `json:"artifactDigest"`
	StateDigest         string                         `json:"stateDigest"`
	EvaluatedAt         string                         `json:"evaluatedAt"`
	ObservedAt          string                         `json:"observedAt"`
	StackID             string                         `json:"stackId"`
	KitSlug             string                         `json:"kitSlug"`
	ModuleRef           string                         `json:"moduleRef"`
	SiteRef             string                         `json:"siteRef"`
	NodeRef             string                         `json:"nodeRef"`
	ExecutionChannelRef string                         `json:"executionChannelRef"`
	Mode                string                         `json:"mode"`
	PolicyRef           string                         `json:"policyRef"`
	RealizationRef      string                         `json:"realizationRef"`
	Fencing             string                         `json:"fencing"`
	FailureDomainSpread int                            `json:"failureDomainSpread"`
	PartitionBehavior   string                         `json:"partitionBehavior"`
	Members             []HAAvailabilityMemberReadback `json:"members"`
	FencingReady        bool                           `json:"fencingReady"`
	ProviderAuthority   bool                           `json:"providerAuthority"`
	WANQuorum           bool                           `json:"wanQuorum"`
	GeneralLANAuthority bool                           `json:"generalLanAuthority"`
	IndependentFailover bool                           `json:"independentFailover"`
}

// HAAvailabilityOperations is implemented by the authenticated member-local
// control-plane owner. Its verbs are deliberately narrower than a provider,
// transport, cluster-management, or generic failover API.
type HAAvailabilityOperations interface {
	ApplyHAAvailability(context.Context, HAAvailabilityApplyPolicy) (HAAvailabilityObservation, error)
	RemoveObsoleteHAAvailability(context.Context, HAAvailabilityExpectation) (HAAvailabilityObservation, error)
	VerifyHAAvailability(context.Context, HAAvailabilityExpectation) (HAAvailabilityObservation, error)
}

type HAAvailabilityAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type HAAvailabilityExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  HAAvailabilityAuthority
	moduleRef  string
	operations HAAvailabilityOperations
	now        func() time.Time
}

func NewHAAvailabilityExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority HAAvailabilityAuthority, moduleRef string, operations HAAvailabilityOperations) *HAAvailabilityExecutor {
	return NewHAAvailabilityExecutorWithClock(identity, binding, authority, moduleRef, operations, time.Now)
}

func NewHAAvailabilityExecutorWithClock(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority HAAvailabilityAuthority, moduleRef string, operations HAAvailabilityOperations, now func() time.Time) *HAAvailabilityExecutor {
	return &HAAvailabilityExecutor{identity: identity, binding: binding, authority: authority, moduleRef: moduleRef, operations: operations, now: now}
}

func (e *HAAvailabilityExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *HAAvailabilityExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("HA availability executor requires a context")
	}
	if e == nil || e.operations == nil || e.now == nil || strings.TrimSpace(e.binding.SiteRef) == "" ||
		strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.ModuleContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("HA availability executor requires one exact authenticated member binding")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed HA availability request: %w", err)
	}
	evaluatedAt := e.now().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("HA availability executor clock returned zero time")
	}
	target, health, policy, err := validateHAAvailabilityRequest(request, e.binding, e.authority, e.moduleRef, evaluatedAt)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	steps := []struct {
		operation string
		status    string
		run       func(context.Context, HAAvailabilityExpectation) (HAAvailabilityObservation, error)
	}{
		{"apply-ha-availability", "applied", func(ctx context.Context, value HAAvailabilityExpectation) (HAAvailabilityObservation, error) {
			return e.operations.ApplyHAAvailability(ctx, value)
		}},
		{"remove-obsolete-ha-availability", "reconciled", e.operations.RemoveObsoleteHAAvailability},
		{"verify-ha-availability", "ready", e.operations.VerifyHAAvailability},
	}
	observations := make([]HAAvailabilityObservation, 0, len(steps))
	notBefore := evaluatedAt
	for _, step := range steps {
		observation, runErr := step.run(ctx, cloneHAAvailabilityPolicy(policy))
		if runErr != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s: %w", step.operation, runErr)
		}
		checkedAt := e.now().UTC()
		if checkedAt.IsZero() || checkedAt.Before(notBefore) ||
			!validHAAvailabilityObservation(observation, policy, step.operation, step.status, evaluatedAt, checkedAt) {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("%s observation does not prove exact member-local HA state", step.operation)
		}
		observedAt, _ := time.Parse(time.RFC3339Nano, observation.ObservedAt)
		notBefore = observedAt
		observations = append(observations, observation)
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                    `json:"schemaVersion"`
		Apply         HAAvailabilityObservation `json:"apply"`
		Reconcile     HAAvailabilityObservation `json:"reconcile"`
		Verify        HAAvailabilityObservation `json:"verify"`
	}{"stackkit.ha-availability-evidence/v1", observations[0], observations[1], observations[2]})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal exact HA availability evidence: %w", err)
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	ref := strings.TrimPrefix(digest, "sha256:")
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://ha-availability/" + ref, ObservationDigest: digest,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://ha-availability/" + ref, ObservationDigest: digest,
		}},
	}, nil
}

func validateHAAvailabilityRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority HAAvailabilityAuthority, moduleRef string, evaluatedAt time.Time) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, HAAvailabilityApplyPolicy, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	profile, ok := haAvailabilityProfiles[moduleRef]
	if !ok || len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 ||
		len(request.AccessBindings) != 0 || len(request.BackupTargetBindings) != 0 || len(request.Artifacts) != 1 ||
		!validCoreHostBootstrapDigest(request.RequestDigest) {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, errors.New("HA availability executor requires one known realization, runtime, health target, artifact, and no external binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.HAAvailabilityExecutorContractRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != profile.moduleRef || target.OwnerVersion != "" ||
		target.ProviderRef != profile.providerRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != profile.moduleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.OwnerContractHash != authority.ModuleContractHash || target.UnitRef != haAvailabilityUnitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 ||
		len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, errors.New("runtime target is not the exact bound HA member contract")
	}
	instanceRef := haAvailabilityUnitRef + "-node-" + binding.NodeRef
	artifactID := profile.moduleRef + "-executor-contract-instance-" + instanceRef
	requirementID := profile.moduleRef + "/" + haAvailabilityUnitRef + "/" + instanceRef
	if target.RequirementID != requirementID || target.InstanceRef != instanceRef || target.ArtifactRefs[0] != artifactID {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, errors.New("runtime target does not bind the exact HA member artifact")
	}
	health := request.HealthTargets[0]
	healthID := "module-" + profile.moduleRef + "-" + profile.healthRef + "-node-" + binding.NodeRef
	if health.RequirementID != healthID || health.SourceRef != profile.healthRef || health.ContractHash != authority.HealthContractHash ||
		health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" || health.TargetRef != profile.moduleRef ||
		health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) ||
		!slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, errors.New("health target is not the exact member-local HA postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != artifactID || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != instanceRef || artifact.OwnerContractHash != target.UnitContractHash ||
		artifact.ProviderRef != profile.providerRef || artifact.ProviderContractHash != target.ProviderContractHash ||
		artifact.ModuleRef != profile.moduleRef || artifact.ModuleContractHash != target.ModuleContractHash ||
		artifact.UnitRef != haAvailabilityUnitRef || artifact.UnitContractHash != target.UnitContractHash ||
		artifact.InstanceRef != instanceRef || artifact.OutputRef != architecturev2renderer.HAAvailabilityOutputRef(profile.moduleRef) ||
		!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > haAvailabilityMaxArtifactBytes {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, errors.New("artifact is not the exact CUE-owned HA member instance")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, errors.New("HA availability artifact digest does not match immutable content")
	}
	governed, err := architecturev2renderer.ValidateHAAvailabilityExecutorArtifact(artifact.Content, profile.moduleRef, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, fmt.Errorf("validate governed HA availability policy: %w", err)
	}
	if governed.KitSlug != profile.kitSlug || governed.Policy.Mode != profile.mode {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, errors.New("HA artifact does not match the selected kit and mode realization")
	}
	stateDigest, err := digestHAAvailability(struct {
		StackID, KitSlug, ModuleRef, SiteRef, NodeRef string
		Policy                                        architecturev2renderer.HAAvailabilityPolicy
		FailureModel                                  architecturev2renderer.HAFailureModel
		Members                                       []architecturev2renderer.HAAvailabilityMember
	}{governed.StackID, governed.KitSlug, governed.ModuleID, binding.SiteRef, binding.NodeRef, governed.Policy, governed.FailureModel, governed.Members})
	if err != nil {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, err
	}
	policyDigest, err := digestHAAvailability(struct {
		RequestDigest, ArtifactDigest, StateDigest, ProviderContractHash, ModuleContractHash, HealthContractHash, ExecutionChannelRef string
	}{request.RequestDigest, artifact.Digest, stateDigest, authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, HAAvailabilityApplyPolicy{}, err
	}
	return target, health, HAAvailabilityApplyPolicy{
		PolicyDigest: policyDigest, RequestDigest: request.RequestDigest, ArtifactDigest: artifact.Digest, StateDigest: stateDigest,
		EvaluatedAt: evaluatedAt.Format(time.RFC3339Nano), StackID: governed.StackID, KitSlug: governed.KitSlug,
		ModuleRef: governed.ModuleID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		Policy: governed.Policy, FailureModel: governed.FailureModel,
		Members: append([]architecturev2renderer.HAAvailabilityMember(nil), governed.Members...),
	}, nil
}

func validHAAvailabilityObservation(observation HAAvailabilityObservation, expectation HAAvailabilityExpectation, operation, status string, evaluatedAt, checkedAt time.Time) bool {
	if observation.Operation != operation || observation.Status != status ||
		observation.PolicyDigest != expectation.PolicyDigest || observation.RequestDigest != expectation.RequestDigest ||
		observation.ArtifactDigest != expectation.ArtifactDigest || observation.StateDigest != expectation.StateDigest ||
		observation.EvaluatedAt != expectation.EvaluatedAt || observation.StackID != expectation.StackID ||
		observation.KitSlug != expectation.KitSlug || observation.ModuleRef != expectation.ModuleRef ||
		observation.SiteRef != expectation.SiteRef || observation.NodeRef != expectation.NodeRef ||
		observation.ExecutionChannelRef != expectation.ExecutionChannelRef || observation.Mode != expectation.Policy.Mode ||
		observation.PolicyRef != expectation.Policy.PolicyRef || observation.RealizationRef != expectation.Policy.RealizationRef ||
		observation.Fencing != expectation.Policy.Fencing || observation.FailureDomainSpread != expectation.Policy.FailureDomainSpread ||
		observation.PartitionBehavior != expectation.FailureModel.PartitionBehavior || !observation.FencingReady ||
		observation.ProviderAuthority || observation.WANQuorum || observation.GeneralLANAuthority || observation.IndependentFailover ||
		len(observation.Members) != len(expectation.Members) {
		return false
	}
	for index, member := range expectation.Members {
		readback := observation.Members[index]
		if readback.NodeRef != member.NodeRef || readback.SiteRef != member.SiteRef ||
			readback.FailureDomain != member.FailureDomain || !readback.Ready {
			return false
		}
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	return err == nil && observedAt.Location() == time.UTC && !observedAt.Before(evaluatedAt) &&
		!observedAt.After(checkedAt) && checkedAt.Sub(observedAt) <= haAvailabilityMaxReadbackAge
}

func cloneHAAvailabilityPolicy(policy HAAvailabilityApplyPolicy) HAAvailabilityApplyPolicy {
	policy.Members = append([]architecturev2renderer.HAAvailabilityMember(nil), policy.Members...)
	return policy
}

func digestHAAvailability(value any) (string, error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal HA availability authority: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

var _ runtimeexecutor.Executor = (*HAAvailabilityExecutor)(nil)
