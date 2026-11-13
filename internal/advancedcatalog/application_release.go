package advancedcatalog

import "github.com/kombifyio/stackkits/internal/advancedcapability"

// applicationReleaseUpdateEntry lists the scoped release update of one
// application on a managed node. The application-release.update capability
// names the use case, the exact release, its manifest digest and whether the
// Owner approved a tested or an untested release; a standalone node runs the
// same command without one.
func applicationReleaseUpdateEntry() Operation {
	return Operation{
		Operation: advancedcapability.OperationApplicationReleaseUpdate, Status: StatusAvailable, SinceRelease: SincePending,
		Summary: "Move the one application a catalog use case selects to a newer release of its entry image: stop it, retain and read back its data, record the release override in the desired intent, then generate, apply and live verify, rolling back to the prior release and data on failure. " +
			"The result (stackkit.application-update-result/v1) names update_outcome, reason_code and the prior and target release and StackSpec hash. --accept-untested is required when the capability names an untested release.",
		Command: "stackkit use-cases upgrade",
		Argv: []string{"use-cases", "upgrade", "{useCase}", "--to", "{releaseTag}", "--digest", "{imageDigest}",
			"--capability", "{capabilityFile}", "--apply", "--owner-approve", "--json"},
		OptionalArgs: []OptionalArgs{},
		Mutates:      true,
		Requires: Requirements{
			Capability: true, CapabilityOperation: advancedcapability.OperationApplicationReleaseUpdate,
			TrustImported: true, OwnerApproval: true,
		},
		Inputs: []Input{capabilityInput},
		Results: []Outcome{
			{Status: "success", ContractRef: ref("stackkit.application-update-result/v1", "cmd/stackkit/commands/use_cases_upgrade_apply.go"), Description: "Command-result data: update_outcome updated, the operation ID, prior and target release and the target StackSpec hash now in effect."},
			{Status: "failed", ContractRef: ref("stackkit.application-update-result/v1", "cmd/stackkit/commands/use_cases_upgrade_apply.go"), Description: "update_outcome refused (nothing stopped; reason_code names the refusal, an advanced_capability_* code when the capability does not admit the change), rolled_back or rollback_failed."},
		},
		Events: []EventPhase{},
		Modes:  Modes{Standard: AdmissionAllowed, Advanced: AdmissionCapability},
	}
}
