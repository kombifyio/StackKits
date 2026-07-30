//go:build !publisher

package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	publishedV08CurrentVersion       = "v0.8.0"
	publishedV09UpgradeVersion       = "v0.9.0"
	publishedV08CompilerVersion      = "stackkits-resolver/0.8.0"
	publishedV08PlanRendererVersion  = "0.8.0"
	publishedV08ArchiveSHA256        = "0d6840ccb789b14840f0ff4c0b6e21fef290a639a0fd47eb8c65ae43cbcb13de"
	publishedV08IndexSHA256          = "54815571ccb3ac58c9551dfce80d3a331b2a8660e0e1f1acfd0cad22004c9beb"
	publishedV08AuthorityFingerprint = "sha256:082998f2b0843eb6d5dd9cccaede88412c9c73753cfbc201ced8ca5cc19c87ca"
	publishedV08CatalogHash          = "sha256:36e536598cc040ffa6a440d75eeeeb8ff067b813e0cc5549a10d3e41a971d2ab"
	publishedV08DefinitionHash       = "sha256:e605db9c5c0f60b7b571a11ec234f051ca65902402b88aca8b07f74169306cf3"
)

// inspectPublishedV08CurrentGeneration reads the already-applied v0.8
// generation with the exact attested v0.8 binary that authored it. The v0.9
// authority remains strict for every new resolution; no historical plan is
// admitted to v0.9 Generate, Apply, Verify, or recovery.
func inspectPublishedV08CurrentGeneration(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (generationartifact.PlanInspection, bool, error) {
	if !isPublishedV08CompatibilityTarget(kit, target) {
		return generationartifact.PlanInspection{}, false, nil
	}
	platform := currentReleasePlatform()
	installDir := filepath.Join(
		workspace, ".stackkit", "releases", kit, publishedV08CurrentVersion,
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
			if err := validatePublishedV08Receipt(current, kit, platform); err != nil {
				return err
			}
			receipt = current
			return nil
		})
	})
	if errors.Is(err, os.ErrNotExist) {
		return generationartifact.PlanInspection{}, false, nil
	}
	if err != nil {
		return generationartifact.PlanInspection{}, true, err
	}

	var current generationartifact.PlanInspection
	err = withPublicUpgradeInstalledExecutable(ctx, receipt, func(binary string) error {
		runner := newUpgradeInspectionRunner()
		if runner == nil {
			return errors.New("published v0.8 generation inspection requires an execution runner")
		}
		raw, runErr := runner.Run(
			ctx,
			binary,
			append(publicUpgradeCommandPrefix(workspace, requestedSpec), "plan", "--json"),
			workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.8 plan proof: %w", runErr)
		}
		if err := decodeUpgradeExactJSON(raw, &current); err != nil {
			return fmt.Errorf("decode attested v0.8 plan proof: %w", err)
		}
		return validatePublishedV08Inspection(current)
	})
	if err != nil {
		return generationartifact.PlanInspection{}, true, err
	}
	return current, true, nil
}

