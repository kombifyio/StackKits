package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"golang.org/x/mod/semver"
)

// publicUpgradeForwardTimeout bounds one forward completion. A full target
// Apply can outlast the upgrade transaction's own budget, and a forward run
// that times out mid-Apply would only interrupt it again; an earlier caller
// deadline still applies.
const publicUpgradeForwardTimeout = 90 * time.Minute

// forwardPublicUpgradeSession is the recovery-only re-admission of target
// Apply, or of a newer target's Generate, that an explicit lifecycle recovery
// session offers.
type forwardPublicUpgradeSession interface {
	BeginForwardRecoveryJoin(expected, binaryVersion, executableSHA256 string) (string, error)
	BeginForwardRetargetJoin(expected string, target lifecyclemutation.ReleaseAuthority) (string, error)
}

// completePublicUpgradeForward finishes an upgrade interrupted after target
// Apply was admitted with its exact signed target release. It reruns target
// Apply, the documented resume of a killed Apply (ADR-0035), and Verify, then
// commits the same success proof a completed upgrade writes. With retargetTo
// the owner approves a newer verified release instead, for a signed target
// whose own Apply cannot finish: that release is installed like a fresh
// upgrade's target, becomes the signed target and regenerates from the
// Inventory the interrupted target sealed. The prior runtime never starts, so
// live data the target may already have changed is opened only by the same or
// a newer runtime. A failed step leaves the operation active at its signed
// phase for another attempt. It does not repeat an Advanced change set's
// Terramate stack convergence.
func completePublicUpgradeForward(
	ctx context.Context,
	workspace, requestedSpec string,
	record lifecyclemutation.Record,
	mutation publicUpgradeLifecycleSession,
	retargetTo string,
	result *publicUpgradeTransaction,
) error {
	forwarder, ok := mutation.(forwardPublicUpgradeSession)
	if !ok || result == nil {
		return errors.New("forward recovery requires an explicit lifecycle recovery session")
	}
	result.FailedPhase = "forward-recovery"
	result.Target.ReleaseVersion = record.Target.Version
	checkpoint := publicUpgradeCheckpoint{
		OperationID:             record.OperationID,
		KopiaAnchorID:           record.Checkpoint.KopiaAnchorID,
		ExecutorStateSnapshotID: record.Checkpoint.ExecutorStateSnapshotID,
	}
	if err := checkpoint.validate(); err != nil {
		return fmt.Errorf("validate forward recovery checkpoint: %w", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	operationCtx, cancel := context.WithTimeout(ctx, publicUpgradeForwardTimeout)
	defer cancel()
	return withPublicUpgradeTransactionLock(workspace, func(control *confinedfs.Transaction) error {
		snapshot, err := loadPublicUpgradeRecoveryCheckpoint(workspace, checkpoint.ExecutorStateSnapshotID)
		if err != nil {
			return err
		}
		if err := validateExplicitRecoveryAuthority(record, snapshot); err != nil {
			return err
		}
		// A pending backup target move belongs to an Advanced change set,
		// whose own commit or rollback settles it; forward recovery neither
		// commits nor discards it.
		if pending, err := backupcustody.S3TargetMovePending(workspace); err != nil || pending {
			return errors.Join(errors.New("forward recovery refuses an operation with a pending backup target move; recover it without --forward"), err)
		}
		// At a target Generate phase a retarget was interrupted: the signed
		// target, already the newer release, regenerates.
		regenerate := retargetTo == "" && !lifecyclemutation.ForwardRecoveryPhase(record.Phase)
		var receipt releaseindex.Receipt
		if retargetTo == "" {
			receipt, err = forwardPublicUpgradeTargetReceipt(operationCtx, workspace, record.Target)
		} else {
			receipt, err = installForwardRetargetRelease(operationCtx, workspace, retargetTo, record.Target)
		}
		if err != nil {
			return err
		}
		result.Target.ReleaseVersion = receipt.Version
		return withPublicUpgradeInstalledExecutable(operationCtx, receipt, func(binary string) error {
			executableDigest, err := hashPublicUpgradeExecutable(binary)
			if err != nil {
				return fmt.Errorf("hash the exact target executable: %w", err)
			}
			var retarget *lifecyclemutation.ReleaseAuthority
			if retargetTo != "" {
				retarget = &lifecyclemutation.ReleaseAuthority{
					Version:          architectureV2ComponentVersion(receipt.Version),
					ArchiveSHA256:    "sha256:" + receipt.ArchiveSHA256,
					ExecutableSHA256: executableDigest,
				}
			} else if executableDigest != record.Target.ExecutableSHA256 {
				return errors.New("installed target executable differs from the signed lifecycle journal")
			} else if regenerate {
				signed := record.Target
				retarget = &signed
			}
			// An interrupted commit may have written its proof before its
			// journal phase; this run writes it again from its own Verify.
			if err := removePublicUpgradeCommittedSuccess(control, record.OperationID); err != nil {
				return fmt.Errorf("remove stale upgrade success authority before forward recovery: %w", err)
			}
			return runPublicUpgradeForward(
				operationCtx, workspace, requestedSpec, binary, executableDigest,
				record, snapshot, receipt, retarget, mutation, forwarder, control, result,
			)
		})
	})
}

// installForwardRetargetRelease resolves and installs the owner-approved
// newer release through the verified public Release Index, exactly as a fresh
// upgrade installs its target. It refuses a release that is not newer than
// the signed target.
func installForwardRetargetRelease(
	ctx context.Context,
	workspace, to string,
	signed lifecyclemutation.ReleaseAuthority,
) (releaseindex.Receipt, error) {
	if signed.Authority != "" || !semver.IsValid("v"+signed.Version) {
		return releaseindex.Receipt{}, errors.New(
			"forward retarget needs an upgrade whose signed target came from the verified workspace release cache",
		)
	}
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return releaseindex.Receipt{}, fmt.Errorf("load StackKit identity before forward retarget: %w", err)
	}
	// --recover --forward is the owner's approval of this retarget.
	if _, err := admitPublicUpgradeKit(kit, false, true); err != nil {
		return releaseindex.Receipt{}, err
	}
	source := newPublicReleaseSource()
	attestations := newPublicAttestationVerifier()
	resolution, err := (releaseindex.Resolver{Source: source, Attestations: attestations}).Resolve(ctx, releaseindex.ResolveRequest{
		Kit: kit, Target: to, OS: currentReleasePlatform().OS, Arch: currentReleasePlatform().Arch,
	})
	if err != nil {
		return releaseindex.Receipt{}, fmt.Errorf("resolve the forward retarget release: %w", err)
	}
	if semver.Compare("v"+architectureV2ComponentVersion(resolution.Asset.Version), "v"+signed.Version) <= 0 {
		return releaseindex.Receipt{}, fmt.Errorf(
			"forward retarget release %s is not newer than the signed target %s", resolution.Asset.Version, signed.Version,
		)
	}
	receipt, err := installPublicUpgradeRelease(ctx, source, attestations, resolution, workspace)
	if err != nil {
		return releaseindex.Receipt{}, fmt.Errorf("install the verified forward retarget release: %w", err)
	}
	return receipt, nil
}

func runPublicUpgradeForward(
	ctx context.Context,
	workspace, requestedSpec, binary, executableDigest string,
	record lifecyclemutation.Record,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
	receipt releaseindex.Receipt,
	retarget *lifecyclemutation.ReleaseAuthority,
	mutation publicUpgradeLifecycleSession,
	forwarder forwardPublicUpgradeSession,
	control *confinedfs.Transaction,
	result *publicUpgradeTransaction,
) error {
	operationID := record.OperationID
	targetVersion := record.Target.Version
	livePlan, err := forwardPublicUpgradeLivePlan(workspace, snapshot)
	if err != nil {
		return err
	}
	// The target Generate sealed its Inventory into the plan it wrote; a
	// retarget Generate, Apply and Verify resolve exactly that document again.
	inventoryPath, cleanupInventory, err := materializePlanInventory(livePlan)
	if err != nil {
		return fmt.Errorf("materialize the target plan's sealed Inventory: %w", err)
	}
	defer cleanupInventory()
	withInventory := func(args []string) []string {
		if inventoryPath != "" {
			return append(args, "--inventory", inventoryPath)
		}
		return args
	}
	runner := newPublicUpgradeTransactionRunner()
	common := publicUpgradeCommandPrefix(workspace, requestedSpec)
	if retarget != nil {
		targetVersion = retarget.Version
		result.Target.GenerateInvoked = true
		emitPublicUpgradeTransactionEvent(operationID, "forward-generate", "started")
		generateNonce, err := forwarder.BeginForwardRetargetJoin(mutation.Record().Phase, *retarget)
		if err != nil {
			return &publicUpgradePhaseError{phase: "target-generate-journal", cause: err}
		}
		generateArgs := append(
			append(common, lifecycleChildFlags(
				operationID, lifecyclemutation.PhaseTargetGenerateStarted, generateNonce,
			)...),
			"generate",
		)
		if _, err := runner.Run(ctx, binary, withInventory(generateArgs), workspace); err != nil {
			emitPublicUpgradeTransactionEvent(operationID, "forward-generate", "failed")
			return &publicUpgradePhaseError{phase: "target-generate", cause: err}
		}
		if err := mutation.Transition(
			lifecyclemutation.PhaseTargetGenerateStarted,
			lifecyclemutation.PhaseTargetGenerateSucceeded,
		); err != nil {
			return &publicUpgradePhaseError{phase: "target-generate-journal", cause: err}
		}
		emitPublicUpgradeTransactionEvent(operationID, "forward-generate", "succeeded")
		if livePlan, err = forwardPublicUpgradeLivePlan(workspace, snapshot); err != nil {
			return err
		}
	}
	rawPlan, err := runner.Run(ctx, binary, withInventory(append(common, "plan", "--json")), workspace)
	if err != nil {
		return &publicUpgradePhaseError{phase: "target-plan", cause: err}
	}
	var plan generationartifact.PlanInspection
	if err := decodeUpgradeExactJSON(rawPlan, &plan); err != nil {
		return &publicUpgradePhaseError{phase: "target-plan", cause: fmt.Errorf("decode exact target plan: %w", err)}
	}
	if err := validateUpgradePlanInspection(plan, "forward target"); err != nil {
		return &publicUpgradePhaseError{phase: "target-plan", cause: err}
	}
	if _, err := generationartifact.ParseHistoricalPlan(livePlan, plan); err != nil {
		return &publicUpgradePhaseError{phase: "target-plan", cause: fmt.Errorf("target plan differs from its generated ResolvedPlan: %w", err)}
	}
	result.Target.PlanHash = plan.Binding.PlanHash
	result.Target.ManifestHash = plan.Manifest.Hash
	result.Target.PlanVerified = true

	checkpointed, checkpointedRaw, err := forwardCheckpointedOwnerBinding(workspace, snapshot)
	if err != nil {
		return err
	}
	undoReinstatement, err := reinstateForwardOwnerBinding(
		workspace, targetVersion, snapshot, checkpointed, checkpointedRaw,
	)
	if err != nil {
		return err
	}
	applyErr := func(cause error) error { return errors.Join(cause, undoReinstatement()) }

	result.Target.ApplyInvoked = true
	emitPublicUpgradeTransactionEvent(operationID, "forward-apply", "started")
	var applyNonce string
	if retarget != nil {
		applyNonce, err = mutation.BeginJoin(
			lifecyclemutation.PhaseTargetGenerateSucceeded,
			lifecyclemutation.PhaseTargetApplyStarted,
			"apply", targetVersion, executableDigest,
		)
	} else {
		applyNonce, err = forwarder.BeginForwardRecoveryJoin(
			mutation.Record().Phase, targetVersion, executableDigest,
		)
	}
	if err != nil {
		return applyErr(&publicUpgradePhaseError{phase: "target-apply-journal", cause: err})
	}
	applyArgs := append(
		append(common, lifecycleChildFlags(
			operationID, lifecyclemutation.PhaseTargetApplyStarted, applyNonce,
		)...),
		"apply", "--auto-approve",
	)
	if _, err := runner.Run(ctx, binary, withInventory(applyArgs), workspace); err != nil {
		emitPublicUpgradeTransactionEvent(operationID, "forward-apply", "failed")
		return applyErr(&publicUpgradePhaseError{phase: "target-apply", cause: err})
	}
	if err := mutation.Transition(
		lifecyclemutation.PhaseTargetApplyStarted,
		lifecyclemutation.PhaseTargetApplySucceeded,
	); err != nil {
		return &publicUpgradePhaseError{phase: "target-apply-journal", cause: err}
	}
	emitPublicUpgradeTransactionEvent(operationID, "forward-apply", "succeeded")

	result.Target.VerifyInvoked = true
	emitPublicUpgradeTransactionEvent(operationID, "forward-verify", "started")
	verifyNonce, err := mutation.BeginJoin(
		lifecyclemutation.PhaseTargetApplySucceeded,
		lifecyclemutation.PhaseTargetVerifyStarted,
		"verify", targetVersion, executableDigest,
	)
	if err != nil {
		return &publicUpgradePhaseError{phase: "target-verify-journal", cause: err}
	}
	verifyArgs := append(
		append(common, lifecycleChildFlags(
			operationID, lifecyclemutation.PhaseTargetVerifyStarted, verifyNonce,
		)...),
		"verify", "--json",
	)
	rawVerify, err := runner.Run(ctx, binary, withInventory(verifyArgs), workspace)
	if err != nil {
		emitPublicUpgradeTransactionEvent(operationID, "forward-verify", "failed")
		return &publicUpgradePhaseError{phase: "target-verify", cause: err}
	}
	ownerBindingDigest, err := forwardVerifiedOwnerBindingDigest(workspace, snapshot, checkpointed, rawVerify)
	if err != nil {
		emitPublicUpgradeTransactionEvent(operationID, "forward-verify", "failed")
		return &publicUpgradePhaseError{phase: "target-verify", cause: err}
	}
	report, err := decodeAndValidateUpgradeVerify(
		rawVerify, plan.Binding.PlanHash, receipt,
		snapshot.OwnerRef, ownerBindingDigest,
	)
	if err != nil {
		emitPublicUpgradeTransactionEvent(operationID, "forward-verify", "failed")
		return &publicUpgradePhaseError{phase: "target-verify", cause: err}
	}
	result.Target.ApplyResultHash = report.Apply.ResultHash
	result.Target.EvidenceBundleHash = report.Apply.EvidenceBundleHash
	result.Target.OwnerRef = report.Owner.OwnerRef
	result.Target.OwnerBindingHash = report.Owner.OwnerBindingDigest
	result.Target.Verified = true
	for _, phase := range [][2]string{
		{lifecyclemutation.PhaseTargetVerifyStarted, lifecyclemutation.PhaseTargetVerifySucceeded},
		{lifecyclemutation.PhaseTargetVerifySucceeded, lifecyclemutation.PhaseCommitStarted},
	} {
		if err := mutation.Transition(phase[0], phase[1]); err != nil {
			return &publicUpgradePhaseError{phase: "commit-journal", cause: err}
		}
	}
	emitPublicUpgradeTransactionEvent(operationID, "forward-verify", "succeeded")

	if err := reverifyPublicUpgradeInstalledReceipt(ctx, receipt); err != nil {
		return &publicUpgradePhaseError{
			phase: "target-release-reverify",
			cause: fmt.Errorf("reverify exact installed target after live verification: %w", err),
		}
	}
	now := time.Now().UTC()
	proof := publicUpgradeSuccessAuthority{
		APIVersion:         publicUpgradeTransactionAPIVersion,
		OperationID:        operationID,
		TargetVersion:      receipt.Version,
		TargetArchiveHash:  "sha256:" + receipt.ArchiveSHA256,
		PlanHash:           result.Target.PlanHash,
		ManifestHash:       result.Target.ManifestHash,
		ApplyResultHash:    result.Target.ApplyResultHash,
		EvidenceBundleHash: result.Target.EvidenceBundleHash,
		OwnerRef:           result.Target.OwnerRef,
		OwnerBindingHash:   result.Target.OwnerBindingHash,
		VerifiedAt:         now,
	}
	if err := commitPublicUpgradeSuccess(control, proof); err != nil {
		return &publicUpgradePhaseError{phase: "commit", cause: fmt.Errorf("commit verified upgrade authority: %w", err)}
	}
	if err := mutation.Transition(
		lifecyclemutation.PhaseCommitStarted,
		lifecyclemutation.PhaseCommitSucceeded,
	); err != nil {
		return &publicUpgradePhaseError{phase: "commit-journal", cause: err}
	}
	if err := mutation.Complete(lifecyclemutation.StatusSucceeded); err != nil {
		return &publicUpgradePhaseError{phase: "commit-journal", cause: err}
	}
	result.CommittedAt = &now
	result.SuccessProof = &proof
	emitPublicUpgradeTransactionEvent(operationID, "upgrade", "succeeded")
	return nil
}

// forwardPublicUpgradeTargetReceipt re-verifies the workspace release cache
// installation that the signed journal names as the target. A running-
// executable target has no cached installation the recovery executable could
// prove, so it is refused.
func forwardPublicUpgradeTargetReceipt(
	ctx context.Context,
	workspace string,
	target lifecyclemutation.ReleaseAuthority,
) (releaseindex.Receipt, error) {
	if target.Authority != "" {
		return releaseindex.Receipt{}, errors.New(
			"forward recovery needs the target's verified workspace release cache; this operation's target was the executable that ran it",
		)
	}
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return releaseindex.Receipt{}, fmt.Errorf("load StackKit identity before forward recovery: %w", err)
	}
	_, installDir, err := appliedPublicUpgradeReleasePath(workspace, kit, target.Version, currentReleasePlatform())
	if err != nil {
		return releaseindex.Receipt{}, err
	}
	receipt, err := (releaseindex.Installer{
		Attestations: newPublicAttestationVerifier(),
	}).VerifyInstalled(ctx, installDir)
	if err != nil {
		return releaseindex.Receipt{}, fmt.Errorf("verify the cached target release: %w", err)
	}
	if receipt.Kit != kit ||
		architectureV2ComponentVersion(receipt.Version) != target.Version ||
		"sha256:"+receipt.ArchiveSHA256 != target.ArchiveSHA256 {
		return releaseindex.Receipt{}, errors.New("cached target release differs from the signed lifecycle journal")
	}
	return receipt, nil
}

