package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/releaseindex"
	"golang.org/x/mod/semver"
)

// inspectAttestedCurrentGeneration uses the installed source release to read
// the generation it authored. A newer compiler must not reinterpret an older
// ResolvedPlan, even when the surrounding catalog did not change.
func inspectAttestedCurrentGeneration(
	ctx context.Context,
	workspace, requestedSpec, kit string,
	target releaseindex.Resolution,
) (publicUpgradeBridge, error) {
	if target.Asset.Kit != kit || target.Asset.Channel != releaseindex.ChannelStable ||
		target.Asset.Platform != currentReleasePlatform() {
		return publicUpgradeBridge{}, nil
	}
	releasesRoot := filepath.Join(workspace, ".stackkit", "releases", kit)
	entries, err := os.ReadDir(releasesRoot)
	if errors.Is(err, os.ErrNotExist) {
		return publicUpgradeBridge{}, nil
	}
	if err != nil {
		return publicUpgradeBridge{}, fmt.Errorf("read installed source releases: %w", err)
	}
	var matched publicUpgradeBridge
	var candidateErrors []error
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return publicUpgradeBridge{}, errors.New("installed source release path is a symlink")
		}
		tag := entry.Name()
		if !entry.IsDir() || !semver.IsValid(tag) ||
			semver.Compare(tag, target.Asset.Version) >= 0 {
			continue
		}
		installDir := filepath.Join(releasesRoot, tag,
			target.Asset.Platform.OS+"-"+target.Asset.Platform.Arch)
		var receipt releaseindex.Receipt
		err := (releaseindex.Installer{
			Attestations: newPublicAttestationVerifier(),
		}).InspectInstalled(ctx, installDir, func(proof releaseindex.VerifiedInstallation) error {
			return proof.Inspect(func(
				current releaseindex.Receipt, _ releaseindex.Asset, _ io.Reader,
			) error {
				if current.SchemaVersion != releaseindex.ReceiptSchemaVersion ||
					current.Channel != releaseindex.ChannelStable ||
					filepath.Clean(current.InstallDir) != filepath.Clean(installDir) {
					return errors.New("installed source receipt is outside stable release custody")
				}
				if err := validateExpectedCurrentReleaseReceipt(
					current, kit, tag, target.Asset.Platform,
				); err != nil {
					return err
				}
				receipt = current
				return nil
			})
		})
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("verify installed source %s: %w", tag, err))
			continue
		}
		bridge, err := inspectAttestedSourceRelease(ctx, workspace, requestedSpec, receipt)
		if err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("inspect attested source %s: %w", tag, err))
			continue
		}
		if matched.Enabled {
			return publicUpgradeBridge{}, errors.New("more than one installed source release proves the current generation")
		}
		matched = bridge
	}
	if !matched.Enabled && len(candidateErrors) > 0 {
		return publicUpgradeBridge{}, errors.Join(candidateErrors...)
	}
	return matched, nil
}

func inspectAttestedSourceRelease(
	ctx context.Context,
	workspace, requestedSpec string,
	receipt releaseindex.Receipt,
) (publicUpgradeBridge, error) {
	bridge := publicUpgradeBridge{Receipt: receipt}
	err := withPublicUpgradeInstalledExecutable(ctx, receipt, func(binary string) error {
		runner := newUpgradeInspectionRunner()
		if runner == nil {
			return errors.New("attested source inspection runner is unavailable")
		}
		common := publicUpgradeCommandPrefix(workspace, requestedSpec)
		rawPlan, err := runner.Run(ctx, binary, append(common, "plan", "--json"), workspace)
		if err != nil {
			return fmt.Errorf("run attested source plan proof: %w", err)
		}
		if err := decodeUpgradeExactJSON(rawPlan, &bridge.Current); err != nil {
			return fmt.Errorf("decode attested source plan proof: %w", err)
		}
		if err := validateUpgradePlanInspection(bridge.Current, "attested source"); err != nil {
			return err
		}
		sourceVersion := strings.TrimPrefix(receipt.Version, "v")
		if bridge.Current.Binding.CompilerVersion != "stackkits-resolver/"+sourceVersion ||
			bridge.Current.Binding.Renderer.Version != sourceVersion {
			return errors.New("attested source compiler does not match its installed release")
		}
		rawVerify, err := runner.Run(
			ctx, binary, append(common, "verify", "--offline", "--json"), workspace,
		)
		if err != nil {
			return fmt.Errorf("run attested source offline verification: %w", err)
		}
		bridge.Verify, err = validatePublishedStableVerifyResult(
			rawVerify, bridge.Current, receipt,
		)
		if err != nil {
			return err
		}
		rawLive, err := runner.Run(
			ctx, binary, append(common, "verify", "--json"), workspace,
		)
		if err != nil {
			return fmt.Errorf("run attested source live verification: %w", err)
		}
		bridge.LiveVerify, err = decodeAndValidateUpgradeVerify(
			rawLive, bridge.Current.Binding.PlanHash, receipt,
			bridge.Verify.Owner.OwnerRef, bridge.Verify.Owner.OwnerBindingDigest,
		)
		return err
	})
	if err != nil {
		return publicUpgradeBridge{}, err
	}
	bridge.Enabled = true
	return bridge, nil
}

func inspectAttestedCurrentBackupAuthority(
	ctx context.Context,
	workspace, requestedSpec, kit string,
	target releaseindex.Resolution,
) (nativeV2BackupAuthority, error) {
	bridge, err := inspectAttestedCurrentGeneration(
		ctx, workspace, requestedSpec, kit, target,
	)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if !bridge.Enabled {
		return nativeV2BackupAuthority{}, errors.New("no attested installed source release proves the current generation")
	}
	state, err := readPublishedV08CheckpointState(workspace, bridge)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	return state.authority, nil
}
