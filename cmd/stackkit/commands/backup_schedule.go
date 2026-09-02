package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/clibinding"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackupschedule"
	"github.com/spf13/cobra"
)

var backupRunScheduled bool

func init() {
	schedule := &cobra.Command{Use: "schedule", Short: "Control the Owner-approved local backup timer", Args: cobra.NoArgs}
	schedule.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	schedule.PersistentFlags().BoolVar(&backupOutputJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	var enableApproved, disableApproved bool
	enable := &cobra.Command{Use: "enable", Short: "Approve and enable the exact CUE backup cadence", Args: cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE:        func(cmd *cobra.Command, _ []string) error { return enableNativeBackupSchedule(cmd, enableApproved) }}
	enable.Flags().BoolVar(&enableApproved, "owner-approve", false, "Approve this exact Plan, CLI and local backup cadence")
	disable := &cobra.Command{Use: "disable", Short: "Revoke scheduled backup execution and stop its timer", Args: cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE:        func(cmd *cobra.Command, _ []string) error { return disableNativeBackupSchedule(cmd, disableApproved) }}
	disable.Flags().BoolVar(&disableApproved, "owner-approve", false, "Revoke local scheduled backup authority")
	status := &cobra.Command{Use: "status", Short: "Show timer, authorization and last scheduled snapshot separately", Args: cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}, RunE: statusNativeBackupSchedule}
	schedule.AddCommand(enable, disable, status)
	backupCmd.AddCommand(schedule)
	backupRunCmd.Flags().BoolVar(&backupRunScheduled, "scheduled", false, "Execute only through the current signed local schedule authorization")
	originalRun := backupRunCmd.RunE
	backupRunCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !backupRunScheduled {
			return originalRun(cmd, args)
		}
		if cmd.Flags().Changed("operation-id") {
			return errors.New("scheduled backup operation IDs are derived from the approved UTC slot")
		}
		return runNativeV2BackupRequest(cmd, nativeV2BackupRun, nativeV2BackupRequest{Scheduled: true})
	}
}

type nativeBackupScheduleStatus struct {
	APIVersion           string                             `json:"apiVersion"`
	Authorization        *localbackupschedule.Authorization `json:"authorization,omitempty"`
	AuthorizationCurrent bool                               `json:"authorizationCurrent"`
	Timer                *localbackupschedule.UnitStatus    `json:"timer,omitempty"`
	State                string                             `json:"state"`
	Reason               string                             `json:"reason,omitempty"`
}

type nativeBackupScheduleNoop struct {
	APIVersion      string                                  `json:"apiVersion"`
	State           string                                  `json:"state"`
	SnapshotCreated bool                                    `json:"snapshotCreated"`
	LastExecution   *localbackupschedule.ScheduledExecution `json:"lastExecution,omitempty"`
}

func nativeBackupScheduleInputs(ctx context.Context, authority nativeV2BackupAuthority) (*localbackupschedule.Scheduler, localbackupschedule.RenderRequest, localbackupschedule.AuthorizationBinding, error) {
	if authority.AppliedAuthority == nil || authority.Policy.Schedule == nil {
		return nil, localbackupschedule.RenderRequest{}, localbackupschedule.AuthorizationBinding{}, errors.New("local scheduling requires a current native Apply with CUE backup schedule intent")
	}
	cliPath, err := clibinding.Sibling()
	if err != nil {
		return nil, localbackupschedule.RenderRequest{}, localbackupschedule.AuthorizationBinding{}, err
	}
	cli, err := clibinding.Bind(ctx, cliPath, version, gitCommit)
	if err != nil {
		return nil, localbackupschedule.RenderRequest{}, localbackupschedule.AuthorizationBinding{}, err
	}
	currentExecutable, err := os.Executable()
	if err != nil || filepath.Clean(currentExecutable) != cli.Path() {
		return nil, localbackupschedule.RenderRequest{}, localbackupschedule.AuthorizationBinding{}, errors.New("backup schedule must be controlled by its bound packaged CLI")
	}
	specPath, _, _, err := config.NewLoader(authority.WorkspaceRoot).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return nil, localbackupschedule.RenderRequest{}, localbackupschedule.AuthorizationBinding{}, err
	}
	scheduler := localbackupschedule.New(localbackupschedule.Options{})
	request := localbackupschedule.RenderRequest{WorkspacePath: authority.WorkspaceRoot, SpecPath: specPath, Schedule: *authority.Policy.Schedule, CLI: cli}
	units, err := scheduler.Render(request)
	if err != nil {
		return nil, localbackupschedule.RenderRequest{}, localbackupschedule.AuthorizationBinding{}, err
	}
	binding := localbackupschedule.AuthorizationBinding{
		OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef, Lineage: authority.Lineage,
		PolicyDigest: authority.PolicyDigest, WorkspaceRoot: authority.WorkspaceRoot, SpecPath: specPath,
		ProcessUID: units.ProcessUID, CLI: cli.Identity(), Schedule: *authority.Policy.Schedule,
		UnitName: units.Names.Timer, UnitDigest: units.Digest(),
	}
	return scheduler, request, binding, nil
}

