package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	exactBeta4UpgradeVersion          = "v0.8.0-beta.4"
	exactBeta4UpgradeTargetVersion    = "v0.8.0"
	exactBeta4UpgradeArchiveSHA256    = "5a84fabc3229bd1c45620cbbebe31f6e3f1d67a3658f400ff7bd345336dab4c3"
	exactBeta4UpgradeIndexSHA256      = "06c547f5494ce7fd0e2e88b5ef2a82c41ab7f1d95169b99128889a28c002a49f"
	exactBeta4AuthorityFingerprint    = "sha256:575debed771be518ee5627a6d7c6b125813a10e4af366bc61a26e7cf7b90c80e"
	exactBeta4CatalogHash             = "sha256:af19894dfda87bf364d2c753acb11000eb532c3c0c736935ea6414dfe2a9a3ae"
	exactBeta4DefinitionHash          = "sha256:7bfee231cf6fb90c954fb2a4b01c8d475147df54e1d07c6879b7c12b2454ac48"
	exactBeta4DistributionFingerprint = "sha256:0a6a896af7c49bc5065fb378fa693ab4b54050d23ce1d19174ff1b98229d48fe"
	exactBeta4CompilerVersion         = "stackkits-resolver/0.8.0-beta.4"
	exactBeta4RendererVersion         = "0.8.0-beta.4"
)

type publicUpgradeBridge struct {
	Enabled    bool
	Current    generationartifact.PlanInspection
	Verify     architectureV2VerifyReport
	LiveVerify architectureV2VerifyReport
	Receipt    releaseindex.Receipt
}

