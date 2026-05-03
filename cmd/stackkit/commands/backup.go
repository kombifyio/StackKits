package commands

// stackkit backup — operator surface for the addons/backup add-on.
//
// All subcommands are thin orchestration layers over the local kopia-agent
// container. They do not reach into Kopia's repository directly; the agent
// already holds the credentials and the cache, so we shell out via
// `docker exec` and let it do the work. This keeps the CLI honest:
// anything you can do from the CLI you can also do from the Kopia Web UI,
// because both end up calling the same binary in the same container.
//
// Subcommands:
//   init                    print first-run wizard instructions
//   run                     force a snapshot of all configured paths
//   list                    list snapshots (table or --json)
//   restore <snapshot>      restore a snapshot to --target (default: tmpfs)
//   verify                  trigger validate-provider ad-hoc
//   migrate-from-restic     drive the one-shot Restic→Kopia importer
//   enroll                  switch this host into agent mode (SaaS path)
//
// `enroll` is a Phase-4 stub today — it parses the token but the
// controller endpoint it would talk to does not exist yet. The command
// is wired now so users learn the surface, and it errors clearly until
// the controller lands.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/spf13/cobra"
)

// backupContainer is the default name of the local Kopia agent container.
// It matches the `name` field in addons/backup/addon.cue → #KopiaAgentService.
// Operators can override via --container if they renamed it (rare).
const backupContainer = "kopia-agent"

var (
	backupContainerName  string
	backupOutputJSON     bool
	backupRestoreTarget  string
	backupMigrateDryRun  bool
	backupEnrollToken    string
	backupEnrollEndpoint string
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Manage backups (Kopia engine)",
	Long: `Manage backups for this StackKit deployment.

Backups are powered by Kopia (see ADR-0016) and run in the local
kopia-agent container. The same actions are available in the Kopia Web
UI under https://backups.<domain> — the CLI is here for power users and
scripting.

Examples:
  stackkit backup run
  stackkit backup list --json
  stackkit backup restore k1234567 --target /tmp/restore
  stackkit backup verify
  stackkit backup migrate-from-restic
  stackkit backup enroll --token <token-from-techstack>`,
}

var backupInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Print first-run setup instructions",
	Long: `Print the first-run setup steps for the backup addon.

This command does not modify anything. It tells you which addon to enable,
which secrets to provision, and how to bring the kopia-agent up.`,
	RunE: runBackupInit,
}

var backupRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Force a snapshot now (out of band)",
	RunE:  runBackupRun,
}

var backupListCmd = &cobra.Command{
	Use:   "list",
	Short: "List snapshots in the local repository",
	RunE:  runBackupList,
}

var backupRestoreCmd = &cobra.Command{
	Use:   "restore <snapshot-id>",
	Short: "Restore a snapshot to a target directory",
	Args:  cobra.ExactArgs(1),
	RunE:  runBackupRestore,
}

var backupVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Validate the repository against its storage provider",
	RunE:  runBackupVerify,
}

var backupMigrateResticCmd = &cobra.Command{
	Use:   "migrate-from-restic",
	Short: "Import an existing Restic repository into Kopia (one-shot)",
	Long: `Drive the one-shot Restic-to-Kopia importer.

Reads the Restic repository configured in the v1 addon, walks every
snapshot, and re-creates it inside Kopia preserving original timestamps.
After a successful import the addon flips engine: "restic-import" to
"kopia" automatically.`,
	RunE: runBackupMigrateRestic,
}

var backupEnrollCmd = &cobra.Command{
	Use:   "enroll",
	Short: "Enroll this host with the kombify Backup-Controller (SaaS)",
	Long: `Enroll this host as a fleet member of a kombify Backup tenant.

Switches the local addon into agentMode and registers the host against
the controller endpoint shown in the kombify-TechStack dashboard.

Phase-4 stub: the controller endpoint is not yet operational. The
command parses --token and --endpoint and reports a clear "not
implemented" error until the controller lands.`,
	RunE: runBackupEnroll,
}

func init() {
	// Shared flag: container name override.
	backupCmd.PersistentFlags().StringVar(&backupContainerName, "container", backupContainer,
		"Name of the local Kopia agent container")

	// Subcommand-specific flags.
	backupListCmd.Flags().BoolVar(&backupOutputJSON, "json", false, "Output snapshots as JSON")
	backupRestoreCmd.Flags().StringVar(&backupRestoreTarget, "target", "/tmp/stackkit-restore",
		"Directory to restore into (default: /tmp/stackkit-restore)")
	backupMigrateResticCmd.Flags().BoolVar(&backupMigrateDryRun, "dry-run", false,
		"Print the plan without importing")
	backupEnrollCmd.Flags().StringVar(&backupEnrollToken, "token", "", "Enrollment token from kombify-TechStack")
	backupEnrollCmd.Flags().StringVar(&backupEnrollEndpoint, "endpoint", "",
		"Backup-Controller gRPC/REST endpoint (e.g. https://backup.kombify.io)")
	_ = backupEnrollCmd.MarkFlagRequired("token")

	// Wire subcommands.
	backupCmd.AddCommand(backupInitCmd)
	backupCmd.AddCommand(backupRunCmd)
	backupCmd.AddCommand(backupListCmd)
	backupCmd.AddCommand(backupRestoreCmd)
	backupCmd.AddCommand(backupVerifyCmd)
	backupCmd.AddCommand(backupMigrateResticCmd)
	backupCmd.AddCommand(backupEnrollCmd)

	// Self-register on the root command — same pattern as break_glass.go.
	rootCmd.AddCommand(backupCmd)
}

// =============================================================================
// COMMAND IMPLEMENTATIONS
// =============================================================================