func enableNativeBackupSchedule(cmd *cobra.Command, ownerApproved bool) error {
	if !ownerApproved {
		return errors.New("backup schedule enable requires --owner-approve")
	}
	ctx, workspace := cmd.Context(), getWorkDir()
	initial, err := inspectNativeV2BackupAuthority(ctx, workspace, specFile)
	if err != nil {
		return err
	}
	var result localbackupschedule.Authorization
	err = withLifecycleMutation(workspace, "backup schedule enable", func() error {
		return withArchitectureV2OutputLock(workspace, initial.OutputRoot, func(_ *confinedfs.Transaction, _ *confinedfs.OutputLock) (returnErr error) {
			current, err := inspectNativeV2BackupAuthority(ctx, workspace, specFile)
			if err != nil {
				return err
			}
			if !sameNativeV2BackupAuthority(initial, current) {
				return errors.New("backup schedule authority changed while acquiring the output lock")
			}
			scheduler, request, binding, err := nativeBackupScheduleInputs(ctx, current)
			if err != nil {
				return err
			}
			// The existing lifecycle checks current repository configuration and
			// source custody. A timer cannot turn an unconfigured backup green.
			repositoryResult, err := continueNativeV2Backup(ctx, nativeV2BackupStatus, current, nativeV2BackupRequest{})
			if err != nil {
				return err
			}
			repositoryStatus, ok := repositoryResult.(backuplifecycle.RepositoryStatus)
			if !ok || !repositoryStatus.Ready {
				return errors.New("backup repository is not ready for scheduled snapshots")
			}
			if _, err := localbackupschedule.PrepareAuthorization(workspace, binding, ownerApproved); err != nil {
				return err
			}
			activated := false
			defer func() {
				if activated {
					return
				}
				// Revoke dispatch even if systemd partly enabled the timer. The
				// original request may be canceled, so cleanup gets its own bound.
				_, revokeErr := localbackupschedule.DisableAuthorization(workspace, true)
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				timerErr := scheduler.Disable(cleanupCtx, workspace)
				if revokeErr != nil {
					returnErr = errors.Join(returnErr, fmt.Errorf("revoke incomplete backup schedule activation: %w", revokeErr))
				}
				if timerErr != nil {
					returnErr = errors.Join(returnErr, fmt.Errorf("stop incompletely activated backup timer: %w", timerErr))
				}
			}()
			if _, err := scheduler.Install(ctx, request); err != nil {
				return err
			}
			if err := scheduler.Enable(ctx, request); err != nil {
				return err
			}
			result, err = localbackupschedule.ActivateAuthorization(workspace, binding)
			activated = err == nil
			return err
		})
	})
	if err != nil {
		return err
	}
	return emitNativeBackupSchedule(cmd, "enabled", result)
}

