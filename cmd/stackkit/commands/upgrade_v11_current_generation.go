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
	publishedV011CurrentVersion = "v0.11.0"
	publishedV012UpgradeVersion = "v0.12.0"
	publishedV011ArchiveSHA256  = "24ce735c6c575bea02267ad01bba625488f7fe8ba880c70a89fba4a515a90fcd"
	publishedV011IndexSHA256    = "33dcfb6f94053c6a5e1ed959ae5d60dd55f84476f07790b869065eee98afac65"
)

// inspectPublishedV011CurrentGeneration reads the applied v0.11 generation
// with the exact attested v0.11 binary that authored it. The v0.12 authority
// remains strict for all target resolution and mutation.
func inspectPublishedV011CurrentGeneration(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (generationartifact.PlanInspection, bool, error) {
	bridge, err := inspectPublishedV011UpgradeBridge(
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

func inspectPublishedV011UpgradeBridge(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (publicUpgradeBridge, error) {
	if !isPublishedV011CompatibilityTarget(kit, target) {
		return publicUpgradeBridge{}, nil
	}
	platform := currentReleasePlatform()
	installDir := filepath.Join(
		workspace, ".stackkit", "releases", kit, publishedV011CurrentVersion,
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
			if err := validatePublishedV011Receipt(current, kit, platform); err != nil {
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
			return errors.New("published v0.11 bridge requires an execution runner")
		}
		common := publicUpgradeCommandPrefix(workspace, requestedSpec)
		rawPlan, runErr := runner.Run(
			ctx, binary, append(common, "plan", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.11 plan proof: %w", runErr)
		}
		if err := decodeUpgradeExactJSON(rawPlan, &bridge.Current); err != nil {
			return fmt.Errorf("decode attested v0.11 plan proof: %w", err)
		}
		if err := validateUpgradePlanInspection(bridge.Current, "published v0.11 current"); err != nil {
			return err
		}
		rawVerify, runErr := runner.Run(
			ctx, binary, append(common, "verify", "--offline", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.11 offline verification: %w", runErr)
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
			return fmt.Errorf("run attested v0.11 live verification: %w", runErr)
		}
		liveReport, liveVerifyErr := decodeAndValidateUpgradeVerify(
			rawLiveVerify, bridge.Current.Binding.PlanHash, receipt,
			bridge.Verify.Owner.OwnerRef, bridge.Verify.Owner.OwnerBindingDigest,
		)
		if liveVerifyErr != nil {
			return fmt.Errorf("validate attested v0.11 live verification: %w", liveVerifyErr)
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

func isPublishedV011CompatibilityTarget(
	kit string,
	target releaseindex.Resolution,
) bool {
	return strings.TrimSpace(kit) != "" &&
		target.Asset.Kit == kit &&
		target.Asset.Version == publishedV012UpgradeVersion &&
		target.Asset.Channel == releaseindex.ChannelStable &&
		target.Asset.Platform == (releaseindex.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH})
}

func validatePublishedV011Receipt(
	receipt releaseindex.Receipt,
	kit string,
	platform releaseindex.Platform,
) error {
	if receipt.SchemaVersion != releaseindex.ReceiptSchemaVersion ||
		receipt.Kit != kit ||
		receipt.Version != publishedV011CurrentVersion ||
		receipt.Channel != releaseindex.ChannelStable ||
		receipt.Platform != platform ||
		receipt.ArchiveSHA256 != publishedV011ArchiveSHA256 ||
		receipt.IndexSHA256 != publishedV011IndexSHA256 ||
		strings.TrimSpace(receipt.InstallDir) == "" {
		return errors.New("installed release is not the attested published v0.11 stable source")
	}
	return nil
}