func runBackupInit(cmd *cobra.Command, args []string) error {
	printInfo("Backup addon (Kopia engine) — first-run checklist")
	fmt.Println()
	fmt.Println("  1. Enable the addon in your stack-spec.yaml:")
	fmt.Println("       stackkit addon add backup")
	fmt.Println()
	fmt.Println("  2. Provide secrets for the offsite target. Pick one:")
	fmt.Println("       Backblaze B2     → secret://b2/accountId, secret://b2/accountKey")
	fmt.Println("       Hetzner Storagebox → secret://hetzner/password")
	fmt.Println("       S3-compatible    → secret://s3/accessKey, secret://s3/secretKey")
	fmt.Println()
	fmt.Println("  3. Provision a backup-encryption passphrase via break-glass:")
	fmt.Println("       stackkit break-glass set backup-encryption-key")
	fmt.Println()
	fmt.Println("  4. Apply:")
	fmt.Println("       stackkit apply")
	fmt.Println()
	fmt.Println("  5. Open the Kopia Web UI (login-gateway gates it):")
	fmt.Println("       https://backups.<your-domain>")
	fmt.Println()
	printInfo("Documentation: addons/backup/README.md and docs/BACKUP-ARCHITECTURE.md")
	return nil
}

func runBackupRun(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	out, err := backupExec(ctx, []string{
		"kopia", "snapshot", "create", "/source/docker-volumes",
		"--description", fmt.Sprintf("ad-hoc via stackkit backup run @ %s", time.Now().UTC().Format(time.RFC3339)),
	})
	if err != nil {
		printError("snapshot failed: %v", err)
		if out != "" {
			fmt.Fprintln(os.Stderr, out)
		}
		return err
	}
	if verbose {
		fmt.Println(out)
	}
	printSuccess("Snapshot created")
	return nil
}

func runBackupList(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args2 := []string{"kopia", "snapshot", "list", "--json"}
	out, err := backupExec(ctx, args2)
	if err != nil {
		return fmt.Errorf("list snapshots: %w", err)
	}

	if backupOutputJSON {
		fmt.Print(out)
		return nil
	}

	// Pretty-print: id, source, time, size.
	var snapshots []struct {
		ID     string `json:"id"`
		Source struct {
			Path string `json:"path"`
			Host string `json:"host"`
		} `json:"source"`
		StartTime time.Time `json:"startTime"`
		Stats     struct {
			TotalSize int64 `json:"totalSize"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(out), &snapshots); err != nil {
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
			truncate(s.Source.Path, 30),
			s.StartTime.UTC().Format(time.RFC3339),
			humanSize(s.Stats.TotalSize),
		)
	}
	return nil
}

func runBackupRestore(cmd *cobra.Command, args []string) error {
	snapshotID := args[0]
	if backupRestoreTarget == "" {
		return fmt.Errorf("--target is required (use a path on the host that the agent can write to)")
	}
	if err := os.MkdirAll(backupRestoreTarget, 0o700); err != nil {
		return fmt.Errorf("prepare target dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	printInfo("Restoring snapshot %s → %s", snapshotID, backupRestoreTarget)
	_, err := backupExec(ctx, []string{
		"kopia", "snapshot", "restore", snapshotID, backupRestoreTarget,
	})
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	printSuccess("Restore complete")
	printWarning("Verify the restored data before pointing services at it. The restore drill (monthly cron) does this automatically — manual restores do not.")
	return nil
}

func runBackupVerify(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	printInfo("Running kopia repository validate-provider (this may take a while)…")
	out, err := backupExec(ctx, []string{
		"kopia", "repository", "validate-provider",
	})
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
	cmdLine := exec.Command("docker", args2...)
	cmdLine.Stdout = os.Stdout
	cmdLine.Stderr = os.Stderr
	cmdLine.Stdin = os.Stdin
	if err := cmdLine.Run(); err != nil {
		return fmt.Errorf("restic-importer failed: %w (run with --verbose for details)", err)
	}
	if !backupMigrateDryRun {
		printSuccess("Restic snapshots imported into Kopia. Update addons/backup engine: \"restic-import\" → \"kopia\" and re-apply.")
	}
	return nil
}

func runBackupEnroll(cmd *cobra.Command, args []string) error {
	if backupEnrollToken == "" {
		return fmt.Errorf("--token is required")
	}
	// Phase-4 stub. The controller does not exist yet; we error out
	// transparently rather than pretending to enroll.
	printError("backup enroll is not implemented yet (Phase 4 of the rollout)")
	printInfo("Token accepted: %s…", truncate(backupEnrollToken, 12))
	if backupEnrollEndpoint != "" {
		printInfo("Endpoint: %s", backupEnrollEndpoint)
	}
	printInfo("Track progress in docs/plans/2026-05-01-backup-rollout.md (Phase 4).")
	return fmt.Errorf("backup-controller not yet available")
}

// =============================================================================
// HELPERS
// =============================================================================

// backupExec runs a command inside the local Kopia agent container and
// returns its stdout. Stderr is captured and returned in the error on
// failure so the operator sees Kopia's own message.
func backupExec(ctx context.Context, command []string) (string, error) {
	client := docker.NewClient()
	if !client.IsInstalled() {
		return "", fmt.Errorf("docker is not installed on this host — backup CLI requires the local kopia-agent container")
	}
	if !client.IsRunning(ctx) {
		return "", fmt.Errorf("docker daemon is not running")
	}
	if _, err := client.InspectContainer(ctx, backupContainerName); err != nil {
		return "", fmt.Errorf("kopia-agent container %q not found: %w (run 'stackkit addon add backup && stackkit apply')", backupContainerName, err)
	}
	out, err := client.Exec(ctx, backupContainerName, command)
	if err != nil {
		return out, err
	}
	return out, nil
}

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
