package nativehost

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

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/mereach"
	"github.com/kombifyio/stackkits/internal/remotereach"
)

const (
	homePrivateRemoteAccessProviderRef      = "stackkits-home-private-remote-access"
	homePrivateRemoteAccessModuleRef        = "stackkits-home-private-remote-access-runtime"
	homePrivateRemoteAccessUnitRef          = "executor-contract"
	homePrivateRemoteAccessOutputRef        = "home/remote-access/executor-contract.json"
	homePrivateRemoteAccessArtifactPrefix   = "home-private-remote-access-executor-contract-instance-"
	homePrivateRemoteAccessHealthSourceRef  = "home-private-remote-access-health"
	homePrivateRemoteAccessMaxArtifactBytes = 512 << 10

	// HomePrivateRemoteAccessPathConnector is a reach connector that is
	// installed on this node (kombify.me tunnel mode, an own Cloudflare
	// Tunnel or a Wallet-delivered token).
	HomePrivateRemoteAccessPathConnector = "reach-connector"
	// HomePrivateRemoteAccessPathMeRegistration is the homelab's own kombify
	// Me registration: the first Apply of a kombify.me homelab happens before
	// the connector can start, and `stackkit reach enable --kombify-me` follows.
	HomePrivateRemoteAccessPathMeRegistration = "kombify-me-registration"
)

// HomePrivateRemoteAccessObservation names the remote reach path that realizes
// the capability. It carries no endpoint, token or credential.
type HomePrivateRemoteAccessObservation struct {
	Path       string `json:"path"`
	Mode       string `json:"mode,omitempty"`
	ObservedAt string `json:"observedAt"`
}

// HomePrivateRemoteAccessOperations is the finite capability of the Home
// private-remote-access owner: it proves that a standard reach path exists on
// this node. It starts nothing and exposes no generic shell or credential.
type HomePrivateRemoteAccessOperations interface {
	RequireReachPath(context.Context) (HomePrivateRemoteAccessObservation, error)
}

type osHomePrivateRemoteAccessOperations struct {
	workspace string
	now       func() time.Time
}

// NewOSHomePrivateRemoteAccessOperations reads the node's reach custody.
func NewOSHomePrivateRemoteAccessOperations(workspace string) HomePrivateRemoteAccessOperations {
	return osHomePrivateRemoteAccessOperations{workspace: workspace, now: time.Now}
}

// RequireReachPath fails closed with the way to enable one when the node has
// neither a reach connector nor a kombify Me registration of its own.
func (o osHomePrivateRemoteAccessOperations) RequireReachPath(context.Context) (HomePrivateRemoteAccessObservation, error) {
	observedAt := o.now().UTC().Format(time.RFC3339Nano)
	if state, err := (remotereach.Host{Workspace: o.workspace}).Load(); err == nil {
		return HomePrivateRemoteAccessObservation{Path: HomePrivateRemoteAccessPathConnector, Mode: state.Mode, ObservedAt: observedAt}, nil
	}
	if _, err := mereach.LoadRegistration(o.workspace); err == nil {
		return HomePrivateRemoteAccessObservation{Path: HomePrivateRemoteAccessPathMeRegistration, Mode: remotereach.ModeKombifyMe, ObservedAt: observedAt}, nil
	}
	return HomePrivateRemoteAccessObservation{}, errors.New("a route with exposure remote-private needs a remote reach path, and this node has none: " +
		"for a kombify.me homelab run 'stackkit reach registration set --base <base> --key-file <file>' and then 'sudo stackkit reach enable --kombify-me'; " +
		"for your own Cloudflare Tunnel or a Wallet-delivered token apply once without the remote-private route, run 'sudo stackkit reach enable --owner-token-file <file>' " +
		"or '--wallet-item <tenant>/<item>', and apply again. Without a reach path the route would be reachable from the LAN only")
}

// HomePrivateRemoteAccessAuthority is service-owned catalog authority selected
// at adapter registration. Request data can never define these hashes.
type HomePrivateRemoteAccessAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

