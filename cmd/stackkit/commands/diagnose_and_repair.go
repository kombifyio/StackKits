package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/diagnose"
	"github.com/spf13/cobra"
)

func newDiagnoseCommand() *cobra.Command {
	var (
		outputJSON    bool
		writeSnapshot bool
		snapshotPath  string
	)
	cmd := &cobra.Command{
		Use:         "diagnose",
		Short:       "Observe host health and emit a redacted, secret-free diagnostic snapshot (ADR-0051)",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			wd := getWorkDir()
			now := time.Now().UTC()
			_ = diagnose.EnsureClockAnchor(wd, now)
			clockSkew := diagnose.DetectHostClockSkew(wd, now)
			snap, err := diagnose.CollectHostSnapshot(wd, clockSkew, now)
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			writtenPath := ""
			if writeSnapshot {
				target := strings.TrimSpace(snapshotPath)
				if target == "" {
					target = filepath.Join(wd, filepath.FromSlash(diagnose.DefaultSnapshotRelPath))
				}
				if err := diagnose.WriteSnapshot(target, snap); err != nil {
					return machineAwareCommandError(cmd, fmt.Errorf("write redacted observation snapshot: %w", err))
				}
				writtenPath = target
			}
			if outputJSON {
				payload := map[string]any{
					"schemaVersion": snap.SchemaVersion,
					"generatedAt":   snap.GeneratedAt,
					"workspaceHash": snap.WorkspaceHash,
					"coreHealth":    snap.CoreHealth,
					"appHealth":     snap.AppHealth,
					"extHealth":     snap.ExtHealth,
					"liveDrift":     snap.LiveDrift,
					"tlsStatus":     snap.TLSStatus,
					"diskRamClock":  snap.DiskRAMClock,
					"disk_ram_clock": map[string]any{
						"disk": map[string]any{
							"freeBytes":      snap.DiskRAMClock.DiskFreeBytes,
							"totalBytes":     snap.DiskRAMClock.DiskTotalBytes,
							"safeFloorBytes": snap.DiskRAMClock.DiskSafeFloor,
							"belowFloor":     snap.DiskRAMClock.DiskBelowFloor,
						},
						"clock": map[string]any{
							"skewSeconds":  snap.DiskRAMClock.ClockSkewSec,
							"skewExceeded": snap.DiskRAMClock.ClockSkewBad,
						},
					},
					"backupAge":       snap.BackupAge,
					"signatures":      snap.Signatures,
					"blastRadius":     snap.BlastRadius,
					"hardStopReasons": snap.HardStopReasons,
				}
				if writtenPath != "" {
					payload["snapshotPath"] = writtenPath
				}
				return writeCommandResult(cmd, "diagnose", payload)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Core: %s | BlastRadius: %s | HardStops: %d | Signatures: %d\n",
				snap.CoreHealth.Status, snap.BlastRadius, len(snap.HardStopReasons), len(snap.Signatures))
			for _, sig := range snap.Signatures {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  - [%s] (%s, hardStop=%v): %s\n", sig.ID, sig.BlastRadius, sig.HardStop, sig.Detail)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Emit the secret-free observation snapshot as JSON")
	cmd.Flags().BoolVar(&writeSnapshot, "write-snapshot", false, "Atomically write the redacted observation snapshot for stackkit-server read-only mount")
	cmd.Flags().StringVar(&snapshotPath, "snapshot-path", "", "Override snapshot output path (default .stackkit/runtime/observation-snapshot.json)")
	return cmd
}

func newRepairCommand() *cobra.Command {
	var (
		target      string
		approvalID  string
		signatureID string
		outputJSON  bool
	)
	cmd := &cobra.Command{
		Use:         "repair <action>",
		Short:       "Run an effect-classified host repair under ADR-0051 authorization and circuit breaker",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			wd := getWorkDir()
			action := diagnose.RepairAction(strings.TrimSpace(args[0]))
			policy, err := diagnose.LoadRepairPolicy(wd)
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			decision, authErr := diagnose.EvaluateRepairAuthorization(action, policy, strings.TrimSpace(approvalID) != "")
			if authErr != nil {
				return machineAwareCommandError(cmd, authErr)
			}
			if decision.AllowedWithoutApprove {
				sig := strings.TrimSpace(signatureID)
				if sig == "" {
					sig = string(action)
				}
				if _, cbErr := diagnose.OpenCircuitBreaker(wd).CheckAndRecord(sig); cbErr != nil {
					return machineAwareCommandError(cmd, cbErr)
				}
			}
			status := "admitted"
			affected := []string{}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if action == diagnose.RepairReconcileCoolifyProxy {
				stopped, recErr := diagnose.ReconcileCoolifyProxyLive(ctx)
				if recErr != nil {
					return machineAwareCommandError(cmd, recErr)
				}
				affected = stopped
				status = "executed"
			}
			out := map[string]any{
				"schemaVersion": "stackkit.repair-result/v1",
				"action":        action,
				"target":        strings.TrimSpace(target),
				"decision":      decision,
				"status":        status,
				"affected":      affected,
			}
			if outputJSON {
				return writeCommandResult(cmd, "repair", out)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "repair %s %s (%s)\n", action, status, decision.Reason)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "Optional service or workload target")
	cmd.Flags().StringVar(&approvalID, "approval-id", "", "Verified owner approval ID for D-2026-11-20-06 guarded actions")
	cmd.Flags().StringVar(&signatureID, "signature", "", "Failure signature ID for circuit-breaker accounting")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Emit repair decision as JSON")
	return cmd
}

func newHostExecCommand() *cobra.Command {
	var (
		once       bool
		outputJSON bool
	)
	cmd := &cobra.Command{
		Use:         "host-exec",
		Short:       "Consume queued ADR-0051 MCP spool jobs on the host",
		Hidden:      true,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = once
			wd := getWorkDir()
			now := time.Now().UTC()
			snap, err := diagnose.CollectHostSnapshot(wd, 0, now)
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			receipts, err := diagnose.ProcessSpoolOnce(wd, snap, now, nil)
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			if outputJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(receipts)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "processed %d spool job(s)\n", len(receipts))
			return nil
		},
	}
	cmd.Flags().BoolVar(&once, "once", true, "Process one spool batch and exit")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Emit receipts as JSON")
	return cmd
}

