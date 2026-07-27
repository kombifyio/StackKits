//go:build !publisher

package commands

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"github.com/spf13/cobra"
)

var (
	publicUpgradeTo                string
	publicUpgradeDryRun            bool
	publicUpgradeJSON              bool
	newUpgradeInspectionRunner     = func() upgradelifecycle.Runner { return upgradelifecycle.ExecRunner{} }
	newCurrentUpgradeInspection    = currentUpgradeInspection
	inspectPublicUpgradeTarget     = inspectVerifiedPublicUpgradeTarget
	preparePublicUpgradeCheckpoint = createPublicUpgradeCheckpoint
	installPublicUpgradeRelease    = func(ctx context.Context, source releaseindex.Source, attestations releaseindex.AttestationVerifier, resolution releaseindex.Resolution, workspace string) (releaseindex.Receipt, error) {
		return (releaseindex.Installer{Source: source, Attestations: attestations}).Install(ctx, resolution, workspace)
	}
	executePublicUpgradeTransaction = runPublicUpgradeTransaction
)

type publicUpgradeCheckpoint struct {
	OperationID             string `json:"operationId"`
	KopiaAnchorID           string `json:"kopiaAnchorId"`
	ExecutorStateSnapshotID string `json:"executorStateSnapshotId"`
}

type releaseCommandResult struct {
	Kit           string                       `json:"kit"`
	Version       string                       `json:"version"`
	Channel       releaseindex.Channel         `json:"channel"`
	Platform      releaseindex.Platform        `json:"platform"`
	Asset         string                       `json:"asset"`
	ArchiveSHA256 string                       `json:"archiveSha256"`
	InstallDir    string                       `json:"installDir,omitempty"`
	Receipt       *releaseindex.Receipt        `json:"receipt,omitempty"`
	Inspection    *upgradelifecycle.Inspection `json:"inspection,omitempty"`
	Checkpoint    *publicUpgradeCheckpoint     `json:"checkpoint,omitempty"`
	Transaction   *publicUpgradeTransaction    `json:"transaction,omitempty"`
	DryRun        bool                         `json:"dryRun"`
	ApplyInvoked  bool                         `json:"applyInvoked"`
}

func newPublicUpgradeCmd(deprecatedAlias bool) *cobra.Command {
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Resolve and install a verified StackKit release",
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
		Long: `Resolve a public StackKit release from GitHub, verify its archive,
SPDX SBOM, GitHub OIDC/Sigstore attestation, and cached trusted root, then
atomically install it under .stackkit/releases/.

With --dry-run it generates the verified target only in a bounded shadow
workspace and reports a canonical plan/artifact diff. Without --dry-run it
first inspects that target, creates a native Kopia snapshot plus an
owner-signed executor-state recovery checkpoint, stages and verifies the
rollback data without activating it, and only then installs and executes the
exact target generate/apply/verify transaction. A failed target transaction
automatically restores the captured prior configuration and executor, then
re-applies and verifies the prior release. The Kopia data stays isolated in
staging until a separately governed live-volume cutover exists.`,
		RunE: runPublicUpgrade,
	}
	if deprecatedAlias {
		command.Deprecated = "use stackkit upgrade"
	}
	command.Flags().StringVar(&publicUpgradeTo, "to", "latest", "Target: latest, vX.Y.Z, or channel:stable|beta|edge.")
	command.Flags().BoolVar(&publicUpgradeDryRun, "dry-run", false, "Verify and inspect target generation in a bounded shadow workspace without applying it.")
	command.Flags().BoolVar(&publicUpgradeJSON, "json", false, "Emit stackkit.command-result/v1 JSON.")
	return command
}

func init() {
	rootCmd.AddCommand(newPublicUpgradeCmd(false))
	kitCmd.AddCommand(newPublicUpgradeCmd(true))
}

