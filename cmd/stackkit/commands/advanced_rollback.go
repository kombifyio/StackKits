package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advancedrollback"
	"github.com/kombifyio/stackkits/internal/terramatehost"
	"github.com/spf13/cobra"
)

// The Advanced coordinated rollback returns every local Terramate stack to
// one verified executor-state checkpoint (docs/ARCHITECTURE.md "Coordinated
// rollback across stacks (Stage 1)").

const (
	advancedRollbackCommandName   = "advanced rollback run"
	advancedRollbackRolloutPrefix = "advanced.rollback."
)

var (
	advancedRollbackCapability   string
	advancedRollbackTo           string
	advancedRollbackOwnerApprove bool
	advancedRollbackJSON         bool
)

var advancedRollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Run capability-gated coordinated rollbacks across Terramate stacks",
}

var advancedRollbackRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Roll every local Terramate stack back to one verified checkpoint",
	Long: `Roll the local host back to one verified executor-state checkpoint as the
Advanced operation rollback.coordinated.

The rollback is admitted only with an offline-verified capability that allows
rollback.coordinated, the local Owner-approved issuer trust, and explicit
--owner-approve. --to names the checkpoint directly or the change set whose
pre-apply checkpoint is the target. Stacks run in reverse run order: stacks
added after the checkpoint are destroyed through OpenTofu (docker compose down
without volumes) and removed, changed stacks get the checkpoint's state,
configuration and payload back and are forced to converge, and stacks removed
after the checkpoint are recreated. The checkpoint's StackSpec and Inventory
are restored and regenerated before the stacks run; afterwards joined apply
and verify children record and verify the checkpoint's Apply evidence. An
interrupted rollback resumes on the next run with the same --to and skips
stacks that already converged.`,
	Example: `  stackkit advanced rollback run --capability capability.json --to sha256:<change-set-id> --owner-approve --json`,
	Args:    cobra.NoArgs,
	RunE:    runAdvancedRollback,
}

func init() {
	advancedRollbackDeps.execute = executeAdvancedRollback
	flags := advancedRollbackRunCmd.Flags()
	flags.StringVar(&advancedRollbackCapability, "capability", "",
		"Path to a canonical stackkit.advanced-capability/v1 file that allows rollback.coordinated")
	flags.StringVar(&advancedRollbackTo, "to", "",
		"Target sha256 executor-state snapshot ID, or the sha256 change-set ID whose pre-apply checkpoint is the target")
	flags.BoolVar(&advancedRollbackOwnerApprove, "owner-approve", false,
		"Explicitly approve destroying, restoring and recreating stacks")
	flags.BoolVar(&advancedRollbackJSON, "json", false,
		"Emit stackkit.command-result/v1 JSON")
	advancedRollbackCmd.AddCommand(advancedRollbackRunCmd)
	advancedCmd.AddCommand(advancedRollbackCmd)
}

type advancedRollbackAdmission struct {
	grant         advancedcapability.Grant
	capabilityRaw []byte
	scope         advancedRestoreDrillScope
}

type advancedRollbackRequest struct {
	workspace      string
	capabilityPath string
	to             string
	now            time.Time
	admission      advancedRollbackAdmission
	tools          terramatehost.Tools
}

type advancedRollbackDependencies struct {
	scope   func(workspace string) (advancedRestoreDrillScope, error)
	tools   func() (terramatehost.Tools, error)
	execute func(context.Context, *cobra.Command, advancedRollbackRequest) (advancedrollback.Report, error)
	now     func() time.Time
}

// execute is bound in init: it revalidates the admission, which reads these
// dependencies.
var advancedRollbackDeps = advancedRollbackDependencies{
	scope: advancedRestoreDrillScopeFromWorkspace,
	tools: terramatehost.PackagedTools,
	now:   time.Now,
}

func runAdvancedRollback(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	capabilityPath := strings.TrimSpace(advancedRollbackCapability)
	if capabilityPath == "" {
		// Standard Mode has no capability and is denied like every other
		// Advanced operation.
		return writeAdvancedRollbackDenial(cmd, &advancedcapability.Denial{
			Code:   advancedcapability.ReasonCapabilityUnavailable,
			Field:  "capability",
			Detail: "advanced rollback requires an offline-verifiable capability",
		})
	}
	if !advancedRollbackOwnerApprove {
		return writeAdvancedRollbackDenialCode(cmd, "owner_approval_required",
			"advanced rollback requires explicit --owner-approve")
	}
	to := strings.TrimSpace(advancedRollbackTo)
	if !advancedSHA256Pattern.MatchString(to) {
		return writeAdvancedRollbackDenialCode(cmd, "advanced_rollback_request_invalid",
			"--to must be a sha256:<hex> executor-state snapshot ID or change-set ID")
	}
	workspace := getWorkDir()
	now := advancedRollbackDeps.now().UTC().Truncate(time.Second)
	capabilityFile := resolvePathFromWorkDir(workspace, capabilityPath)
	// Capability, trust and Owner scope are verified before any lock,
	// journal, Terramate or OpenTofu side effect.
	admission, err := admitAdvancedRollback(workspace, capabilityFile, now)
	if err != nil {
		return writeAdvancedRollbackDenial(cmd, err)
	}
	// Missing packaged Terramate or OpenTofu fails closed before any write.
	tools, err := advancedRollbackDeps.tools()
	if err != nil {
		return writeAdvancedRollbackDenial(cmd, err)
	}
	report, err := advancedRollbackDeps.execute(ctx, cmd, advancedRollbackRequest{
		workspace: workspace, capabilityPath: capabilityFile, to: to, now: now,
		admission: admission, tools: tools,
	})
	return writeAdvancedRollbackReport(cmd, report, err)
}

