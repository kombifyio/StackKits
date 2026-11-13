package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/applicationrelease"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/spf13/cobra"
)

// runApplicationReleaseRollback returns the application to its prior release
// and the data retained at the checkpoint. A checkpoint that never completed
// is released instead: Apply starts nothing of the application while it is
// open, so live data is unchanged and the unchanged application restarts.
// Every precondition is checked under the lifecycle lock before live data is
// touched, and the retained copy is read back before it overwrites live data.
func runApplicationReleaseRollback(cmd *cobra.Command, o useCaseUpgradeOptions) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), applicationReleaseTimeout)
	defer cancel()
	record, abandoned, err := rollbackApplicationRelease(ctx, getWorkDir(), strings.TrimSpace(o.rollback))
	if err != nil {
		return err
	}
	if abandoned {
		return writeApplicationReleaseResult(cmd, o, record, "Released the incomplete checkpoint; "+record.WorkloadRef+" keeps "+record.PriorRelease, nil)
	}
	return writeApplicationReleaseResult(cmd, o, record, "Restored "+record.WorkloadRef+" data to its "+record.PriorRelease+" checkpoint; selected "+record.PriorRelease+" (data written since the checkpoint is discarded)",
		[]string{"stackkit generate", "stackkit apply", "stackkit use-cases upgrade --finalize " + record.OperationID + " --owner-approve (after the apply)"})
}

// rollbackApplicationRelease runs the rollback of one operation and reports
// whether it only released an incomplete checkpoint.
func rollbackApplicationRelease(ctx context.Context, wd, operation string) (applicationrelease.Record, bool, error) {
	var record applicationrelease.Record
	service, specPath, err := applicationReleaseIntentAuthority(wd)
	if err != nil {
		return record, false, err
	}
	runtime, err := restoreactivation.NewDockerRuntime(wd)
	if err != nil {
		return record, false, err
	}
	abandoned := false
	err = withLifecycleMutation(wd, "use-cases upgrade", func() error {
		var err error
		if record, err = applicationrelease.Read(wd, operation); err != nil {
			return err
		}
		if err := requireApplicationReleaseIntent(service, specPath, record); err != nil {
			return err
		}
		authority := applicationReleaseAuthority(record)
		switch record.State {
		case applicationrelease.StateCheckpointStarted:
			abandoned = true
			if err := restoreactivation.HoldApplicationProject(ctx, wd, authority, record.ComposeProject); err != nil {
				return err
			}
			for _, volume := range authority.VolumeDetails {
				if err := runtime.CleanupRollback(ctx, authority, volume); err != nil {
					return err
				}
			}
			// Live runtime files never change while the checkpoint is open.
			return applicationrelease.RemoveRetainedRuntimeFiles(wd, record)
		case applicationrelease.StateCheckpointed, applicationrelease.StateRollbackStarted:
		default:
			return fmt.Errorf("release operation %s is %s and has nothing to roll back", record.OperationID, record.State)
		}
		for index, volume := range authority.VolumeDetails {
			retained, err := restoreactivation.DigestRetainedVolume(ctx, wd, authority, volume)
			if err != nil {
				return err
			}
			if !sameReadback(retained, record.Volumes[index].Checkpoint) {
				return fmt.Errorf("the retained copy of %s differs from its checkpoint; live data was not changed", volume.LiveName)
			}
		}
		// Runtime files restore after the volumes, so their retained copies
		// are checked before any live data changes.
		if err := applicationrelease.VerifyRetainedRuntimeFiles(wd, record); err != nil {
			return err
		}
		if record.State == applicationrelease.StateCheckpointed {
			record.State = applicationrelease.StateRollbackStarted
			if record, err = applicationrelease.Persist(wd, record); err != nil {
				return err
			}
		}
		if err := restoreactivation.HoldApplicationProject(ctx, wd, authority, record.ComposeProject); err != nil {
			return err
		}
		for index, volume := range authority.VolumeDetails {
			if err := runtime.RestoreVolume(ctx, authority, volume); err != nil {
				return err
			}
			live, err := runtime.DigestVolume(ctx, authority, volume, false)
			if err != nil {
				return err
			}
			if !sameReadback(live, record.Volumes[index].Checkpoint) {
				return fmt.Errorf("restored %s differs from its checkpoint; the application stays stopped", volume.LiveName)
			}
		}
		// A native installation's private runtime files (the Marketplace's
		// APP_KEY, credentials and API token custody) return with the data
		// they belong to.
		return applicationrelease.RestoreRuntimeFiles(wd, record)
	})
	// The intent writer takes the lifecycle lock itself; while the record is
	// incomplete, Apply starts nothing of this application.
	if err == nil {
		err = persistApplicationReleaseIntent(wd, service, specPath, record.TargetSpecHash, record.PriorSpec)
	}
	if err == nil && abandoned {
		if err = restoreactivation.ResumeApplicationProject(ctx, wd, record.ComposeProject); err == nil {
			record.State = applicationrelease.StateAbandoned
			record, err = applicationrelease.Persist(wd, record)
		}
	} else if err == nil {
		record.State = applicationrelease.StateRolledBack
		record, err = applicationrelease.Persist(wd, record)
	}
	if err != nil {
		return record, abandoned, fmt.Errorf("rollback of %s did not complete; repeat the command: %w", operation, err)
	}
	return record, abandoned, nil
}

