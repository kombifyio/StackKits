package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// inspectAttestedCurrentGeneration uses the installed source release to read
// the generation it authored. A newer compiler must not reinterpret an older
// ResolvedPlan, even when the surrounding catalog did not change.
func inspectAttestedCurrentGeneration(
	ctx context.Context,
	workspace, requestedSpec, kit string,
	target releaseindex.Resolution,
	allowSourceInstall bool,
	freezeSourceInventory bool,
) (publicUpgradeBridge, error) {
	if target.Asset.Kit != kit || target.Asset.Channel != releaseindex.ChannelStable ||
		target.Asset.Platform != currentReleasePlatform() {
		return publicUpgradeBridge{}, nil
	}
	if allowSourceInstall {
		if err := installAttestedSourceForCurrentGeneration(
			ctx, workspace, requestedSpec, kit, target,
		); err != nil {
			return publicUpgradeBridge{}, err
		}
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
		bridge, err := inspectAttestedSourceRelease(ctx, workspace, requestedSpec, receipt, freezeSourceInventory)
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
	freezeSourceInventory bool,
) (publicUpgradeBridge, error) {
	bridge := publicUpgradeBridge{Receipt: receipt}
	var inventoryPath string
	if freezeSourceInventory {
		var cleanup func()
		var err error
		inventoryPath, cleanup, err = materializeHistoricalPlanInventory(workspace, requestedSpec)
		if err != nil {
			return publicUpgradeBridge{}, err
		}
		defer cleanup()
	}
	err := withPublicUpgradeInstalledExecutable(ctx, receipt, func(binary string) error {
		runner := newUpgradeInspectionRunner()
		if runner == nil {
			return errors.New("attested source inspection runner is unavailable")
		}
		common := publicUpgradeCommandPrefix(workspace, requestedSpec)
		withInventory := func(args ...string) []string {
			command := append(append([]string(nil), common...), args...)
			if inventoryPath != "" {
				command = append(command, "--inventory", inventoryPath)
			}
			return command
		}
		rawPlan, err := runner.Run(ctx, binary, withInventory("plan", "--json"), workspace)
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
			ctx, binary, withInventory("verify", "--offline", "--json"), workspace,
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
			ctx, binary, withInventory("verify", "--json"), workspace,
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
	freezeSourceInventory bool,
) (nativeV2BackupAuthority, error) {
	bridge, err := inspectAttestedCurrentGeneration(
		ctx, workspace, requestedSpec, kit, target, true, freezeSourceInventory,
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

// A public installer may put the source CLI on PATH without putting its
// verified release receipt in this workspace. The persisted generation receipt
// is only a version hint; the signed index, installed archive and source CLI's
// complete current-state proof establish authority before it is used.
func installAttestedSourceForCurrentGeneration(
	ctx context.Context,
	workspace, requestedSpec, kit string,
	target releaseindex.Resolution,
) error {
	tag, err := currentGenerationSourceHint(workspace, requestedSpec)
	if err != nil || tag == "" {
		return err
	}
	if semver.Compare(tag, target.Asset.Version) >= 0 {
		return nil
	}
	installDir := filepath.Join(workspace, ".stackkit", "releases", kit,
		tag, target.Asset.Platform.OS+"-"+target.Asset.Platform.Arch)
	if _, err := os.Stat(filepath.Join(installDir, releaseindex.ReleaseReceiptName)); err == nil {
		return nil // InspectInstalled below still verifies the cached release.
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect source release receipt: %w", err)
	}
	source := newPublicReleaseSource()
	attestations := newPublicAttestationVerifier()
	resolved, err := (releaseindex.Resolver{
		Source: source, Attestations: attestations,
	}).Resolve(ctx, releaseindex.ResolveRequest{
		Kit: kit, Target: tag,
		OS: target.Asset.Platform.OS, Arch: target.Asset.Platform.Arch,
	})
	if err != nil {
		return fmt.Errorf("resolve attested source release %s: %w", tag, err)
	}
	_, err = (releaseindex.Installer{
		Source: source, Attestations: attestations,
	}).Install(ctx, resolved, workspace)
	if err != nil {
		return fmt.Errorf("install attested source release %s: %w", tag, err)
	}
	return nil
}

func currentGenerationSourceHint(workspace, requestedSpec string) (string, error) {
	outputRoot, err := historicalGenerationOutputRoot(workspace, requestedSpec)
	if err != nil || outputRoot == "" {
		return "", err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return "", err
	}
	defer transaction.Close()
	relative := filepath.Join(outputRoot, ".stackkit", "generation-receipt.json")
	raw, info, err := transaction.ReadStable(relative)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read generation release hint: %w", err)
	}
	if !info.Mode().IsRegular() || len(raw) == 0 || len(raw) > 1<<20 {
		return "", errors.New("generation release hint is not a bounded regular file")
	}
	var receipt struct {
		Binding struct {
			CompilerVersion string `json:"compilerVersion"`
		} `json:"binding"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return "", fmt.Errorf("decode generation release hint: %w", err)
	}
	version, found := strings.CutPrefix(receipt.Binding.CompilerVersion, "stackkits-resolver/")
	tag := "v" + version
	if !found || !semver.IsValid(tag) {
		return "", errors.New("generation compiler does not identify a stable source release")
	}
	return tag, nil
}

func historicalGenerationOutputRoot(workspace, requestedSpec string) (string, error) {
	loaded, err := config.NewLoader(workspace).ReadStackSpecDocument(requestedSpec)
	if err != nil {
		return "", fmt.Errorf("read current StackSpec for release hint: %w", err)
	}
	if !loaded.Document.Version.IsV2() {
		return "", nil
	}
	var spec struct {
		Generation struct {
			OutputRoot string `yaml:"outputRoot"`
		} `yaml:"generation"`
	}
	if err := yaml.Unmarshal(loaded.Document.Raw, &spec); err != nil {
		return "", fmt.Errorf("decode generation output root for release hint: %w", err)
	}
	outputRoot := spec.Generation.OutputRoot
	if outputRoot == "" {
		outputRoot = "deploy"
	}
	if filepath.IsAbs(outputRoot) || filepath.Clean(outputRoot) != outputRoot ||
		outputRoot == ".." || strings.HasPrefix(outputRoot, "../") ||
		strings.Contains(outputRoot, "\\") {
		return "", errors.New("generation output root is not a confined release hint path")
	}
	return outputRoot, nil
}

// The source release must re-resolve its persisted Plan with the exact
// Inventory document that produced it. A later live free-space probe can
// change the conventional Inventory file without changing that Plan.
func materializeHistoricalPlanInventory(
	workspace, requestedSpec string,
) (string, func(), error) {
	outputRoot, err := historicalGenerationOutputRoot(workspace, requestedSpec)
	if err != nil {
		return "", nil, err
	}
	if outputRoot == "" {
		return "", nil, errors.New("historical Plan requires a v2 StackSpec output root")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return "", nil, err
	}
	defer transaction.Close()
	planPath := filepath.Join(outputRoot, ".stackkit", "resolved-plan.json")
	raw, info, err := transaction.ReadStableBounded(planPath, 16<<20)
	if err != nil {
		return "", nil, fmt.Errorf("read historical resolved Plan Inventory: %w", err)
	}
	if !info.Mode().IsRegular() || len(raw) == 0 {
		return "", nil, errors.New("historical resolved Plan is not a bounded regular file")
	}
	path, cleanup, err := materializePlanInventory(raw)
	if err != nil {
		return "", nil, fmt.Errorf("decode historical Plan Inventory projection: %w", err)
	}
	if path == "" {
		return "", nil, errors.New("historical Plan has no valid Inventory document")
	}
	return path, cleanup, nil
}
