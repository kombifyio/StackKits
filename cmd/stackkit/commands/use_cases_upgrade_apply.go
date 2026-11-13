package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/kombifyio/stackkits/internal/applicationrelease"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"github.com/spf13/cobra"
)

type applicationUpdateRelease struct {
	Version string `json:"version"`
	Ref     string `json:"ref"`
	Digest  string `json:"digest"`
}

// applicationUpdateResult is the outcome of `use-cases upgrade --apply`.
type applicationUpdateResult struct {
	APIVersion  string                    `json:"apiVersion"`
	Outcome     string                    `json:"update_outcome"`
	Reason      string                    `json:"reason_code,omitempty"`
	Message     string                    `json:"message,omitempty"`
	OperationID string                    `json:"operation_id,omitempty"`
	WorkloadRef string                    `json:"workload_ref,omitempty"`
	State       applicationrelease.State  `json:"operation_state,omitempty"`
	Tested      *bool                     `json:"target_tested,omitempty"`
	Prior       *applicationUpdateRelease `json:"prior,omitempty"`
	Target      *applicationUpdateRelease `json:"target,omitempty"`
	NextSteps   []string                  `json:"next_steps,omitempty"`
	// PriorSpecHash and TargetSpecHash are the desired-intent hashes before
	// and after the release override; the outcome names which one is in
	// effect (target when updated, prior when rolled back).
	PriorSpecHash  string `json:"prior_spec_hash,omitempty"`
	TargetSpecHash string `json:"target_spec_hash,omitempty"`
	// CapabilityID names the Techstack capability that admitted the change on
	// a managed node.
	CapabilityID string `json:"capability_id,omitempty"`
}

func applicationUpdateResultFor(record applicationrelease.Record, outcome, reason, message string) applicationUpdateResult {
	tested := !record.TargetUntested
	capabilityID := ""
	if record.Managed != nil {
		capabilityID = record.Managed.CapabilityID
	}
	return applicationUpdateResult{
		PriorSpecHash: record.PriorSpecHash, TargetSpecHash: record.TargetSpecHash, CapabilityID: capabilityID,
		Outcome: outcome, Reason: reason, Message: message,
		OperationID: record.OperationID, WorkloadRef: record.WorkloadRef, State: record.State, Tested: &tested,
		Prior:  &applicationUpdateRelease{Version: record.PriorRelease, Ref: record.PriorImage.Ref, Digest: record.PriorImage.Digest},
		Target: &applicationUpdateRelease{Version: record.TargetRelease, Ref: record.TargetImage.Ref, Digest: record.TargetImage.Digest},
	}
}

// writeApplicationUpdateResult reports the outcome; cause is the error the
// command returns for every outcome but updated.
func writeApplicationUpdateResult(cmd *cobra.Command, o useCaseUpgradeOptions, result applicationUpdateResult, cause error) error {
	result.APIVersion = applicationUpdateResultVersion
	if o.json {
		status := "success"
		if result.Outcome != updateOutcomeUpdated {
			status = "failed"
		}
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, result); err != nil {
			return errors.Join(cause, err)
		}
		return cause
	}
	if result.Outcome == updateOutcomeUpdated {
		printSuccess("Updated %s from %s to %s", result.WorkloadRef, result.Prior.Version, result.Target.Version)
		for _, step := range result.NextSteps {
			printInfo("Next: %s", step)
		}
		return nil
	}
	printInfo("Update outcome: %s (%s)", result.Outcome, result.Reason)
	return cause
}

// applyApplicationRelease converges the node on a completed checkpoint:
// generate, apply and live verify run as child invocations of this CLI, each
// taking the lifecycle lock itself. A failure rolls back to the prior release
// and data, converges again and finalizes the rollback.
func applyApplicationRelease(ctx context.Context, cmd *cobra.Command, o useCaseUpgradeOptions, record applicationrelease.Record, checkpointFailure error) error {
	wd := getWorkDir()
	executable, err := os.Executable()
	if err != nil {
		return writeApplicationUpdateResult(cmd, o, applicationUpdateResultFor(record, updateOutcomeRollbackFailed, updateFailedRollback, err.Error()), err)
	}
	runner := upgradelifecycle.ExecRunner{}
	converge := func() (string, error) {
		for _, step := range []struct {
			reason string
			args   []string
		}{
			{updateFailedGenerate, []string{"generate"}},
			{updateFailedApply, []string{"apply", "--auto-approve"}},
			{updateFailedVerify, []string{"verify"}},
		} {
			if _, err := runner.Run(ctx, executable, append(publicUpgradeCommandPrefix(wd, specFile), step.args...), wd); err != nil {
				return step.reason, err
			}
		}
		return "", nil
	}
	if checkpointFailure == nil && record.State == applicationrelease.StateCheckpointed {
		reason, failure := converge()
		if failure == nil {
			result := applicationUpdateResultFor(record, updateOutcomeUpdated, "", "")
			result.NextSteps = []string{
				"stackkit use-cases upgrade --rollback " + record.OperationID + " --owner-approve (return to " + record.PriorRelease + " with its checkpoint data)",
				"stackkit use-cases upgrade --finalize " + record.OperationID + " --owner-approve (accept and remove the retained data)",
			}
			return writeApplicationUpdateResult(cmd, o, result, nil)
		}
		return rollbackFailedApplicationUpdate(ctx, cmd, o, record, reason, failure, converge)
	}
	if checkpointFailure == nil {
		checkpointFailure = fmt.Errorf("release operation %s is %s", record.OperationID, record.State)
	}
	return rollbackFailedApplicationUpdate(ctx, cmd, o, record, updateFailedCheckpoint, checkpointFailure, converge)
}

