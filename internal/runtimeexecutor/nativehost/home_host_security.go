package nativehost

import (
	"bytes"
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
	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	homeHostSecurityProviderRef       = "stackkits-home-host-security"
	homeHostSecurityModuleRef         = "stackkits-home-host-security-runtime"
	homeHostSecurityUnitRef           = "executor-contract"
	homeHostSecurityOutputRef         = "home/host-security/executor-contract.json"
	homeHostSecurityArtifactPrefix    = "home-host-security-executor-contract-instance-"
	homeHostSecurityHealthSourceRef   = "home-host-security-health"
	homeHostSecurityMaxArtifactBytes  = 64 << 10
	homeHostSecurityMaxObservationAge = 5 * time.Minute
)

// HomeHostSecurityControlState is one enforced control as the host reported it.
type HomeHostSecurityControlState struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	ReasonCode string `json:"reasonCode,omitempty"`
}

// HomeHostSecurityObservation is what the enforcement operation proved about
// the host after it changed it: the baseline version, every enforced control's
// measured state, and the digest of the evidence document it committed.
type HomeHostSecurityObservation struct {
	BaselineVersion string                         `json:"baselineVersion"`
	ObservedAt      string                         `json:"observedAt"`
	Controls        []HomeHostSecurityControlState `json:"controls"`
	EvidenceDigest  string                         `json:"evidenceDigest"`
	// Findings are open, actionable deviations the apply tolerated (the module
	// is applied and healthy with findings); each has a stable reason code.
	Findings []string `json:"findings,omitempty"`
}

// HomeHostSecurityOperations is the finite capability of the Home host
// security owner. It enforces the baseline on the local host without cutting
// the management path and reads the result back; it exposes no generic shell,
// provider, endpoint, credential or server-lifecycle operation.
type HomeHostSecurityOperations interface {
	Enforce(context.Context) (HomeHostSecurityObservation, error)
}

// HomeHostSecurityAuthority is service-owned catalog authority selected at
// adapter registration. Request data can never define these hashes.
type HomeHostSecurityAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

// HomeHostSecurityExecutor enforces the exact CUE-owned Home host security
// policy on one previously authorized node.
type HomeHostSecurityExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  HomeHostSecurityAuthority
	operations HomeHostSecurityOperations
	clock      func() time.Time
}

func NewHomeHostSecurityExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority HomeHostSecurityAuthority, operations HomeHostSecurityOperations) *HomeHostSecurityExecutor {
	return &HomeHostSecurityExecutor{identity: identity, binding: binding, authority: authority, operations: operations, clock: time.Now}
}

func (e *HomeHostSecurityExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *HomeHostSecurityExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home host-security executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home host-security executor requires one explicit Home target binding")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed Home host-security request: %w", err)
	}
	target, health, err := validateHomeHostSecurityRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	startedAt := e.clock().UTC()
	observation, err := e.operations.Enforce(ctx)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("enforce the Home host security baseline: %w", err)
	}
	if err := validateHomeHostSecurityObservation(observation, startedAt, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(observation)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Home host-security evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://home-host-security/" + strings.TrimPrefix(digestString, "sha256:"), ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://home-host-security/" + strings.TrimPrefix(digestString, "sha256:"), ObservationDigest: digestString}},
	}, nil
}

