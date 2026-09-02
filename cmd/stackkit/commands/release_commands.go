package commands

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"github.com/spf13/cobra"
)

var (
	publicUpgradeTo                string
	publicUpgradeDryRun            bool
	publicUpgradeJSON              bool
	publicUpgradeRecover           string
	newUpgradeInspectionRunner     = func() upgradelifecycle.Runner { return upgradelifecycle.ExecRunner{} }
	newCurrentUpgradeInspection    = currentUpgradeInspection
	inspectPublishedV012Current    = inspectPublishedV012CurrentGeneration
	inspectPublishedV011Current    = inspectPublishedV011CurrentGeneration
	inspectPublishedV010Current    = inspectPublishedV010CurrentGeneration
	inspectPublishedV09Current     = inspectPublishedV09CurrentGeneration
	inspectPublishedV08Current     = inspectPublishedV08CurrentGeneration
	inspectPublicUpgradeTarget     = inspectVerifiedPublicUpgradeTarget
	inspectPublicUpgradeBridge     = inspectExactBeta4UpgradeBridge
	preparePublicUpgradeCheckpoint = createPublicUpgradeCheckpoint
	installPublicUpgradeRelease    = func(ctx context.Context, source releaseindex.Source, attestations releaseindex.AttestationVerifier, resolution releaseindex.Resolution, workspace string) (releaseindex.Receipt, error) {
		return (releaseindex.Installer{Source: source, Attestations: attestations}).Install(ctx, resolution, workspace)
	}
	executePublicUpgradeTransaction = runPublicUpgradeTransaction
	beginPublicUpgradeMutation      = func(
		workspace string,
		prepare func() (lifecyclemutation.BeginRequest, error),
	) (publicUpgradeLifecycleSession, error) {
		return lifecyclemutation.BeginUpgradePrepared(workspace, prepare)
	}
)