func rollbackFailedApplicationUpdate(ctx context.Context, cmd *cobra.Command, o useCaseUpgradeOptions, record applicationrelease.Record, reason string, failure error, converge func() (string, error)) error {
	wd := getWorkDir()
	rolled, abandoned, err := rollbackApplicationRelease(ctx, wd, record.OperationID)
	if err == nil {
		record = rolled
		if !abandoned {
			if _, err = converge(); err == nil {
				record, err = finalizeApplicationRelease(ctx, wd, record.OperationID)
			}
		}
	}
	if err != nil {
		result := applicationUpdateResultFor(record, updateOutcomeRollbackFailed, reason, failure.Error()+"; rollback: "+err.Error())
		result.NextSteps = []string{"stackkit use-cases upgrade --status " + record.OperationID, "stackkit use-cases upgrade --rollback " + record.OperationID + " --owner-approve (repeat the rollback)"}
		return writeApplicationUpdateResult(cmd, o, result, errors.Join(failure, err))
	}
	return writeApplicationUpdateResult(cmd, o, applicationUpdateResultFor(record, updateOutcomeRolledBack, reason, failure.Error()), failure)
}

// runApplicationReleaseOverrides reports every release override of the
// applied plan and whether this release keeps it (its pin runs) or
// supersedes it (the StackKits default is at or above it).
func runApplicationReleaseOverrides(cmd *cobra.Command, o useCaseUpgradeOptions) error {
	applied, err := inspectNativeV2AppliedAuthority(cmd.Context(), getWorkDir(), specFile)
	if err != nil {
		return fmt.Errorf("release overrides are read from the current applied plan: %w", err)
	}
	var document struct {
		Workloads []struct {
			ID          string         `json:"id"`
			Settings    map[string]any `json:"settings"`
			Alternative struct {
				ModuleRef string `json:"moduleRef"`
			} `json:"alternative"`
		} `json:"workloads"`
	}
	if err := json.Unmarshal(applied.Plan.Canonical(), &document); err != nil {
		return err
	}
	overrides := []map[string]any{}
	for _, workload := range document.Workloads {
		raw, present := workload.Settings[resolvedplan.ComponentReleaseSetting]
		if !present {
			continue
		}
		entry := map[string]any{"workloadRef": workload.ID, "moduleRef": workload.Alternative.ModuleRef}
		override, ok := resolvedplan.DecodeComponentReleaseSetting(raw)
		choice, choiceOK := architecturev2renderer.WorkloadReleaseChoice(workload.Alternative.ModuleRef)
		if !ok || !choiceOK {
			entry["state"] = "invalid"
			overrides = append(overrides, entry)
			continue
		}
		entry["version"], entry["ref"], entry["digest"], entry["tested"] = override.Version, override.Ref, override.Digest, override.Tested
		entry["defaultRelease"] = choice.Admitted[0].Release
		state, err := resolvedplan.EvaluateComponentRelease(choice, override)
		switch {
		case err != nil:
			entry["state"], entry["reason"] = "invalid", err.Error()
		case state == resolvedplan.ComponentReleaseApplied:
			entry["state"] = "kept"
		default:
			entry["state"] = "superseded"
		}
		overrides = append(overrides, entry)
	}
	if o.json {
		return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"apiVersion": "stackkit.application-release-overrides/v1", "overrides": overrides})
	}
	if len(overrides) == 0 {
		printInfo("No application release overrides")
	}
	for _, entry := range overrides {
		printInfo("%s: %v %v (default %v)", entry["workloadRef"], entry["version"], entry["state"], entry["defaultRelease"])
	}
	return nil
}