func disableNativeBackupSchedule(cmd *cobra.Command, ownerApproved bool) error {
	if !ownerApproved {
		return errors.New("backup schedule disable requires --owner-approve")
	}
	workspace := getWorkDir()
	var result localbackupschedule.Authorization
	err := withLifecycleMutation(workspace, "backup schedule disable", func() error {
		var revokeErr error
		result, revokeErr = localbackupschedule.DisableAuthorization(workspace, ownerApproved)
		if revokeErr != nil {
			revokeErr = fmt.Errorf("revoke backup schedule authorization: %w", revokeErr)
		}
		// Stop future timer dispatch even if the signed authorization cannot
		// be updated. Neither independent failure may suppress the other action.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		timerErr := localbackupschedule.New(localbackupschedule.Options{}).Disable(cleanupCtx, workspace)
		return errors.Join(revokeErr, timerErr)
	})
	if err != nil {
		return fmt.Errorf("backup schedule disable is incomplete: %w", err)
	}
	return emitNativeBackupSchedule(cmd, "disabled", result)
}

func statusNativeBackupSchedule(cmd *cobra.Command, _ []string) error {
	ctx, workspace := cmd.Context(), getWorkDir()
	result := nativeBackupScheduleStatus{APIVersion: "stackkit.local-backup-schedule-status/v1", State: "not-configured"}
	record, err := localbackupschedule.LoadAuthorization(workspace)
	if errors.Is(err, os.ErrNotExist) {
		return emitNativeBackupSchedule(cmd, result.State, result)
	}
	if err != nil {
		return err
	}
	result.Authorization, result.State = &record, record.State
	if status, err := localbackupschedule.New(localbackupschedule.Options{}).Status(ctx, workspace); err == nil {
		result.Timer = &status
	} else {
		result.Reason = "timer-unavailable"
	}
	if current, err := inspectNativeV2BackupAuthority(ctx, workspace, specFile); err == nil {
		if scheduler, request, binding, err := nativeBackupScheduleInputs(ctx, current); err == nil && reflect.DeepEqual(binding, record.Binding) {
			result.AuthorizationCurrent = true
			if err := scheduler.VerifyInstalled(request); err != nil {
				result.AuthorizationCurrent = false
				if result.Reason == "" {
					result.Reason = "timer-artifact-drift"
				}
			}
		}
	}
	if !result.AuthorizationCurrent && result.Reason == "" {
		result.Reason = "local-authority-changed"
	}
	return emitNativeBackupSchedule(cmd, result.State, result)
}

// authorizeScheduledNativeBackup runs only after the common lifecycle/output
// locks and current backup authority have been acquired. It never calls Kopia.
func authorizeScheduledNativeBackup(ctx context.Context, current nativeV2BackupAuthority) (localbackupschedule.Authorization, bool, error) {
	scheduler, request, binding, err := nativeBackupScheduleInputs(ctx, current)
	if err != nil {
		return localbackupschedule.Authorization{}, false, err
	}
	if err := scheduler.VerifyInstalled(request); err != nil {
		return localbackupschedule.Authorization{}, false, err
	}
	return localbackupschedule.BeginScheduledAttempt(current.WorkspaceRoot, binding)
}

func completeScheduledNativeBackup(current nativeV2BackupAuthority, record localbackupschedule.Authorization, result any) error {
	anchor, ok := result.(backuplifecycle.SnapshotAnchor)
	if !ok {
		return errors.New("scheduled backup returned no signed snapshot anchor")
	}
	_, err := localbackupschedule.CompleteScheduledAttempt(current.WorkspaceRoot, record.Binding, anchor)
	return err
}

func emitNativeBackupSchedule(cmd *cobra.Command, state string, result any) error {
	if backupOutputJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Local backup schedule: %s\n", state)
	if status, ok := result.(nativeBackupScheduleStatus); ok {
		if status.Reason != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Attention: %s\n", status.Reason)
		}
		if status.Timer != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Timer: %s / %s\n", status.Timer.EnabledState, status.Timer.ActiveState)
		}
		if status.Authorization != nil && status.Authorization.Execution != nil {
			execution := status.Authorization.Execution
			fmt.Fprintf(cmd.OutOrStdout(), "Last scheduled attempt: %s (%s)\n", execution.State, execution.Slot.Format(time.RFC3339))
			if execution.SnapshotAnchorID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Snapshot receipt: %s\n", execution.SnapshotAnchorID)
			}
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Scheduled snapshot receipt: unverified")
		}
	}
	return nil
}
