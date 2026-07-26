//go:build !publisher

package commands

import (
	"context"
	"fmt"
	"runtime"

	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/spf13/cobra"
)

var (
	publicUpgradeTo     string
	publicUpgradeDryRun bool
	publicUpgradeJSON   bool
)

type releaseCommandResult struct {
	Kit           string                `json:"kit"`
	Version       string                `json:"version"`
	Channel       releaseindex.Channel  `json:"channel"`
	Platform      releaseindex.Platform `json:"platform"`
	Asset         string                `json:"asset"`
	ArchiveSHA256 string                `json:"archiveSha256"`
	InstallDir    string                `json:"installDir,omitempty"`
	Receipt       *releaseindex.Receipt `json:"receipt,omitempty"`
	DryRun        bool                  `json:"dryRun"`
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

This release-distribution command does not apply the installed release. The
snapshot, plan diff, apply, verification, and rollback transaction is added by
the governed Day-2 lifecycle slice.`,
		RunE: runPublicUpgrade,
	}
	if deprecatedAlias {
		command.Deprecated = "use stackkit upgrade"
	}
	command.Flags().StringVar(&publicUpgradeTo, "to", "latest", "Target: latest, vX.Y.Z, or channel:stable|beta|edge.")
	command.Flags().BoolVar(&publicUpgradeDryRun, "dry-run", false, "Resolve and print the target without downloading or installing it.")
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
	if !publicUpgradeDryRun {
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
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Resolved %s %s (%s, %s/%s)\nAsset: %s\nSHA-256: %s\n",
			result.Kit, result.Version, result.Channel, result.Platform.OS, result.Platform.Arch, result.Asset, result.ArchiveSHA256)
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Installed verified %s %s at %s\n", result.Kit, result.Version, result.InstallDir)
	return err
}
