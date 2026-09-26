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
	TargetArchiveSHA256 string `json:"targetArchiveSha256,omitempty"`
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
		attested, attestedErr := inspectAttestedCurrentBackupAuthority(
			checkpointContext, workspace, specFile, kit, target, false,
		)
		if attestedErr == nil {
			return attested, nil
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
				currentErr, attestedErr, stableV012Err, stableV011Err, stableV010Err,
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
	// A running-executable target (a same-release mutation without a
	// workspace release cache) has no archive; every other target binds its
	// verified archive digest.
	targetArchive := ""
	if target.Asset.Archive.SHA256 != "" {
		targetArchive = "sha256:" + target.Asset.Archive.SHA256
	}
	if transaction == nil ||
		!nativeV2BackupDigestPattern.MatchString(currentPlanHash) ||
		(targetArchive != "" && !nativeV2BackupDigestPattern.MatchString(targetArchive)) ||
		(targetArchive == "" && strings.TrimSpace(target.Asset.Version) == "") {
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
		TargetArchiveSHA256: targetArchive,
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
	// The configuration follows the current authority: the first checkpoint
	// creates it, and a later one rebinds it in place when a converged change
	// set or reconcile moved the lineage or policy artifact (an added workload
	// selects its data volume). Owner, authority and repository never move.
	configureInput := backuplifecycle.ConfigureInput{
		OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef,
		Lineage: authority.Lineage, PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
	}
	configureContext, cancelConfigure := nativeV2BackupOperationContext(
		ctx, backupLongOperationTimeout,
	)
	_, _, err = service.Rebind(configureContext, configureInput)
	if errors.Is(err, os.ErrNotExist) {
		_, err = service.Configure(configureContext, configureInput)
	}
	cancelConfigure()
	if err != nil {
		return backuplifecycle.SnapshotAnchor{}, fmt.Errorf(
			"bind the pre-upgrade Kopia repository to the current authority: %w", err,
		)
	}
	statusContext, cancelStatus := nativeV2BackupOperationContext(
		ctx, backupQuickOperationTimeout,
	)
	status, err := service.Status(statusContext, backuplifecycle.StatusInput{
		OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef,
		Lineage: authority.Lineage, PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
	})
	cancelStatus()
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
	var anchor backuplifecycle.SnapshotAnchor
	err = withGameServersHeld(ctx, authority.WorkspaceRoot, false, func() error {
		var runErr error
		anchor, runErr = service.Run(snapshotContext, backuplifecycle.RunInput{
			OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef,
			Lineage: authority.Lineage, PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
			OperationID:     "backup-" + operationID,
			ProtectRecovery: true,
		})
		return runErr
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
	generationTarget, err := upgradelifecycle.GenerationTargetForPlan(plan)
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
	inventoryBytes, inventoryPath, err := locateArchitectureV2Inventory(workspace, "")
	if err != nil {
		return err
	}
	if len(inventoryBytes) > 0 {
		// Every non-generate command re-attests the local free-space sample
		// (a Kopia snapshot right before this checkpoint changes it), so the
		// captured and re-resolved Inventory must be the stable projection of
		// the persisted plan, exactly as normal execution and Advanced
		// admission use. Every other fact stays binding.
		stable, _, stableErr := inventoryForGeneratedPlan(
			workspace, loaded.Document.Raw, inventoryBytes,
			architectureV2ExecutionCLIOptions{}, plan,
		)
		if stableErr != nil {
			return stableErr
		}
		inventoryBytes = stable
	}
	var inventoryRelative string
	if len(inventoryBytes) > 0 {
		inventoryRelative, err = filepath.Rel(workspace, inventoryPath)
		if err != nil {
			return err
		}
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
	var coreArtifact []byte
	for _, artifact := range manifest.Artifacts {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(artifact.Path)))
		if err != nil {
			return fmt.Errorf("read recovery artifact %s: %w", artifact.ID, err)
		}
		recoveryPath := artifact.Path
		if artifact.ID == coreProfile.ComposeArtifactID {
			recoveryPath = coreProfile.CoreArtifactOutputRef
			coreArtifact = append([]byte(nil), data...)
		}
		artifacts = append(artifacts, upgradelifecycle.ExecutorStateBlobInput{
			ID: artifact.ID, Path: recoveryPath, Mode: artifact.Mode, Data: data,
		})
	}
	// Under the opentofu and terramate targets the Core artifact is the
	// OpenTofu root that embeds the Compose payload byte for byte.
	generatedCompose, err := coreProfile.CoreComposePayload(coreArtifact)
	if err != nil {
		return err
	}
	if err := verifyPublicUpgradeManagedVolumeAuthority(
		generatedCompose, authority.Policy.SourceProjection(),
		publicUpgradeApplicationComposeReader(workspace),
	); err != nil {
		return err
	}
	// The compose target captures the native runtime Compose file; the
	// OpenTofu targets capture every OpenTofu root (state, configuration,
	// and the runtime Compose file and .env each root writes).
	var runtimeCompose []byte
	var runtimeOpenTofu []upgradelifecycle.ExecutorStateOpenTofuRootInput
	if generationTarget == "compose" {
		runtimeCompose, err = os.ReadFile(filepath.Join(workspace, filepath.FromSlash(coreProfile.RuntimeComposePath)))
		if err != nil {
			return fmt.Errorf("read current runtime Compose: %w", err)
		}
	} else if runtimeOpenTofu, err = upgradelifecycle.CollectOpenTofuRootStates(workspace); err != nil {
		return fmt.Errorf("read current OpenTofu roots: %w", err)
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
	continueWith := func(
		proof releaseindex.VerifiedInstallation,
		running *upgradelifecycle.RunningExecutableRelease,
		executableBytes, serverBytes []byte,
	) error {
		capture := upgradelifecycle.ExecutorStateCaptureInput{
			GenerationTarget: generationTarget, Release: proof, RunningRelease: running,
			Executable: upgradelifecycle.ExecutorStateExecutableInput{Blob: upgradelifecycle.ExecutorStateBlobInput{
				ID: "stackkit", Path: executorRecoveryBinaryPath(platform),
				Mode: "0755", Data: executableBytes,
			}, Server: executorRecoveryServerBlob(platform, serverBytes)},
			Lineage: authority.Lineage,
			StackSpec: upgradelifecycle.ExecutorStateBlobInput{
				ID: "stack-spec", Path: filepath.ToSlash(specRelative), Mode: "0600",
				Data: append([]byte(nil), loaded.Document.Raw...),
			},
			Artifacts:       artifacts,
			RuntimeOpenTofu: runtimeOpenTofu,
		}
		if runtimeOpenTofu == nil {
			capture.RuntimeCompose = upgradelifecycle.ExecutorStateBlobInput{
				ID: "basement-core-runtime-compose", Path: coreProfile.RuntimeComposePath,
				Mode: "0600", Data: runtimeCompose,
			}
		}
		if len(inventoryBytes) > 0 {
			capture.Inventory = &upgradelifecycle.ExecutorStateBlobInput{
				ID: "inventory", Path: filepath.ToSlash(inventoryRelative), Mode: "0600",
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
	}
	// The applied release is the running release and the workspace holds no
	// release cache for it (a Techstack-managed host: the Agent verified the
	// pinned release before starting this executable, and init creates no
	// cache). The checkpoint then captures the running executable as the
	// prior release. Any other applied release requires its verified cache.
	if runningExecutableIsAppliedRelease(appliedTag, appliedInstallDir) {
		running, runningErr := upgradelifecycle.NewRunningExecutableRelease(kit, appliedTag, platform)
		if runningErr != nil {
			return fmt.Errorf("prepare verified current executor-state authority: %w", runningErr)
		}
		executableBytes, serverBytes := running.Executables()
		err = continueWith(releaseindex.VerifiedInstallation{}, &running, executableBytes, serverBytes)
	} else {
		err = (releaseindex.Installer{
			Attestations: newPublicAttestationVerifier(),
		}).InspectInstalled(ctx, appliedInstallDir, func(proof releaseindex.VerifiedInstallation) error {
			if inspectErr := proof.Inspect(func(
				receipt releaseindex.Receipt,
				_ releaseindex.Asset,
				_ io.Reader,
			) error {
				return validateExpectedCurrentReleaseReceipt(
					receipt, kit, appliedTag, platform,
				)
			}); inspectErr != nil {
				return inspectErr
			}
			executableBytes, serverBytes, executableErr := upgradelifecycle.ReleaseExecutablesFromVerifiedRelease(proof)
			if executableErr != nil {
				return executableErr
			}
			return continueWith(proof, nil, executableBytes, serverBytes)
		})
	}
	if err != nil {
		return fmt.Errorf("prepare verified current executor-state authority: %w", err)
	}
	return nil
}

// runningExecutableIsAppliedRelease reports whether the running executable is
// the exact applied release and no workspace release cache entry exists for
// it.
func runningExecutableIsAppliedRelease(appliedTag, appliedInstallDir string) bool {
	runningTag, err := releaseindex.ExactTagForBuildVersion(version)
	if err != nil || runningTag != appliedTag {
		return false
	}
	_, statErr := os.Lstat(appliedInstallDir)
	return errors.Is(statErr, os.ErrNotExist)
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

// publicUpgradeApplicationComposeReader returns the governed Compose file of
// one selected Standalone-Compose workload project as Apply materialized it
// under .stackkit/runtime/applications/<project>/compose.yaml. Its volume
// names are literal; the private .env beside it supplies secrets only.
func publicUpgradeApplicationComposeReader(workspace string) func(string) ([]byte, error) {
	return func(project string) ([]byte, error) {
		if !localbackuppolicy.ValidComposeVolumeName(project) || strings.Contains(project, "..") {
			return nil, fmt.Errorf("workload Compose project %q is not a portable project name", project)
		}
		path := filepath.Join(workspace, ".stackkit", "runtime", "applications", project, "compose.yaml")
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("read applied workload Compose %s: %w", project, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("applied workload Compose %s is not a regular file", project)
		}
		return os.ReadFile(path) //nolint:gosec // fixed workspace runtime path of a validated project name
	}
}

type publicUpgradeComposeVolumes struct {
	Name     string `yaml:"name"`
	Services map[string]struct {
		Volumes []composeServiceVolume `yaml:"volumes"`
	} `yaml:"services"`
	Volumes map[string]any `yaml:"volumes"`
}

// consumedNamedVolumes returns the top-level named volumes that the project's
// services (except skip) mount.
func (compose publicUpgradeComposeVolumes) consumedNamedVolumes(skip string) (map[string]string, error) {
	consumed := map[string]string{}
	for serviceName, service := range compose.Services {
		if serviceName == skip {
			continue
		}
		for _, mount := range service.Volumes {
			sourceRef, found := mount.namedSource()
			if !found {
				return nil, fmt.Errorf("Compose service %s has a non-canonical volume mount", serviceName)
			}
			if _, named := compose.Volumes[sourceRef]; named {
				consumed[sourceRef] = serviceName
			}
		}
	}
	return consumed, nil
}

// verifyPublicUpgradeManagedVolumeAuthority proves that the CUE-owned Kopia
// allowlist is exactly the union of the managed named volumes the Core Compose
// project and every selected workload Compose project declare and consume,
// and that the Core Kopia service mounts exactly that allowlist read-only.
// The workload projects are the ones the verified policy selects through the
// localbackuppolicy application volume derivation; readApplicationCompose
// returns each project's applied Compose file.
func verifyPublicUpgradeManagedVolumeAuthority(
	composeBytes []byte,
	source localbackuppolicy.Source,
	readApplicationCompose func(project string) ([]byte, error),
) error {
	if len(composeBytes) == 0 {
		return errors.New("verified Basement Compose artifact is required for backup selection")
	}
	var compose publicUpgradeComposeVolumes
	if err := yaml.Unmarshal(composeBytes, &compose); err != nil {
		return fmt.Errorf("decode verified Basement Compose backup authority: %w", err)
	}
	if compose.Name != source.ComposeProject() ||
		len(compose.Services) == 0 ||
		len(compose.Volumes) == 0 {
		return errors.New("verified Basement Compose has no exact project, services, or volumes")
	}
	internal := map[string]struct{}{
		"kopia-repository": {}, "kopia-config": {},
		"kopia-cache": {}, "kopia-restore-staging": {},
	}
	selectedManaged := make(map[string]struct{}, len(source.ManagedVolumeNames))
	for _, fullName := range source.ManagedVolumeNames {
		selectedManaged[fullName] = struct{}{}
	}
	coreConsumed, err := compose.consumedNamedVolumes(localbackuppolicy.ServiceRef)
	if err != nil {
		return err
	}
	managed := map[string]struct{}{}
	coreManaged := 0
	for sourceRef, serviceName := range coreConsumed {
		if _, forbidden := internal[sourceRef]; forbidden {
			return fmt.Errorf("Compose service %s consumes a Kopia-internal volume", serviceName)
		}
		if _, selected := selectedManaged[compose.Name+"_"+sourceRef]; selected {
			managed[compose.Name+"_"+sourceRef] = struct{}{}
			coreManaged++
		}
	}
	if coreManaged == 0 {
		return errors.New("verified Basement Compose has no managed backup volumes")
	}

	// Every selected workload project contributes the allowlisted volumes it
	// declares and mounts. A policy volume that no selected project declares
	// stays outside the derived set and fails the exact comparison below.
	projects := map[string]struct{}{}
	for _, application := range source.ApplicationVolumes {
		projects[application.ComposeProject] = struct{}{}
	}
	for project := range projects {
		if project == compose.Name {
			return fmt.Errorf("workload Compose project %s collides with the Core project", project)
		}
		if readApplicationCompose == nil {
			return fmt.Errorf("applied workload Compose %s is required for backup selection", project)
		}
		raw, readErr := readApplicationCompose(project)
		if readErr != nil {
			return readErr
		}
		var workload publicUpgradeComposeVolumes
		if err := yaml.Unmarshal(raw, &workload); err != nil {
			return fmt.Errorf("decode applied workload Compose %s: %w", project, err)
		}
		if workload.Name != project || len(workload.Services) == 0 {
			return fmt.Errorf("applied workload Compose %s has no exact project or services", project)
		}
		if _, kopia := workload.Services[localbackuppolicy.ServiceRef]; kopia {
			return fmt.Errorf("applied workload Compose %s declares a Kopia service", project)
		}
		consumed, consumedErr := workload.consumedNamedVolumes("")
		if consumedErr != nil {
			return fmt.Errorf("applied workload Compose %s: %w", project, consumedErr)
		}
		for sourceRef := range consumed {
			if _, selected := selectedManaged[project+"_"+sourceRef]; selected {
				managed[project+"_"+sourceRef] = struct{}{}
			}
		}
	}

	wantManagedMounts := map[string]string{}
	managedNames := make([]string, 0, len(managed))
	for fullName := range managed {
		managedNames = append(managedNames, fullName)
		wantManagedMounts[source.HostPath+"/"+fullName+"/_data"] =
			source.ContainerPath + "/" + fullName + "/_data"
	}
	sort.Strings(managedNames)
	allowlist := append([]string(nil), source.ManagedVolumeNames...)
	sort.Strings(allowlist)
	if !equalExactStrings(managedNames, allowlist) {
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
		if mount.long {
			return errors.New("Kopia Compose volume mount is not canonical")
		}
		parts := strings.Split(mount.short, ":")
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

// composeServiceVolume is one entry of a Compose service's volumes list. It
// accepts both the short form ("source:target[:mode]") and the long form
// (a mapping with type, source and target), which the Basement core uses for
// the stackkit-server bind mounts.
type composeServiceVolume struct {
	short  string
	long   bool
	kind   string
	source string
	target string
}

func (volume *composeServiceVolume) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Decode(&volume.short)
	case yaml.MappingNode:
		var entry struct {
			Type   string `yaml:"type"`
			Source string `yaml:"source"`
			Target string `yaml:"target"`
		}
		if err := node.Decode(&entry); err != nil {
			return err
		}
		volume.long = true
		volume.kind = entry.Type
		volume.source = entry.Source
		volume.target = entry.Target
		return nil
	default:
		return fmt.Errorf("unsupported Compose volume entry at line %d", node.Line)
	}
}

// namedSource returns the mount source that may name a top-level Compose
// volume. Long-form bind and tmpfs mounts never reference a named volume and
// return an empty source; long-form volume mounts return their source. The
// boolean is false only for an entry that has no usable shape at all.
func (volume composeServiceVolume) namedSource() (string, bool) {
	if !volume.long {
		sourceRef, _, found := strings.Cut(volume.short, ":")
		return sourceRef, found
	}
	switch volume.kind {
	case "volume":
		return volume.source, volume.source != "" && volume.target != ""
	case "bind", "tmpfs", "npipe", "cluster":
		return "", volume.target != ""
	default:
		return "", false
	}
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

// executorRecoveryServerBlob captures the release's stackkit-server beside
// stackkit so a rollback Apply can run the v2 Core. Releases without one
// return nil.
func executorRecoveryServerBlob(platform releaseindex.Platform, data []byte) *upgradelifecycle.ExecutorStateBlobInput {
	if len(data) == 0 {
		return nil
	}
	path := "stackkit-server"
	if platform.OS == "windows" {
		path += ".exe"
	}
	return &upgradelifecycle.ExecutorStateBlobInput{ID: "stackkit-server", Path: path, Mode: "0755", Data: data}
}

func executorRecoveryBinaryPath(platform releaseindex.Platform) string {
	if platform.OS == "windows" {
		return "stackkit.exe"
	}
	return "stackkit"
}

// requireBeta4CheckpointGenerationTarget stops a beta.4 bridge upgrade before
// its checkpoint unless the historical StackSpec uses the compose target.
// beta.4 generated only Compose; OpenTofu and Terramate installs checkpoint
// through the current-state path.
func requireBeta4CheckpointGenerationTarget(target string) error {
	if target == "compose" {
		return nil
	}
	return fmt.Errorf("upgrade checkpoint: unsupported_state_snapshot: the beta.4 bridge checkpoints only the compose generation target, not %q", target)
}
