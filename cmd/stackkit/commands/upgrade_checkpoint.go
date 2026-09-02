package commands

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"gopkg.in/yaml.v3"
)

const (
	upgradeCheckpointComposeArtifactID  = "basement-core-compose-instance-compose-node-main"
	upgradeCheckpointRuntimeComposePath = ".stackkit/runtime/basement-core/compose.yaml"
	publicUpgradeAttemptAPIVersion      = "stackkit.upgrade-attempt/v1"
	publicUpgradeAttemptRoot            = ".stackkit/upgrades/attempts"
)

var (
	publicUpgradeOperationIDPattern       = regexp.MustCompile(`^upgrade-[a-f0-9]{32}-[a-f0-9]{32}$`)
	readPublicUpgradeAttemptNonce         = rand.Read
	inspectPublicUpgradeBackupAuthority   = inspectNativeV2BackupAuthority
	withPublicUpgradeCheckpointOutputLock = withArchitectureV2OutputLock
	preparePublicUpgradeCurrentAuthority  = withPreparedPublicUpgradeCapture
	persistPublicUpgradeAttemptIdentity   = persistPublicUpgradeAttempt
	createPublicUpgradeSnapshotAnchor     = createPublicUpgradeSnapshot
	sealPublicUpgradeExecutorState        = sealAndCapturePublicUpgradeExecutorState
)

type publicUpgradeAttempt struct {
	APIVersion          string `json:"apiVersion"`
	OperationID         string `json:"operationId"`
	TargetKit           string `json:"targetKit"`
	TargetVersion       string `json:"targetVersion"`
	TargetArchiveSHA256 string `json:"targetArchiveSha256"`
	CurrentPlanHash     string `json:"currentPlanHash"`
	ResumeMode          string `json:"resumeMode"`
}

func createPublicUpgradeCheckpoint(
	ctx context.Context,
	workspace string,
	kit string,
	target releaseindex.Resolution,
) (publicUpgradeCheckpoint, error) {
	checkpointContext, cancelCheckpoint := nativeV2BackupOperationContext(
		ctx, backupLongOperationTimeout,
	)
	defer cancelCheckpoint()

	inspectCheckpointAuthority := func() (nativeV2BackupAuthority, error) {
		current, currentErr := inspectPublicUpgradeBackupAuthority(
			checkpointContext, workspace, specFile,
		)
		if currentErr == nil {
			return current, nil
		}
		stableV012, stableV012Err := inspectPublishedV012BackupAuthority(
			checkpointContext, workspace, specFile, kit, target,
		)
		if stableV012Err == nil {
			return stableV012, nil
		}
		stableV011, stableV011Err := inspectPublishedV011BackupAuthority(
			checkpointContext, workspace, specFile, kit, target,
		)
		if stableV011Err == nil {
			return stableV011, nil
		}
		stableV010, stableV010Err := inspectPublishedV010BackupAuthority(
			checkpointContext, workspace, specFile, kit, target,
		)
		if stableV010Err == nil {
			return stableV010, nil
		}
		stableV09, stableV09Err := inspectPublishedV09BackupAuthority(
			checkpointContext, workspace, specFile, kit, target,
		)
		if stableV09Err == nil {
			return stableV09, nil
		}
		stableV08, stableV08Err := inspectPublishedV08BackupAuthority(
			checkpointContext, workspace, specFile, kit, target,
		)
		if stableV08Err == nil {
			return stableV08, nil
		}
		legacy, legacyErr := inspectExactBeta4BackupAuthority(
			checkpointContext, workspace, specFile, kit, target,
		)
		if legacyErr != nil {
			return nativeV2BackupAuthority{}, errors.Join(
				currentErr, stableV012Err, stableV011Err, stableV010Err,
				stableV09Err, stableV08Err, legacyErr,
			)
		}
		return legacy, nil
	}
	initial, err := inspectCheckpointAuthority()
	if err != nil {
		return publicUpgradeCheckpoint{}, fmt.Errorf("verify current backup authority: %w", err)
	}
	var checkpoint publicUpgradeCheckpoint
	err = withPublicUpgradeCheckpointOutputLock(
		initial.WorkspaceRoot,
		initial.OutputRoot,
		func(transaction *confinedfs.Transaction, _ *confinedfs.OutputLock) error {
			current, inspectErr := inspectCheckpointAuthority()
			if inspectErr != nil {
				return fmt.Errorf("verify locked current backup authority: %w", inspectErr)
			}
			if !sameNativeV2BackupAuthority(initial, current) {
				return errors.New(
					"current upgrade authority changed while acquiring the Architecture-v2 output lock",
				)
			}
			return preparePublicUpgradeCurrentAuthority(
				checkpointContext,
				current.WorkspaceRoot,
				kit,
				current,
				func(prepared upgradelifecycle.CurrentStateAuthorityInput) error {
					attempt, attemptErr := persistPublicUpgradeAttemptIdentity(
						transaction, target, current.Lineage.Binding.PlanHash,
					)
					if attemptErr != nil {
						return attemptErr
					}
					anchor, snapshotErr := createPublicUpgradeSnapshotAnchor(
						checkpointContext, current, attempt.OperationID,
					)
					if snapshotErr != nil {
						return snapshotErr
					}
					snapshotID, captureErr := sealPublicUpgradeExecutorState(
						current.WorkspaceRoot, prepared, attempt.OperationID, anchor,
					)
					if captureErr != nil {
						return captureErr
					}
					checkpoint = publicUpgradeCheckpoint{
						OperationID: attempt.OperationID, KopiaAnchorID: anchor.ID,
						ExecutorStateSnapshotID:  snapshotID,
						ApplicationLifecyclePlan: current.Plan,
					}
					return nil
				},
			)
		},
	)
	if err != nil {
		return publicUpgradeCheckpoint{}, err
	}
	return checkpoint, nil
}

