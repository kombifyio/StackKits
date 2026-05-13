package commands

// kit_upgrade_rollback.go implements `stackkit kit upgrade rollback`
// (kit-update-phase-1, ADR-0018 §3 rollback path).
//
// Rollback uses the manifest written by the upgrade's atomic-snapshot:
//
//   .stackkit/snapshots/<ts>-<old-kit>/
//     ├── manifest.yaml         # KopiaSnapshots, TofuStatePath, ChannelMap
//     ├── state.tfstate         # the pre-apply tofu state copy
//     └── (kopia snapshots live in the configured Kopia repo, not here)
//
// Sequence:
//   1. Resolve --to-snapshot (or fall back to state.LastSnapshotDir).
//   2. snapshot.Verify checks manifest + tfstate file presence.
//   3. Confirm-gate (skipped under --auto-approve).
//   4. Copy <snapshot>/state.tfstate back to deploy/terraform.tfstate.
//   5. Kopia restore each manifest.KopiaSnapshots[i] back to its volume
//      path (skipped under --skip-volume-restore).
//   6. Update .stackkit/state.yaml: KitVersionID/KitSemver/KitChannel
//      reset to the manifest's old values; status='rolled-back'.
//
// Known Phase-1 limitations (documented in docs/runbooks/kit-rollback.md):
//   - Templates in deploy/*.tf are not snapshotted. If the operator ran
//     `stackkit generate` for the new kit-version before the upgrade,
//     rollback's tfstate will not match the on-disk templates. The
//     runbook tells the operator to re-checkout/regenerate the old
//     kit-version's templates before the next `stackkit apply`.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/snapshot"
	"github.com/kombifyio/stackkits/pkg/models"
)

var (
	rollbackToSnapshot       string
	rollbackAutoApprove      bool
	rollbackSkipVolRestore   bool
	rollbackKopiaRestoreOnly bool
)

var kitUpgradeRollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Rollback to a previous atomic-snapshot",
	Long: `Restore the deployment to the state captured by an atomic-snapshot
written by an earlier 'stackkit kit upgrade'.

Default behavior:
  - Restore deploy/terraform.tfstate from the snapshot.
  - Restore each persistent volume from its Kopia snapshot.
  - Update .stackkit/state.yaml to reflect the rolled-back kit-version.

If you ran 'stackkit generate' for the new kit-version before the upgrade,
your deploy/*.tf templates may not match the restored tfstate — see
docs/runbooks/kit-rollback.md for the recovery procedure.

Examples:
  stackkit kit upgrade rollback --to-snapshot=20260508T120000Z-1.0.0
  stackkit kit upgrade rollback --auto-approve --skip-volume-restore
`,
	RunE: runKitUpgradeRollback,
}

func init() {
	kitUpgradeRollbackCmd.Flags().StringVar(&rollbackToSnapshot, "to-snapshot", "",
		"Snapshot directory name (under .stackkit/snapshots/). Defaults to LastSnapshotDir from state.yaml.")
	kitUpgradeRollbackCmd.Flags().BoolVar(&rollbackAutoApprove, "auto-approve", false,
		"Skip the interactive confirm gate.")
	kitUpgradeRollbackCmd.Flags().BoolVar(&rollbackSkipVolRestore, "skip-volume-restore", false,
		"Restore tfstate but NOT kopia volumes. Use when volumes are healthy and only the tofu state needs reverting.")
	kitUpgradeRollbackCmd.Flags().BoolVar(&rollbackKopiaRestoreOnly, "kopia-restore-only", false,
		"Restore kopia volumes only — leave tfstate untouched.")

	kitUpgradeCmd.AddCommand(kitUpgradeRollbackCmd)
}

