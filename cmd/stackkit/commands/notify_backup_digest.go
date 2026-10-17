package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/ownernotify"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/spf13/cobra"
)

var notifyBackupDigestDryRun bool

var notifyBackupDigestCmd = &cobra.Command{
	Use:   "backup-digest",
	Short: "Send the backup digest to the owner notification channels",
	Args:  cobra.NoArgs,
	Long: `Read the local backup state without changing it and send one owner
message: backup-failed when configuration, schedule, repository, the latest
snapshot or a recovery objective needs attention; backup-restore-verification-overdue
when only the staged restore proof is missing or older than the plan's
restoreVerification.maxEvidenceAgeDays; otherwise backup-digest.

The message is sent only when its event is in spec.notifications.events
(backup-digest is opt-in). It carries states and ages only, never a repository
location or credential. Run it from a timer or an automation for a regular
digest.`,
	Example: `  # Preview the message without sending it
  stackkit notify backup-digest --dry-run

  # Send it and print the receipts as JSON
  stackkit notify backup-digest --json`,
	RunE: runNotifyBackupDigest,
}

func init() {
	notifyBackupDigestCmd.Flags().BoolVar(&notifyBackupDigestDryRun, "dry-run", false, "Print the composed message without sending it")
	notifyBackupDigestCmd.Flags().BoolVar(&notifyJSON, "json", false, "Output the message and receipts as JSON")
	notifyBackupDigestCmd.Flags().StringVar(&notifyChannel, "channel", "", "Deliver only to this declared channel name")
	notifyBackupDigestCmd.Flags().StringVar(&notifyResolvedPlan, "resolved-plan", "", "Canonical Architecture v2 ResolvedPlan (default: beneath the StackSpec generation.outputRoot)")
	notifyCmd.AddCommand(notifyBackupDigestCmd)
}

func runNotifyBackupDigest(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	wd := getWorkDir()
	plan, _, err := loadVerifiedArchitectureV2Plan(wd, notifyResolvedPlan)
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	intent, err := ownernotify.PlanIntentFromPlan(plan)
	if err != nil {
		return err
	}
	message := ownernotify.ComposeBackupDigest(readBackupDigestInput(ctx, wd, plan))
	admitted := intent != nil && containsString(intent.Events, message.Event)

	var receipts []ownernotify.Receipt
	var deliverErr error
	if !notifyBackupDigestDryRun {
		if intent == nil {
			deliverErr = ownernotify.ErrNotConfigured
		} else if admitted {
			receipts, deliverErr = deliverOwnerNotification(ctx, wd, notifyResolvedPlan, notifyChannel, message)
		}
	}
	if notifyJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]any{
			"apiVersion": "stackkit.notify-backup-digest/v1",
			"event":      message.Event,
			"admitted":   admitted,
			"dryRun":     notifyBackupDigestDryRun,
			"message":    map[string]any{"title": message.Title, "body": message.Body, "priority": message.Priority},
			"receipts":   receipts,
		}); err != nil {
			return err
		}
		return deliverErr
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "%s [%s]\n%s\n", message.Title, message.Event, message.Body)
	switch {
	case notifyBackupDigestDryRun:
		_, _ = fmt.Fprintln(out, "Dry run: nothing sent.")
	case intent != nil && !admitted:
		_, _ = fmt.Fprintf(out, "Not sent: %s is not in spec.notifications.events.\n", message.Event)
	}
	for _, receipt := range receipts {
		line := fmt.Sprintf("%s (%s): %s", receipt.Channel, receipt.Type, receipt.Result)
		if receipt.Reason != "" {
			line += " - " + receipt.Reason
		}
		_, _ = fmt.Fprintln(out, line)
	}
	return deliverErr
}

// readBackupDigestInput gathers the digest facts read-only: the configuration
// and schedule binding `stackkit status` reports and the repository status
// `stackkit backup status` reports. Any unreadable axis becomes a fact the
// owner sees, never a silent green digest.
func readBackupDigestInput(ctx context.Context, wd string, plan resolvedplan.ResolvedPlan) ownernotify.BackupDigestInput {
	input := ownernotify.BackupDigestInput{
		Configuration: backupBindingNotConfigured, Schedule: backupBindingNotConfigured,
		MaxRestoreEvidenceAge: restoreEvidenceMaxAge(plan),
	}
	if binding := readArchitectureV2BackupStatus(ctx, wd); binding != nil {
		input.Configuration, input.Schedule = binding.Configuration, binding.Schedule
		input.Attention = append(input.Attention, binding.Attention...)
	}
	status, err := readNativeV2BackupRepositoryStatus(ctx, wd)
	if err != nil {
		input.RepositoryProblem = "status unavailable"
		return input
	}
	input.RepositoryReady = status.Ready
	if status.History != nil {
		input.SnapshotState, input.SnapshotAgeSeconds = status.History.Snapshot.State, status.History.Snapshot.AgeSeconds
		input.StagedRestoreState, input.StagedRestoreAge = status.History.StagedRestore.State, status.History.StagedRestore.AgeSeconds
		for _, objective := range status.History.RecoveryObjectives {
			switch objective.DataLoss.State {
			case "breached":
				input.ObjectivesBreached++
			case "unverified":
				input.ObjectivesUnverified++
			}
		}
	}
	return input
}

// readNativeV2BackupRepositoryStatus runs the same read-only observation as
// `stackkit backup status` without taking the output lock.
func readNativeV2BackupRepositoryStatus(ctx context.Context, wd string) (backuplifecycle.RepositoryStatus, error) {
	initial, err := inspectNativeV2BackupAuthorityForRequest(ctx, wd, specFile)
	if err != nil {
		return backuplifecycle.RepositoryStatus{}, err
	}
	var status backuplifecycle.RepositoryStatus
	err = withArchitectureV2ReadOnlyOutput(wd, initial.OutputRoot, func() error {
		current, err := inspectNativeV2BackupAuthorityForRequest(ctx, wd, specFile)
		if err != nil {
			return err
		}
		if !sameNativeV2BackupAuthority(initial, current) {
			return errors.New("backup authority changed during the digest read")
		}
		result, err := continueNativeV2Backup(ctx, nativeV2BackupStatus, current, nativeV2BackupRequest{})
		if err != nil {
			return err
		}
		repository, ok := result.(backuplifecycle.RepositoryStatus)
		if !ok {
			return errors.New("backup status returned no repository status")
		}
		status = repository
		return nil
	})
	return status, err
}

// restoreEvidenceMaxAge reads backupPolicy.restoreVerification.maxEvidenceAgeDays
// from the verified plan; the CUE default is 35 days.
func restoreEvidenceMaxAge(plan resolvedplan.ResolvedPlan) time.Duration {
	days := 35.0
	if policy, ok := plan["backupPolicy"].(map[string]any); ok {
		if verification, ok := policy["restoreVerification"].(map[string]any); ok {
			switch value := verification["maxEvidenceAgeDays"].(type) {
			case float64:
				days = value
			case int:
				days = float64(value)
			case int64:
				days = float64(value)
			case json.Number:
				if parsed, err := value.Float64(); err == nil {
					days = parsed
				}
			}
		}
	}
	return time.Duration(days * float64(24*time.Hour))
}