func inspectExactBeta4UpgradeBridge(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (publicUpgradeBridge, error) {
	if !isExactBeta4BridgeTarget(kit, target) {
		return publicUpgradeBridge{}, nil
	}
	platform := currentReleasePlatform()
	installDir := filepath.Join(
		workspace, ".stackkit", "releases", kit, exactBeta4UpgradeVersion,
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
			if err := validateExactBeta4ReleaseReceipt(current, kit, platform); err != nil {
				return err
			}
			receipt = current
			return nil
		})
	})
	if err != nil {
		// Absence is not a compatibility match; malformed or tampered material
		// is still surfaced by the ordinary current-authority path.
		return publicUpgradeBridge{}, nil
	}

	var current generationartifact.PlanInspection
	bridge := publicUpgradeBridge{}
	err = withVerifiedPublicUpgradeExecutable(ctx, receipt, func(binary string) error {
		runner := newUpgradeInspectionRunner()
		if runner == nil {
			return errors.New("beta.4 bridge requires an execution runner")
		}
		common := publicUpgradeCommandPrefix(workspace, requestedSpec)
		rawPlan, runErr := runner.Run(
			ctx, binary, append(common, "plan", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested beta.4 plan proof: %w", runErr)
		}
		if err := decodeUpgradeExactJSON(rawPlan, &current); err != nil {
			return fmt.Errorf("decode attested beta.4 plan proof: %w", err)
		}
		if err := validateExactBeta4PlanInspection(current); err != nil {
			return err
		}
		rawVerify, runErr := runner.Run(
			ctx, binary, append(common, "verify", "--offline", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested beta.4 offline verification: %w", runErr)
		}
		report, verifyErr := validateExactBeta4VerifyResult(rawVerify, current, receipt)
		if verifyErr != nil {
			return verifyErr
		}
		bridge.Verify = report
		rawLiveVerify, runErr := runner.Run(
			ctx, binary, append(common, "verify", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested beta.4 live verification: %w", runErr)
		}
		liveReport, liveVerifyErr := decodeAndValidateUpgradeVerify(
			rawLiveVerify, current.Binding.PlanHash, receipt,
			bridge.Verify.Owner.OwnerRef, bridge.Verify.Owner.OwnerBindingDigest,
		)
		if liveVerifyErr != nil {
			return fmt.Errorf("validate attested beta.4 live verification: %w", liveVerifyErr)
		}
		bridge.LiveVerify = liveReport
		return nil
	})
	if err != nil {
		return publicUpgradeBridge{}, err
	}
	bridge.Enabled = true
	bridge.Current = current
	bridge.Receipt = receipt
	return bridge, nil
}

func isExactBeta4BridgeTarget(kit string, target releaseindex.Resolution) bool {
	return kit == "basement-kit" &&
		target.Asset.Kit == kit &&
		target.Asset.Version == exactBeta4UpgradeTargetVersion &&
		target.Asset.Channel == releaseindex.ChannelStable &&
		target.Asset.Platform == currentReleasePlatform()
}

func validateExactBeta4ReleaseReceipt(
	receipt releaseindex.Receipt,
	kit string,
	platform releaseindex.Platform,
) error {
	if receipt.SchemaVersion != releaseindex.ReceiptSchemaVersion ||
		receipt.Kit != kit ||
		receipt.Version != exactBeta4UpgradeVersion ||
		receipt.Channel != releaseindex.ChannelBeta ||
		receipt.Platform != platform ||
		receipt.ArchiveSHA256 != exactBeta4UpgradeArchiveSHA256 ||
		receipt.IndexSHA256 != exactBeta4UpgradeIndexSHA256 ||
		strings.TrimSpace(receipt.InstallDir) == "" {
		return errors.New("installed release is not the exact attested v0.8.0-beta.4 bridge source")
	}
	return nil
}

func validateExactBeta4PlanInspection(current generationartifact.PlanInspection) error {
	if err := validateUpgradePlanInspection(current, "v0.8.0-beta.4 current"); err != nil {
		return err
	}
	wantAuthority := resolvedplan.PlanAuthority{
		Class: "product", Document: "catalog", GraduationEligible: true,
		Issuer:               "stackkits-product-authority/v1",
		AuthorityFingerprint: exactBeta4AuthorityFingerprint,
		CatalogHash:          exactBeta4CatalogHash,
	}
	if current.Binding.DefinitionHash != exactBeta4DefinitionHash ||
		current.Binding.CompilerVersion != exactBeta4CompilerVersion ||
		current.Binding.Renderer.Version != exactBeta4RendererVersion ||
		!reflect.DeepEqual(current.Binding.Authority, wantAuthority) {
		return errors.New("current plan is outside the exact v0.8.0-beta.4 authority tuple")
	}
	return nil
}

func validateExactBeta4VerifyResult(
	raw []byte,
	current generationartifact.PlanInspection,
	receipt releaseindex.Receipt,
) (architectureV2VerifyReport, error) {
	var envelope publicUpgradeRawCommandResult
	if err := decodeUpgradeExactJSON(raw, &envelope); err != nil {
		return architectureV2VerifyReport{}, fmt.Errorf("decode beta.4 verify envelope: %w", err)
	}
	if envelope.SchemaVersion != commandResultSchemaVersion ||
		envelope.Command != "stackkit verify" ||
		envelope.Status != "success" {
		return architectureV2VerifyReport{}, errors.New("beta.4 offline verification did not return a successful canonical command result")
	}
	var report architectureV2VerifyReport
	if err := decodeUpgradeExactJSON(envelope.Data, &report); err != nil {
		return architectureV2VerifyReport{}, fmt.Errorf("decode beta.4 verify report: %w", err)
	}
	if report.SchemaVersion != "stackkit.verify-result/v1" ||
		!report.Offline ||
		report.PlanHash != current.Binding.PlanHash ||
		report.Apply.ResultHash == "" ||
		report.Apply.EvidenceBundleHash == "" ||
		report.Owner.OwnerRef == "" ||
		report.Owner.KeyID == "" ||
		report.Owner.PocketIDSubject == "" ||
		report.Owner.OwnerBindingDigest == "" ||
		report.Runtime != nil ||
		exactBeta4ReceiptMatches(report.Releases, receipt) != 1 {
		return architectureV2VerifyReport{}, errors.New("beta.4 offline verification is not bound to the exact plan, owner, apply evidence, and release")
	}
	return report, nil
}

func exactBeta4ReceiptMatches(
	receipts []releaseindex.Receipt,
	expected releaseindex.Receipt,
) int {
	matches := 0
	for _, candidate := range receipts {
		if reflect.DeepEqual(candidate, expected) {
			matches++
		}
	}
	return matches
}

// Keep the distribution pin next to the semantic tuple even though it is not
// serialized into PlanAuthority. The exact archive and release-index digests
// above are the independently verified public-distribution binding.
var _ = exactBeta4DistributionFingerprint