func publicUpgradeOperationID(target releaseindex.Resolution, nonce []byte) string {
	sum := sha256.Sum256([]byte(
		target.Asset.Kit + "\x00" + target.Asset.Version + "\x00" + target.Asset.Archive.SHA256,
	))
	return "upgrade-" + hex.EncodeToString(sum[:16]) + "-" + hex.EncodeToString(nonce)
}

func persistPublicUpgradeAttempt(
	transaction *confinedfs.Transaction,
	target releaseindex.Resolution,
	currentPlanHash string,
) (publicUpgradeAttempt, error) {
	if transaction == nil ||
		!nativeV2BackupDigestPattern.MatchString(currentPlanHash) ||
		!nativeV2BackupDigestPattern.MatchString("sha256:"+target.Asset.Archive.SHA256) {
		return publicUpgradeAttempt{}, errors.New(
			"persist upgrade attempt requires a held workspace and canonical current/target digests",
		)
	}
	nonce := make([]byte, 16)
	if read, err := readPublicUpgradeAttemptNonce(nonce); err != nil || read != len(nonce) {
		return publicUpgradeAttempt{}, fmt.Errorf("create fresh upgrade attempt identity: %w", err)
	}
	attempt := publicUpgradeAttempt{
		APIVersion:          publicUpgradeAttemptAPIVersion,
		OperationID:         publicUpgradeOperationID(target, nonce),
		TargetKit:           target.Asset.Kit,
		TargetVersion:       target.Asset.Version,
		TargetArchiveSHA256: "sha256:" + target.Asset.Archive.SHA256,
		CurrentPlanHash:     currentPlanHash,
		ResumeMode:          "never-implicit",
	}
	if !publicUpgradeOperationIDPattern.MatchString(attempt.OperationID) {
		return publicUpgradeAttempt{}, errors.New("generated upgrade attempt operation ID is invalid")
	}
	canonical, err := resolvedplan.CanonicalJSON(attempt)
	if err != nil {
		return publicUpgradeAttempt{}, fmt.Errorf("encode upgrade attempt: %w", err)
	}
	if err := transaction.MkdirAll(publicUpgradeAttemptRoot, 0o700); err != nil {
		return publicUpgradeAttempt{}, fmt.Errorf("create upgrade attempt store: %w", err)
	}
	attemptPath := publicUpgradeAttemptRoot + "/" + attempt.OperationID + ".json"
	if err := transaction.WriteFileExclusive(attemptPath, canonical, 0o600); err != nil {
		return publicUpgradeAttempt{}, fmt.Errorf("persist fresh upgrade attempt: %w", err)
	}
	return attempt, nil
}