func runPublicUpgrade(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	workspace := getWorkDir()
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return fmt.Errorf("load StackKit identity before release resolution: %w", err)
	}
	source := newPublicReleaseSource()
	resolution, err := (releaseindex.Resolver{
		Source: source, Attestations: newPublicAttestationVerifier(),
	}).Resolve(ctx, releaseindex.ResolveRequest{
		Kit: kit, Target: publicUpgradeTo, OS: runtime.GOOS, Arch: runtime.GOARCH,
	})
	if err != nil {
		return fmt.Errorf("resolve public StackKit release: %w", err)
	}
	result := releaseCommandResult{
		Kit: kit, Version: resolution.Asset.Version, Channel: resolution.Asset.Channel,
		Platform: resolution.Asset.Platform, Asset: resolution.Asset.Archive.Name,
		ArchiveSHA256: resolution.Asset.Archive.SHA256, DryRun: publicUpgradeDryRun,
	}
	current, currentErr := newCurrentUpgradeInspection(ctx, workspace, specFile)
	if currentErr != nil {
		return fmt.Errorf("inspect authoritative current generation: %w", currentErr)
	}
	attestations := newPublicAttestationVerifier()
	inspection, inspectErr := inspectPublicUpgradeTarget(
		ctx, source, attestations, newUpgradeInspectionRunner(),
		resolution, workspace, specFile, current,
	)
	if inspectErr != nil {
		return fmt.Errorf("inspect verified StackKit upgrade: %w", inspectErr)
	}
	result.Inspection = &inspection
	if !publicUpgradeDryRun {
		checkpoint, checkpointErr := preparePublicUpgradeCheckpoint(ctx, workspace, kit, resolution)
		if checkpointErr != nil {
			return fmt.Errorf("create pre-upgrade rollback checkpoint: %w", checkpointErr)
		}
		if checkpointErr := checkpoint.validate(); checkpointErr != nil {
			return fmt.Errorf("create pre-upgrade rollback checkpoint: %w", checkpointErr)
		}
		result.Checkpoint = &checkpoint
		receipt, installErr := installPublicUpgradeRelease(ctx, source, attestations, resolution, workspace)
		if installErr != nil {
			return fmt.Errorf("install verified StackKit release: %w", installErr)
		}
		result.InstallDir = receipt.InstallDir
		result.Receipt = &receipt
		transaction, transactionErr := executePublicUpgradeTransaction(
			ctx, workspace, specFile, receipt, inspection, checkpoint,
		)
		result.Transaction = &transaction
		result.ApplyInvoked = transaction.Target.ApplyInvoked
		if transactionErr != nil {
			if publicUpgradeJSON {
				if writeErr := writeCommandResultStatus(cmd, cmd.CommandPath(), "failed", result); writeErr != nil {
					return errors.Join(transactionErr, writeErr)
				}
			} else {
				_, _ = fmt.Fprintf(
					cmd.OutOrStdout(),
					"Upgrade failed during %s; rollback status: %s\n",
					transaction.FailedPhase, transaction.Rollback.Status,
				)
			}
			return transactionErr
		}
	}
	if publicUpgradeJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	if publicUpgradeDryRun {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Inspected verified %s %s (%s, %s/%s)\nAsset: %s\nSHA-256: %s\nPlan changed: %t\nArtifacts: %d\nApply: not invoked\n",
			result.Kit, result.Version, result.Channel, result.Platform.OS, result.Platform.Arch,
			result.Asset, result.ArchiveSHA256, result.Inspection.Plan.Changed, len(result.Inspection.Artifacts))
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Upgraded and verified %s %s from %s\nRollback checkpoint: %s (Kopia %s)\nApply: completed\n",
		result.Kit, result.Version, result.InstallDir,
		result.Checkpoint.ExecutorStateSnapshotID, result.Checkpoint.KopiaAnchorID)
	return err
}

func (checkpoint publicUpgradeCheckpoint) validate() error {
	switch {
	case !publicUpgradeOperationIDPattern.MatchString(checkpoint.OperationID):
		return fmt.Errorf("operation ID must be canonical")
	case !nativeV2BackupDigestPattern.MatchString(checkpoint.KopiaAnchorID):
		return fmt.Errorf("Kopia anchor ID must be a canonical sha256 digest")
	case !nativeV2BackupDigestPattern.MatchString(checkpoint.ExecutorStateSnapshotID):
		return fmt.Errorf("executor-state snapshot ID must be a canonical sha256 digest")
	default:
		return nil
	}
}

func inspectVerifiedPublicUpgradeTarget(
	ctx context.Context,
	source releaseindex.Source,
	attestations releaseindex.AttestationVerifier,
	runner upgradelifecycle.Runner,
	resolution releaseindex.Resolution,
	workspace string,
	requestedSpec string,
	current generationartifact.PlanInspection,
) (upgradelifecycle.Inspection, error) {
	return (upgradelifecycle.Inspector{
		Source: source, Attestations: attestations, Runner: runner,
	}).Inspect(ctx, resolution, workspace, requestedSpec, current)
}

func currentUpgradeInspection(ctx context.Context, workspace, requestedSpec string) (generationartifact.PlanInspection, error) {
	var inspection generationartifact.PlanInspection
	options := architectureV2ExecutionCLIOptions{
		context: ctx,
		inspectionSink: func(value generationartifact.PlanInspection) error {
			inspection = value
			return nil
		},
	}
	handled, err := newArchitectureV2ExecutionGate().preflight(workspace, requestedSpec, architectureV2Plan, options)
	if err != nil {
		return generationartifact.PlanInspection{}, err
	}
	if !handled {
		return generationartifact.PlanInspection{}, fmt.Errorf("upgrade inspection requires the authoritative Architecture v2 generation closure")
	}
	return inspection, nil
}
