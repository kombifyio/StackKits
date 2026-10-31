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

type KomodoBootstrapObservation struct {
	Status             string `json:"status"`
	ServerID           string `json:"serverId"`
	CoreImage          string `json:"coreImage"`
	PeripheryImage     string `json:"peripheryImage"`
	PeripheryConnected bool   `json:"peripheryConnected"`
	MinimumTLSVersion  string `json:"minimumTLSVersion"`
	PrivateAPI         bool   `json:"privateApi"`
}

type KomodoBootstrapOperations interface {
	ApplyKomodo(context.Context, architecturev2renderer.KomodoBootstrapDocument) (KomodoBootstrapObservation, error)
}

type KomodoBootstrapExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	operations KomodoBootstrapOperations
}

func NewKomodoBootstrapExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, operations KomodoBootstrapOperations) *KomodoBootstrapExecutor {
	return &KomodoBootstrapExecutor{identity: identity, binding: binding, operations: operations}
}
func (e *KomodoBootstrapExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }
func (e *KomodoBootstrapExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil || e == nil || e.operations == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Komodo bootstrap requires its construction-owned local node")
	}
	target, health, document, err := validateKomodoBootstrapRequest(request, e.binding)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	observed, err := e.operations.ApplyKomodo(ctx, document)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	if observed.Status != "ready" || observed.ServerID == "" || !observed.PeripheryConnected || !observed.PrivateAPI ||
		observed.MinimumTLSVersion != "TLS1.3" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Komodo bootstrap did not prove its private API and outbound TLS/mutual-key postconditions")
	}
	images := map[string]string{}
	for _, image := range document.Components {
		images[image.ID] = image.ImageRef + "@" + image.ImageDigest
	}
	if observed.CoreImage != images["core"] || observed.PeripheryImage != images["periphery"] {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Komodo bootstrap observation differs from the selected image pins")
	}
	raw, err := json.Marshal(observed)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	sum := sha256.Sum256(raw)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://komodo-bootstrap/" + target.InstanceRef, ObservationDigest: digest}},
		Health: []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://komodo-bootstrap/" + target.InstanceRef, ObservationDigest: digest}},
	}, nil
}

func validateKomodoBootstrapRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, architecturev2renderer.KomodoBootstrapDocument, error) {
	invalid := func(message string) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, architecturev2renderer.KomodoBootstrapDocument, error) {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}, architecturev2renderer.KomodoBootstrapDocument{}, errors.New(message)
	}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 {
		return invalid("Komodo bootstrap requires exactly its runtime and health target")
	}
	target, health := request.RuntimeTargets[0], request.HealthTargets[0]
	module, unit := architecturev2renderer.KomodoBootstrapModuleRef, architecturev2renderer.KomodoBootstrapUnitRef
	if target.OwnerKind != "module" || target.OwnerRef != module || target.ModuleRef != module || target.UnitRef != unit ||
		target.ProviderRef != "stackkits-komodo" || target.OwnerVersion != "" || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return invalid("Komodo bootstrap target differs from the exact node-local selected adapter contract")
	}
	if health.SourceRef != "komodo-bootstrap-ready" || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != module || health.RouteRef != "" || health.BackendPoolRef != "" ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return invalid("Komodo bootstrap health target differs from its closed postcondition")
	}
	artifact, err := exactOwnedArtifactWithPlanMetadata(request.Artifacts, target.ArtifactRefs[0])
	if err != nil {
		return invalid(fmt.Sprintf("select Komodo bootstrap artifact: %v", err))
	}
	contract := architecturev2renderer.KomodoBootstrapRendererContract()
	if artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0600" || artifact.OwnerKind != "render-instance" ||
		artifact.ModuleRef != module || artifact.UnitRef != unit || artifact.OutputRef != architecturev2renderer.KomodoBootstrapOutputRef ||
		target.UnitContractHash != contract.ContractHash || artifact.UnitContractHash != contract.ContractHash ||
		len(artifact.Content) == 0 || len(artifact.Content) > 64<<10 {
		return invalid("Komodo bootstrap artifact differs from its exact CUE-owned recipe")
	}
	document, err := architecturev2renderer.ParseKomodoBootstrapDocument(artifact.Content)
	if err != nil {
		return invalid(err.Error())
	}
	if document.SiteRef != binding.SiteRef || document.NodeRef != binding.NodeRef {
		return invalid("Komodo bootstrap recipe names another node")
	}
	return target, health, document, nil
}