// runApplicationReleaseFinalize removes the retained data. After a rollback
// it first requires the prior release to be applied, because the open
// rolled-back record is what admits that earlier release.
func runApplicationReleaseFinalize(cmd *cobra.Command, o useCaseUpgradeOptions) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), applicationReleaseTimeout)
	defer cancel()
	record, err := finalizeApplicationRelease(ctx, getWorkDir(), strings.TrimSpace(o.finalize))
	if err != nil {
		return err
	}
	return writeApplicationReleaseResult(cmd, o, record, "Removed the retained data of "+record.OperationID, nil)
}

func finalizeApplicationRelease(ctx context.Context, wd, operation string) (applicationrelease.Record, error) {
	var record applicationrelease.Record
	runtime, err := restoreactivation.NewDockerRuntime(wd)
	if err != nil {
		return record, err
	}
	err = withLifecycleMutation(wd, "use-cases upgrade", func() error {
		var err error
		if record, err = applicationrelease.Read(wd, operation); err != nil {
			return err
		}
		if record.State != applicationrelease.StateCheckpointed && record.State != applicationrelease.StateRolledBack {
			return fmt.Errorf("release operation %s is %s; only a completed checkpoint or rollback is finalized", record.OperationID, record.State)
		}
		if record.State == applicationrelease.StateRolledBack {
			image, applied, err := nativehost.PersistedApplicationEntryImage(wd, record.ComposeProject, record.EntryComponent)
			if err != nil {
				return err
			}
			if !applied || image != record.PriorImage.Ref+"@"+record.PriorImage.Digest {
				return fmt.Errorf("apply %s %s before finalizing its rollback", record.WorkloadRef, record.PriorRelease)
			}
		}
		authority := applicationReleaseAuthority(record)
		for _, volume := range authority.VolumeDetails {
			if err := runtime.CleanupRollback(ctx, authority, volume); err != nil {
				return err
			}
		}
		if err := applicationrelease.RemoveRetainedRuntimeFiles(wd, record); err != nil {
			return err
		}
		record.State = applicationrelease.StateFinalized
		record, err = applicationrelease.Persist(wd, record)
		return err
	})
	return record, err
}

// requireApplicationReleaseIntent refuses before any runtime change when the
// StackSpec names neither side of the release operation.
func requireApplicationReleaseIntent(service *architecturev2.Service, specPath string, record applicationrelease.Record) error {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	current, err := service.ValidateStackSpec(raw)
	if err != nil {
		return err
	}
	if current.SpecHash != record.TargetSpecHash && current.SpecHash != record.PriorSpecHash {
		return fmt.Errorf("intent changed after release operation %s (spec hash %s); restore it before rolling back", record.OperationID, current.SpecHash)
	}
	return nil
}

func sameReadback(observed backuplifecycle.VolumeReadback, checkpoint *applicationrelease.Readback) bool {
	return checkpoint != nil && observed.Files == checkpoint.Files && observed.Bytes == checkpoint.Bytes && observed.Digest == checkpoint.Digest
}

func runApplicationReleaseStatus(cmd *cobra.Command, o useCaseUpgradeOptions) error {
	record, err := applicationrelease.Read(getWorkDir(), strings.TrimSpace(o.status))
	if err != nil {
		return err
	}
	return writeApplicationReleaseResult(cmd, o, record, record.OperationID+" is "+string(record.State), nil)
}

// applicationReleaseAuthority rebuilds the narrowed restore authority from the
// signed record alone, so retained data stays reachable whatever the current
// generated plan is.
func applicationReleaseAuthority(record applicationrelease.Record) restoreactivation.Authority {
	authority := restoreactivation.Authority{OperationID: record.OperationID, PlanHash: record.PlanHash, KopiaHelperImage: record.KopiaHelperImage}
	for _, volume := range record.Volumes {
		authority.Volumes = append(authority.Volumes, volume.LiveName)
		authority.VolumeDetails = append(authority.VolumeDetails, restoreactivation.Volume{ComponentRef: volume.ComponentRef, LogicalName: volume.LogicalName, ComposeProject: volume.ComposeProject, LiveName: volume.LiveName, RollbackName: volume.RollbackName})
	}
	return authority
}

func applicationReleaseIntentAuthority(wd string) (*architecturev2.Service, string, error) {
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return nil, "", err
	}
	specPath, _, _, err := config.NewLoader(wd).ResolveStackSpecPathForRead(specFile)
	return service, specPath, err
}

// releaseOrder orders two release tags on one release line
// (resolvedplan.ComponentReleaseLater).
func releaseOrder(candidate, current string) (later, comparable bool) {
	return resolvedplan.ComponentReleaseLater(candidate, current)
}

func writeApplicationReleaseResult(cmd *cobra.Command, o useCaseUpgradeOptions, record applicationrelease.Record, summary string, next []string) error {
	volumes := make([]map[string]any, len(record.Volumes))
	for index, volume := range record.Volumes {
		volumes[index] = map[string]any{"liveName": volume.LiveName, "retainedName": volume.RollbackName, "checkpoint": volume.Checkpoint}
	}
	result := map[string]any{
		"apiVersion": "stackkit.application-release-result/v1", "operationId": record.OperationID, "state": record.State,
		"workloadRef": record.WorkloadRef, "composeProject": record.ComposeProject,
		"priorRelease": record.PriorRelease, "targetRelease": record.TargetRelease, "targetTested": !record.TargetUntested,
		"priorSpecHash": record.PriorSpecHash, "targetSpecHash": record.TargetSpecHash,
		"volumes": volumes, "runtimeApplied": false, "nextSteps": next,
	}
	if o.json {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	printSuccess("%s", summary)
	printInfo("Operation: %s (%s)", record.OperationID, record.State)
	for _, step := range next {
		printInfo("Next: %s", step)
	}
	return nil
}
