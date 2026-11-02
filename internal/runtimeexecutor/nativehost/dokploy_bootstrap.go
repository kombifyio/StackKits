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

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

// DokployBootstrapObservation is the secret-free readback of the node's
// Dokploy: one owner, closed registration, a node-local API key that
// authorizes the StackKits project, no Swarm and no second ingress.
type DokployBootstrapObservation struct {
	Status             string `json:"status"`
	Image              string `json:"image"`
	DatabaseImage      string `json:"databaseImage"`
	OwnerEmail         string `json:"ownerEmail"`
	SingleOwner        bool   `json:"singleOwner"`
	RegistrationClosed bool   `json:"registrationClosed"`
	APIPrivate         bool   `json:"apiPrivate"`
	APIKeyAuthorized   bool   `json:"apiKeyAuthorized"`
	EnvironmentID      string `json:"environmentId"`
	Swarm              bool   `json:"swarm"`
	Ingress            string `json:"ingress"`
	UIProtected        bool   `json:"uiProtected"`
	Login              string `json:"login"`
}

type DokployBootstrapOperations interface {
	ApplyDokploy(context.Context, architecturev2renderer.DokployBootstrapDocument) (DokployBootstrapObservation, error)
}

type DokployBootstrapExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	operations DokployBootstrapOperations
}

func NewDokployBootstrapExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, operations DokployBootstrapOperations) *DokployBootstrapExecutor {
	return &DokployBootstrapExecutor{identity: identity, binding: binding, operations: operations}
}

func (e *DokployBootstrapExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *DokployBootstrapExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil || e == nil || e.operations == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Dokploy bootstrap requires its construction-owned local node")
	}
	target, health, document, err := validateDokployBootstrapRequest(request, e.binding)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	observed, err := e.operations.ApplyDokploy(ctx, document)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	if observed.Status != "ready" || !observed.SingleOwner || !observed.RegistrationClosed || !observed.APIPrivate || !observed.APIKeyAuthorized ||
		observed.EnvironmentID == "" || observed.Swarm || observed.Ingress != "kit-router" || !observed.UIProtected {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Dokploy bootstrap did not prove its single owner, private API, Kit-router ingress and protected UI postconditions")
	}
	images := map[string]string{}
	for _, image := range document.Components {
		images[image.ID] = image.ImageRef + "@" + image.ImageDigest
	}
	if observed.Image != images["dokploy"] || observed.DatabaseImage != images["postgres"] {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Dokploy bootstrap observation differs from the selected image pins")
	}
	raw, err := json.Marshal(observed)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	sum := sha256.Sum256(raw)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://dokploy-bootstrap/" + target.InstanceRef, ObservationDigest: digest}},
		Health: []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://dokploy-bootstrap/" + target.InstanceRef, ObservationDigest: digest}},
	}, nil
}

func validateDokployBootstrapRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, architecturev2renderer.DokployBootstrapDocument, error) {
	invalid := func(message string) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, architecturev2renderer.DokployBootstrapDocument, error) {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, architecturev2renderer.DokployBootstrapDocument{}, errors.New(message)
	}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 {
		return invalid("Dokploy bootstrap requires exactly its runtime and health target")
	}
	target, health := request.RuntimeTargets[0], request.HealthTargets[0]
	module, unit := architecturev2renderer.DokployBootstrapModuleRef, architecturev2renderer.DokployBootstrapUnitRef
	if target.OwnerKind != "module" || target.OwnerRef != module || target.ModuleRef != module || target.UnitRef != unit ||
		target.ProviderRef != "stackkits-dokploy" || target.OwnerVersion != "" || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return invalid("Dokploy bootstrap target differs from the exact node-local selected adapter contract")
	}
	if health.SourceRef != "dokploy-bootstrap-ready" || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != module || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return invalid("Dokploy bootstrap health target differs from its closed postcondition")
	}
	artifact, err := exactOwnedArtifactWithPlanMetadata(request.Artifacts, target.ArtifactRefs[0])
	if err != nil {
		return invalid(fmt.Sprintf("select Dokploy bootstrap artifact: %v", err))
	}
	contract := architecturev2renderer.DokployBootstrapRendererContract()
	if artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0600" || artifact.OwnerKind != "render-instance" ||
		artifact.ModuleRef != module || artifact.UnitRef != unit || artifact.OutputRef != architecturev2renderer.DokployBootstrapOutputRef ||
		target.UnitContractHash != contract.ContractHash || artifact.UnitContractHash != contract.ContractHash ||
		len(artifact.Content) == 0 || len(artifact.Content) > 64<<10 {
		return invalid("Dokploy bootstrap artifact differs from its exact CUE-owned recipe")
	}
	document, err := architecturev2renderer.ParseDokployBootstrapDocument(artifact.Content)
	if err != nil {
		return invalid(err.Error())
	}
	if document.SiteRef != binding.SiteRef || document.NodeRef != binding.NodeRef {
		return invalid("Dokploy bootstrap recipe names another node")
	}
	return target, health, document, nil
}