func newWatchdogCommand() *cobra.Command {
	var (
		once             bool
		offline          bool
		localLANFallback string
		outputJSON       bool
	)
	cmd := &cobra.Command{
		Use:         "watchdog",
		Short:       "Run one host-side ADR-0051 watchdog observation and snapshot refresh",
		Hidden:      true,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = once
			wd := getWorkDir()
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			report, err := diagnose.RunWatchdogCycle(ctx, diagnose.WatchdogRunOptions{
				Workspace:        wd,
				Offline:          offline,
				LocalLANFallback: localLANFallback,
				Now:              time.Now().UTC(),
			})
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			if outputJSON {
				return writeCommandResult(cmd, "watchdog", report)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "watchdog snapshot written to %s (stopped=%v, stopReason=%s, dns=%s, repairs=%d)\n",
				report.SnapshotPath, report.Stopped, report.StopReason, report.DNS.Status, len(report.Repairs))
			return nil
		},
	}
	cmd.Flags().BoolVar(&once, "once", true, "Execute one watchdog cycle and exit")
	cmd.Flags().BoolVar(&offline, "offline", false, "Enforce Offline/Local-Only DNS resilience (never use public DNS fallback)")
	cmd.Flags().StringVar(&localLANFallback, "local-lan-dns", "", "Optional local LAN fallback resolver IP for offline mode")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Emit watchdog run outcome as JSON")
	return cmd
}

func init() {
	rootCmd.AddCommand(newDiagnoseCommand())
	rootCmd.AddCommand(newRepairCommand())
	rootCmd.AddCommand(newHostExecCommand())
	rootCmd.AddCommand(newWatchdogCommand())
}
