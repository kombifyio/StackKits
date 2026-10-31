package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

type RuntipiMarketplaceExecutor struct {
	identity           runtimeexecutor.ExecutorIdentity
	binding            LocalTargetBinding
	healthContractHash string
	operations         RuntipiMarketplaceOperations
}

func NewRuntipiMarketplaceExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, healthContractHash string, operations RuntipiMarketplaceOperations) *RuntipiMarketplaceExecutor {
	return &RuntipiMarketplaceExecutor{identity, binding, healthContractHash, operations}
}
func (e *RuntipiMarketplaceExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }
func (e *RuntipiMarketplaceExecutor) Execute(ctx context.Context, r runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil || e == nil || e.operations == nil || len(r.RuntimeTargets) != 1 || len(r.HealthTargets) == 0 {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Marketplace requires its exact construction-owned node runtime and health requirements")
	}
	t := r.RuntimeTargets[0]
	module := architecturev2renderer.MarketplaceRuntipiModuleRef
	d, err := RuntipiMarketplaceInstallationDocument(t, r.Artifacts, e.binding)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	// Reuse the central application module/backend health boundary; the
	// Marketplace installation does not introduce a second route owner.
	spec := selectedPaaSApplicationSpec{name: "Marketplace", moduleRef: module, healthRef: "runtipi-bootstrap-ready", expectedStatuses: []int{200}}
	if err := spec.validateHealth(r.HealthTargets, t, SelectedPaaSWorkloadAuthority{HealthContractHash: e.healthContractHash}, d.Route, architecturev2renderer.ApplicationDeliveryComponentDescriptor{HealthPort: 3000, HealthPath: "/api/health"}); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	observation, err := e.operations.ApplyRuntipiMarketplace(ctx, d)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	if observation.Status != "ready" || !observation.OperatorRegistered || !observation.RegistrationClosed || !observation.APIPrivate || observation.InstallationID == "" || observation.Release != architecturev2renderer.RuntipiRelease {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Marketplace installation did not establish its admin, identity and private API postconditions")
	}
	if d.Route.ID != "" && !observation.UIProtected {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Marketplace private UI postcondition is missing")
	}
	data, err := json.Marshal(observation)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	hash := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	health := make([]runtimeexecutor.HealthOutcome, 0, len(r.HealthTargets))
	for _, requirement := range r.HealthTargets {
		health = append(health, runtimeexecutor.HealthOutcome{RequirementID: requirement.RequirementID, TargetRef: requirement.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://marketplace/" + t.InstanceRef, ObservationDigest: digest})
	}
	return runtimeexecutor.ExecutionOutcome{Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: t.RequirementID, InstanceRef: t.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://marketplace/" + t.InstanceRef, ObservationDigest: digest}}, Health: health}, nil
}

// RuntipiMarketplaceInstallationDocument narrows an authenticated Apply request to
// the same immutable node recipe used by installation and backup custody.
// It grants no effects and never accepts paths or mutable Docker metadata as
// artifact authority.
func RuntipiMarketplaceInstallationDocument(t runtimeexecutor.RuntimeTarget, artifacts []runtimeexecutor.Artifact, binding LocalTargetBinding) (architecturev2renderer.MarketplaceRuntipiDocument, error) {
	module := architecturev2renderer.MarketplaceRuntipiModuleRef
	contract := architecturev2renderer.MarketplaceRuntipiRendererContract()
	if t.OwnerKind != "module" || t.OwnerRef != module || t.ModuleRef != module || t.UnitRef != architecturev2renderer.MarketplaceRuntipiUnitRef || t.ProviderRef != "stackkits-marketplace" || t.WorkloadRef != "marketplace" || t.RuntimeKind != "host" || t.RuntimeDelivery != "stackkit" || t.RuntimeEngine != "" || t.RuntimeAdapter != nil || len(t.DaemonBindings) != 0 || len(t.ArtifactRefs) != 1 || !slices.Equal(t.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(t.NodeRefs, []string{binding.NodeRef}) || t.ExecutionChannelRef != binding.ExecutionChannelRef || t.UnitContractHash != contract.ContractHash {
		return architecturev2renderer.MarketplaceRuntipiDocument{}, errors.New("Marketplace target differs from its exact node application installation contract")
	}
	a, err := exactOwnedArtifactWithPlanMetadata(artifacts, t.ArtifactRefs[0])
	if err != nil {
		return architecturev2renderer.MarketplaceRuntipiDocument{}, err
	}
	if a.Kind != "native-config" || a.Format != "json" || a.Mode != "0600" || a.OwnerKind != "render-instance" || a.ModuleRef != module || a.UnitRef != t.UnitRef || a.OutputRef != architecturev2renderer.MarketplaceRuntipiOutputRef || a.UnitContractHash != contract.ContractHash || len(a.Content) > 128<<10 {
		return architecturev2renderer.MarketplaceRuntipiDocument{}, errors.New("Marketplace requires the exact private CUE-owned installation artifact")
	}
	d, err := architecturev2renderer.ParseMarketplaceRuntipiDocument(a.Content)
	if err == nil && (d.SiteRef != binding.SiteRef || d.NodeRef != binding.NodeRef) {
		err = errors.New("Marketplace installation names another node")
	}
	return d, err
}