func admitAdvancedRollback(
	workspace, capabilityPath string,
	now time.Time,
) (advancedRollbackAdmission, error) {
	capabilityRaw, err := readAdvancedRegular(capabilityPath, maxAdvancedTrustBundleBytes, "Advanced capability")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return advancedRollbackAdmission{}, &advancedcapability.Denial{
				Code: advancedcapability.ReasonCapabilityRequired, Field: "capability", Detail: "file is required",
			}
		}
		return advancedRollbackAdmission{}, err
	}
	scope, err := advancedRollbackDeps.scope(workspace)
	if err != nil {
		return advancedRollbackAdmission{}, err
	}
	trust := scope.Trust
	grant, err := advancedcapability.Verify(capabilityRaw, advancedcapability.Request{
		Now: now, TrustBundle: &trust,
		StackID: scope.StackID, OwnerRef: scope.OwnerRef,
		Operation: advancedcapability.OperationRollbackCoordinated,
	})
	if err != nil {
		return advancedRollbackAdmission{}, err
	}
	return advancedRollbackAdmission{
		grant: grant, capabilityRaw: bytes.Clone(capabilityRaw), scope: scope,
	}, nil
}

func equalAdvancedRollbackAdmission(left, right advancedRollbackAdmission) bool {
	return left.grant.CapabilityID == right.grant.CapabilityID &&
		left.grant.KeyID == right.grant.KeyID &&
		left.scope.StackID == right.scope.StackID &&
		left.scope.OwnerRef == right.scope.OwnerRef &&
		left.scope.TrustSHA256 == right.scope.TrustSHA256 &&
		bytes.Equal(left.capabilityRaw, right.capabilityRaw)
}

func writeAdvancedRollbackDenial(cmd *cobra.Command, err error) error {
	reason, ok := advancedcapability.Reason(err)
	if !ok {
		if code, typed := terramatehost.Reason(err); typed {
			reason, ok = advancedcapability.ReasonCode(code), true
		}
	}
	if !ok {
		return writeAdvancedRollbackReport(cmd, advancedrollback.Report{
			SchemaVersion: advancedrollback.ResultSchemaVersion, Order: []string{},
			Stacks: []advancedrollback.StackReport{}, SealStatus: advancedrollback.SealNotAttempted,
			Status: advancedrollback.StatusFailed,
		}, err)
	}
	denial := driftOperationDenial{
		SchemaVersion: operationDenialSchemaVersion,
		Operation:     advancedcapability.OperationRollbackCoordinated, Mode: "advanced",
		ReasonCode: string(reason), Message: err.Error(),
	}
	return emitAdvancedRollbackDenial(cmd, denial, err)
}

func writeAdvancedRollbackDenialCode(cmd *cobra.Command, code, message string) error {
	denial := driftOperationDenial{
		SchemaVersion: operationDenialSchemaVersion,
		Operation:     advancedcapability.OperationRollbackCoordinated, Mode: "advanced",
		ReasonCode: code, Message: message,
	}
	return emitAdvancedRollbackDenial(cmd, denial, &driftReconcileDeniedError{denial: denial})
}

func emitAdvancedRollbackDenial(cmd *cobra.Command, denial driftOperationDenial, cause error) error {
	if advancedRollbackJSON {
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), "denied", denial); err != nil {
			return errors.Join(cause, err)
		}
	} else {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Denied: %s\n", denial.Message)
	}
	return cause
}

func writeAdvancedRollbackReport(cmd *cobra.Command, report advancedrollback.Report, rollbackErr error) error {
	if advancedRollbackJSON {
		status := "success"
		if rollbackErr != nil {
			status = "failed"
		}
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, report); err != nil {
			return errors.Join(rollbackErr, err)
		}
		return rollbackErr
	}
	if rollbackErr != nil {
		return rollbackErr
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Rollback %s %s to checkpoint %s\n",
		report.RollbackID, report.Status, report.TargetSnapshotID); err != nil {
		return err
	}
	for _, stack := range report.Stacks {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "- %s (%s): %s, %s\n",
			stack.StackID, stack.Role, stack.Action, stack.Status); err != nil {
			return err
		}
	}
	return nil
}