// forwardCheckpointedOwnerBinding returns the Owner runtime binding the
// checkpoint retained and its exact bytes, or nil for a checkpoint without
// one.
func forwardCheckpointedOwnerBinding(
	workspace string,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
) (*localevidence.OwnerRuntimeBinding, []byte, error) {
	retained := false
	for _, artifact := range snapshot.Artifacts {
		retained = retained || artifact.ID == upgradelifecycle.OwnerRuntimeBindingArtifactID
	}
	if !retained {
		return nil, nil, nil
	}
	raw, err := (upgradelifecycle.ExecutorStateStore{}).ReadArtifact(
		workspace, snapshot.ID, upgradelifecycle.OwnerRuntimeBindingArtifactID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("read the checkpointed Owner runtime binding: %w", err)
	}
	binding, err := localevidence.ParseOwnerRuntimeBinding(workspace, raw)
	if err != nil {
		return nil, nil, fmt.Errorf("verify the checkpointed Owner runtime binding: %w", err)
	}
	return &binding, raw, nil
}

// lastIncarnationDigestRelease is the last release whose Owner binding digest
// covered the PocketID runtime incarnation (#2562 changed the rule after it).
const lastIncarnationDigestRelease = "v0.54.19"

// reinstateForwardOwnerBinding lets a target released up to
// lastIncarnationDigestRelease load its own checkpoint again. Such a target
// compares the incarnation digest of the live binding with the lineage, and
// its interrupted Apply already re-signed the binding while it replaced
// PocketID. The checkpoint retained the exact owner-signed binding the lineage
// names; it is reinstated only when it names the same owner identity. The
// target's PocketID transition then re-observes the real container, so a stale
// incarnation is never trusted; a PocketID still running is drained and
// recreated by that transition. The returned undo puts the saved live binding
// back when the target failed before it re-observed PocketID.
func reinstateForwardOwnerBinding(
	workspace, targetVersion string,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
	checkpointed *localevidence.OwnerRuntimeBinding,
	raw []byte,
) (func() error, error) {
	noUndo := func() error { return nil }
	lineage := snapshot.Lineage.OwnerBindingDigest
	if checkpointed == nil ||
		semver.Compare("v"+targetVersion, lastIncarnationDigestRelease) > 0 ||
		localevidence.OwnerRuntimeBindingDigest(*checkpointed) == lineage ||
		localevidence.IncarnationOwnerRuntimeBindingDigest(*checkpointed) != lineage {
		return noUndo, nil
	}
	current, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil {
		return nil, err
	}
	if localevidence.IncarnationOwnerRuntimeBindingDigest(current) == lineage {
		return noUndo, nil
	}
	// The live binding is canonical, so this is its exact file content.
	saved, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := localevidence.ReinstateOwnerRuntimeBinding(workspace, raw); err != nil {
		return nil, fmt.Errorf("reinstate the checkpointed Owner runtime binding for the target Apply: %w", err)
	}
	return func() error {
		after, err := localevidence.LoadOwnerRuntimeBinding(workspace)
		if err != nil {
			return err
		}
		if localevidence.IncarnationOwnerRuntimeBindingDigest(after) != lineage {
			return nil
		}
		if err := localevidence.ReinstateOwnerRuntimeBinding(workspace, saved); err != nil {
			return fmt.Errorf("restore the live Owner runtime binding after the failed target Apply: %w", err)
		}
		return nil
	}, nil
}

