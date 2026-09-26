package advancedcatalog

import "github.com/kombifyio/stackkits/internal/advancedcapability"

const (
	RollbackResultSchemaVersion = "stackkit.rollback-result/v1"
	RollbackResultSchema        = "schemas/stackkit-rollback-result-v1.schema.json"
)

// rollbackEntry is the coordinated rollback operation
// (`stackkit advanced rollback run`).
func rollbackEntry() Operation {
	return Operation{
		Operation:    advancedcapability.OperationRollbackCoordinated,
		Status:       StatusAvailable,
		SinceRelease: SincePending,
		Summary:      "Roll every local Terramate stack back to one verified executor-state checkpoint in reverse run order: restore the checkpoint StackSpec and Inventory and regenerate its plan, destroy stacks added after it, restore and force-converge changed stacks, recreate removed stacks, then apply and verify the checkpoint plan through joined children. A rerun with the same target resumes an interrupted rollback.",
		Command:      "stackkit advanced rollback run",
		Argv:         []string{"advanced", "rollback", "run", "--capability", "{capabilityFile}", "--to", "{targetRef}", "--owner-approve", "--json"},
		OptionalArgs: []OptionalArgs{},
		Mutates:      true,
		Requires: Requirements{
			Capability: true, CapabilityOperation: advancedcapability.OperationRollbackCoordinated,
			TrustImported: true, OwnerApproval: true,
		},
		Inputs: []Input{capabilityInput},
		Results: []Outcome{
			{Status: "success", ContractRef: ref(RollbackResultSchemaVersion, RollbackResultSchema)},
			{Status: "failed", ContractRef: ref(RollbackResultSchemaVersion, RollbackResultSchema), Description: "status is failed; stacks[] shows the failed and pending stacks, and a rerun with the same --to resumes."},
			denialOutcome,
		},
		Events: []EventPhase{{
			Phase: "advanced.rollback.", Match: "prefix",
			Statuses: []string{"started", "succeeded", "failed", "skipped", "converged"},
		}},
		Modes: capabilityModes,
	}
}
