package commands

import (
	"encoding/json"
	"errors"
	"time"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advancedtrust"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/ownapplication"
	"github.com/spf13/cobra"
)

// Reason codes of an own-application call refused before its action runs.
const (
	ownAppReasonInvalidRequest  = "invalid_request"
	ownAppReasonDecisionReceipt = "decision_receipt_refused"
	ownAppReasonDocumentInvalid = "document_invalid"
	ownAppReasonResolvedImages  = "resolved_images_invalid"
	ownAppReasonSecretInput     = "secret_input_invalid"
	ownAppReasonInstallation    = "installation_unavailable"
	ownAppReasonNameCollision   = "name_collision"
	ownAppReasonCapability      = "capability_denied"
	ownAppReasonCustody         = "custody_unavailable"
	ownAppReasonBackupCoverage  = "backup_coverage_missing"
	ownAppReasonActionRefused   = "action_refused"
)

// ownApplicationLifecycleAction names the executed own-application actions
// that change the application's intent on a running node: its power state or
// its image. On a Techstack-managed node each needs the exact Techstack
// capability; create and remove keep their Owner and decision-receipt
// admission, and reads never need one.
func ownApplicationLifecycleAction(action string) bool {
	switch action {
	case "start", "stop", "restart", "update", "rollback", "discard-checkpoint":
		return true
	}
	return false
}

// admitOwnApplicationLifecycle verifies the own-application.lifecycle
// capability on a managed node (install mode advanced or imported Advanced
// trust) against this node's stack and Owner and the exact call: application
// name, action, operation ID and the review digest of its dry run. A
// standalone node runs the action in Standard Mode under --owner-approve alone
// and refuses a capability.
func admitOwnApplicationLifecycle(authority nativeV2AppliedAuthority, capabilityFile, name, action, operationID, reviewDigest string) error {
	workspace := getWorkDir()
	managed, err := applicationTransferManaged(workspace)
	if err != nil {
		return err
	}
	if !managed {
		if capabilityFile != "" {
			return errors.New("a standalone node runs own-application actions without a Techstack capability; this node has no Advanced trust")
		}
		return nil
	}
	if capabilityFile == "" {
		return &advancedcapability.Denial{Code: advancedcapability.ReasonCapabilityRequired, Field: "capability", Detail: "a managed node runs this own-application action only with its Techstack own-application.lifecycle capability; nothing changed"}
	}
	raw, err := readAdvancedRegular(resolvePathFromWorkDir(workspace, capabilityFile), maxAdvancedTrustBundleBytes, "Advanced capability")
	if err != nil {
		return err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return err
	}
	trusted, err := advancedtrust.Load(workspace)
	if err != nil {
		return &advancedcapability.Denial{Code: advancedcapability.ReasonTrustBundleUnavailable, Field: "trustBundle", Detail: "verified Owner-approved local trust is required"}
	}
	stackID, err := applicationTransferPlanStackID(authority.Plan.Canonical())
	if err != nil {
		return err
	}
	trust := trusted.TrustBundle()
	_, err = advancedcapability.Verify(raw, advancedcapability.Request{
		Now: time.Now().UTC(), TrustBundle: &trust, StackID: stackID, OwnerRef: owner.OwnerRef,
		Operation:               advancedcapability.OperationOwnApplicationLifecycle,
		OwnApplicationLifecycle: &advancedcapability.OwnApplicationLifecycle{Action: action, Name: name, OperationID: operationID, ReviewDigest: reviewDigest},
	})
	return err
}

// writeOwnApplicationFailure writes the one result of a refused, pending or
// denied call with its stable reasonCode before returning the error, so a
// dispatcher always reads one JSON document. A tag update or a checkpoint
// rollback answers in its own result form.
func writeOwnApplicationFailure(cmd *cobra.Command, action, name string, updateForm bool, code string, cause error) error {
	result := ownapplication.OutcomeFor(action, code, cause)
	if denial, ok := advancedcapability.Reason(cause); ok {
		result.Outcome, result.ReasonCode = "denied", string(denial)
	} else if result.ReasonCode == "" {
		result.ReasonCode = ownAppReasonActionRefused
	}
	var document any = result
	if updateForm {
		document = ownapplication.ImageUpdateResult{APIVersion: ownapplication.ImageUpdateVersion, Name: name, Outcome: result.Outcome, Reason: result.ReasonCode, Message: cause.Error(), TargetCheck: ownapplication.TargetNotChecked}
	}
	_ = json.NewEncoder(cmd.OutOrStdout()).Encode(document)
	return cause
}

// runOwnApplicationImageUpdate runs a tag update and always writes its
// stackkit.own-application-update/v1 result.
func runOwnApplicationImageUpdate(cmd *cobra.Command, installation contract.Installation, req ownapplication.ImageUpdateRequest) error {
	ctx := commandContext(cmd)
	result, err := ownapplication.UpdateImage(ctx, getWorkDir(), installation, req)
	if !req.DryRun && (result.Update != nil || result.Rollback != nil) {
		runAutomaticBackupRebind(ctx, getWorkDir())
	}
	if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(result); encodeErr != nil {
		return errors.Join(err, encodeErr)
	}
	return err
}

// writeOwnApplicationUpdateResult runs one checkpoint call and always writes
// its result.
func writeOwnApplicationUpdateResult(cmd *cobra.Command, run func() (ownapplication.ImageUpdateResult, error)) error {
	result, err := run()
	if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(result); encodeErr != nil {
		return errors.Join(err, encodeErr)
	}
	return err
}

// runOwnApplicationCheckpointRollback restores a tag update's data checkpoint
// and returns to its prior image, and always writes its result.
func runOwnApplicationCheckpointRollback(cmd *cobra.Command, installation contract.Installation, req ownapplication.Request) error {
	ctx := commandContext(cmd)
	result, err := ownapplication.RollbackWithCheckpoint(ctx, getWorkDir(), installation, req)
	if !req.DryRun && result.Rollback != nil {
		runAutomaticBackupRebind(ctx, getWorkDir())
	}
	if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(result); encodeErr != nil {
		return errors.Join(err, encodeErr)
	}
	return err
}