func createPublicUpgradeSnapshot(
	ctx context.Context,
	authority nativeV2BackupAuthority,
	operationID string,
) (backuplifecycle.SnapshotAnchor, error) {
	service, err := newNativeV2BackupService(authority)
	if err != nil {
		return backuplifecycle.SnapshotAnchor{}, err
	}
	statusContext, cancelStatus := nativeV2BackupOperationContext(
		ctx, backupQuickOperationTimeout,
	)
	statusInput := backuplifecycle.StatusInput{
		OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef,
		Lineage: authority.Lineage, PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
	}
	status, err := service.Status(statusContext, statusInput)
	cancelStatus()
	if errors.Is(err, os.ErrNotExist) {
		configureContext, cancelConfigure := nativeV2BackupOperationContext(
			ctx, backupLongOperationTimeout,
		)
		_, err = service.Configure(configureContext, backuplifecycle.ConfigureInput{
			OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef,
			Lineage: authority.Lineage, PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
		})
		cancelConfigure()
		if err != nil {
			return backuplifecycle.SnapshotAnchor{}, fmt.Errorf(
				"configure missing pre-upgrade Kopia repository: %w", err,
			)
		}
		statusContext, cancelStatus = nativeV2BackupOperationContext(
			ctx, backupQuickOperationTimeout,
		)
		status, err = service.Status(statusContext, statusInput)
		cancelStatus()
	}
	if err != nil {
		return backuplifecycle.SnapshotAnchor{}, fmt.Errorf(
			"verify configured pre-upgrade Kopia repository (run stackkit backup configure first): %w",
			err,
		)
	}
	if !status.Ready || status.Consistency != backuplifecycle.ConsistencyCrashConsistent {
		return backuplifecycle.SnapshotAnchor{}, errors.New(
			"pre-upgrade Kopia repository is not ready for crash-consistent snapshots; run stackkit backup configure first",
		)
	}
	snapshotContext, cancelSnapshot := nativeV2BackupOperationContext(
		ctx, backupLongOperationTimeout,
	)
	anchor, err := service.Run(snapshotContext, backuplifecycle.RunInput{
		OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef,
		Lineage: authority.Lineage, PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
		OperationID:     "backup-" + operationID,
		ProtectRecovery: true,
	})
	cancelSnapshot()
	if err != nil {
		return backuplifecycle.SnapshotAnchor{}, fmt.Errorf(
			"create pre-upgrade Kopia snapshot: %w", err,
		)
	}
	return anchor, nil
}

func sealAndCapturePublicUpgradeExecutorState(
	workspace string,
	prepared upgradelifecycle.CurrentStateAuthorityInput,
	operationID string,
	anchor backuplifecycle.SnapshotAnchor,
) (string, error) {
	prepared.Capture.OperationID = operationID
	prepared.Capture.KopiaSnapshotAnchor = anchor
	var verified upgradelifecycle.VerifiedExecutorStateCapture
	var err error
	if prepared.Legacy != nil {
		legacy := *prepared.Legacy
		legacy.Capture = prepared.Capture
		verified, err = upgradelifecycle.NewVerifiedLegacyExecutorStateCapture(legacy)
	} else {
		verified, err = upgradelifecycle.NewVerifiedExecutorStateCapture(prepared)
	}
	if err != nil {
		return "", fmt.Errorf("seal current executor-state authority: %w", err)
	}
	snapshot, err := (upgradelifecycle.ExecutorStateStore{}).Capture(workspace, verified)
	if err != nil {
		return "", fmt.Errorf("persist signed executor-state checkpoint: %w", err)
	}
	return snapshot.ID, nil
}

