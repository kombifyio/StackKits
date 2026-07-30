//go:build !publisher

package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/releaseindex"
)

const (
	publishedV010CurrentVersion = "v0.10.0"
	publishedV011UpgradeVersion = "v0.11.0"
	publishedV010ArchiveSHA256  = "f4bfbd633c20417479fb76ce7df7b67828c2dba25f50743fed3a48d80cc49105"
	publishedV010IndexSHA256    = "90f42b95a359d4d27d1cb84ac6a33d98a1d216050d0a51b7d22a26214fe8f089"
)

// inspectPublishedV010CurrentGeneration reads the applied v0.10 generation
// with the exact attested v0.10 binary that authored it. The v0.11 authority
// remains strict for all target resolution and mutation.
func inspectPublishedV010CurrentGeneration(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (generationartifact.PlanInspection, bool, error) {
	bridge, err := inspectPublishedV010UpgradeBridge(
		ctx, workspace, requestedSpec, kit, target,
	)
	if err != nil {
		return generationartifact.PlanInspection{}, true, err
	}
	if !bridge.Enabled {
		return generationartifact.PlanInspection{}, false, nil
	}
	return bridge.Current, true, nil
}

func inspectPublishedV010UpgradeBridge(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (publicUpgradeBridge, error) {
	if !isPublishedV010CompatibilityTarget(kit, target) {
		return publicUpgradeBridge{}, nil
	}
	platform := currentReleasePlatform()
	installDir := filepath.Join(
		workspace, ".stackkit", "releases", kit, publishedV010CurrentVersion,
		platform.OS+"-"+platform.Arch,
	)
	var receipt releaseindex.Receipt
	err := (releaseindex.Installer{
		Attestations: newPublicAttestationVerifier(),
	}).InspectInstalled(ctx, installDir, func(proof releaseindex.VerifiedInstallation) error {
		return proof.Inspect(func(
			current releaseindex.Receipt,
			_ releaseindex.Asset,
			_ io.Reader,
		) error {
			if err := validatePublishedV010Receipt(current, kit, platform); err != nil {
				return err
			}
			receipt = current
			return nil
		})
	})
	if errors.Is(err, os.ErrNotExist) {
		return publicUpgradeBridge{}, nil
	}
	if err != nil {
		return publicUpgradeBridge{}, err
	}

	bridge := publicUpgradeBridge{Receipt: receipt}
	err = withPublicUpgradeInstalledExecutable(ctx, receipt, func(binary string) error {
		runner := newUpgradeInspectionRunner()
		if runner == nil {
			return errors.New("published v0.10 bridge requires an execution runner")
		}
		common := publicUpgradeCommandPrefix(workspace, requestedSpec)
		rawPlan, runErr := runner.Run(
			ctx, binary, append(common, "plan", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.10 plan proof: %w", runErr)
		}
		if err := decodeUpgradeExactJSON(rawPlan, &bridge.Current); err != nil {
			return fmt.Errorf("decode attested v0.10 plan proof: %w", err)
		}
		if err := validateUpgradePlanInspection(bridge.Current, "published v0.10 current"); err != nil {
			return err
		}
		rawVerify, runErr := runner.Run(
			ctx, binary, append(common, "verify", "--offline", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.10 offline verification: %w", runErr)
		}
		report, verifyErr := validatePublishedStableVerifyResult(
			rawVerify, bridge.Current, receipt,
		)
		if verifyErr != nil {
			return verifyErr
		}
		bridge.Verify = report
		rawLiveVerify, runErr := runner.Run(
			ctx, binary, append(common, "verify", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.10 live verification: %w", runErr)
		}
		liveReport, liveVerifyErr := decodeAndValidateUpgradeVerify(
			rawLiveVerify, bridge.Current.Binding.PlanHash, receipt,
			bridge.Verify.Owner.OwnerRef, bridge.Verify.Owner.OwnerBindingDigest,
		)
		if liveVerifyErr != nil {
			return fmt.Errorf("validate attested v0.10 live verification: %w", liveVerifyErr)
		}
		bridge.LiveVerify = liveReport
		return nil
	})
	if err != nil {
		return publicUpgradeBridge{}, err
	}
	bridge.Enabled = true
	return bridge, nil
}

func isPublishedV010CompatibilityTarget(
	kit string,
	target releaseindex.Resolution,
) bool {
	return strings.TrimSpace(kit) != "" &&
		target.Asset.Kit == kit &&
		target.Asset.Version == publishedV011UpgradeVersion &&
		target.Asset.Channel == releaseindex.ChannelStable &&
		target.Asset.Platform == (releaseindex.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH})
}

func validatePublishedV010Receipt(
	receipt releaseindex.Receipt,
	kit string,
	platform releaseindex.Platform,
) error {
	if receipt.SchemaVersion != releaseindex.ReceiptSchemaVersion ||
		receipt.Kit != kit ||
		receipt.Version != publishedV010CurrentVersion ||
		receipt.Channel != releaseindex.ChannelStable ||
		receipt.Platform != platform ||
		receipt.ArchiveSHA256 != publishedV010ArchiveSHA256 ||
		receipt.IndexSHA256 != publishedV010IndexSHA256 ||
		strings.TrimSpace(receipt.InstallDir) == "" {
		return errors.New("installed release is not the attested published v0.10 stable source")
	}
	return nil
}
