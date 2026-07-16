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
//   configure               configure/connect the local Kopia repository
//   status                  show local Kopia repository status
//   run                     force a snapshot of all configured paths
//   list                    list snapshots (table or --json)
//   restore <snapshot>      restore a snapshot to --target (default: tmpfs)
//   verify                  trigger validate-provider ad-hoc
//   migrate-from-restic     drive the one-shot Restic→Kopia importer
//
// Managed fleet enrollment is intentionally implemented in
// backup_managed.go. Public exports omit that file while retaining this
// complete local operator surface.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/spf13/cobra"
)

const (
	// backupContainer is the default name of the local Kopia agent container.
	// Operators can override via --container if they renamed it (rare).
	backupContainer = backupexec.DefaultContainer

	// Aliased from backupexec so CLI command budgets and the shared docker
	// adapter can never drift apart.
	backupLongOperationTimeout  = backupexec.LongOperationTimeout
	backupQuickOperationTimeout = backupexec.QuickOperationTimeout

	defaultBackupRepo = "local:/backup/kopia"
)

var (
	backupContainerName                 string
	backupOutputJSON                    bool
	backupRestoreTarget                 string
	backupConfigureRepo                 string
	backupEmergencyExportTarget         string
	backupEmergencyExportFormat         string
	backupEmergencyExportLargeMediaMode string
	backupEmergencyExportIncludeClasses []string
	backupEmergencyExportSourcePaths    []string
	backupMigrateDryRun                 bool
)

type backupExecutor func(context.Context, []string) (string, error)

var backupExec backupExecutor = dockerBackupExec

