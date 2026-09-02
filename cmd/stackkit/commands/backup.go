package commands

// stackkit backup — operator surface for the addons/backup add-on.
//
// Native v2 configure/status/run/restore are authorized from the exact local
// ResolvedPlan, generation closure, owner custody, and Apply evidence before
// entering the Kopia runtime. Remaining v0.6-only verbs retain their isolated
// compatibility adapter until their native lifecycle slices replace it.
//
// Subcommands:
//   init                    print first-run wizard instructions
//   configure               configure/connect the local Kopia repository
//   status                  show local Kopia repository status
//   run                     force a snapshot of all configured paths
//   list                    list snapshots (table or --json)
//   restore <anchor>        verify and stage one owner-signed snapshot anchor
//   restore abandon <id>    close one pending or staged restore journal
//   verify                  trigger validate-provider ad-hoc
//   migrate-from-restic     drive the one-shot Restic→Kopia importer
//
// Managed fleet enrollment is intentionally implemented in
// backup_managed.go. Public exports omit that file while retaining this
// complete local operator surface.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/spf13/cobra"
)

const (
	// Aliased from backupexec so CLI command budgets and the shared docker
	// adapter can never drift apart.
	backupLongOperationTimeout  = backupexec.LongOperationTimeout
	backupQuickOperationTimeout = backupexec.QuickOperationTimeout
)

var (
	backupOutputJSON                    bool
	backupRestoreOperationID            string
	backupRestoreOwnerApproved          bool
	backupRestoreAbandonOwnerApproved   bool
	backupActivationOperationID         string
	backupActivationOwnerApproved       bool
	backupRecoveryOwnerApproved         bool
	backupEmergencyExportTarget         string
	backupEmergencyExportFormat         string
	backupEmergencyExportLargeMediaMode string
	backupEmergencyExportRecipients     []string
	backupEmergencyExportSourcePaths    []string
	backupMigrateDryRun                 bool
	backupRunOperationID                string
)

// backupEngine exists only for the exact-v0.6 compatibility verbs that remain
// in source during the v0.8 deprecation window. Native configure/status/run use
// the CUE-owned Compose service through localbackupruntime.
var backupEngine = func() backupexec.Engine {
	return backupexec.NewDockerEngine(backupexec.DefaultContainer)
}

var runResticImporter = func(ctx context.Context, args []string) error {
	cmdLine := exec.CommandContext(ctx, "docker", args...)
	cmdLine.Stdout = os.Stdout
	cmdLine.Stderr = os.Stderr
	cmdLine.Stdin = os.Stdin
	return cmdLine.Run()
}

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Manage backups (Kopia engine)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
	Long: `Manage backups for this StackKit deployment.

Backups are powered by Kopia (see ADR-0016) and run in the local
kopia-agent service rendered by the Basement core. Native configure, status,
and run revalidate the exact local Plan, generated artifacts, owner custody,
and Apply evidence before touching Kopia. Repository, source, exclusions,
service identity, and credentials are CUE- or owner-custody-owned.

Examples:
  stackkit backup configure --json
  stackkit backup status
  stackkit backup run --operation-id nightly-20260727
  stackkit backup list --json
  stackkit backup restore sha256:<snapshot-anchor-id> --owner-approve
  stackkit backup verify
  stackkit backup migrate-from-restic`,
}

var backupInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Print first-run setup instructions",
	Long: `Print the first-run setup steps for the backup addon.

This command is read-only. It describes the native lifecycle that renders and
applies the CUE-owned local kopia-agent before repository configuration.`,
	RunE: runBackupInit,
}

var backupConfigureCmd = &cobra.Command{
	Use:         "configure",
	Short:       "Configure the CUE-governed local Kopia repository",
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	Args:        cobra.NoArgs,
	Long: `Configure the local Kopia repository from the exact generated StackSpec v2 backup policy.

The repository path and kopia-agent service are CUE-owned and cannot be
overridden at the command line.`,
	RunE: runBackupConfigure,
}

var backupStatusCmd = &cobra.Command{
	Use:         "status",
	Short:       "Show local Kopia repository status",
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	Args:        cobra.NoArgs,
	RunE:        runBackupStatus,
}

