package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const hostAdmissionProviderRef = "stackkits-host-admission"

type HostAdmissionAuthority struct {
	ProviderContractHash string
	HealthContractHash   string
}

// HostAdmissionExecutor projects the host-conformance boundary that Product
// Apply has already verified into the runtime graph. It owns no probe,
// enrollment, credential, host mutation, provider lifecycle, or artifact
// capability.
type HostAdmissionExecutor struct {
	identity  runtimeexecutor.ExecutorIdentity
	binding   LocalTargetBinding
	authority HostAdmissionAuthority
}

func NewHostAdmissionExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority HostAdmissionAuthority) *HostAdmissionExecutor {
	return &HostAdmissionExecutor{identity: identity, binding: binding, authority: authority}
}

func (e *HostAdmissionExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *HostAdmissionExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("host-admission executor requires a context")
	}
	if e == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" ||
		strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("host-admission executor requires exact local authority")
	}
	if err := ctx.Err(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 ||
		!hostAdmissionHasOnlyPlanMetadata(request.Artifacts) || len(request.AccessBindings) != 0 {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("host-admission projection requires one runtime, one health target, and no mutable input")
	}
	target := request.RuntimeTargets[0]
	if target.OwnerKind != "provider-owner" || target.OwnerRef != hostAdmissionProviderRef ||
		target.ProviderRef != hostAdmissionProviderRef || target.OwnerVersion != "1.0.0" ||
		target.OwnerContractHash != e.authority.ProviderContractHash ||
		target.ProviderContractHash != e.authority.ProviderContractHash ||
		target.ModuleRef != "" || target.ModuleContractHash != "" ||
		target.UnitRef != "" || target.UnitContractHash != "" || target.WorkloadRef != "" ||
		target.ImageRef != "" || target.ImageDigest != "" || target.RuntimeAdapter != nil ||
		target.RuntimeKind != "host" ||
		target.RuntimeDelivery != "provider-owner" || target.RuntimeEngine != "" ||
		target.RequirementID != "provider-owner/stackkits-host-admission/"+e.binding.NodeRef ||
		target.InstanceRef != e.binding.NodeRef || target.ExecutionChannelRef != e.binding.ExecutionChannelRef ||
		len(target.ArtifactRefs) != 0 || len(target.DaemonBindings) != 0 ||
		len(target.AccessCapabilities) != 0 || len(target.BackupTargetCapabilities) != 0 ||
		len(target.AccessBindingRefs) != 0 || len(target.BackupTargetBindingRefs) != 0 ||
		len(target.SiteRefs) != 1 || target.SiteRefs[0] != e.binding.SiteRef ||
		len(target.NodeRefs) != 1 || target.NodeRefs[0] != e.binding.NodeRef {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("runtime target is not the exact verified local host-admission projection")
	}
	health := request.HealthTargets[0]
	if health.RequirementID != "provider-stackkits-host-admission-stackkits-host-admission-contract" ||
		health.RuntimeRequirementID != "" || health.SourceRef != "stackkits-host-admission-contract" ||
		health.ContractHash != e.authority.HealthContractHash ||
		health.Phase != "post-apply" || health.Kind != "contract" ||
		health.TargetKind != "provider" || health.TargetRef != hostAdmissionProviderRef ||
		health.Probe != nil || health.RouteRef != "" || health.BackendPoolRef != "" ||
		len(health.SiteRefs) != 1 || health.SiteRefs[0] != e.binding.SiteRef ||
		len(health.NodeRefs) != 1 || health.NodeRefs[0] != e.binding.NodeRef {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("health target is not the exact verified local host-admission contract")
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string `json:"schemaVersion"`
		PlanHash      string `json:"planHash"`
		Requirements  string `json:"requirementsHash"`
		Evidence      string `json:"evidenceBundleHash"`
		NodeRef       string `json:"nodeRef"`
	}{
		SchemaVersion: "stackkit.host-admission-projection-evidence/v1",
		PlanHash:      request.PlanHash, Requirements: request.RequirementsHash,
		Evidence: request.EvidenceBundleHash, NodeRef: target.InstanceRef,
	})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:            runtimeexecutor.RuntimeStatusApplied,
			ObservationRef:    "runtime-observation://host-admission/" + target.InstanceRef,
			ObservationDigest: digest,
		}},
		Health: []runtimeexecutor.HealthOutcome{{
			RequirementID: health.RequirementID, TargetRef: health.TargetRef,
			Status:            runtimeexecutor.HealthStatusHealthy,
			ObservationRef:    "health-observation://host-admission/" + target.InstanceRef,
			ObservationDigest: digest,
		}},
	}, nil
}

func hostAdmissionHasOnlyPlanMetadata(artifacts []runtimeexecutor.Artifact) bool {
	for _, artifact := range artifacts {
		if artifact.OwnerKind != "plan" || artifact.ExecutionClass != runtimeexecutor.ArtifactExecutionClassPlan ||
			artifact.Kind != "metadata" || artifact.Format != "json" {
			return false
		}
	}
	return true
}

var _ runtimeexecutor.Executor = (*HostAdmissionExecutor)(nil)