func runKitUpgradeRollback(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	wd := getWorkDir()
	loader := config.NewLoader(wd)

	// Load current state — needed to fall back on LastSnapshotDir and to
	// rewrite state.yaml after rollback succeeds.
	stateFile := filepath.Join(wd, ".stackkit", "state.yaml")
	state, err := loader.LoadDeploymentState(stateFile)
	if err != nil || state == nil {
		return fmt.Errorf("no deployment state at %s — rollback only works on an applied stack", stateFile)
	}

	dir, err := resolveSnapshotDir(wd, rollbackToSnapshot, state.LastSnapshotDir)
	if err != nil {
		return err
	}

	if err := snapshot.Verify(dir); err != nil {
		return fmt.Errorf("snapshot %s is not usable: %w", dir, err)
	}

	manifest, err := snapshot.LoadManifest(dir)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}

	printPlanRollback(manifest, rollbackSkipVolRestore, rollbackKopiaRestoreOnly)

	if !rollbackAutoApprove {
		ok, err := confirm(cmd, "Apply this rollback? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("aborted by operator")
		}
	}

	// Step 4 — restore tfstate (unless --kopia-restore-only)
	if !rollbackKopiaRestoreOnly {
		dst := filepath.Join(wd, "deploy", "terraform.tfstate")
		if err := restoreTfstate(manifest.TofuStatePath, dst); err != nil {
			return fmt.Errorf("restore tfstate: %w", err)
		}
		printSuccess("tfstate restored from %s", manifest.TofuStatePath)
	}

	// Step 5 — kopia restore (unless --skip-volume-restore)
	if !rollbackSkipVolRestore && len(manifest.KopiaSnapshots) > 0 {
		kopia := snapshot.NewKopia()
		status, err := kopia.Status(ctx)
		if err != nil {
			return fmt.Errorf("kopia status: %w", err)
		}
		if !status.Configured {
			printError("kopia repository not configured — cannot restore volumes")
			printInfo("re-run with --skip-volume-restore if you want tfstate-only rollback")
			return snapshot.ErrKopiaNotConfigured
		}
		for _, ref := range manifest.KopiaSnapshots {
			printInfo("restoring %s from kopia snapshot %s ...", ref.Path, shortHash(ref.SnapshotID))
			if err := kopia.SnapshotRestore(ctx, ref.SnapshotID, ref.Path); err != nil {
				return fmt.Errorf("kopia restore %s: %w", ref.Path, err)
			}
		}
		printSuccess("%d volume(s) restored from kopia", len(manifest.KopiaSnapshots))
	} else if rollbackSkipVolRestore && len(manifest.KopiaSnapshots) > 0 {
		printWarning("skipped restore of %d kopia snapshot(s) — operator must restore volumes manually if needed",
			len(manifest.KopiaSnapshots))
	}

	// Step 6 — update state.yaml so 'stackkit doctor' / 'stackkit kit list'
	// reflect the rollback.
	state.KitSemver = manifest.OldKitVersion
	state.KitChannel = ""   // rollback erases channel pin; next apply re-pins
	state.KitVersionID = "" // ditto — operator must re-resolve
	state.LastApplied = time.Now()
	state.Status = models.StatusDegraded // rolled-back stacks are degraded until re-applied
	state.LastSnapshotDir = dir
	if err := writeDeploymentState(stateFile, state); err != nil {
		printWarning("failed to update state.yaml: %v", err)
	}

	printSuccess("rolled back to %s (snapshot %s)", manifest.OldKitVersion, filepath.Base(dir))
	printInfo("templates in deploy/ may still reflect the new kit-version — see docs/runbooks/kit-rollback.md")
	return nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// resolveSnapshotDir picks the snapshot directory the operator wants