// validateHomeHostSecurityObservation refuses anything short of every enforced
// control being compliant or an owner-approved exception, measured during this
// invocation: an unknown control is not proof. One open finding is tolerated: a
// host with no authorized SSH key keeps password logins (disabling them would
// lock the owner out), reported as ssh.password_authentication drifted with the
// no_authorized_key reason code. Everything else stays enforced.
func validateHomeHostSecurityObservation(observation HomeHostSecurityObservation, startedAt, checkedAt time.Time) error {
	if observation.BaselineVersion != hostsecurity.BaselineVersion {
		return fmt.Errorf("the enforcement proved baseline %q, not %q", observation.BaselineVersion, hostsecurity.BaselineVersion)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil || observedAt.Location() != time.UTC || observedAt.Before(startedAt.Add(-time.Second)) || observedAt.After(checkedAt.Add(time.Second)) ||
		checkedAt.Sub(observedAt) > homeHostSecurityMaxObservationAge {
		return errors.New("the Home host-security observation is not canonical UTC within the invocation freshness window")
	}
	if !validCoreHostBootstrapDigest(observation.EvidenceDigest) {
		return errors.New("the Home host-security evidence was not committed under a digest")
	}
	states := map[string]HomeHostSecurityControlState{}
	for _, control := range observation.Controls {
		states[control.ID] = control
	}
	for _, id := range hostsecurity.EnforcedControls() {
		control := states[id]
		switch {
		case control.State == string(hostsecurity.StateCompliant) || control.State == string(hostsecurity.StateException):
		case id == hostsecurity.ControlSSHPassword && control.State == string(hostsecurity.StateDrifted) && control.ReasonCode == hostsecurity.ReasonNoAuthorizedKey:
		default:
			return fmt.Errorf("control %s is %q after enforcement; the Home host security baseline is not met", id, valueOrMissing(control.State))
		}
	}
	return nil
}

func valueOrMissing(state string) string {
	if state == "" {
		return "missing"
	}
	return state
}

//nolint:gocyclo // Keep the exact fail-closed authority checks linear and auditable at this executor boundary.
func validateHomeHostSecurityRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority HomeHostSecurityAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if !validCoreHostBootstrapDigest(request.RequestDigest) || len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, errors.New("Home host-security executor requires exactly one runtime, one health target, and no access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.HomeHostSecurityRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != homeHostSecurityModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != homeHostSecurityProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != homeHostSecurityModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != homeHostSecurityUnitRef || target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" ||
		target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, errors.New("runtime target is not the exact bound Home host-security contract")
	}
	wantInstance := homeHostSecurityUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := homeHostSecurityArtifactPrefix + wantInstance
	wantRequirementID := homeHostSecurityModuleRef + "/" + homeHostSecurityUnitRef + "/" + wantInstance
	if target.RequirementID != wantRequirementID || target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, errors.New("runtime target does not bind the exact node-local Home host-security artifact")
	}
	health := request.HealthTargets[0]
	wantHealthRequirementID := "module-" + homeHostSecurityModuleRef + "-" + homeHostSecurityHealthSourceRef + "-node-" + binding.NodeRef
	if health.RequirementID != wantHealthRequirementID ||
		health.SourceRef != homeHostSecurityHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != homeHostSecurityModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, errors.New("health target is not the exact Home host-security postcondition")
	}
	artifact, err := exactOwnedArtifactWithPlanMetadata(request.Artifacts, wantArtifactID)
	if err != nil {
		return emptyTarget, emptyHealth, fmt.Errorf("select Home host-security artifact: %w", err)
	}
	if artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != homeHostSecurityProviderRef || artifact.ProviderContractHash != target.ProviderContractHash ||
		artifact.ModuleRef != homeHostSecurityModuleRef || artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != homeHostSecurityUnitRef || artifact.UnitContractHash != target.UnitContractHash ||
		artifact.InstanceRef != wantInstance || artifact.OutputRef != homeHostSecurityOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > homeHostSecurityMaxArtifactBytes {
		return emptyTarget, emptyHealth, errors.New("artifact is not the exact CUE-owned Home host-security instance")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, errors.New("Home host-security artifact digest does not match its immutable content")
	}
	if !bytes.Equal(artifact.Content, architecturev2renderer.HomeHostSecurityPolicyBytes()) {
		return emptyTarget, emptyHealth, errors.New("artifact is not the exact governed Home host-security policy")
	}
	return target, health, nil
}

var _ runtimeexecutor.Executor = (*HomeHostSecurityExecutor)(nil)