var backupRunCmd = &cobra.Command{
	Use:         "run",
	Short:       "Force a snapshot now (out of band)",
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	Args:        cobra.NoArgs,
	RunE:        runBackupRun,
}

var backupListCmd = &cobra.Command{
	Use:         "list",
	Short:       "List snapshots in the local repository",
	Annotations: map[string]string{legacyV06BeforeObservabilityAnnotation: "backup list"},
	RunE:        runBackupList,
}

var backupRestoreCmd = &cobra.Command{
	Use:         "restore <snapshot-anchor-id>",
	Short:       "Verify and restore a signed snapshot into isolated staging",
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	Args:        cobra.ExactArgs(1),
	Long: `Restore one content-addressed, owner-signed snapshot anchor into the
CUE-owned isolated staging volume. The raw Kopia snapshot ID and staging path
are not caller-controlled. --owner-approve records explicit local Owner
authorization; this command never requires a Kombify account or Cloud service.`,
	RunE: runBackupRestore,
}

var backupRestoreAbandonCmd = &cobra.Command{
	Use:         "abandon <restore-operation-id>",
	Short:       "Abandon one pending or staged restore operation",
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	Args:        cobra.ExactArgs(1),
	RunE:        runBackupRestoreAbandon,
}

var backupRestoreActivateCmd = &cobra.Command{
	Use:         "activate <restore-result-id>",
	Short:       "Activate one verified staged restore into the live Basement volumes",
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	Args:        cobra.ExactArgs(1),
	RunE:        runBackupRestoreActivate,
}

var backupRestoreRecoverCmd = &cobra.Command{
	Use:   "recover <activation-operation-id>",
	Short: "Recover an interrupted restore or finish its committed result",
	Long: `Recover the exact owner-approved restore activation. Before commit, recovery
restores the prior live volumes. After commit, it preserves the activated data
and resumes result cleanup and application finalization. Repeating recovery for
a completed operation returns its original signed result.`,
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	Args:        cobra.ExactArgs(1),
	RunE:        runBackupRestoreRecover,
}

var backupVerifyCmd = &cobra.Command{
	Use:         "verify",
	Short:       "Validate the repository against its storage provider",
	Annotations: map[string]string{legacyV06BeforeObservabilityAnnotation: "backup verify"},
	RunE:        runBackupVerify,
}

var backupEmergencyExportCmd = &cobra.Command{
	Use:   "emergency-export",
	Short: "Export selected local data as an encrypted portable recovery archive",
	Args:  machineAwareNoArgs,
	Long: `Create a portable age-encrypted tar/gzip archive with per-file checksums and a
restore runbook. Select each source explicitly as CLASS=PATH and provide an age
recipient public key. The target must be a new directory outside the sources.
File copying does not prove application consistency; stop writers or export database-native dumps first.
Recovery stages data without the original host, Kopia, or a Kombify account.`,
	RunE: runBackupEmergencyExport,
}

var backupMigrateResticCmd = &cobra.Command{
	Use:         "migrate-from-restic",
	Short:       "Import an existing Restic repository into Kopia (one-shot)",
	Annotations: map[string]string{legacyV06BeforeObservabilityAnnotation: "backup migrate-from-restic"},
	Long: `Drive the one-shot Restic-to-Kopia importer.

Reads the Restic repository configured in the v1 addon, walks every
snapshot, and re-creates it inside Kopia preserving original timestamps.
After a successful import the addon flips engine: "restic-import" to
"kopia" automatically.`,
	RunE: runBackupMigrateRestic,
}

