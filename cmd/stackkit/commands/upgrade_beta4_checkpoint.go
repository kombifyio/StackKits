package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"gopkg.in/yaml.v3"
)

var (
	inspectCurrentNativeV2BackupAuthorityForRequest = inspectNativeV2BackupAuthority
	inspectExactBeta4BackupAuthorityForRequest      = inspectExactBeta4BackupAuthority
)

type exactBeta4CheckpointState struct {
	authority         nativeV2BackupAuthority
	manifest          generationartifact.ArtifactManifest
	generationReceipt generationartifact.GenerationReceipt
	applyResult       []byte
	applyReceipt      []byte
}

func inspectExactBeta4BackupAuthority(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (nativeV2BackupAuthority, error) {
	bridge, err := inspectExactBeta4UpgradeBridge(
		ctx, workspace, requestedSpec, kit, target,
	)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if !bridge.Enabled {
		return nativeV2BackupAuthority{}, errors.New(
			"current state is not eligible for the exact beta.4 checkpoint bridge",
		)
	}
	state, err := readExactBeta4CheckpointState(workspace, bridge)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	return state.authority, nil
}

func inspectPublishedV08BackupAuthority(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (nativeV2BackupAuthority, error) {
	bridge, err := inspectPublishedV08UpgradeBridge(
		ctx, workspace, requestedSpec, kit, target,
	)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if !bridge.Enabled {
		return nativeV2BackupAuthority{}, errors.New(
			"current state is not eligible for the published v0.8 checkpoint bridge",
		)
	}
	state, err := readPublishedV08CheckpointState(workspace, bridge)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	return state.authority, nil
}

func inspectPublishedV09BackupAuthority(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (nativeV2BackupAuthority, error) {
	bridge, err := inspectPublishedV09UpgradeBridge(
		ctx, workspace, requestedSpec, kit, target,
	)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if !bridge.Enabled {
		return nativeV2BackupAuthority{}, errors.New(
			"current state is not eligible for the published v0.9 checkpoint bridge",
		)
	}
	state, err := readPublishedV08CheckpointState(workspace, bridge)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	return state.authority, nil
}

func inspectPublishedV010BackupAuthority(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (nativeV2BackupAuthority, error) {
	bridge, err := inspectPublishedV010UpgradeBridge(
		ctx, workspace, requestedSpec, kit, target,
	)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if !bridge.Enabled {
		return nativeV2BackupAuthority{}, errors.New(
			"current state is not eligible for the published v0.10 checkpoint bridge",
		)
	}
	state, err := readPublishedV08CheckpointState(workspace, bridge)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	return state.authority, nil
}

func inspectPublishedV011BackupAuthority(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (nativeV2BackupAuthority, error) {
	bridge, err := inspectPublishedV011UpgradeBridge(
		ctx, workspace, requestedSpec, kit, target,
	)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if !bridge.Enabled {
		return nativeV2BackupAuthority{}, errors.New(
			"current state is not eligible for the published v0.11 checkpoint bridge",
		)
	}
	state, err := readPublishedV08CheckpointState(workspace, bridge)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	return state.authority, nil
}

func inspectPublishedV012BackupAuthority(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	kit string,
	target releaseindex.Resolution,
) (nativeV2BackupAuthority, error) {
	bridge, err := inspectPublishedV012UpgradeBridge(
		ctx, workspace, requestedSpec, kit, target,
	)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if !bridge.Enabled {
		return nativeV2BackupAuthority{}, errors.New(
			"current state is not eligible for the published v0.12 checkpoint bridge",
		)
	}
	state, err := readPublishedV08CheckpointState(workspace, bridge)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	return state.authority, nil
}

// inspectNativeV2BackupAuthorityForRequest keeps the normal current authority
// path as the default. It may retain the beta.4 authority only when its full
// attested bridge proof succeeds for the sole supported v0.8.0 target.
func inspectNativeV2BackupAuthorityForCommand(
	ctx context.Context,
	workspace string,
	requestedSpec string,
) (nativeV2BackupAuthority, error) {
	current, currentErr := inspectCurrentNativeV2BackupAuthorityForRequest(ctx, workspace, requestedSpec)
	if currentErr == nil {
		return current, nil
	}
	legacy, legacyErr := inspectExactBeta4BackupAuthorityForRequest(
		ctx, workspace, requestedSpec, "basement-kit",
		releaseindex.Resolution{Asset: releaseindex.Asset{
			Kit:      "basement-kit",
			Version:  exactBeta4UpgradeTargetVersion,
			Channel:  releaseindex.ChannelStable,
			Platform: currentReleasePlatform(),
		}},
	)
	if legacyErr != nil {
		return nativeV2BackupAuthority{}, errors.Join(currentErr, legacyErr)
	}
	return legacy, nil
}
func readExactBeta4CheckpointState(
	workspace string,
	bridge publicUpgradeBridge,
) (exactBeta4CheckpointState, error) {
	return readHistoricalCheckpointStateWithPolicy(
		workspace, bridge, nativeV2BackupPolicyArtifactID,
	)
}

func readHistoricalCheckpointStateWithPolicy(
	workspace string,
	bridge publicUpgradeBridge,
	policyArtifactID string,
) (exactBeta4CheckpointState, error) {
	if !bridge.Enabled {
		return exactBeta4CheckpointState{}, errors.New(
			"exact beta.4 bridge proof is required",
		)
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	defer func() { _ = root.Close() }()
	workspace = root.Name()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	defer func() { _ = transaction.Close() }()

	metadataRoot := filepath.Join(
		workspace, filepath.FromSlash(bridge.Current.OutputRoot), ".stackkit",
	)
	manifestPath := filepath.Join(
		metadataRoot, generationartifact.ArtifactManifestFileName,
	)
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	manifestRaw, _, err := transaction.ReadStable(
		workspaceRelativeBackupPath(workspace, manifestPath),
	)
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	canonicalManifest, err := manifest.MarshalCanonical()
	if err != nil || !bytes.Equal(manifestRaw, canonicalManifest) ||
		manifest.Binding != bridge.Current.Binding {
		return exactBeta4CheckpointState{}, errors.New(
			"beta.4 generation manifest differs from the attested plan proof",
		)
	}
	manifestHash, err := manifest.Hash()
	if err != nil || manifestHash != bridge.Current.Manifest.Hash {
		return exactBeta4CheckpointState{}, errors.New(
			"beta.4 generation manifest hash differs from the attested plan proof",
		)
	}

	receiptPath := filepath.Join(
		metadataRoot, generationartifact.GenerationReceiptFileName,
	)
	generationReceipt, err := generationartifact.ReadReceipt(receiptPath)
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	receiptRaw, _, err := transaction.ReadStable(
		workspaceRelativeBackupPath(workspace, receiptPath),
	)
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	canonicalReceipt, err := generationReceipt.MarshalCanonical()
	if err != nil || !bytes.Equal(receiptRaw, canonicalReceipt) ||
		generationReceipt.Binding != bridge.Current.Binding ||
		generationReceipt.ManifestHash != manifestHash {
		return exactBeta4CheckpointState{}, errors.New(
			"beta.4 generation receipt differs from the attested plan proof",
		)
	}
	generationReceiptHash, err := generationReceipt.Hash()
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}

	if policyArtifactID == "" {
		for _, artifact := range manifest.Artifacts {
			if !strings.HasPrefix(artifact.ID, "local-kopia-backup-source-policy") {
				continue
			}
			if policyArtifactID != "" {
				return exactBeta4CheckpointState{}, errors.New(
					"historical generation has more than one local Kopia policy artifact",
				)
			}
			policyArtifactID = artifact.ID
		}
		if policyArtifactID == "" {
			return exactBeta4CheckpointState{}, errors.New(
				"historical generation has no local Kopia policy artifact",
			)
		}
	}
	policyArtifact, policyDigest, policy, err := readNativeV2BackupPolicyForArtifact(
		transaction, manifest, policyArtifactID,
	)
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	runtimeBinding, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	ownerBindingDigest := localevidence.OwnerRuntimeBindingDigest(runtimeBinding)
	if bridge.Verify.Owner.OwnerRef != owner.OwnerRef ||
		bridge.Verify.Owner.KeyID != owner.KeyID ||
		bridge.Verify.Owner.PocketIDSubject != runtimeBinding.PocketIDSubject ||
		bridge.Verify.Owner.OwnerBindingDigest != ownerBindingDigest ||
		runtimeBinding.OwnerRef != owner.OwnerRef ||
		policy.Target.SiteRef != owner.Binding.SiteRef ||
		policy.Target.NodeRef != owner.Binding.NodeRef {
		return exactBeta4CheckpointState{}, errors.New(
			"beta.4 offline proof, Owner custody, runtime binding, and backup target differ",
		)
	}

	resultHash := bridge.Verify.Apply.ResultHash
	if !nativeV2BackupDigestPattern.MatchString(resultHash) {
		return exactBeta4CheckpointState{}, errors.New(
			"beta.4 offline proof has no canonical Apply result hash",
		)
	}
	name := strings.TrimPrefix(resultHash, "sha256:") + ".json"
	resultPath := filepath.ToSlash(filepath.Join(
		architectureV2ApplyEvidenceRoot, "results", name,
	))
	applyResult, info, err := transaction.ReadStable(resultPath)
	if err != nil || !info.Mode().IsRegular() ||
		info.Size() <= 0 || info.Size() > 16<<20 ||
		exactBeta4CheckpointDigest(applyResult) != resultHash {
		return exactBeta4CheckpointState{}, errors.New(
			"beta.4 Apply result is not the attested bounded content-addressed file",
		)
	}
	applyReceiptPath := filepath.ToSlash(filepath.Join(
		architectureV2ApplyEvidenceRoot, "receipts", name,
	))
	applyReceipt, info, err := transaction.ReadStable(applyReceiptPath)
	if err != nil || !info.Mode().IsRegular() ||
		info.Size() <= 0 || info.Size() > 64<<10 {
		return exactBeta4CheckpointState{}, errors.New(
			"beta.4 owner-signed Apply receipt is not a bounded content-addressed file",
		)
	}
	if err := verifyOwnerApplyResultReceipt(
		workspace, applyResult, applyReceipt, resultHash,
	); err != nil {
		return exactBeta4CheckpointState{}, err
	}
	applyReceiptHash := exactBeta4CheckpointDigest(applyReceipt)
	bridgeCopy := bridge
	authority := nativeV2BackupAuthority{
		OwnerRef:      owner.OwnerRef,
		AuthorityRef:  owner.Trust.HumanAuthorityRef,
		WorkspaceRoot: workspace,
		OutputRoot:    bridge.Current.OutputRoot,
		Lineage: backuplifecycle.AuthorityLineage{
			Binding:               bridge.Current.Binding,
			ManifestHash:          manifestHash,
			GenerationReceiptHash: generationReceiptHash,
			ApplyResultHash:       resultHash,
			ApplyReceiptHash:      applyReceiptHash,
			OwnerBindingDigest:    ownerBindingDigest,
			PocketIDSubject:       runtimeBinding.PocketIDSubject,
		},
		PolicyDigest:   policyDigest,
		PolicyArtifact: policyArtifact,
		Policy:         policy,
		LegacyBeta4:    &bridgeCopy,
	}
	return exactBeta4CheckpointState{
		authority: authority, manifest: manifest,
		generationReceipt: generationReceipt,
		applyResult:       append([]byte(nil), applyResult...),
		applyReceipt:      append([]byte(nil), applyReceipt...),
	}, nil
}

func readPublishedV08CheckpointState(
	workspace string,
	bridge publicUpgradeBridge,
) (exactBeta4CheckpointState, error) {
	state, err := readHistoricalCheckpointStateWithPolicy(workspace, bridge, "")
	if err != nil {
		return exactBeta4CheckpointState{}, err
	}
	bridgeCopy := bridge
	state.authority.LegacyBeta4 = nil
	state.authority.HistoricalStable = &bridgeCopy
	return state, nil
}

func withPreparedExactBeta4UpgradeCapture(
	ctx context.Context,
	workspace string,
	kit string,
	authority nativeV2BackupAuthority,
	continuePrepared func(upgradelifecycle.CurrentStateAuthorityInput) error,
) error {
	return withPreparedHistoricalUpgradeCapture(
		ctx, workspace, kit, authority, authority.LegacyBeta4,
		false, continuePrepared,
	)
}

func withPreparedPublishedStableUpgradeCapture(
	ctx context.Context,
	workspace string,
	kit string,
	authority nativeV2BackupAuthority,
	continuePrepared func(upgradelifecycle.CurrentStateAuthorityInput) error,
) error {
	return withPreparedHistoricalUpgradeCapture(
		ctx, workspace, kit, authority, authority.HistoricalStable,
		true, continuePrepared,
	)
}

func withPreparedHistoricalUpgradeCapture(
	ctx context.Context,
	workspace string,
	kit string,
	authority nativeV2BackupAuthority,
	bridge *publicUpgradeBridge,
	publishedV08 bool,
	continuePrepared func(upgradelifecycle.CurrentStateAuthorityInput) error,
) error {
	if bridge == nil {
		return errors.New("exact beta.4 bridge proof is required")
	}
	var state exactBeta4CheckpointState
	var err error
	if publishedV08 {
		state, err = readPublishedV08CheckpointState(workspace, *bridge)
	} else {
		state, err = readExactBeta4CheckpointState(workspace, *bridge)
	}
	if err != nil {
		return err
	}
	if !sameNativeV2BackupAuthority(authority, state.authority) {
		return errors.New("beta.4 checkpoint authority changed before capture")
	}
	loaded, err := config.NewLoader(workspace).ReadStackSpecDocument(specFile)
	if err != nil {
		return err
	}
	generationTarget, err := historicalStackSpecGenerationTarget(loaded.Document.Raw)
	if err != nil {
		return err
	}
	if err := requireBeta4CheckpointGenerationTarget(generationTarget); err != nil {
		return err
	}
	specRelative, err := filepath.Rel(workspace, loaded.Path)
	if err != nil {
		return err
	}
	inventoryBytes, inventoryPath, err := locateArchitectureV2Inventory(workspace, "")
	if err != nil {
		return err
	}
	var inventoryRelative string
	if len(inventoryBytes) > 0 {
		inventoryRelative, err = filepath.Rel(workspace, inventoryPath)
		if err != nil {
			return err
		}
	}

	artifacts := make([]upgradelifecycle.ExecutorStateBlobInput, 0, len(state.manifest.Artifacts))
	composeArtifactID := upgradeCheckpointComposeArtifactID
	if publishedV08 {
		composeArtifactID, err = historicalBasementComposeArtifactID(state.manifest)
		if err != nil {
			return err
		}
	}
	policyArtifactID, err := historicalKopiaPolicyArtifactID(state.manifest)
	if err != nil {
		return err
	}
	coreModuleRef := authority.Policy.Source.CoreModuleRef
	if coreModuleRef == "" {
		coreModuleRef = "stackkits-basement-core-runtime"
	}
	coreProfile, ok := nativehost.BasementCoreRuntimeProfileForModule(coreModuleRef)
	if !ok {
		return errors.New("historical generation has no supported Basement Core profile")
	}
	var generatedCompose []byte
	for _, artifact := range state.manifest.Artifacts {
		data, readErr := os.ReadFile(
			filepath.Join(workspace, filepath.FromSlash(artifact.Path)),
		)
		if readErr != nil {
			return fmt.Errorf("read beta.4 recovery artifact %s: %w", artifact.ID, readErr)
		}
		if exactBeta4CheckpointDigest(data) != artifact.SHA256 {
			return fmt.Errorf("beta.4 recovery artifact %s changed after proof", artifact.ID)
		}
		recoveryPath := artifact.Path
		if artifact.ID == composeArtifactID {
			recoveryPath = coreProfile.OutputRef
			generatedCompose = append([]byte(nil), data...)
		}
		artifacts = append(artifacts, upgradelifecycle.ExecutorStateBlobInput{
			ID: artifact.ID, Path: recoveryPath, Mode: artifact.Mode, Data: data,
		})
	}
	if err := verifyPublicUpgradeManagedVolumeAuthority(
		generatedCompose, authority.Policy.SourceProjection(),
		publicUpgradeApplicationComposeReader(workspace),
	); err != nil {
		return err
	}
	runtimeCompose, err := os.ReadFile(filepath.Join(
		workspace, filepath.FromSlash(upgradeCheckpointRuntimeComposePath),
	))
	if err != nil {
		return err
	}
	if !bytes.Equal(runtimeCompose, generatedCompose) {
		return errors.New("beta.4 runtime Compose differs from governed generation artifact")
	}

	return (releaseindex.Installer{
		Attestations: newPublicAttestationVerifier(),
	}).InspectInstalled(ctx, bridge.Receipt.InstallDir, func(
		proof releaseindex.VerifiedInstallation,
	) error {
		var currentReceipt releaseindex.Receipt
		if err := proof.Inspect(func(
			receipt releaseindex.Receipt,
			_ releaseindex.Asset,
			_ io.Reader,
		) error {
			currentReceipt = receipt
			if publishedV08 {
				switch receipt.Version {
				case publishedV08CurrentVersion:
					return validatePublishedV08Receipt(
						receipt, kit, currentReleasePlatform(),
					)
				case publishedV09CurrentVersion:
					return validatePublishedV09Receipt(
						receipt, kit, currentReleasePlatform(),
					)
				case publishedV010CurrentVersion:
					return validatePublishedV010Receipt(
						receipt, kit, currentReleasePlatform(),
					)
				case publishedV011CurrentVersion:
					return validatePublishedV011Receipt(
						receipt, kit, currentReleasePlatform(),
					)
				case publishedV012CurrentVersion:
					return validatePublishedV012Receipt(
						receipt, kit, currentReleasePlatform(),
					)
				default:
					if receipt.Channel != releaseindex.ChannelStable {
						return errors.New("installed source is not a stable release")
					}
					return validateExpectedCurrentReleaseReceipt(
						receipt, kit, bridge.Receipt.Version, currentReleasePlatform(),
					)
				}
			}
			return validateExactBeta4ReleaseReceipt(
				receipt, kit, currentReleasePlatform(),
			)
		}); err != nil {
			return err
		}
		if currentReceipt != bridge.Receipt {
			return errors.New("beta.4 installed release changed before capture")
		}
		executableBytes, serverBytes, err := upgradelifecycle.ReleaseExecutablesFromVerifiedRelease(proof)
		if err != nil {
			return err
		}
		capture := upgradelifecycle.ExecutorStateCaptureInput{
			GenerationTarget:      generationTarget,
			CoreModuleRef:         coreModuleRef,
			CoreComposeArtifactID: composeArtifactID,
			CorePolicyArtifactID:  policyArtifactID,
			Release:               proof,
			Executable: upgradelifecycle.ExecutorStateExecutableInput{
				Blob: upgradelifecycle.ExecutorStateBlobInput{
					ID:   "stackkit",
					Path: executorRecoveryBinaryPath(currentReceipt.Platform),
					Mode: "0755", Data: executableBytes,
				},
				Server: executorRecoveryServerBlob(currentReceipt.Platform, serverBytes),
			},
			Lineage: authority.Lineage,
			StackSpec: upgradelifecycle.ExecutorStateBlobInput{
				ID: "stack-spec", Path: filepath.ToSlash(specRelative),
				Mode: "0600", Data: append([]byte(nil), loaded.Document.Raw...),
			},
			Artifacts: artifacts,
			RuntimeCompose: upgradelifecycle.ExecutorStateBlobInput{
				ID:   "basement-core-runtime-compose",
				Path: upgradeCheckpointRuntimeComposePath,
				Mode: "0600", Data: runtimeCompose,
			},
		}
		if len(inventoryBytes) > 0 {
			capture.Inventory = &upgradelifecycle.ExecutorStateBlobInput{
				ID: "inventory", Path: filepath.ToSlash(inventoryRelative),
				Mode: "0600", Data: inventoryBytes,
			}
		}
		return continuePrepared(upgradelifecycle.CurrentStateAuthorityInput{
			Capture: capture,
			Legacy: &upgradelifecycle.LegacyCurrentStateAuthorityInput{
				WorkspaceRoot:     workspace,
				Inspection:        bridge.Current,
				Manifest:          state.manifest,
				GenerationReceipt: state.generationReceipt,
				ApplyResult:       state.applyResult,
				ApplyReceipt:      state.applyReceipt,
				Capture:           capture,
			},
		})
	})
}

func historicalBasementComposeArtifactID(
	manifest generationartifact.ArtifactManifest,
) (string, error) {
	var selected string
	for _, artifact := range manifest.Artifacts {
		if !strings.HasPrefix(artifact.ID, "basement-core-compose-") &&
			!strings.HasPrefix(artifact.ID, "basement-core-lite-compose-") {
			continue
		}
		if artifact.Kind != "compose" || artifact.Format != "yaml" ||
			!strings.HasSuffix(artifact.Path, "/compose.yaml") || selected != "" {
			return "", errors.New("historical generation has no unique governed Basement Compose artifact")
		}
		selected = artifact.ID
	}
	if selected == "" {
		return "", errors.New("historical generation has no unique governed Basement Compose artifact")
	}
	return selected, nil
}

func historicalKopiaPolicyArtifactID(
	manifest generationartifact.ArtifactManifest,
) (string, error) {
	var selected string
	for _, artifact := range manifest.Artifacts {
		if !strings.HasPrefix(artifact.ID, "local-kopia-backup-source-policy") {
			continue
		}
		if selected != "" || artifact.Kind != "native-config" || artifact.Format != "json" {
			return "", errors.New("historical generation has no unique governed local Kopia policy artifact")
		}
		selected = artifact.ID
	}
	if selected == "" {
		return "", errors.New("historical generation has no unique governed local Kopia policy artifact")
	}
	return selected, nil
}

func inspectPublicUpgradeSnapshotAuthority(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
) (nativeV2BackupAuthority, error) {
	targetTag, tagErr := releaseindex.ExactTagForBuildVersion(version)
	var currentErr error
	if tagErr != nil || snapshot.Release.Version == targetTag {
		current, err := inspectNativeV2BackupAuthority(ctx, workspace, requestedSpec)
		if err == nil {
			return current, nil
		}
		currentErr = err
	} else {
		currentErr = errors.New("historical source requires its attested release compiler")
	}
	var attestedErr error
	if tagErr == nil {
		var attested nativeV2BackupAuthority
		attested, attestedErr = inspectAttestedCurrentBackupAuthority(
			ctx, workspace, requestedSpec, snapshot.Release.Kit,
			releaseindex.Resolution{Asset: releaseindex.Asset{
				Kit: snapshot.Release.Kit, Version: targetTag,
				Channel: releaseindex.ChannelStable, Platform: currentReleasePlatform(),
			}}, true,
		)
		if attestedErr == nil && attested.HistoricalStable != nil {
			receipt := attested.HistoricalStable.Receipt
			if receipt.Kit == snapshot.Release.Kit &&
				receipt.Version == snapshot.Release.Version &&
				receipt.Channel == snapshot.Release.Channel &&
				receipt.Platform == snapshot.Release.Platform &&
				"sha256:"+receipt.ArchiveSHA256 == snapshot.Release.ArchiveSHA256 &&
				"sha256:"+receipt.IndexSHA256 == snapshot.Release.IndexSHA256 {
				return attested, nil
			}
		}
		if attestedErr == nil {
			attestedErr = errors.New("attested source authority differs from the verified rollback checkpoint")
		}
	}
	if snapshot.Release.Kit == "basement-kit" &&
		snapshot.Release.Version == publishedV012CurrentVersion &&
		snapshot.Release.Channel == releaseindex.ChannelStable &&
		snapshot.Release.Platform == currentReleasePlatform() &&
		snapshot.Release.ArchiveSHA256 == "sha256:"+publishedV012ArchiveSHA256 &&
		snapshot.Release.IndexSHA256 == "sha256:"+publishedV012IndexSHA256 {
		historical, historicalErr := inspectPublishedV012BackupAuthority(
			ctx,
			workspace,
			requestedSpec,
			snapshot.Release.Kit,
			releaseindex.Resolution{Asset: releaseindex.Asset{
				Kit:      snapshot.Release.Kit,
				Version:  publishedV013UpgradeVersion,
				Channel:  releaseindex.ChannelStable,
				Platform: snapshot.Release.Platform,
			}},
		)
		if historicalErr != nil {
			return nativeV2BackupAuthority{}, errors.Join(currentErr, historicalErr)
		}
		return historical, nil
	}
	if snapshot.Release.Kit == "basement-kit" &&
		snapshot.Release.Version == publishedV011CurrentVersion &&
		snapshot.Release.Channel == releaseindex.ChannelStable &&
		snapshot.Release.Platform == currentReleasePlatform() &&
		snapshot.Release.ArchiveSHA256 == "sha256:"+publishedV011ArchiveSHA256 &&
		snapshot.Release.IndexSHA256 == "sha256:"+publishedV011IndexSHA256 {
		historical, historicalErr := inspectPublishedV011BackupAuthority(
			ctx,
			workspace,
			requestedSpec,
			snapshot.Release.Kit,
			releaseindex.Resolution{Asset: releaseindex.Asset{
				Kit:      snapshot.Release.Kit,
				Version:  publishedV012UpgradeVersion,
				Channel:  releaseindex.ChannelStable,
				Platform: snapshot.Release.Platform,
			}},
		)
		if historicalErr != nil {
			return nativeV2BackupAuthority{}, errors.Join(currentErr, historicalErr)
		}
		return historical, nil
	}
	if snapshot.Release.Kit == "basement-kit" &&
		snapshot.Release.Version == publishedV010CurrentVersion &&
		snapshot.Release.Channel == releaseindex.ChannelStable &&
		snapshot.Release.Platform == currentReleasePlatform() &&
		snapshot.Release.ArchiveSHA256 == "sha256:"+publishedV010ArchiveSHA256 &&
		snapshot.Release.IndexSHA256 == "sha256:"+publishedV010IndexSHA256 {
		historical, historicalErr := inspectPublishedV010BackupAuthority(
			ctx,
			workspace,
			requestedSpec,
			snapshot.Release.Kit,
			releaseindex.Resolution{Asset: releaseindex.Asset{
				Kit:      snapshot.Release.Kit,
				Version:  publishedV011UpgradeVersion,
				Channel:  releaseindex.ChannelStable,
				Platform: snapshot.Release.Platform,
			}},
		)
		if historicalErr != nil {
			return nativeV2BackupAuthority{}, errors.Join(currentErr, historicalErr)
		}
		return historical, nil
	}
	if snapshot.Release.Kit == "basement-kit" &&
		snapshot.Release.Version == publishedV09CurrentVersion &&
		snapshot.Release.Channel == releaseindex.ChannelStable &&
		snapshot.Release.Platform == currentReleasePlatform() &&
		snapshot.Release.ArchiveSHA256 == "sha256:"+publishedV09ArchiveSHA256 &&
		snapshot.Release.IndexSHA256 == "sha256:"+publishedV09IndexSHA256 {
		historical, historicalErr := inspectPublishedV09BackupAuthority(
			ctx,
			workspace,
			requestedSpec,
			snapshot.Release.Kit,
			releaseindex.Resolution{Asset: releaseindex.Asset{
				Kit:      snapshot.Release.Kit,
				Version:  publishedV010UpgradeVersion,
				Channel:  releaseindex.ChannelStable,
				Platform: snapshot.Release.Platform,
			}},
		)
		if historicalErr != nil {
			return nativeV2BackupAuthority{}, errors.Join(currentErr, historicalErr)
		}
		return historical, nil
	}
	if snapshot.Release.Kit == "basement-kit" &&
		snapshot.Release.Version == publishedV08CurrentVersion &&
		snapshot.Release.Channel == releaseindex.ChannelStable &&
		snapshot.Release.Platform == currentReleasePlatform() &&
		snapshot.Release.ArchiveSHA256 == "sha256:"+publishedV08ArchiveSHA256 &&
		snapshot.Release.IndexSHA256 == "sha256:"+publishedV08IndexSHA256 {
		legacy, legacyErr := inspectPublishedV08BackupAuthority(
			ctx,
			workspace,
			requestedSpec,
			snapshot.Release.Kit,
			releaseindex.Resolution{Asset: releaseindex.Asset{
				Kit:      snapshot.Release.Kit,
				Version:  publishedV09UpgradeVersion,
				Channel:  releaseindex.ChannelStable,
				Platform: snapshot.Release.Platform,
			}},
		)
		if legacyErr != nil {
			return nativeV2BackupAuthority{}, errors.Join(currentErr, legacyErr)
		}
		return legacy, nil
	}
	if snapshot.Release.Kit != "basement-kit" ||
		snapshot.Release.Version != exactBeta4UpgradeVersion ||
		snapshot.Release.Channel != releaseindex.ChannelBeta ||
		snapshot.Release.Platform != currentReleasePlatform() ||
		snapshot.Release.ArchiveSHA256 != "sha256:"+exactBeta4UpgradeArchiveSHA256 ||
		snapshot.Release.IndexSHA256 != "sha256:"+exactBeta4UpgradeIndexSHA256 {
		return nativeV2BackupAuthority{}, errors.Join(currentErr, attestedErr)
	}
	legacy, legacyErr := inspectExactBeta4BackupAuthorityForRequest(
		ctx,
		workspace,
		requestedSpec,
		snapshot.Release.Kit,
		releaseindex.Resolution{Asset: releaseindex.Asset{
			Kit:      snapshot.Release.Kit,
			Version:  exactBeta4UpgradeTargetVersion,
			Channel:  releaseindex.ChannelStable,
			Platform: snapshot.Release.Platform,
		}},
	)
	if legacyErr != nil {
		return nativeV2BackupAuthority{}, errors.Join(currentErr, legacyErr)
	}
	return legacy, nil
}

func verifyExactBeta4BackupRestore(
	ctx context.Context,
	expected nativeV2BackupAuthority,
	request backuplifecycle.RestoreVerificationRequest,
) (backuplifecycle.RestoreVerification, error) {
	return verifyHistoricalBackupRestore(ctx, expected, request, false)
}

func verifyHistoricalBackupRestore(
	ctx context.Context,
	expected nativeV2BackupAuthority,
	request backuplifecycle.RestoreVerificationRequest,
	stable bool,
) (backuplifecycle.RestoreVerification, error) {
	bridge := expected.LegacyBeta4
	if stable {
		bridge = expected.HistoricalStable
	}
	if bridge == nil ||
		request.OwnerRef != expected.OwnerRef ||
		request.AuthorizationLineage != expected.Lineage ||
		request.SnapshotAnchorID == "" ||
		request.OperationID == "" ||
		request.StagingPath != backuplifecycle.RestoreStagingPath(request.OperationID) {
		return backuplifecycle.RestoreVerification{}, errors.New("historical restore target authority changed before live post-verification")
	}
	var state exactBeta4CheckpointState
	var err error
	if stable {
		state, err = readPublishedV08CheckpointState(expected.WorkspaceRoot, *bridge)
	} else {
		state, err = readExactBeta4CheckpointState(expected.WorkspaceRoot, *bridge)
	}
	if err != nil {
		return backuplifecycle.RestoreVerification{}, err
	}
	if !sameNativeV2BackupAuthority(expected, state.authority) {
		return backuplifecycle.RestoreVerification{}, errors.New("historical restore authority changed before live post-verification")
	}

	report := bridge.LiveVerify
	if report.Offline ||
		report.PlanHash != expected.Lineage.Binding.PlanHash ||
		report.Owner.OwnerRef != expected.OwnerRef ||
		report.Owner.OwnerBindingDigest != expected.Lineage.OwnerBindingDigest ||
		report.Owner.PocketIDSubject != expected.Lineage.PocketIDSubject ||
		exactBeta4ReceiptMatches(report.Releases, bridge.Receipt) != 1 {
		return backuplifecycle.RestoreVerification{}, errors.New("historical live verification proof differs from the exact staged restore authority")
	}
	return backuplifecycle.RestoreVerification{
		APIVersion:         "stackkit.local-backup-restore-verification/v1",
		OwnerRef:           report.Owner.OwnerRef,
		OwnerBindingDigest: report.Owner.OwnerBindingDigest,
		PocketIDSubject:    report.Owner.PocketIDSubject,
		PlanHash:           report.PlanHash,
		ServicesVerified:   true,
		VerifiedAt:         time.Now().UTC(),
	}, nil
}

func verifyPublishedStableBackupRestore(
	ctx context.Context,
	expected nativeV2BackupAuthority,
	request backuplifecycle.RestoreVerificationRequest,
) (backuplifecycle.RestoreVerification, error) {
	return verifyHistoricalBackupRestore(ctx, expected, request, true)
}

func exactBeta4CheckpointDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// historicalStackSpecGenerationTarget reads generation.target from a
// historical StackSpec. Every published kit defaults to compose, and the
// historical releases this bridge admits executed only Compose, so an
// omitted target is compose.
func historicalStackSpecGenerationTarget(raw []byte) (string, error) {
	var document struct {
		Generation struct {
			Target string `yaml:"target"`
		} `yaml:"generation"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return "", fmt.Errorf("read historical StackSpec generation target: %w", err)
	}
	if target := strings.TrimSpace(document.Generation.Target); target != "" {
		return target, nil
	}
	return "compose", nil
}