func withPreparedPublicUpgradeCapture(
	ctx context.Context,
	workspace string,
	kit string,
	authority nativeV2BackupAuthority,
	continuePrepared func(upgradelifecycle.CurrentStateAuthorityInput) error,
) error {
	if continuePrepared == nil {
		return errors.New("prepared public upgrade continuation is required")
	}
	if authority.LegacyBeta4 != nil {
		return withPreparedExactBeta4UpgradeCapture(
			ctx, workspace, kit, authority, continuePrepared,
		)
	}
	if authority.HistoricalStable != nil {
		return withPreparedPublishedStableUpgradeCapture(
			ctx, workspace, kit, authority, continuePrepared,
		)
	}
	gate := newArchitectureV2ExecutionGate()
	sourceService, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	planPath := filepath.Join(workspace, filepath.FromSlash(authority.OutputRoot), ".stackkit", "resolved-plan.json")
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("read current ResolvedPlan: %w", err)
	}
	plan, err := sourceService.VerifyCanonicalPlan(planBytes)
	if err != nil || plan.Binding() != authority.Lineage.Binding {
		return errors.New("current ResolvedPlan differs from backup authority")
	}
	_, manifestPath, receiptPath := plan.MetadataPaths(workspace)
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	receipt, err := generationartifact.ReadReceipt(receiptPath)
	if err != nil {
		return err
	}

	loaded, err := config.NewLoader(workspace).ReadStackSpecDocument(specFile)
	if err != nil {
		return err
	}
	specRelative, err := filepath.Rel(workspace, loaded.Path)
	if err != nil {
		return err
	}
	inventoryBytes, err := readArchitectureV2Inventory(workspace, "")
	if err != nil {
		return err
	}

	rawVerify, err := newArchitectureV2ProductVerifyAuthority(workspace, architectureV2ExecutionCLIOptions{})
	if err != nil {
		return err
	}
	if closer, ok := rawVerify.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	verifyAuthority, ok := rawVerify.(*architectureV2ProductRuntimeAuthority)
	if !ok || verifyAuthority == nil || verifyAuthority.Service == nil {
		return errors.New("current Apply verification service is unavailable")
	}
	verifyService := verifyAuthority.Service
	applyResult, err := readCurrentArchitectureV2ApplyResult(workspace, plan.Binding(), func(data []byte) (architecturev2.VerifiedApplyResult, error) {
		return verifyService.VerifyProductApplyResult(architecturev2.ProductApplyResultVerificationInput{
			Plan: plan, Manifest: manifest, Receipt: receipt, Versions: gate.versions, Result: data,
		})
	})
	if err != nil {
		return err
	}
	canonicalApply, err := applyResult.Canonical()
	if err != nil {
		return err
	}
	applyReceiptPath := filepath.Join(
		workspace, filepath.FromSlash(architectureV2ApplyEvidenceRoot), "receipts",
		strings.TrimPrefix(applyResult.ResultHash(), "sha256:")+".json",
	)
	applyReceipt, err := os.ReadFile(applyReceiptPath)
	if err != nil {
		return fmt.Errorf("read current Apply receipt: %w", err)
	}
	if authority.AppliedAuthority == nil {
		return errors.New("current Core recovery profile requires applied Owner custody")
	}
	coreProfile, err := upgradelifecycle.CurrentStateCoreProfileForPlan(
		plan,
		authority.AppliedAuthority.Owner.Binding.SiteRef,
		authority.AppliedAuthority.Owner.Binding.NodeRef,
	)
	if err != nil {
		return fmt.Errorf("select current Core recovery profile: %w", err)
	}

	artifacts := make([]upgradelifecycle.ExecutorStateBlobInput, 0, len(manifest.Artifacts))
	var generatedCompose []byte
	for _, artifact := range manifest.Artifacts {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(artifact.Path)))
		if err != nil {
			return fmt.Errorf("read recovery artifact %s: %w", artifact.ID, err)
		}
		recoveryPath := artifact.Path
		if artifact.ID == coreProfile.ComposeArtifactID {
			recoveryPath = coreProfile.ComposeOutputRef
			generatedCompose = append([]byte(nil), data...)
		}
		artifacts = append(artifacts, upgradelifecycle.ExecutorStateBlobInput{
			ID: artifact.ID, Path: recoveryPath, Mode: artifact.Mode, Data: data,
		})
	}
	if err := verifyPublicUpgradeManagedVolumeAuthority(
		generatedCompose, authority.Policy.SourceProjection(),
	); err != nil {
		return err
	}
	runtimeCompose, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(upgradeCheckpointRuntimeComposePath)))
	if err != nil {
		return fmt.Errorf("read current runtime Compose: %w", err)
	}

	platform := currentReleasePlatform()
	appliedTag, appliedInstallDir, err := appliedPublicUpgradeReleasePath(
		workspace, kit, applyResult.ExecutorIdentity().Version, platform,
	)
	if err != nil {
		return fmt.Errorf("resolve applied StackKit release identity: %w", err)
	}
	sourceVerifier, err := upgradelifecycle.NewCurrentSourceVerifier(sourceService)
	if err != nil {
		return err
	}
	applyVerifier, err := upgradelifecycle.NewCurrentApplyResultVerifier(ctx, verifyService, verifyAuthority.journal)
	if err != nil {
		return err
	}
	err = (releaseindex.Installer{
		Attestations: newPublicAttestationVerifier(),
	}).InspectInstalled(ctx, appliedInstallDir, func(proof releaseindex.VerifiedInstallation) error {
		var currentReceipt releaseindex.Receipt
		if inspectErr := proof.Inspect(func(
			receipt releaseindex.Receipt,
			_ releaseindex.Asset,
			_ io.Reader,
		) error {
			currentReceipt = receipt
			return validateExpectedCurrentReleaseReceipt(
				receipt, kit, appliedTag, platform,
			)
		}); inspectErr != nil {
			return inspectErr
		}
		executableBytes, executableErr := upgradelifecycle.RecoveryExecutableFromVerifiedRelease(proof)
		if executableErr != nil {
			return executableErr
		}
		capture := upgradelifecycle.ExecutorStateCaptureInput{
			GenerationTarget: "compose", Release: proof,
			Executable: upgradelifecycle.ExecutorStateExecutableInput{Blob: upgradelifecycle.ExecutorStateBlobInput{
				ID: "stackkit", Path: executorRecoveryBinaryPath(currentReceipt.Platform),
				Mode: "0755", Data: executableBytes,
			}},
			Lineage: authority.Lineage,
			StackSpec: upgradelifecycle.ExecutorStateBlobInput{
				ID: "stack-spec", Path: filepath.ToSlash(specRelative), Mode: "0600",
				Data: append([]byte(nil), loaded.Document.Raw...),
			},
			Artifacts: artifacts,
			RuntimeCompose: upgradelifecycle.ExecutorStateBlobInput{
				ID: "basement-core-runtime-compose", Path: upgradeCheckpointRuntimeComposePath,
				Mode: "0600", Data: runtimeCompose,
			},
		}
		if len(inventoryBytes) > 0 {
			capture.Inventory = &upgradelifecycle.ExecutorStateBlobInput{
				ID: "inventory", Path: ".stackkit/inventory.json", Mode: "0600",
				Data: inventoryBytes,
			}
		}
		return continuePrepared(upgradelifecycle.CurrentStateAuthorityInput{
			WorkspaceRoot: workspace, Plan: plan, Manifest: manifest,
			GenerationReceipt: receipt, Versions: gate.versions,
			ApplyResult: canonicalApply, ApplyReceipt: applyReceipt,
			SourceVerifier: sourceVerifier, ApplyVerifier: applyVerifier,
			Capture: capture,
		})
	})
	if err != nil {
		return fmt.Errorf("prepare verified current executor-state authority: %w", err)
	}
	return nil
}