// HomePrivateRemoteAccessExecutor realizes the private-remote-access capability
// through the standard reach paths (target section 8): the remote listener
// serves remote-private routes behind the Access Gate, and this owner refuses
// an Apply that has no reach path to serve them through.
type HomePrivateRemoteAccessExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  HomePrivateRemoteAccessAuthority
	operations HomePrivateRemoteAccessOperations
}

func NewHomePrivateRemoteAccessExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority HomePrivateRemoteAccessAuthority, operations HomePrivateRemoteAccessOperations) *HomePrivateRemoteAccessExecutor {
	return &HomePrivateRemoteAccessExecutor{identity: identity, binding: binding, authority: authority, operations: operations}
}

func (e *HomePrivateRemoteAccessExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	return e.identity
}

func (e *HomePrivateRemoteAccessExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home private-remote-access executor requires a context")
	}
	if e == nil || e.operations == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Home private-remote-access executor requires one explicit Home target binding")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed Home private-remote-access request: %w", err)
	}
	target, health, err := validateHomePrivateRemoteAccessRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	observation, err := e.operations.RequireReachPath(ctx)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(observation)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Home private-remote-access evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://home-private-remote-access/" + target.InstanceRef, ObservationDigest: digestString}},
		Health: []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://home-private-remote-access/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

//nolint:gocyclo // Keep the exact fail-closed authority checks linear and auditable at this executor boundary.
func validateHomePrivateRemoteAccessRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority HomePrivateRemoteAccessAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if !validCoreHostBootstrapDigest(request.RequestDigest) || len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, errors.New("Home private-remote-access executor requires exactly one runtime, one health target, and no access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.HomePrivateRemoteAccessExecutorBundleRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != homePrivateRemoteAccessModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != homePrivateRemoteAccessProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != homePrivateRemoteAccessModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != homePrivateRemoteAccessUnitRef || target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" ||
		target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, errors.New("runtime target is not the exact bound Home private-remote-access contract")
	}
	wantInstance := homePrivateRemoteAccessUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := homePrivateRemoteAccessArtifactPrefix + wantInstance
	wantRequirementID := homePrivateRemoteAccessModuleRef + "/" + homePrivateRemoteAccessUnitRef + "/" + wantInstance
	if target.RequirementID != wantRequirementID || target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, errors.New("runtime target does not bind the exact node-local Home private-remote-access artifact")
	}
	health := request.HealthTargets[0]
	wantHealthRequirementID := "module-" + homePrivateRemoteAccessModuleRef + "-" + homePrivateRemoteAccessHealthSourceRef + "-node-" + binding.NodeRef
	if health.RequirementID != wantHealthRequirementID || health.SourceRef != homePrivateRemoteAccessHealthSourceRef || health.ContractHash != authority.HealthContractHash ||
		health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" || health.TargetRef != homePrivateRemoteAccessModuleRef ||
		health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, errors.New("health target is not the exact Home private-remote-access postcondition")
	}
	artifact, err := exactOwnedArtifactWithPlanMetadata(request.Artifacts, wantArtifactID)
	if err != nil {
		return emptyTarget, emptyHealth, fmt.Errorf("select Home private-remote-access artifact: %w", err)
	}
	if artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != homePrivateRemoteAccessProviderRef || artifact.ProviderContractHash != target.ProviderContractHash ||
		artifact.ModuleRef != homePrivateRemoteAccessModuleRef || artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != homePrivateRemoteAccessUnitRef ||
		artifact.UnitContractHash != target.UnitContractHash || artifact.InstanceRef != wantInstance || artifact.OutputRef != homePrivateRemoteAccessOutputRef ||
		!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > homePrivateRemoteAccessMaxArtifactBytes {
		return emptyTarget, emptyHealth, errors.New("artifact is not the exact CUE-owned Home private-remote-access instance")
	}
	digest := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return emptyTarget, emptyHealth, errors.New("Home private-remote-access artifact digest does not match its immutable content")
	}
	return target, health, nil
}

var _ runtimeexecutor.Executor = (*HomePrivateRemoteAccessExecutor)(nil)
