package advancedcatalog

import "github.com/kombifyio/stackkits/internal/advancedcapability"

// applicationTransferReceiveEntry lists the receiving side of a managed native
// application transfer. One application-transfer.receive capability names one
// step (admit, prepare or import), the exact receiving intent and the source
// Owner; transfer-target and transfer-prepare take a capability of their own
// step the same way this import does. A standalone node receives without one.
func applicationTransferReceiveEntry() Operation {
	return Operation{
		Operation: advancedcapability.OperationApplicationTransferReceive, Status: StatusAvailable, SinceRelease: SincePending,
		Summary: "Receive a native application transfer on a managed node: import the authenticated archive into the prepared receiving application and sign the activated outcome only after the destination data readback. " +
			"transfer-target (step admit: persist and generate the Techstack-approved candidate, issue the destination admission) and transfer-prepare (step prepare: adopt the transferred credentials before the first Apply) take a capability of their own step.",
		Command: "stackkit backup transfer-import",
		Argv: []string{"backup", "transfer-import", "--archive-sha256", "{archiveSha256}", "--source-receipt", "{sourceReceiptFile}",
			"--source-owner", "{sourceOwnerRef}", "--source-fingerprint", "{sourceFingerprint}", "--transfer-id", "{transferId}",
			"--expected-plan-hash", "{planHash}", "--capability", "{capabilityFile}", "--owner-approve"},
		OptionalArgs: []OptionalArgs{},
		Mutates:      true,
		Requires: Requirements{
			Capability: true, CapabilityOperation: advancedcapability.OperationApplicationTransferReceive,
			TrustImported: true, OwnerApproval: true,
		},
		Inputs: []Input{capabilityInput, {
			Placeholder: "sourceReceiptFile",
			ContractRef: ref("stackkit.application-transfer-export-receipt/v1", "internal/backuplifecycle/application_transfer_receipt.go"),
			Description: "Source Owner-signed export receipt that binds the archive digest, the selected applications and the destination admission.",
		}},
		Results: []Outcome{
			{Status: "success", ContractRef: ref("stackkit.application-transfer-outcome/v1", "internal/backuplifecycle/application_transfer_receipt.go"), Description: "Raw signed activated outcome with the destination data readback digest (no command-result envelope)."},
			denialOutcome,
		},
		Events: []EventPhase{},
		Modes:  Modes{Standard: AdmissionAllowed, Advanced: AdmissionCapability},
	}
}