type publicUpgradeCheckpoint struct {
	OperationID              string                          `json:"operationId"`
	KopiaAnchorID            string                          `json:"kopiaAnchorId"`
	ExecutorStateSnapshotID  string                          `json:"executorStateSnapshotId"`
	ApplicationLifecyclePlan generationartifact.VerifiedPlan `json:"-"`
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
can restore and verify the prior executor only before target Apply is admitted.
After Apply, prior-runtime restart is blocked until verified prior-data
activation exists; isolated Kopia staging alone does not authorize it.
A completed target commit keeps its success proof for explicit finalization.
Fresh upgrades require support in the embedded CUE Kit policy; recovery of an
existing operation follows its signed journal and checkpoint.`,
		RunE: runPublicUpgrade,
	}
	if deprecatedAlias {
		command.Deprecated = "use stackkit upgrade"
	}
	command.Flags().StringVar(&publicUpgradeTo, "to", "latest", "Target: latest, vX.Y.Z, or channel:stable|beta|edge.")
	command.Flags().BoolVar(&publicUpgradeDryRun, "dry-run", false, "Verify and inspect target generation in a bounded shadow workspace without applying it.")
	command.Flags().BoolVar(&publicUpgradeJSON, "json", false, "Emit stackkit.command-result/v1 JSON.")
	command.Flags().StringVar(
		&publicUpgradeRecover, "recover", "",
		"Explicitly recover one exact interrupted lifecycle operation ID.",
	)
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
	if strings.TrimSpace(publicUpgradeRecover) != "" {
		if publicUpgradeDryRun || cmd.Flags().Changed("to") {
			return errors.New("--recover cannot be combined with --dry-run or --to")
		}
		recovery, recoverErr := recoverPublicUpgradeOperation(
			ctx, workspace, specFile, strings.TrimSpace(publicUpgradeRecover),
		)
		if publicUpgradeJSON {
			if writeErr := writeCommandResultStatus(
				cmd, cmd.CommandPath(),
				map[bool]string{true: "success", false: "failed"}[recoverErr == nil],
				recovery,
			); writeErr != nil {
				return errors.Join(recoverErr, writeErr)
			}
		} else if recoverErr == nil {
			_, _ = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Recovered lifecycle operation %s: %s\n",
				recovery.OperationID, recovery.Rollback.Status,
			)
		}
		return recoverErr
	}
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return fmt.Errorf("load StackKit identity before upgrade admission: %w", err)
	}
	if err := admitPublicUpgradeKit(kit); err != nil {
		return err
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
	bridge, bridgeErr := inspectPublicUpgradeBridge(
		ctx, workspace, specFile, kit, resolution,
	)
	if bridgeErr != nil {
		return fmt.Errorf("inspect cross-release upgrade authority: %w", bridgeErr)
	}
	if bridge.Enabled && publicUpgradeDryRun {
		return errors.New(
			"the exact v0.8.0-beta.4 to v0.8.0 authority bridge requires a live " +
				"checkpointed upgrade; --dry-run is denied before side effects",
		)
	}
	current := bridge.Current
	if !bridge.Enabled {
		var currentErr error
		var compatible bool
		current, compatible, currentErr = inspectPublishedV012Current(
			ctx, workspace, specFile, kit, resolution,
		)
		if currentErr != nil {
			return fmt.Errorf("inspect published v0.12 current generation: %w", currentErr)
		}
		if !compatible {
			current, compatible, currentErr = inspectPublishedV011Current(
				ctx, workspace, specFile, kit, resolution,
			)
		}
		if currentErr != nil {
			return fmt.Errorf("inspect published v0.11 current generation: %w", currentErr)
		}
		if !compatible {
			current, compatible, currentErr = inspectPublishedV010Current(
				ctx, workspace, specFile, kit, resolution,
			)
			if currentErr != nil {
				return fmt.Errorf("inspect published v0.10 current generation: %w", currentErr)
			}
		}
		if !compatible {
			current, compatible, currentErr = inspectPublishedV09Current(
				ctx, workspace, specFile, kit, resolution,
			)
			if currentErr != nil {
				return fmt.Errorf("inspect published v0.9 current generation: %w", currentErr)
			}
		}
		if !compatible {
			current, compatible, currentErr = inspectPublishedV08Current(
				ctx, workspace, specFile, kit, resolution,
			)
			if currentErr != nil {
				return fmt.Errorf("inspect published v0.8 current generation: %w", currentErr)
			}
		}
		if !compatible {
			current, currentErr = newCurrentUpgradeInspection(ctx, workspace, specFile)
			if currentErr != nil {
				return fmt.Errorf("inspect authoritative current generation: %w", currentErr)
			}
		}
	}
	attestations := newPublicAttestationVerifier()
	var inspection upgradelifecycle.Inspection
	if !bridge.Enabled {
		var inspectErr error
		inspection, inspectErr = inspectPublicUpgradeTarget(
			ctx, source, attestations, newUpgradeInspectionRunner(),
			resolution, workspace, specFile, current,
		)
		if inspectErr != nil {
			return fmt.Errorf("inspect verified StackKit upgrade: %w", inspectErr)
		}
		result.Inspection = &inspection
	}
	if !publicUpgradeDryRun {
		var checkpoint publicUpgradeCheckpoint
		var receipt releaseindex.Receipt
		mutation, mutationErr := beginPublicUpgradeMutation(
			workspace,
			func() (lifecyclemutation.BeginRequest, error) {
				prepared, checkpointErr := preparePublicUpgradeCheckpoint(
					ctx, workspace, kit, resolution,
				)
				if checkpointErr != nil {
					return lifecyclemutation.BeginRequest{}, fmt.Errorf(
						"create pre-upgrade rollback checkpoint: %w", checkpointErr,
					)
				}
				if bridge.Enabled {
					inspection, checkpointErr = inspectPublicUpgradeTarget(
						ctx, source, attestations, newUpgradeInspectionRunner(),
						resolution, workspace, specFile, current,
					)
					if checkpointErr != nil {
						return lifecyclemutation.BeginRequest{}, fmt.Errorf(
							"inspect verified target after beta.4 rollback checkpoint: %w",
							checkpointErr,
						)
					}
					result.Inspection = &inspection
				}
				if checkpointErr := prepared.validate(); checkpointErr != nil {
					return lifecyclemutation.BeginRequest{}, checkpointErr
				}
				installed, installErr := installPublicUpgradeRelease(
					ctx, source, attestations, resolution, workspace,
				)
				if installErr != nil {
					return lifecyclemutation.BeginRequest{}, fmt.Errorf(
						"install verified StackKit release: %w", installErr,
					)
				}
				snapshot, loadErr := loadPublicUpgradeRecoveryCheckpoint(
					workspace, prepared.ExecutorStateSnapshotID,
				)
				if loadErr != nil {
					return lifecyclemutation.BeginRequest{}, fmt.Errorf(
						"load exact upgrade recovery checkpoint: %w", loadErr,
					)
				}
				var targetExecutableDigest string
				if executableErr := withPublicUpgradeInstalledExecutable(
					ctx, installed, func(path string) error {
						var digestErr error
						targetExecutableDigest, digestErr = executableFileSHA256(path)
						return digestErr
					},
				); executableErr != nil {
					return lifecyclemutation.BeginRequest{}, fmt.Errorf(
						"bind exact target lifecycle executable: %w", executableErr,
					)
				}
				checkpoint = prepared
				receipt = installed
				return lifecyclemutation.BeginRequest{
					OperationID: prepared.OperationID,
					OwnerRef:    snapshot.OwnerRef,
					Checkpoint: lifecyclemutation.CheckpointAuthority{
						ExecutorStateSnapshotID: prepared.ExecutorStateSnapshotID,
						KopiaAnchorID:           prepared.KopiaAnchorID,
					},
					Target: lifecyclemutation.ReleaseAuthority{
						Version:          architectureV2ComponentVersion(installed.Version),
						ArchiveSHA256:    "sha256:" + installed.ArchiveSHA256,
						ExecutableSHA256: targetExecutableDigest,
					},
					Prior: lifecyclemutation.ReleaseAuthority{
						Version:          architectureV2ComponentVersion(snapshot.Release.Version),
						ArchiveSHA256:    snapshot.Release.ArchiveSHA256,
						ExecutableSHA256: snapshot.Executable.Blob.SHA256,
					},
				}, nil
			},
		)
		if mutationErr != nil {
			return fmt.Errorf("begin exclusive lifecycle mutation: %w", mutationErr)
		}
		defer mutation.Close()
		var applicationLifecycleRuns []architectureV2ApplicationLifecycleRun
		if len(checkpoint.ApplicationLifecyclePlan.Canonical()) != 0 {
			applicationLifecycleRuns, mutationErr = beginArchitectureV2ApplicationLifecycles(
				workspace,
				checkpoint.ApplicationLifecyclePlan,
				"upgrade",
				"stackkit.upgrade",
				"",
				time.Now().UTC(),
			)
			if mutationErr != nil {
				return fmt.Errorf("begin application upgrade lifecycle: %w", mutationErr)
			}
		}
		result.Checkpoint = &checkpoint
		result.InstallDir = receipt.InstallDir
		result.Receipt = &receipt
		transaction, transactionErr := executePublicUpgradeTransaction(
			ctx, workspace, specFile, receipt, inspection, checkpoint, mutation,
		)
		result.Transaction = &transaction
		result.ApplyInvoked = transaction.Target.ApplyInvoked
		if len(applicationLifecycleRuns) != 0 {
			resultRef, resultDigest, persistErr := persistPublicUpgradeApplicationLifecycleResult(
				workspace, transaction,
			)
			if persistErr != nil {
				transactionErr = requireArchitectureV2ApplicationLifecycleRecovery(
					workspace,
					applicationLifecycleRuns,
					"upgrade transaction completed without durable application owner evidence",
					"urn:stackkit:upgrade-operation:"+checkpoint.OperationID,
					time.Now().UTC(),
					errors.Join(transactionErr, persistErr),
				)
			} else {
				snapshotRef, snapshotErr := backuplifecycle.SnapshotAnchorEvidenceRef(
					checkpoint.KopiaAnchorID,
				)
				evidence := []applicationlifecycle.Evidence{
					{
						Kind: "snapshot-anchor", Ref: snapshotRef,
						Digest: checkpoint.KopiaAnchorID,
					},
					{Kind: "upgrade-result", Ref: resultRef, Digest: resultDigest},
					{
						Kind: "owner-observation", Ref: resultRef + "#success-proof",
						Digest: resultDigest,
					},
				}
				if snapshotErr != nil {
					transactionErr = requireArchitectureV2ApplicationLifecycleRecovery(
						workspace,
						applicationLifecycleRuns,
						"upgrade snapshot evidence could not be bound",
						resultRef,
						time.Now().UTC(),
						errors.Join(transactionErr, snapshotErr),
					)
				} else if transactionErr == nil {
					transactionErr = succeedArchitectureV2ApplicationLifecycles(
						workspace, applicationLifecycleRuns, evidence, time.Now().UTC(),
					)
				} else if transaction.Status == "rolled-back" && transaction.Rollback.Verified {
					evidence[2].Ref = resultRef + "#verified-rollback"
					transactionErr = recoverArchitectureV2ApplicationLifecycles(
						workspace,
						applicationLifecycleRuns,
						"upgrade target failed and the prior runtime was restored and verified",
						resultRef,
						evidence,
						time.Now().UTC(),
						transactionErr,
					)
				} else {
					transactionErr = requireArchitectureV2ApplicationLifecycleRecovery(
						workspace,
						applicationLifecycleRuns,
						"upgrade failed without a verified automatic recovery",
						resultRef,
						time.Now().UTC(),
						transactionErr,
					)
				}
			}
		}
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

// admitPublicUpgradeKit enforces the CUE-owned kit upgrade policy before any
// release resolution or lifecycle mutation. Recovery of an already-authorized
// operation is handled before this fresh-upgrade admission.
func admitPublicUpgradeKit(kit string) error {
	definition, err := architecturev2.EmbeddedKitDefinition(strings.TrimSpace(kit))
	if err != nil {
		return fmt.Errorf("load CUE-owned upgrade policy for %q: %w", kit, err)
	}
	policy, ok := definition["upgradePolicy"].(map[string]any)
	if !ok {
		return fmt.Errorf("CUE-owned upgrade policy for %q is missing", kit)
	}
	support, ok := policy["support"].(string)
	if !ok || strings.TrimSpace(support) == "" {
		return fmt.Errorf("CUE-owned upgrade policy for %q has no support level", kit)
	}
	switch strings.TrimSpace(support) {
	case "preview":
		return nil
	case "unsupported":
		return fmt.Errorf("StackKit %q does not support public upgrades (upgradePolicy.support=%q)", kit, support)
	default:
		return fmt.Errorf("CUE-owned upgrade policy for %q has unsupported support level %q", kit, support)
	}
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
