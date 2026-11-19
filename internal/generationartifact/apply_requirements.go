package generationartifact

import "github.com/kombifyio/stackkits/internal/resolvedplan"

const (
	ApplyExecutionClassExecutable      = "executable"
	ApplyExecutionClassContractHandoff = "contract-handoff"
	ApplyExecutionClassArtifactOnly    = "artifact-only"
	ApplyExecutionClassPlan            = "plan"
	applicationAdapterRuntimeDelivery  = "application-adapter"
)

// ApplyRequirements is the immutable, plan-owned input and postcondition set
// for Architecture v2 execution. ApplyEvidenceRequest projects only its
// pre-mutation Host, Secret, and explicit apply-phase evidence facts; runtime,
// workload, provider-owner, and Health requirements are verified from the exact
// executor result. It contains opaque secret references, never secret material.
// Runtime targets are derived from resolved render instances, never from
// generated files or ambient host discovery.
type ApplyRequirements struct {
	Binding              PlanBinding                           `json:"binding"`
	Workloads            []ApplyWorkloadRequirement            `json:"workloads"`
	Secrets              []ApplySecretRequirement              `json:"secrets"`
	RuntimeInstances     []ApplyRuntimeRequirement             `json:"runtimeInstances"`
	Artifacts            []ApplyArtifactRequirement            `json:"artifacts"`
	Hosts                []ApplyHostRequirement                `json:"hosts"`
	ProviderOwners       []ApplyProviderOwnerRequirement       `json:"providerOwners"`
	AccessBindings       []ApplyAccessBindingRequirement       `json:"accessBindings,omitempty"`
	BackupTargetBindings []ApplyBackupTargetBindingRequirement `json:"backupTargetBindings,omitempty"`
	EvidenceRequirements []ApplyEvidenceRequirement            `json:"evidenceRequirements"`
	HealthRequirements   []ApplyHealthRequirement              `json:"healthRequirements"`
	// ExecutionScope is set only on a host-scoped projection of a multi-host
	// plan (see VerifiedPlan.WithExecutionScope). It is part of the requirements
	// hash, so a scoped Apply can never be mistaken for a whole-plan Apply.
	ExecutionScope *ApplyExecutionScope `json:"executionScope,omitempty"`
}

// ApplyRequirements returns a defensive copy. A caller may inspect it to
// collect evidence, but cannot mutate the verified plan's authorization input.
func (p VerifiedPlan) ApplyRequirements() ApplyRequirements {
	return cloneApplyRequirements(p.applyRequirements)
}

// Clone returns independent inspection data without creating plan authority.
func (requirements ApplyRequirements) Clone() ApplyRequirements {
	return cloneApplyRequirements(requirements)
}

func applyRequirementsFromPlan(plan resolvedplan.ResolvedPlan, binding PlanBinding) (ApplyRequirements, error) {
	result := ApplyRequirements{
		Binding: binding, Workloads: []ApplyWorkloadRequirement{}, Secrets: []ApplySecretRequirement{},
		RuntimeInstances: []ApplyRuntimeRequirement{}, Artifacts: []ApplyArtifactRequirement{}, Hosts: []ApplyHostRequirement{}, ProviderOwners: []ApplyProviderOwnerRequirement{},
		AccessBindings: []ApplyAccessBindingRequirement{}, BackupTargetBindings: []ApplyBackupTargetBindingRequirement{},
		EvidenceRequirements: []ApplyEvidenceRequirement{}, HealthRequirements: []ApplyHealthRequirement{},
	}
	providerContracts, err := parseApplyProviderContracts(plan)
	if err != nil {
		return ApplyRequirements{}, err
	}
	modules, err := parseApplyModules(plan, providerContracts)
	if err != nil {
		return ApplyRequirements{}, err
	}
	workloadByModule, runtimeAdapterByWorkloadModule, err := parseApplyWorkloads(plan, modules, &result)
	if err != nil {
		return ApplyRequirements{}, err
	}
	targetNodes := map[string]struct{}{}
	if err := appendApplyModuleRequirements(modules, workloadByModule, runtimeAdapterByWorkloadModule, targetNodes, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyAccessBindingRequirements(plan, modules, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyBackupTargetBindingRequirements(plan, modules, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyModuleSecretSources(modules, workloadByModule, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyArtifactRequirements(plan, binding, modules, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyIntentSecretRequirements(plan, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyHealthRequirements(plan, modules, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := bindApplyWorkloadHealthRequirements(&result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyProviderOwnerRequirements(plan, targetNodes, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyHostRequirements(plan, targetNodes, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyEvidenceGateRequirements(plan, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := validateApplySecretSourceClosure(result.Secrets); err != nil {
		return ApplyRequirements{}, err
	}
	sortApplyRequirements(&result)
	if err := validateApplyProviderOwnerClosure(result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := validateApplyArtifactRuntimeClosure(result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := validateApplyAccessBindingClosure(result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := validateApplyBackupTargetBindingClosure(result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := validateApplyRequirementIDs(result); err != nil {
		return ApplyRequirements{}, err
	}
	return result, nil
}