// to roll back to. Resolution order:
//
//  1. Explicit --to-snapshot (treated as path or as basename).
//  2. state.LastSnapshotDir (set by the most recent successful upgrade).
//  3. Latest entry under .stackkit/snapshots/ alphabetically (timestamps
//     sort lexically, so newest is last).
func resolveSnapshotDir(wd, flag, lastFromState string) (string, error) {
	snapshotsRoot := filepath.Join(wd, ".stackkit", "snapshots")

	if flag != "" {
		// Allow either a basename ("20260508T120000Z-1.0.0") or a full path.
		if filepath.IsAbs(flag) {
			return flag, nil
		}
		if _, err := os.Stat(flag); err == nil {
			abs, _ := filepath.Abs(flag)
			return abs, nil
		}
		return filepath.Join(snapshotsRoot, flag), nil
	}

	if lastFromState != "" {
		// state.yaml may store either an absolute path or a basename;
		// normalize both shapes.
		if filepath.IsAbs(lastFromState) {
			return lastFromState, nil
		}
		return filepath.Join(snapshotsRoot, lastFromState), nil
	}

	// Last resort: pick the newest entry under .stackkit/snapshots.
	entries, err := os.ReadDir(snapshotsRoot)
	if err != nil {
		return "", fmt.Errorf("no --to-snapshot, no LastSnapshotDir, and %s is unreadable: %w", snapshotsRoot, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no snapshots in %s", snapshotsRoot)
	}
	sort.Strings(names)
	return filepath.Join(snapshotsRoot, names[len(names)-1]), nil
}

// restoreTfstate copies the snapshot tfstate file back to the live
// deploy/terraform.tfstate location, atomically.
func restoreTfstate(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("snapshot tfstate %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	return atomicCopy(src, dst)
}

// atomicCopy writes src to dst.tmp, fsyncs, and renames over dst. We do
// not just os.Rename(src, dst) because src lives in the snapshot
// directory and is used as the rollback anchor for the next operation
// — leave it intact.
func atomicCopy(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- operator-supplied snapshot path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp := dst + ".rollback.tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- destination derives from operator working dir
	if err != nil {
		return err
	}
	outClosed := false
	defer func() {
		if !outClosed {
			_ = out.Close()
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	outClosed = true
	return os.Rename(tmp, dst)
}

// printPlanRollback shows the rollback plan summary so the operator
// knows what's about to happen before the confirm-gate.
func printPlanRollback(m *snapshot.SnapshotManifest, skipVolumes, kopiaOnly bool) {
	fmt.Println()
	fmt.Println(bold("Rollback plan"))
	fmt.Printf("  snapshot timestamp:    %s\n", m.Timestamp.Format(time.RFC3339))
	fmt.Printf("  rolling back to:       %s\n", m.OldKitVersion)
	fmt.Printf("  was upgrading from →:  %s\n", m.NewKitVersion)
	if !kopiaOnly {
		fmt.Printf("  tfstate restore:       %s → deploy/terraform.tfstate\n", m.TofuStatePath)
	} else {
		fmt.Printf("  tfstate restore:       SKIPPED (--kopia-restore-only)\n")
	}
	switch {
	case skipVolumes:
		fmt.Printf("  kopia restores:        SKIPPED (--skip-volume-restore)\n")
	case len(m.KopiaSnapshots) == 0:
		fmt.Printf("  kopia restores:        none recorded in manifest\n")
	default:
		fmt.Printf("  kopia restores:        %d volume(s)\n", len(m.KopiaSnapshots))
		for _, r := range m.KopiaSnapshots {
			fmt.Printf("    - %s ← %s\n", r.Path, shortHash(r.SnapshotID))
		}
	}
	if len(m.ChannelMap) > 0 {
		fallback, override := 0, 0
		for _, e := range m.ChannelMap {
			if e.Reason == "fallback" {
				fallback++
			} else if e.Reason == "override" {
				override++
			}
		}
		fmt.Printf("  resolver decisions:    %d module(s), %d fallback, %d override\n",
			len(m.ChannelMap), fallback, override)
	}
	fmt.Println()
	if strings.TrimSpace(m.OldKitVersion) == "" {
		fmt.Println(yellow("  warning: manifest has no oldKitVersion — operator will need to re-resolve"))
		fmt.Println()
	}
}
