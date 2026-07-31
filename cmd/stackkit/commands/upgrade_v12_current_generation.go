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
	publishedV012CurrentVersion = "v0.12.0"
	publishedV013UpgradeVersion = "v0.13.0"
	publishedV012ArchiveSHA256  = "9a466802c504d28fcc511a64a2a2656a267c6a90205c75a41a6a89bff8d12443"
	publishedV012IndexSHA256    = "8aebcdf5615a3f0cfbe4060eccf08235c54f4f0be3756c397e08bde0b1bd186e"
)

// inspectPublishedV012CurrentGeneration reads the applied v0.12 generation
// with the exact attested v0.12 binary that authored it. The v0.13 authority
// remains strict for all target resolution and mutation.
func inspectPublishedV012CurrentGeneration(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (generationartifact.PlanInspection, bool, error) {
	bridge, err := inspectPublishedV012UpgradeBridge(
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

func inspectPublishedV012UpgradeBridge(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (publicUpgradeBridge, error) {
	if !isPublishedV012CompatibilityTarget(kit, target) {
		return publicUpgradeBridge{}, nil
	}
	platform := currentReleasePlatform()
	installDir := filepath.Join(
		workspace, ".stackkit", "releases", kit, publishedV012CurrentVersion,
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
			if err := validatePublishedV012Receipt(current, kit, platform); err != nil {
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
			return errors.New("published v0.12 bridge requires an execution runner")
		}
		common := publicUpgradeCommandPrefix(workspace, requestedSpec)
		rawPlan, runErr := runner.Run(
			ctx, binary, append(common, "plan", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.12 plan proof: %w", runErr)
		}
		if err := decodeUpgradeExactJSON(rawPlan, &bridge.Current); err != nil {
			return fmt.Errorf("decode attested v0.12 plan proof: %w", err)
		}
		if err := validateUpgradePlanInspection(bridge.Current, "published v0.12 current"); err != nil {
			return err
		}
		rawVerify, runErr := runner.Run(
			ctx, binary, append(common, "verify", "--offline", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.12 offline verification: %w", runErr)
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
			return fmt.Errorf("run attested v0.12 live verification: %w", runErr)
		}
		liveReport, liveVerifyErr := decodeAndValidateUpgradeVerify(
			rawLiveVerify, bridge.Current.Binding.PlanHash, receipt,
			bridge.Verify.Owner.OwnerRef, bridge.Verify.Owner.OwnerBindingDigest,
		)
		if liveVerifyErr != nil {
			return fmt.Errorf("validate attested v0.12 live verification: %w", liveVerifyErr)
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

func isPublishedV012CompatibilityTarget(
	kit string,
	target releaseindex.Resolution,
) bool {
	return strings.TrimSpace(kit) != "" &&
		target.Asset.Kit == kit &&
		target.Asset.Version == publishedV013UpgradeVersion &&
		target.Asset.Channel == releaseindex.ChannelStable &&
		target.Asset.Platform == (releaseindex.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH})
}

func validatePublishedV012Receipt(
	receipt releaseindex.Receipt,
	kit string,
	platform releaseindex.Platform,
) error {
	if receipt.SchemaVersion != releaseindex.ReceiptSchemaVersion ||
		receipt.Kit != kit ||
		receipt.Version != publishedV012CurrentVersion ||
		receipt.Channel != releaseindex.ChannelStable ||
		receipt.Platform != platform ||
		receipt.ArchiveSHA256 != publishedV012ArchiveSHA256 ||
		receipt.IndexSHA256 != publishedV012IndexSHA256 ||
		strings.TrimSpace(receipt.InstallDir) == "" {
		return errors.New("installed release is not the attested published v0.12 stable source")
	}
	return nil
}
