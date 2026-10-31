package runtimeexecutor

import (
	"fmt"

	"github.com/kombifyio/stackkits/contracts/internal/referenceid"
)

func validateRuntimeTargets(targets []RuntimeTarget) error {
	seen := map[string]struct{}{}
	for i, target := range targets {
		prefix := fmt.Sprintf("runtime_targets[%d]", i)
		if err := validateRuntimeOwner(prefix, target); err != nil {
			return err
		}
		if err := validateRuntimeModuleAuthority(prefix, target); err != nil {
			return err
		}
		if err := validateRuntimeWorkload(prefix, target); err != nil {
			return err
		}
		if err := validateRuntimePlacement(prefix, target); err != nil {
			return err
		}
		if err := validateRuntimeAdapterSelection(prefix, target); err != nil {
			return err
		}
		if err := validateRuntimeExternalBindings(prefix, target); err != nil {
			return err
		}
		key := target.RequirementID + "\x00" + target.InstanceRef
		if _, exists := seen[key]; exists {
			return invalidRequest(prefix, "duplicate governed runtime target")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateRuntimeOwner(prefix string, target RuntimeTarget) error {
	for _, field := range []struct{ name, value string }{
		{"requirement_id", target.RequirementID}, {"owner_kind", target.OwnerKind}, {"owner_ref", target.OwnerRef},
		{"provider_ref", target.ProviderRef}, {"runtime_kind", target.RuntimeKind}, {"runtime_delivery", target.RuntimeDelivery},
		{"instance_ref", target.InstanceRef},
	} {
		if err := validateToken(prefix+"."+field.name, field.value); err != nil {
			return err
		}
	}
	switch target.OwnerKind {
	case "module":
		if target.OwnerVersion != "" {
			return invalidRequest(prefix+".owner_version", "module runtime target must not carry provider-owner version authority")
		}
	case "provider-owner":
		if err := validateToken(prefix+".owner_version", target.OwnerVersion); err != nil {
			return err
		}
	default:
		return invalidRequest(prefix+".owner_kind", "must be module or provider-owner")
	}
	for _, field := range []struct{ name, value string }{
		{"owner_contract_hash", target.OwnerContractHash}, {"provider_contract_hash", target.ProviderContractHash},
	} {
		if !validDigest(field.value) {
			return invalidRequest(prefix+"."+field.name, "must be a canonical SHA-256 digest")
		}
	}
	return nil
}

func validateRuntimeModuleAuthority(prefix string, target RuntimeTarget) error {
	if (target.ModuleRef == "") != (target.ModuleContractHash == "") || (target.UnitRef == "") != (target.UnitContractHash == "") {
		return invalidRequest(prefix, "module/unit refs and contract hashes must be paired")
	}
	if target.ModuleRef != "" {
		if err := validateToken(prefix+".module_ref", target.ModuleRef); err != nil {
			return err
		}
		if !validDigest(target.ModuleContractHash) {
			return invalidRequest(prefix+".module_contract_hash", "must be a canonical SHA-256 digest")
		}
	}
	if target.UnitRef != "" {
		if err := validateToken(prefix+".unit_ref", target.UnitRef); err != nil {
			return err
		}
		if !validDigest(target.UnitContractHash) {
			return invalidRequest(prefix+".unit_contract_hash", "must be a canonical SHA-256 digest")
		}
	}
	if target.RuntimeEngine != "" {
		if err := validateToken(prefix+".runtime_engine", target.RuntimeEngine); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeWorkload(prefix string, target RuntimeTarget) error {
	if target.ExecutionChannelRef != "" {
		if !referenceid.ValidExecutionChannel(target.ExecutionChannelRef) {
			return invalidRequest(prefix+".execution_channel_ref", "must be a canonical non-secret execution-channel identity")
		}
	}
	if target.WorkloadRef != "" {
		if err := validateToken(prefix+".workload_ref", target.WorkloadRef); err != nil {
			return err
		}
	}
	if target.ImageRef != "" {
		if err := validateOpaqueRef(prefix+".image_ref", target.ImageRef); err != nil {
			return err
		}
	}
	if (target.ImageRef == "") != (target.ImageDigest == "") {
		return invalidRequest(prefix+".image", "image ref and digest must appear together")
	}
	if target.ImageDigest != "" && !validDigest(target.ImageDigest) {
		return invalidRequest(prefix+".image_digest", "must be a canonical SHA-256 digest")
	}
	if target.UnitRef != "" && target.ModuleRef == "" {
		return invalidRequest(prefix, "unit authority requires module authority")
	}
	return nil
}

func validateRuntimePlacement(prefix string, target RuntimeTarget) error {
	if err := validateRefList(prefix+".site_refs", target.SiteRefs, 1, MaxSiteRefsPerTarget); err != nil {
		return err
	}
	if err := validateRefList(prefix+".node_refs", target.NodeRefs, 1, MaxNodeRefsPerTarget); err != nil {
		return err
	}
	if err := validateDaemonTargets(prefix+".daemon_bindings", target.DaemonBindings); err != nil {
		return err
	}
	for daemonIndex, daemon := range target.DaemonBindings {
		if target.RuntimeEngine == "" || daemon.Engine != target.RuntimeEngine {
			return invalidRequest(fmt.Sprintf("%s.daemon_bindings[%d].engine", prefix, daemonIndex), "must match the governed runtime engine")
		}
	}
	minimumArtifacts := 0
	if target.ModuleRef != "" || target.UnitRef != "" {
		minimumArtifacts = 1
	}
	if err := validateRefList(prefix+".artifact_refs", target.ArtifactRefs, minimumArtifacts, MaxArtifacts); err != nil {
		return err
	}
	return nil
}

func validateRuntimeAdapterSelection(prefix string, target RuntimeTarget) error {
	if target.RuntimeDelivery == "selected-paas" && target.RuntimeAdapter == nil {
		return invalidRequest(prefix+".runtime_adapter", "selected-paas runtime requires one exact adapter binding")
	}
	if target.RuntimeAdapter != nil {
		if target.RuntimeDelivery != "selected-paas" || target.WorkloadRef == "" {
			return invalidRequest(prefix+".runtime_adapter", "is valid only for a workload-scoped selected-paas runtime")
		}
		if err := validateRuntimeAdapterBinding(prefix+".runtime_adapter", *target.RuntimeAdapter); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeExternalBindings(prefix string, target RuntimeTarget) error {
	if err := validateRefList(prefix+".access_binding_refs", target.AccessBindingRefs, 0, MaxAccessBindings); err != nil {
		return err
	}
	if err := validateAccessCapabilities(prefix+".access_capabilities", target.AccessCapabilities); err != nil {
		return err
	}
	if (len(target.AccessBindingRefs) == 0) != (len(target.AccessCapabilities) == 0) {
		return invalidRequest(prefix, "access bindings and capability authority must appear together")
	}
	if err := validateRefList(prefix+".backup_target_binding_refs", target.BackupTargetBindingRefs, 0, MaxBackupTargetBindings); err != nil {
		return err
	}
	if err := validateBackupTargetCapabilities(prefix+".backup_target_capabilities", target.BackupTargetCapabilities); err != nil {
		return err
	}
	if (len(target.BackupTargetBindingRefs) == 0) != (len(target.BackupTargetCapabilities) == 0) {
		return invalidRequest(prefix, "backup-target bindings and capability authority must appear together")
	}
	return nil
}

func validateRuntimeAdapterBinding(field string, binding RuntimeAdapterBinding) error {
	if err := validateAdapterIdentity(field, binding); err != nil {
		return err
	}
	if err := validateRefList(field+".artifact_refs", binding.ArtifactRefs, 1, MaxArtifacts); err != nil {
		return err
	}
	if len(binding.Agents) > MaxAdapterAgents {
		return invalidRequest(field+".agents", "must contain at most %d agent bindings", MaxAdapterAgents)
	}
	referenced := make(map[string]struct{}, len(binding.ArtifactRefs))
	for _, ref := range binding.ArtifactRefs {
		referenced[ref] = struct{}{}
	}
	for index, agent := range binding.Agents {
		prefix := fmt.Sprintf("%s.agents[%d]", field, index)
		if err := validateAdapterAgent(prefix, agent, binding.ModuleRef); err != nil {
			return err
		}
		if index > 0 && binding.Agents[index-1].ID >= agent.ID {
			return invalidRequest(field+".agents", "must be sorted and unique by id")
		}
		for _, ref := range agent.ArtifactRefs {
			if _, duplicate := referenced[ref]; duplicate {
				return invalidRequest(prefix+".artifact_refs", "artifact %q is referenced by more than one adapter authority", ref)
			}
			referenced[ref] = struct{}{}
		}
	}
	return nil
}

func validateAccessCapabilities(field string, capabilities []AccessCapability) error {
	if len(capabilities) > MaxAccessBindings {
		return invalidRequest(field, "must contain at most %d capabilities", MaxAccessBindings)
	}
	for index, capability := range capabilities {
		prefix := fmt.Sprintf("%s[%d]", field, index)
		if capability.Ref != "private-remote-access" && capability.Ref != "public-publish-egress" {
			return invalidRequest(prefix+".ref", "must be a closed Home access capability")
		}
		if !validDigest(capability.ContractHash) {
			return invalidRequest(prefix+".contract_hash", "must be a canonical SHA-256 digest")
		}
		if index > 0 && capabilities[index-1].Ref >= capability.Ref {
			return invalidRequest(field, "must be sorted and unique")
		}
	}
	return nil
}

func validateBackupTargetCapabilities(field string, capabilities []AccessCapability) error {
	if len(capabilities) > MaxBackupTargetBindings {
		return invalidRequest(field, "must contain at most %d capabilities", MaxBackupTargetBindings)
	}
	for index, capability := range capabilities {
		prefix := fmt.Sprintf("%s[%d]", field, index)
		if capability.Ref != "offsite-object-backup" {
			return invalidRequest(prefix+".ref", "must be the closed Cloud backup-target capability")
		}
		if !validDigest(capability.ContractHash) {
			return invalidRequest(prefix+".contract_hash", "must be a canonical SHA-256 digest")
		}
		if index > 0 && capabilities[index-1].Ref >= capability.Ref {
			return invalidRequest(field, "must be sorted and unique")
		}
	}
	return nil
}

func validateAdapterAgent(prefix string, agent RuntimeAdapterAgentBinding, moduleRef string) error {
	for _, value := range []struct{ name, value string }{{"id", agent.ID}, {"module_ref", agent.ModuleRef}} {
		if err := validateToken(prefix+"."+value.name, value.value); err != nil {
			return err
		}
	}
	if !semanticVersionPattern.MatchString(agent.ModuleVersion) {
		return invalidRequest(prefix+".module_version", "must be a semantic version")
	}
	if !validDigest(agent.ModuleContractHash) {
		return invalidRequest(prefix+".module_contract_hash", "must be a canonical SHA-256 digest")
	}
	if agent.ModuleRef == moduleRef {
		return invalidRequest(prefix+".module_ref", "agent authority must be separate from the core adapter module")
	}
	if err := validateRefList(prefix+".artifact_refs", agent.ArtifactRefs, 1, MaxArtifacts); err != nil {
		return err
	}
	return nil
}

func validateAdapterIdentity(field string, binding RuntimeAdapterBinding) error {
	for _, value := range []struct{ name, value string }{
		{"id", binding.ID}, {"provider_ref", binding.ProviderRef}, {"module_ref", binding.ModuleRef},
	} {
		if err := validateToken(field+"."+value.name, value.value); err != nil {
			return err
		}
	}
	for _, value := range []struct{ name, value string }{
		{"provider_version", binding.ProviderVersion}, {"module_version", binding.ModuleVersion},
	} {
		if !semanticVersionPattern.MatchString(value.value) {
			return invalidRequest(field+"."+value.name, "must be a semantic version")
		}
	}
	for _, value := range []struct{ name, value string }{
		{"provider_contract_hash", binding.ProviderContractHash}, {"module_contract_hash", binding.ModuleContractHash},
	} {
		if !validDigest(value.value) {
			return invalidRequest(field+"."+value.name, "must be a canonical SHA-256 digest")
		}
	}
	return nil
}
