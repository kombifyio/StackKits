package runtimeexecutor

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/referenceidv2"
)

// ExecutionChannelRequest is one exact provider-neutral routing scope already
// selected by the owning control plane. ChannelRef is opaque: it is not an
// endpoint, address, provider handle, credential, lease, or discovery grant.
type ExecutionChannelRequest struct {
	ChannelRef     string          `json:"channel_ref"`
	SiteRef        string          `json:"site_ref"`
	NodeRef        string          `json:"node_ref"`
	RuntimeTargets []RuntimeTarget `json:"runtime_targets"`
	HealthTargets  []HealthTarget  `json:"health_targets"`
}

// ExecutionChannelLocalExecutor lazily constructs an in-process executor for
// the exact channel scope. A remote admission never needs to call it or possess
// the local Operations dependencies behind it.
type ExecutionChannelLocalExecutor func() (Executor, error)

// ExecutionChannelAdmission binds one already admitted scope to an executor.
// The implementation owns transport and authentication policy outside this
// DTO; it may return a remote executor without invoking local.
type ExecutionChannelAdmission interface {
	PrepareExecutionChannel(ExecutionChannelLocalExecutor) (Executor, error)
}

// ExecutionChannelFactory admits one exact Site/node/channel scope from
// service-owned configuration before any runtime owner is prepared.
type ExecutionChannelFactory interface {
	AdmitExecutionChannel(ExecutionChannelRequest) (ExecutionChannelAdmission, error)
}

// Validate rejects a channel whose Runtime or Health closure escapes its exact
// Site, node, or opaque channel, or whose Health ownership is ambiguous.
func (request ExecutionChannelRequest) Validate() error {
	if !referenceid.ValidExecutionChannel(request.ChannelRef) {
		return invalidRequest("execution_channel.channel_ref", "must be a canonical non-secret execution-channel identity")
	}
	for _, field := range []struct{ name, value string }{
		{"site_ref", request.SiteRef}, {"node_ref", request.NodeRef},
	} {
		if err := validateToken("execution_channel."+field.name, field.value); err != nil {
			return err
		}
	}
	if len(request.RuntimeTargets) == 0 || len(request.RuntimeTargets) > MaxRuntimeTargets {
		return invalidRequest("execution_channel.runtime_targets", "must contain 1..%d exact targets", MaxRuntimeTargets)
	}
	if len(request.HealthTargets) == 0 || len(request.HealthTargets) > MaxHealthTargets {
		return invalidRequest("execution_channel.health_targets", "must contain 1..%d exact targets", MaxHealthTargets)
	}
	if err := validateRuntimeTargets(request.RuntimeTargets); err != nil {
		return err
	}
	if err := validateHealthTargets(request.HealthTargets); err != nil {
		return err
	}
	seenRequirements := make(map[string]struct{}, len(request.RuntimeTargets))
	healthByRuntime := make(map[string]int, len(request.RuntimeTargets))
	for index, target := range request.RuntimeTargets {
		if len(target.SiteRefs) != 1 || target.SiteRefs[0] != request.SiteRef ||
			len(target.NodeRefs) != 1 || target.NodeRefs[0] != request.NodeRef ||
			target.ExecutionChannelRef != request.ChannelRef {
			return invalidRequest(fmt.Sprintf("execution_channel.runtime_targets[%d]", index), "must bind the exact channel Site, node, and opaque ref")
		}
		if _, duplicate := seenRequirements[target.RequirementID]; duplicate {
			return invalidRequest(fmt.Sprintf("execution_channel.runtime_targets[%d].requirement_id", index), "must be unique inside one channel")
		}
		seenRequirements[target.RequirementID] = struct{}{}
	}
	for index, health := range request.HealthTargets {
		if len(health.SiteRefs) != 1 || health.SiteRefs[0] != request.SiteRef ||
			len(health.NodeRefs) != 1 || health.NodeRefs[0] != request.NodeRef {
			return invalidRequest(fmt.Sprintf("execution_channel.health_targets[%d]", index), "must bind the exact channel Site and node")
		}
		owner := ""
		matches := 0
		for _, target := range request.RuntimeTargets {
			if !executionChannelHealthTargetsRuntime(health, target) {
				continue
			}
			owner = target.RequirementID
			matches++
		}
		if matches != 1 {
			return invalidRequest(fmt.Sprintf("execution_channel.health_targets[%d]", index), "must have exactly one runtime owner, got %d", matches)
		}
		healthByRuntime[owner]++
	}
	for index, target := range request.RuntimeTargets {
		if healthByRuntime[target.RequirementID] == 0 {
			return invalidRequest(fmt.Sprintf("execution_channel.runtime_targets[%d]", index), "must own at least one exact Health target")
		}
	}
	return nil
}

// CloneExecutionChannelRequest returns a defensive deep copy safe to cross a
// service-owned channel factory boundary.
func CloneExecutionChannelRequest(input ExecutionChannelRequest) ExecutionChannelRequest {
	cloned := CloneExecutionRequest(ExecutionRequest{RuntimeTargets: input.RuntimeTargets, HealthTargets: input.HealthTargets})
	return ExecutionChannelRequest{
		ChannelRef: input.ChannelRef, SiteRef: input.SiteRef, NodeRef: input.NodeRef,
		RuntimeTargets: cloned.RuntimeTargets, HealthTargets: cloned.HealthTargets,
	}
}

func executionChannelHealthTargetsRuntime(health HealthTarget, target RuntimeTarget) bool {
	if len(health.SiteRefs) != 1 || len(health.NodeRefs) != 1 || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		health.SiteRefs[0] != target.SiteRefs[0] || health.NodeRefs[0] != target.NodeRefs[0] {
		return false
	}
	if health.RuntimeRequirementID != "" {
		return health.RuntimeRequirementID == target.RequirementID
	}
	return health.TargetKind == "module" && health.TargetRef == target.ModuleRef ||
		health.TargetKind == "provider" && health.TargetRef == target.ProviderRef ||
		health.TargetKind == "runtime" && health.TargetRef == target.InstanceRef
}