// backupEngine wires the shared Kopia primitives to the CLI's executor seam.
// Built per call so tests swapping backupExec keep working.
func backupEngine() backupexec.Engine {
	return backupexec.Engine{Exec: backupexec.Executor(backupExec)}
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
kopia-agent container. The same actions are available in the Kopia Web
UI under https://backups.<domain> — the CLI is here for power users and
scripting.

Examples:
  stackkit backup status
  stackkit backup configure --repo local:/backup/kopia
  stackkit backup run
  stackkit backup list --json
  stackkit backup restore k1234567 --target /tmp/restore
  stackkit backup verify
  stackkit backup migrate-from-restic`,
}

var backupInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Print first-run setup instructions",
	Long: `Print the first-run setup steps for the backup addon.

This command does not modify or provision anything. It describes how to
verify and configure an already materialized local kopia-agent deployment.`,
	RunE: runBackupInit,
}

var backupConfigureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Configure or reconnect the local Kopia repository",
	Long: `Configure or reconnect the local Kopia repository used by the backup addon.

This command intentionally covers the local-first self-hosted path only:
local filesystem repositories mounted into the kopia-agent container.
Object-store onboarding remains an explicit deployment configuration concern.`,
	RunE: runBackupConfigure,
}

var backupStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show local Kopia repository status",
	RunE:  runBackupStatus,
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

var backupEmergencyExportCmd = &cobra.Command{
	Use:   "emergency-export",
	Short: "Write a Kopia-independent emergency export manifest and restore runbook",
	Long: `Write the portable emergency-export metadata layer.

This command intentionally does not call Kopia. It writes a manifest and
restore runbook that describe the minimum state classes, source paths, and
operator steps needed when the primary Kopia client or repository cannot be
used during an incident. Archive byte materialization is handled by the addon
runner/controller path; this CLI path keeps the recovery manifest available
without depending on Docker or Kopia.`,
	RunE: runBackupEmergencyExport,
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

func init() {
	// Shared flag: container name override.
	backupCmd.PersistentFlags().StringVar(&backupContainerName, "container", backupContainer,
		"Name of the local Kopia agent container")

	// Subcommand-specific flags.
	backupConfigureCmd.Flags().StringVar(&backupConfigureRepo, "repo", defaultBackupRepo,
		"Repository to configure; supported shapes: local:/path or filesystem:/path")
	backupStatusCmd.Flags().BoolVar(&backupOutputJSON, "json", false, "Output Kopia repository status as JSON")
	backupListCmd.Flags().BoolVar(&backupOutputJSON, "json", false, "Output snapshots as JSON")
	backupRestoreCmd.Flags().StringVar(&backupRestoreTarget, "target", "/tmp/stackkit-restore",
		"Directory to restore into (default: /tmp/stackkit-restore)")
	backupEmergencyExportCmd.Flags().StringVar(&backupEmergencyExportTarget, "target", "/backup/emergency-export",
		"Directory where the emergency export manifest and runbook are written")
	backupEmergencyExportCmd.Flags().StringVar(&backupEmergencyExportFormat, "format", "tar.zst.age",
		"Planned portable archive format recorded in the manifest")
	backupEmergencyExportCmd.Flags().StringVar(&backupEmergencyExportLargeMediaMode, "large-media-mode", "manifest-only",
		"Large-media handling: manifest-only, include, or exclude")
	backupEmergencyExportCmd.Flags().StringSliceVar(&backupEmergencyExportIncludeClasses, "include-class", defaultEmergencyExportClasses(),
		"State class to include in the emergency export manifest; repeatable")
	backupEmergencyExportCmd.Flags().StringSliceVar(&backupEmergencyExportSourcePaths, "source", defaultEmergencyExportSources(),
		"Source path to record in the emergency export manifest; repeatable")
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
	backupCmd.AddCommand(backupEmergencyExportCmd)
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
	fmt.Println("  1. Confirm this deployment already materializes a kopia-agent container:")
	fmt.Println("       docker ps --filter name=kopia-agent")
	fmt.Println()
	fmt.Println("  2. Configure or connect its local filesystem repository:")
	fmt.Println("       stackkit backup configure --repo local:/backup/kopia")
	fmt.Println()
	fmt.Println("  3. Check repository status and create the first snapshot:")
	fmt.Println("       stackkit backup status")
	fmt.Println("       stackkit backup run")
	fmt.Println()
	printWarning("This command does not install or generate kopia-agent deployment assets.")
	printInfo("Documentation: addons/backup/README.md and docs/CLI.md")
	return nil
}

func runBackupConfigure(cmd *cobra.Command, args []string) error {
	repo, err := parseBackupRepository(backupConfigureRepo)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), backupLongOperationTimeout)
	defer cancel()

	engine := backupEngine()
	out, statusErr := engine.RepositoryStatusJSON(ctx)
	if statusErr == nil && backupStatusConfigured(out) {
		printSuccess("Kopia repository already configured")
		if verbose || backupOutputJSON {
			fmt.Print(out)
			if !strings.HasSuffix(out, "\n") {
				fmt.Println()
			}
		}
		return nil
	}
	if statusErr != nil && !backupOutputLooksNotConfigured(out, statusErr) {
		return fmt.Errorf("check kopia repository status: %w", statusErr)
	}

	printInfo("Configuring Kopia local repository at %s", repo.Path)
	out, err = engine.EnsureFilesystemRepository(ctx, repo.Path)
	if err != nil {
		return err
	}
	if verbose && out != "" {
		fmt.Println(out)
	}
	printSuccess("Kopia repository configured")
	return nil
}

func runBackupStatus(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), backupQuickOperationTimeout)
	defer cancel()

	out, err := backupEngine().RepositoryStatusJSON(ctx)
	if err != nil {
		if backupOutputLooksNotConfigured(out, err) {
			printWarning("Kopia repository is not configured")
			return fmt.Errorf("kopia repository not configured — run 'stackkit backup configure' first")
		}
		return fmt.Errorf("kopia repository status: %w", err)
	}
	if backupOutputJSON {
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
		return nil
	}
	if !backupStatusConfigured(out) {
		printWarning("Kopia repository is not configured")
		return fmt.Errorf("kopia repository not configured — run 'stackkit backup configure' first")
	}
	printSuccess("Kopia repository configured")
	printBackupStatusSummary(out)
	return nil
}

func runBackupRun(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), backupLongOperationTimeout)
	defer cancel()

	out, err := backupEngine().Snapshot(
		ctx,
		backupexec.DefaultVolumeSource,
		fmt.Sprintf("ad-hoc via stackkit backup run @ %s", time.Now().UTC().Format(time.RFC3339)),
	)
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
	snapshotID := args[0]
	if backupRestoreTarget == "" {
		return fmt.Errorf("--target is required (use a path on the host that the agent can write to)")
	}
	if err := os.MkdirAll(backupRestoreTarget, 0o700); err != nil {
		return fmt.Errorf("prepare target dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), backupLongOperationTimeout)
	defer cancel()

	printInfo("Restoring snapshot %s → %s", snapshotID, backupRestoreTarget)
	_, err := backupEngine().Restore(ctx, snapshotID, backupRestoreTarget)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	printSuccess("Restore complete")
	printWarning("Verify the restored data before pointing services at it. The restore drill (monthly cron) does this automatically — manual restores do not.")
	return nil
}

func runBackupVerify(cmd *cobra.Command, args []string) error {
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

func runBackupEmergencyExport(cmd *cobra.Command, args []string) error {
	target := strings.TrimSpace(backupEmergencyExportTarget)
	if target == "" {
		return fmt.Errorf("--target is required")
	}
	format := strings.TrimSpace(backupEmergencyExportFormat)
	if format == "" {
		format = "tar.zst.age"
	}
	switch format {
	case "tar.zst.age", "tar.gz.age", "zip.age":
	default:
		return fmt.Errorf("unsupported emergency export format %q (use tar.zst.age, tar.gz.age, or zip.age)", format)
	}
	largeMediaMode := strings.TrimSpace(backupEmergencyExportLargeMediaMode)
	switch largeMediaMode {
	case "", "manifest-only":
		largeMediaMode = "manifest-only"
	case "include", "exclude":
	default:
		return fmt.Errorf("unsupported --large-media-mode %q (use manifest-only, include, or exclude)", largeMediaMode)
	}

	if len(backupEmergencyExportIncludeClasses) == 0 {
		backupEmergencyExportIncludeClasses = defaultEmergencyExportClasses()
	}
	if len(backupEmergencyExportSourcePaths) == 0 {
		backupEmergencyExportSourcePaths = defaultEmergencyExportSources()
	}

	if err := os.MkdirAll(target, 0o700); err != nil {
		return fmt.Errorf("prepare emergency export target: %w", err)
	}

	manifest := backupEmergencyExportManifest{
		SchemaVersion:          "stackkit.backup-emergency-export/v1",
		CreatedAt:              time.Now().UTC().Format(time.RFC3339),
		Mode:                   "portable-archive",
		Format:                 format,
		ToolDependency:         "none-kopia-independent",
		Target:                 target,
		IncludeClasses:         cleanStringList(backupEmergencyExportIncludeClasses),
		LargeMediaMode:         largeMediaMode,
		Sources:                describeEmergencyExportSources(backupEmergencyExportSourcePaths),
		RestoreRunbook:         "RESTORE.md",
		PrimaryBackupEngine:    "kopia",
		PrimaryFailureFallback: "portable emergency export manifest and encrypted archive lane",
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("render emergency export manifest: %w", err)
	}
	manifestPath := filepath.Join(target, "stackkit-emergency-export-manifest.json")
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write emergency export manifest: %w", err)
	}
	runbookPath := filepath.Join(target, "RESTORE.md")
	if err := os.WriteFile(runbookPath, []byte(renderEmergencyExportRunbook(manifest)), 0o600); err != nil {
		return fmt.Errorf("write emergency export runbook: %w", err)
	}

	printSuccess("Emergency export manifest written: %s", manifestPath)
	printInfo("Restore runbook written: %s", runbookPath)
	if largeMediaMode == "manifest-only" {
		printWarning("Large media is manifest-only; reattach NAS/object-store media according to the runbook.")
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), backupLongOperationTimeout)
	defer cancel()

	cmdLine := exec.CommandContext(ctx, "docker", args2...)
	cmdLine.Stdout = os.Stdout
	cmdLine.Stderr = os.Stderr
	cmdLine.Stdin = os.Stdin
	if err := cmdLine.Run(); err != nil {
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

// =============================================================================
// HELPERS
// =============================================================================

// backupExec runs a command inside the local Kopia agent container via the
// shared backupexec docker adapter — CLI and runtime-action endpoints must
// speak identical argv against the same container. The container name is
// resolved at call time so the --container flag keeps working.
func dockerBackupExec(ctx context.Context, command []string) (string, error) {
	return backupexec.DockerExecutor(backupContainerName)(ctx, command)
}

type backupRepository struct {
	Kind string
	Path string
}

type backupEmergencyExportManifest struct {
	SchemaVersion          string                        `json:"schemaVersion"`
	CreatedAt              string                        `json:"createdAt"`
	Mode                   string                        `json:"mode"`
	Format                 string                        `json:"format"`
	ToolDependency         string                        `json:"toolDependency"`
	Target                 string                        `json:"target"`
	IncludeClasses         []string                      `json:"includeClasses"`
	LargeMediaMode         string                        `json:"largeMediaMode"`
	Sources                []backupEmergencyExportSource `json:"sources"`
	RestoreRunbook         string                        `json:"restoreRunbook"`
	PrimaryBackupEngine    string                        `json:"primaryBackupEngine"`
	PrimaryFailureFallback string                        `json:"primaryFailureFallback"`
}

type backupEmergencyExportSource struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Kind   string `json:"kind,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

func defaultEmergencyExportClasses() []string {
	return []string{"config", "secrets", "platform-state", "database", "documents", "serverless-config"}
}

func defaultEmergencyExportSources() []string {
	return []string{"/opt/stacks", "/var/lib/docker/volumes", "/etc/stackkit", "/opt/stackkit/.stackkit"}
}

func describeEmergencyExportSources(paths []string) []backupEmergencyExportSource {
	cleaned := cleanStringList(paths)
	sources := make([]backupEmergencyExportSource, 0, len(cleaned))
	for _, path := range cleaned {
		src := backupEmergencyExportSource{Path: path}
		info, err := os.Stat(path)
		if err == nil {
			src.Exists = true
			if info.IsDir() {
				src.Kind = "directory"
			} else {
				src.Kind = "file"
				src.Bytes = info.Size()
			}
		}
		sources = append(sources, src)
	}
	return sources
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func renderEmergencyExportRunbook(manifest backupEmergencyExportManifest) string {
	var b strings.Builder
	b.WriteString("# StackKit Emergency Restore Runbook\n\n")
	b.WriteString("Use this path when the primary Kopia client, repository, or operational path is unavailable.\n\n")
	b.WriteString("## Contract\n\n")
	fmt.Fprintf(&b, "- Primary engine: %s\n", manifest.PrimaryBackupEngine)
	fmt.Fprintf(&b, "- Fallback lane: %s\n", manifest.Mode)
	fmt.Fprintf(&b, "- Planned archive format: %s\n", manifest.Format)
	fmt.Fprintf(&b, "- Large media mode: %s\n", manifest.LargeMediaMode)
	b.WriteString("- Tool dependency: none on Kopia for this manifest/runbook layer\n\n")
	b.WriteString("## State Classes\n\n")
	for _, class := range manifest.IncludeClasses {
		fmt.Fprintf(&b, "- %s\n", class)
	}
	b.WriteString("\n## Sources\n\n")
	for _, source := range manifest.Sources {
		status := "missing at manifest time"
		if source.Exists {
			status = source.Kind
		}
		fmt.Fprintf(&b, "- `%s` (%s)\n", source.Path, status)
	}
	b.WriteString("\n## Restore Order\n\n")
	b.WriteString("1. Recreate the StackKit version/channel and deployment intent from the manifest.\n")
	b.WriteString("2. Restore config, secrets, platform state, and serverless config before application data.\n")
	b.WriteString("3. Restore database dumps with the matching consistency hook family.\n")
	b.WriteString("4. Restore documents and user content; reattach large-media stores when largeMediaMode is manifest-only.\n")
	b.WriteString("5. Run `stackkit backup verify` when Kopia is available again, then perform an application-level restore drill.\n")
	return b.String()
}

func parseBackupRepository(raw string) (backupRepository, error) {
	if strings.TrimSpace(raw) == "" {
		raw = defaultBackupRepo
	}
	kind, path, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(path) == "" {
		return backupRepository{}, fmt.Errorf("invalid --repo %q (expected local:/path or filesystem:/path)", raw)
	}
	switch kind {
	case "local", "filesystem":
		return backupRepository{Kind: "filesystem", Path: path}, nil
	default:
		return backupRepository{}, fmt.Errorf("unsupported backup repository %q: configure currently supports local:/path or filesystem:/path; use the backup addon/Web UI for object-store targets", raw)
	}
}

// Classification lives in internal/backupexec so the CLI and the node-local
// runtime-action endpoints share one implementation; these wrappers keep the
// historical names used across this package and its tests.
func backupStatusConfigured(out string) bool {
	return backupexec.StatusConfigured(out)
}

func backupOutputLooksNotConfigured(out string, err error) bool {
	return backupexec.OutputLooksNotConfigured(out, err)
}

func backupOutputLooksRepoExists(out string, err error) bool {
	return backupexec.OutputLooksRepoExists(out, err)
}

func printBackupStatusSummary(out string) {
	var status struct {
		ConfigFile string `json:"configFile"`
		Storage    string `json:"storage"`
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return
	}
	if status.ConfigFile != "" {
		printInfo("Config: %s", status.ConfigFile)
	}
	if status.Storage != "" {
		printInfo("Storage: %s", status.Storage)
	}
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