func init() {
	// Subcommand-specific flags.
	backupConfigureCmd.Flags().BoolVar(&backupOutputJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	backupStatusCmd.Flags().BoolVar(&backupOutputJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	backupRunCmd.Flags().BoolVar(&backupOutputJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	backupRunCmd.Flags().StringVar(&backupRunOperationID, "operation-id", "",
		"Stable idempotency key for this snapshot operation")
	backupListCmd.Flags().BoolVar(&backupOutputJSON, "json", false, "Output snapshots as JSON")
	backupRestoreCmd.Flags().BoolVar(&backupOutputJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	backupRestoreCmd.Flags().StringVar(&backupRestoreOperationID, "operation-id", "",
		"Stable idempotency key for this staged restore operation")
	backupRestoreCmd.Flags().BoolVar(&backupRestoreOwnerApproved, "owner-approve", false,
		"Authorize this staged restore with the established local Owner custody")
	backupRestoreAbandonCmd.Flags().BoolVar(&backupOutputJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	backupRestoreAbandonCmd.Flags().BoolVar(&backupRestoreAbandonOwnerApproved, "owner-approve",
		false,
		"Authorize this restore abandonment with the established local Owner custody")
	backupRestoreActivateCmd.Flags().BoolVar(&backupOutputJSON, "json", false,
		"Emit stackkit.command-result/v1 JSON")
	backupRestoreActivateCmd.Flags().StringVar(&backupActivationOperationID, "operation-id", "",
		"Stable idempotency key for this live restore activation")
	backupRestoreActivateCmd.Flags().BoolVar(&backupActivationOwnerApproved, "owner-approve", false,
		"Authorize live activation with the established local Owner custody")
	backupRestoreRecoverCmd.Flags().BoolVar(&backupOutputJSON, "json", false,
		"Emit stackkit.command-result/v1 JSON")
	backupRestoreRecoverCmd.Flags().BoolVar(&backupRecoveryOwnerApproved, "owner-approve", false,
		"Authorize recovery with the established local Owner custody")
	backupRestoreRecoverCmd.Flags().Bool("rollback", false,
		"Authorize rollback if the interrupted activation has not committed")
	backupRestoreCmd.AddCommand(backupRestoreAbandonCmd, backupRestoreActivateCmd, backupRestoreRecoverCmd)
	configureEmergencyBackupCommands()
	backupMigrateResticCmd.Flags().BoolVar(&backupMigrateDryRun, "dry-run", false,
		"Print the plan without importing")
	// Wire subcommands.
	backupCmd.AddCommand(backupInitCmd)
	backupCmd.AddCommand(backupConfigureCmd)
	backupCmd.AddCommand(backupStatusCmd)
	backupCmd.AddCommand(backupRunCmd)
	backupCmd.AddCommand(backupListCmd)
	backupCmd.AddCommand(backupRestoreCmd)
	backupCmd.AddCommand(backupVerifyCmd)
	backupCmd.AddCommand(backupEmergencyExportCmd, backupEmergencyRestoreCmd)
	backupCmd.AddCommand(backupMigrateResticCmd)

	// Self-register on the root command — same pattern as break_glass.go.
	rootCmd.AddCommand(backupCmd)
}

// =============================================================================
// COMMAND IMPLEMENTATIONS
// =============================================================================

func runBackupInit(cmd *cobra.Command, args []string) error {
	printInfo("Local backup CLI (Kopia engine) — readiness checklist")
	fmt.Println()
	fmt.Println("  1. Materialize and verify the standalone Basement core:")
	fmt.Println("       stackkit init --owner-source=local")
	fmt.Println("       stackkit validate")
	fmt.Println("       stackkit generate")
	fmt.Println("       stackkit apply")
	fmt.Println("       stackkit verify")
	fmt.Println()
	fmt.Println("  2. Configure the CUE-owned local repository:")
	fmt.Println("       stackkit backup configure")
	fmt.Println()
	fmt.Println("  3. Check repository status and create the first snapshot:")
	fmt.Println("       stackkit backup status")
	fmt.Println("       stackkit backup run")
	fmt.Println()
	printInfo("Repository, source, exclusions, service, and passphrase custody have no CLI overrides.")
	printInfo("Documentation: addons/backup/README.md and docs/CLI.md")
	return nil
}

func runBackupConfigure(cmd *cobra.Command, args []string) error {
	return runNativeV2BackupCommand(cmd, nativeV2BackupConfigure, "")
}

func runBackupStatus(cmd *cobra.Command, args []string) error {
	return runNativeV2BackupCommand(cmd, nativeV2BackupStatus, "")
}

func runBackupRun(cmd *cobra.Command, args []string) error {
	return runNativeV2BackupCommand(cmd, nativeV2BackupRun, backupRunOperationID)
}

func runBackupList(cmd *cobra.Command, args []string) error {
	if err := requireLegacyBackupCLI("list"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), backupQuickOperationTimeout)
	defer cancel()

	out, err := backupEngine().ListSnapshotsJSON(ctx)
	if err != nil {
		return fmt.Errorf("list snapshots: %w", err)
	}

	if backupOutputJSON {
		fmt.Print(out)
		return nil
	}

	// Pretty-print: id, source, time, size.
	snapshots, parseErr := backupexec.ParseSnapshots(out)
	if parseErr != nil {
		// Fall back to raw output if Kopia's schema drifts under us.
		fmt.Print(out)
		return nil
	}
	if len(snapshots) == 0 {
		printWarning("No snapshots yet")
		return nil
	}
	fmt.Printf("%-14s %-30s %-25s %s\n", "ID", "SOURCE", "TIME (UTC)", "SIZE")
	for _, s := range snapshots {
		fmt.Printf("%-14s %-30s %-25s %s\n",
			truncate(s.ID, 14),
			truncate(s.SourcePath, 30),
			s.StartTime.UTC().Format(time.RFC3339),
			humanSize(s.TotalSize),
		)
	}
	return nil
}

func runBackupRestore(cmd *cobra.Command, args []string) error {
	return runNativeV2BackupRestoreCommand(
		cmd,
		args[0],
		backupRestoreOperationID,
		backupRestoreOwnerApproved,
	)
}

func runBackupRestoreAbandon(cmd *cobra.Command, args []string) error {
	return runNativeV2BackupRestoreAbandonCommand(
		cmd,
		args[0],
		backupRestoreAbandonOwnerApproved,
	)
}

func runBackupRestoreActivate(cmd *cobra.Command, args []string) error {
	return runNativeV2RestoreActivationCommand(
		cmd, args[0], backupActivationOperationID,
		backupActivationOwnerApproved,
	)
}

func runBackupRestoreRecover(cmd *cobra.Command, args []string) error {
	rollback, err := cmd.Flags().GetBool("rollback")
	if err != nil {
		return err
	}
	if !rollback {
		return fmt.Errorf("backup restore recover requires --rollback")
	}
	return runNativeV2RestoreRecoveryCommand(
		cmd, args[0], backupRecoveryOwnerApproved,
	)
}

func runBackupVerify(cmd *cobra.Command, args []string) error {
	if err := requireLegacyBackupCLI("verify"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), backupLongOperationTimeout)
	defer cancel()
	printInfo("Running kopia repository validate-provider (this may take a while)…")
	out, err := backupEngine().ValidateProvider(ctx)
	if err != nil {
		printError("validate-provider failed: %v", err)
		fmt.Fprintln(os.Stderr, out)
		return err
	}
	if verbose {
		fmt.Println(out)
	}
	printSuccess("Repository validates against the storage provider")
	return nil
}

func runBackupMigrateRestic(cmd *cobra.Command, args []string) error {
	if err := requireLegacyBackupCLI("migrate-from-restic"); err != nil {
		return err
	}
	if backupMigrateDryRun {
		printInfo("DRY RUN — no data will be written")
	}
	// The importer ships as a one-shot service in the addon
	// (addons/backup/restic-importer.cue). We trigger it by having
	// docker-compose run that service. The image's entrypoint reads
	// RESTIC_REPOSITORY / RESTIC_PASSWORD / KOPIA_PASSWORD from env
	// (already wired by the addon) and walks every snapshot.
	args2 := []string{"compose", "run", "--rm", "restic-importer"}
	if backupMigrateDryRun {
		args2 = append(args2, "--dry-run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), backupLongOperationTimeout)
	defer cancel()

	if err := runResticImporter(ctx, args2); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("restic-importer exceeded %s command budget: %w", backupLongOperationTimeout, ctx.Err())
		}
		return fmt.Errorf("restic-importer failed: %w (run with --verbose for details)", err)
	}
	if !backupMigrateDryRun {
		printSuccess("Restic snapshots imported into Kopia. Update addons/backup engine: \"restic-import\" → \"kopia\" and re-apply.")
	}
	return nil
}

func requireLegacyBackupCLI(operation string) error {
	return requireLegacyV06Command(
		"backup "+strings.TrimSpace(operation),
		"this compatibility operation has no canonical StackSpec v2 contract; use exact v0.6 compatibility or a native v2 lifecycle command",
	)
}

// =============================================================================
// HELPERS
// =============================================================================

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// silence unused-import linter when the tests run with build tags that
// strip stub-only code paths.
var _ = strings.TrimSpace