func appliedPublicUpgradeReleasePath(
	workspace string,
	kit string,
	appliedExecutorVersion string,
	platform releaseindex.Platform,
) (string, string, error) {
	appliedTag, err := releaseindex.ExactTagForBuildVersion(appliedExecutorVersion)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(workspace) == "" ||
		strings.TrimSpace(kit) == "" ||
		strings.TrimSpace(platform.OS) == "" ||
		strings.TrimSpace(platform.Arch) == "" {
		return "", "", errors.New("workspace, kit, and platform are required")
	}
	return appliedTag, filepath.Join(
		workspace, ".stackkit", "releases", kit, appliedTag,
		platform.OS+"-"+platform.Arch,
	), nil
}

func verifyPublicUpgradeManagedVolumeAuthority(
	composeBytes []byte,
	source localbackuppolicy.Source,
) error {
	if len(composeBytes) == 0 {
		return errors.New("verified Basement Compose artifact is required for backup selection")
	}
	var compose struct {
		Name     string `yaml:"name"`
		Services map[string]struct {
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
		Volumes map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(composeBytes, &compose); err != nil {
		return fmt.Errorf("decode verified Basement Compose backup authority: %w", err)
	}
	if compose.Name != "stackkit-basement-core" ||
		len(compose.Services) == 0 ||
		len(compose.Volumes) == 0 {
		return errors.New("verified Basement Compose has no exact project, services, or volumes")
	}
	internal := map[string]struct{}{
		"kopia-repository": {}, "kopia-config": {},
		"kopia-cache": {}, "kopia-restore-staging": {},
	}
	managedShort := map[string]struct{}{}
	for serviceName, service := range compose.Services {
		if serviceName == localbackuppolicy.ServiceRef {
			continue
		}
		for _, mount := range service.Volumes {
			sourceRef, _, found := strings.Cut(mount, ":")
			if !found {
				return fmt.Errorf("Compose service %s has a non-canonical volume mount", serviceName)
			}
			if _, named := compose.Volumes[sourceRef]; !named {
				continue
			}
			if _, forbidden := internal[sourceRef]; forbidden {
				return fmt.Errorf("Compose service %s consumes a Kopia-internal volume", serviceName)
			}
			managedShort[sourceRef] = struct{}{}
		}
	}
	for volumeName := range compose.Volumes {
		if _, managed := managedShort[volumeName]; managed {
			continue
		}
		if _, allowedInternal := internal[volumeName]; !allowedInternal {
			return fmt.Errorf("Compose volume %s is neither managed nor Kopia-internal", volumeName)
		}
	}
	if len(managedShort) == 0 {
		return errors.New("verified Basement Compose has no managed backup volumes")
	}

	wantManagedMounts := map[string]string{}
	managedNames := make([]string, 0, len(managedShort))
	for shortName := range managedShort {
		fullName := compose.Name + "_" + shortName
		managedNames = append(managedNames, fullName)
		wantManagedMounts[source.HostPath+"/"+fullName+"/_data"] =
			source.ContainerPath + "/" + fullName + "/_data"
	}
	sort.Strings(managedNames)
	if !equalExactStrings(managedNames, source.ManagedVolumeNames) {
		return errors.New(
			"verified Compose managed volume set differs from the CUE-owned Kopia allowlist",
		)
	}
	kopia, exists := compose.Services[localbackuppolicy.ServiceRef]
	if !exists {
		return errors.New("verified Basement Compose has no Kopia service")
	}
	observedManagedMounts := map[string]string{}
	for _, mount := range kopia.Volumes {
		parts := strings.Split(mount, ":")
		if len(parts) != 2 && len(parts) != 3 {
			return errors.New("Kopia Compose volume mount is not canonical")
		}
		if strings.HasPrefix(parts[0], source.HostPath+"/") {
			if len(parts) != 3 || parts[2] != "ro" {
				return errors.New("Kopia managed-volume bind must be read-only")
			}
			if _, duplicate := observedManagedMounts[parts[0]]; duplicate {
				return errors.New("Kopia managed-volume bind is duplicated")
			}
			observedManagedMounts[parts[0]] = parts[1]
		}
		if parts[0] == source.HostPath {
			return errors.New("Kopia must not mount the whole Docker volume root")
		}
	}
	if len(observedManagedMounts) != len(wantManagedMounts) {
		return errors.New("Kopia Compose service does not mount the exact managed-volume allowlist")
	}
	for hostPath, containerPath := range wantManagedMounts {
		if observedManagedMounts[hostPath] != containerPath {
			return errors.New("Kopia Compose managed-volume bind differs from verified authority")
		}
	}
	return nil
}

func equalExactStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func executorRecoveryBinaryPath(platform releaseindex.Platform) string {
	if platform.OS == "windows" {
		return "stackkit.exe"
	}
	return "stackkit"
}