// forwardVerifiedOwnerBindingDigest returns the Owner binding digest the
// target Verify must report: the live binding's under either digest rule,
// since a target released before the identity-only rule reports the
// incarnation digest. The live binding must still name the checkpoint's
// owner identity.
func forwardVerifiedOwnerBindingDigest(
	workspace string,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
	checkpointed *localevidence.OwnerRuntimeBinding,
	rawVerify []byte,
) (string, error) {
	var envelope publicUpgradeRawCommandResult
	if err := decodeUpgradeExactJSON(rawVerify, &envelope); err != nil {
		return "", fmt.Errorf("decode command result: %w", err)
	}
	var report architectureV2VerifyReport
	if err := decodeUpgradeExactJSON(envelope.Data, &report); err != nil {
		return "", fmt.Errorf("decode verify authority: %w", err)
	}
	current, err := localevidence.LoadOwnerRuntimeBinding(workspace)
	if err != nil {
		return "", err
	}
	if !upgradelifecycle.OwnerBindingMatchesLineage(current, snapshot.Lineage.OwnerBindingDigest, checkpointed) {
		return "", errors.New("the live Owner runtime binding names an owner other than the checkpoint's")
	}
	for _, digest := range []string{
		localevidence.OwnerRuntimeBindingDigest(current),
		localevidence.IncarnationOwnerRuntimeBindingDigest(current),
	} {
		if report.Owner.OwnerBindingDigest == digest {
			return digest, nil
		}
	}
	return "", errors.New("target verify reported an Owner runtime binding other than the live one")
}

// forwardPublicUpgradeLivePlan reads the canonical ResolvedPlan the target
// Generate wrote at the output root the checkpoint recorded.
func forwardPublicUpgradeLivePlan(
	workspace string,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
) ([]byte, error) {
	relative := ""
	for _, artifact := range snapshot.Artifacts {
		if artifact.ID == "resolved-plan" {
			relative = artifact.Path
		}
	}
	if relative == "" || !strings.HasSuffix(relative, "/.stackkit/resolved-plan.json") {
		return nil, errors.New("checkpoint records no generated ResolvedPlan location")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Close() }()
	data, info, err := transaction.ReadStable(relative)
	if err != nil {
		return nil, fmt.Errorf("read the target ResolvedPlan: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("target ResolvedPlan is not a regular file")
	}
	if _, err := resolvedplan.DecodeCanonicalPlan(data); err != nil {
		return nil, fmt.Errorf("decode the target ResolvedPlan: %w", err)
	}
	return data, nil
}
