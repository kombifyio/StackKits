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

type MarketplaceExecutor struct {
	identity           runtimeexecutor.ExecutorIdentity
	binding            LocalTargetBinding
	healthContractHash string
	operations         MarketplaceOperations
}

func NewMarketplaceExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, healthContractHash string, operations MarketplaceOperations) *MarketplaceExecutor {
	return &MarketplaceExecutor{identity, binding, healthContractHash, operations}
}
func (e *MarketplaceExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }
func (e *MarketplaceExecutor) Execute(ctx context.Context, r runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil || e == nil || e.operations == nil || len(r.RuntimeTargets) != 1 || len(r.HealthTargets) == 0 {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Marketplace requires its exact construction-owned node runtime and health requirements")
	}
	t := r.RuntimeTargets[0]
	module := architecturev2renderer.MarketplaceCoolifyModuleRef
	d, err := MarketplaceInstallationDocument(t, r.Artifacts, e.binding)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	// Reuse the central application module/backend health boundary; the
	// Marketplace installation does not introduce a second route owner.
	spec := selectedPaaSApplicationSpec{name: "Marketplace", moduleRef: module, healthRef: "marketplace-bootstrap-ready", expectedStatuses: []int{200}}
	if err := spec.validateHealth(r.HealthTargets, t, SelectedPaaSWorkloadAuthority{HealthContractHash: e.healthContractHash}, d.Route, architecturev2renderer.ApplicationDeliveryComponentDescriptor{HealthPort: 8080, HealthPath: "/api/health"}); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	observation, err := e.operations.ApplyMarketplace(ctx, d)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	if observation.Status != "ready" || !observation.RootAdmin || !observation.RegistrationClosed || !observation.AutoUpdateDisabled || !observation.APIPrivate || observation.PocketIDClientID != "stackkit-marketplace" || observation.ServerID == "" || observation.Proxy != "NONE" || !observation.OwnHostSSH {
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

// MarketplaceInstallationDocument narrows an authenticated Apply request to
// the same immutable node recipe used by installation and backup custody.
// It grants no effects and never accepts paths or mutable Docker metadata as
// artifact authority.
func MarketplaceInstallationDocument(t runtimeexecutor.RuntimeTarget, artifacts []runtimeexecutor.Artifact, binding LocalTargetBinding) (architecturev2renderer.MarketplaceCoolifyDocument, error) {
	module := architecturev2renderer.MarketplaceCoolifyModuleRef
	contract := architecturev2renderer.MarketplaceCoolifyRendererContract()
	if t.OwnerKind != "module" || t.OwnerRef != module || t.ModuleRef != module || t.UnitRef != architecturev2renderer.MarketplaceCoolifyUnitRef || t.ProviderRef != "stackkits-marketplace" || t.WorkloadRef != "marketplace" || t.RuntimeKind != "host" || t.RuntimeDelivery != "stackkit" || t.RuntimeEngine != "" || t.RuntimeAdapter != nil || len(t.DaemonBindings) != 0 || len(t.ArtifactRefs) != 1 || !slices.Equal(t.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(t.NodeRefs, []string{binding.NodeRef}) || t.ExecutionChannelRef != binding.ExecutionChannelRef || t.UnitContractHash != contract.ContractHash {
		return architecturev2renderer.MarketplaceCoolifyDocument{}, errors.New("Marketplace target differs from its exact node application installation contract")
	}
	a, err := exactOwnedArtifactWithPlanMetadata(artifacts, t.ArtifactRefs[0])
	if err != nil {
		return architecturev2renderer.MarketplaceCoolifyDocument{}, err
	}
	if a.Kind != "native-config" || a.Format != "json" || a.Mode != "0600" || a.OwnerKind != "render-instance" || a.ModuleRef != module || a.UnitRef != t.UnitRef || a.OutputRef != architecturev2renderer.MarketplaceCoolifyOutputRef || a.UnitContractHash != contract.ContractHash || len(a.Content) > 128<<10 {
		return architecturev2renderer.MarketplaceCoolifyDocument{}, errors.New("Marketplace requires the exact private CUE-owned installation artifact")
	}
	d, err := architecturev2renderer.ParseMarketplaceCoolifyDocument(a.Content)
	if err == nil && (d.SiteRef != binding.SiteRef || d.NodeRef != binding.NodeRef) {
		err = errors.New("Marketplace installation names another node")
	}
	return d, err
}
