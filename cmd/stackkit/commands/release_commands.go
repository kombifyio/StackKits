//go:build !publisher

package commands

import (
	"context"
	"fmt"
	"runtime"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"github.com/spf13/cobra"
)

var (
	publicUpgradeTo             string
	publicUpgradeDryRun         bool
	publicUpgradeJSON           bool
	newUpgradeInspectionRunner  = func() upgradelifecycle.Runner { return upgradelifecycle.ExecRunner{} }
	newCurrentUpgradeInspection = currentUpgradeInspection
)

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
	DryRun        bool                         `json:"dryRun"`
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
installs the verified release but never applies it. Snapshot, apply,
verification, and rollback remain separate lifecycle operations.`,
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
	if publicUpgradeDryRun {
		current, currentErr := newCurrentUpgradeInspection(ctx, workspace, specFile)
		if currentErr != nil {
			return fmt.Errorf("inspect authoritative current generation: %w", currentErr)
		}
		inspection, inspectErr := (upgradelifecycle.Inspector{
			Source: source, Attestations: newPublicAttestationVerifier(),
			Runner: newUpgradeInspectionRunner(),
		}).Inspect(ctx, resolution, workspace, specFile, current)
		if inspectErr != nil {
			return fmt.Errorf("inspect verified StackKit upgrade: %w", inspectErr)
		}
		result.Inspection = &inspection
	} else {
		receipt, installErr := (releaseindex.Installer{
			Source: source, Attestations: newPublicAttestationVerifier(),
		}).Install(ctx, resolution, workspace)
		if installErr != nil {
			return fmt.Errorf("install verified StackKit release: %w", installErr)
		}
		result.InstallDir = receipt.InstallDir
		result.Receipt = &receipt
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
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Installed verified %s %s at %s\n", result.Kit, result.Version, result.InstallDir)
	return err
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
