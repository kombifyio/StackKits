package commands

import (
	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
)

// Optional restore evidence never makes status unavailable. ReadResult verifies
// owner custody; the exact operation, Plan and content-addressed lifecycle proof
// must match before projecting an activation outcome.
func readApplicationRestoreActivation(workspace string, contract applicationlifecycle.Contract, state applicationlifecycle.State) *applicationlifecycle.RestoreActivationInput {
	for index := len(state.Operations) - 1; index >= 0; index-- {
		operation := state.Operations[index]
		if operation.Stage != "restore" {
			continue
		}
		if operation.OperationRef != "stackkit.restore" || (operation.Status != applicationlifecycle.StatusSucceeded && operation.Status != applicationlifecycle.StatusRecovered) {
			return nil
		}
		result, err := restoreactivation.ReadResult(workspace, operation.ID)
		if err != nil || result.PlanHash != contract.PlanHash {
			return nil
		}
		ref, digest, err := restoreactivation.ResultEvidence(workspace, result)
		if err != nil {
			return nil
		}
		for _, evidence := range operation.Evidence {
			if evidence.Kind == "restore-result" && evidence.Ref == ref && evidence.Digest == digest {
				return &applicationlifecycle.RestoreActivationInput{OperationID: result.OperationID, PlanHash: result.PlanHash, Status: result.Status, Evidence: evidence, VerifiedAt: result.Verification.VerifiedAt}
			}
		}
		return nil
	}
	return nil
}