// inspectPublishedV08UpgradeBridge proves the full historical current-state
// closure with the exact public v0.8.0 executable before that state may be
// captured as a rollback checkpoint. It never authorizes target generation.
func inspectPublishedV08UpgradeBridge(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (publicUpgradeBridge, error) {
	if !isPublishedV08CompatibilityTarget(kit, target) {
		return publicUpgradeBridge{}, nil
	}
	platform := currentReleasePlatform()
	installDir := filepath.Join(
		workspace, ".stackkit", "releases", kit, publishedV08CurrentVersion,
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
			if err := validatePublishedV08Receipt(current, kit, platform); err != nil {
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

	bridge := publicUpgradeBridge{}
	err = withPublicUpgradeInstalledExecutable(ctx, receipt, func(binary string) error {
		runner := newUpgradeInspectionRunner()
		if runner == nil {
			return errors.New("published v0.8 checkpoint bridge requires an execution runner")
		}
		common := publicUpgradeCommandPrefix(workspace, requestedSpec)
		rawPlan, runErr := runner.Run(
			ctx, binary, append(common, "plan", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.8 plan proof: %w", runErr)
		}
		if err := decodeUpgradeExactJSON(rawPlan, &bridge.Current); err != nil {
			return fmt.Errorf("decode attested v0.8 plan proof: %w", err)
		}
		if err := validatePublishedV08Inspection(bridge.Current); err != nil {
			return err
		}
		rawVerify, runErr := runner.Run(
			ctx, binary, append(common, "verify", "--offline", "--json"), workspace,
		)
		if runErr != nil {
			return fmt.Errorf("run attested v0.8 offline verification: %w", runErr)
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
			return fmt.Errorf("run attested v0.8 live verification: %w", runErr)
		}
		liveReport, liveVerifyErr := decodeAndValidateUpgradeVerify(
			rawLiveVerify, bridge.Current.Binding.PlanHash, receipt,
			bridge.Verify.Owner.OwnerRef, bridge.Verify.Owner.OwnerBindingDigest,
		)
		if liveVerifyErr != nil {
			return fmt.Errorf("validate attested v0.8 live verification: %w", liveVerifyErr)
		}
		bridge.LiveVerify = liveReport
		return nil
	})
	if err != nil {
		return publicUpgradeBridge{}, err
	}
	bridge.Enabled = true
	bridge.Receipt = receipt
	return bridge, nil
}

func isPublishedV08CompatibilityTarget(
	kit string,
	target releaseindex.Resolution,
) bool {
	return strings.TrimSpace(kit) != "" &&
		target.Asset.Kit == kit &&
		target.Asset.Version == publishedV09UpgradeVersion &&
		target.Asset.Channel == releaseindex.ChannelStable &&
		target.Asset.Platform == currentReleasePlatform()
}

func validatePublishedV08Receipt(
	receipt releaseindex.Receipt,
	kit string,
	platform releaseindex.Platform,
) error {
	if receipt.SchemaVersion != releaseindex.ReceiptSchemaVersion ||
		receipt.Kit != kit ||
		receipt.Version != publishedV08CurrentVersion ||
		receipt.Channel != releaseindex.ChannelStable ||
		receipt.Platform != platform ||
		receipt.ArchiveSHA256 != publishedV08ArchiveSHA256 ||
		receipt.IndexSHA256 != publishedV08IndexSHA256 ||
		strings.TrimSpace(receipt.InstallDir) == "" {
		return errors.New("installed release is not the attested published v0.8 stable source")
	}
	return nil
}

func validatePublishedV08Inspection(current generationartifact.PlanInspection) error {
	if err := validateUpgradePlanInspection(current, "published v0.8 current"); err != nil {
		return err
	}
	if current.Binding.CompilerVersion != publishedV08CompilerVersion ||
		current.Binding.Renderer.ID != "stackkit" ||
		current.Binding.Renderer.Version != publishedV08PlanRendererVersion ||
		current.Binding.DefinitionHash != publishedV08DefinitionHash ||
		!reflect.DeepEqual(current.Binding.Authority, resolvedplan.PlanAuthority{
			Class: "product", Document: "catalog", GraduationEligible: true,
			Issuer:               "stackkits-product-authority/v1",
			AuthorityFingerprint: publishedV08AuthorityFingerprint,
			CatalogHash:          publishedV08CatalogHash,
		}) {
		return errors.New("current plan is outside the published v0.8 compiler and renderer tuple")
	}
	return nil
}

func validatePublishedStableVerifyResult(
	raw []byte,
	current generationartifact.PlanInspection,
	receipt releaseindex.Receipt,
) (architectureV2VerifyReport, error) {
	var envelope publicUpgradeRawCommandResult
	if err := decodeUpgradeExactJSON(raw, &envelope); err != nil {
		return architectureV2VerifyReport{}, fmt.Errorf("decode historical stable verify envelope: %w", err)
	}
	if envelope.SchemaVersion != commandResultSchemaVersion ||
		envelope.Command != "stackkit verify" ||
		envelope.Status != "success" {
		return architectureV2VerifyReport{}, errors.New(
			"historical stable offline verification did not return a successful canonical command result",
		)
	}
	var report architectureV2VerifyReport
	if err := decodeUpgradeExactJSON(envelope.Data, &report); err != nil {
		return architectureV2VerifyReport{}, fmt.Errorf("decode historical stable verify report: %w", err)
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
		return architectureV2VerifyReport{}, errors.New(
			"historical stable offline verification is not bound to the exact plan, owner, apply evidence, and release",
		)
	}
	return report, nil
}
