package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/advancedrollback"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/kombifyio/stackkits/internal/terramatehost"
	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"github.com/spf13/cobra"
)

// coordinatedRollback is one rollback of the local host to one verified
// checkpoint under an active upgrade-kind lifecycle mutation. The standalone
// `advanced rollback run` begins or resumes that mutation; a failed change
// set hands over its own.
//
// Journal phases: rollback-started (plan persisted), rollback-generate
// (checkpoint StackSpec and Inventory restored, then a joined `generate`),
// after rollback-generate-done the per-stack destroy, restore and forced
// convergence in reverse run order (resumable per stack), rollback-apply (a
// joined `apply` of the regenerated checkpoint generation, which records its
// signed Apply result), rollback-verify (joined native `verify` of the
// checkpoint plan), rollback-succeeded. Every child phase is entered with its
// one-use join authority, exactly as the upgrade rollback does.
type coordinatedRollback struct {
	workspace   string
	session     publicUpgradeLifecycleSession
	release     *lifecycleReleaseAuthority
	custody     upgradelifecycle.ExecutorStateRollbackCustody
	current     *terramatehost.Layout
	target      terramatehost.Layout
	tools       terramatehost.Tools
	changeSetID string
	rollbackID  string
	report      advancedrollback.Report
	verified    architectureV2VerifyReport
}

func newAdvancedRollbackReport(snapshotID, changeSetID string) advancedrollback.Report {
	return advancedrollback.Report{
		SchemaVersion: advancedrollback.ResultSchemaVersion, TargetSnapshotID: snapshotID,
		ChangeSetID: changeSetID, Order: []string{}, Stacks: []advancedrollback.StackReport{},
		SealStatus: advancedrollback.SealNotAttempted, Status: advancedrollback.StatusFailed,
	}
}

func advancedRollbackEvent(phase, status string, attributes map[string]string) {
	rolloutEvent(advancedRollbackRolloutPrefix+phase, status, "advanced rollback "+phase+" "+status, attributes)
}

func (rollback *coordinatedRollback) run(ctx context.Context) error {
	snapshot := rollback.custody.Snapshot
	record := rollback.session.Record()
	switch record.Phase {
	case lifecyclemutation.PhaseCommitSucceeded:
		return fmt.Errorf("operation %s already committed; roll it back with a new advanced rollback run", record.OperationID)
	case lifecyclemutation.PhaseRollbackGenerateStarted, lifecyclemutation.PhaseRollbackApplyStarted,
		lifecyclemutation.PhaseRollbackVerifyStarted:
		// A joined child's one-use admission may already be consumed.
		return fmt.Errorf("operation %s stopped inside the joined %s child; finish it with explicit upgrade recovery", record.OperationID, record.Phase)
	}
	if !strings.HasPrefix(record.Phase, "rollback-") {
		if err := rollback.session.Transition(record.Phase, lifecyclemutation.PhaseRollbackStarted); err != nil {
			return fmt.Errorf("start durable coordinated rollback: %w", err)
		}
	}

	request := advancedrollback.Request{
		WorkspaceRoot: rollback.workspace, RollbackID: rollback.rollbackID,
		TargetSnapshotID: snapshot.ID, ChangeSetID: rollback.changeSetID,
		LifecycleOperationID: rollback.session.Record().OperationID,
		Target:               advancedRollbackStacks(rollback.target),
		TargetRoots:          advancedRollbackTargetRoots(rollback.custody),
		Tools:                rollback.tools,
		Environment:          advancedRollbackStackEnvironment(rollback.workspace),
		Timeout:              backupLongOperationTimeout,
		Event:                advancedRollbackEvent,
	}
	if rollback.current != nil {
		request.Current = advancedRollbackStacks(*rollback.current)
	}
	advancedRollbackEvent("plan", "started", map[string]string{"targetSnapshotId": snapshot.ID})
	journal, resumed, err := advancedrollback.Prepare(request)
	if err != nil {
		rolloutFailure(advancedRollbackRolloutPrefix+"plan", err)
		return err
	}
	if journal.LifecycleOperationID != request.LifecycleOperationID {
		return fmt.Errorf("an unfinished rollback %s to this checkpoint belongs to lifecycle operation %s; resume it with advanced rollback run", journal.RollbackID, journal.LifecycleOperationID)
	}
	if !resumed && rollback.current == nil {
		return errors.New("a new coordinated rollback requires the current Terramate host layout")
	}
	rollback.report.RollbackID = journal.RollbackID
	rollback.report.LifecycleOperationID = journal.LifecycleOperationID
	rollback.report.Resumed = resumed
	advancedRollbackEvent("plan", "succeeded", map[string]string{
		"rollbackId": journal.RollbackID, "stacks": fmt.Sprint(len(journal.Steps)), "resumed": fmt.Sprint(resumed),
	})
	// Stack files of every stack either graph names must exist before
	// `terramate run` enters its root: the current layout for destroyed
	// stacks, the checkpoint layout for restored and recreated ones.
	if rollback.current != nil {
		if _, err := terramatehost.Materialize(rollback.workspace, *rollback.current); err != nil {
			return err
		}
	}
	if _, err := terramatehost.Materialize(rollback.workspace, rollback.target); err != nil {
		return err
	}

	advancedRollbackEvent("restore-authority", "started", map[string]string{"targetSnapshotId": snapshot.ID})
	_, err = (upgradelifecycle.ExecutorStateStore{}).RecoverWith(
		ctx, rollback.workspace, snapshot.ID,
		upgradelifecycle.RecoveryOptions{ReplaceAuthority: true, SkipOpenTofuRoots: true},
		func(recoveryContext context.Context, priorBinary string, recovered upgradelifecycle.ExecutorStateSnapshot) error {
			if recovered.ID != snapshot.ID || !reflect.DeepEqual(recovered.Release, snapshot.Release) {
				return errors.New("recovered executor snapshot differs from the rollback checkpoint")
			}
			rollback.report.AuthorityRestored = true
			advancedRollbackEvent("restore-authority", "succeeded", map[string]string{"targetSnapshotId": snapshot.ID})
			if !advancedRollbackSameRelease(snapshot, rollback.release) {
				// The checkpoint's release differs from the running one: its
				// captured executable regenerates and verifies, as the
				// existing upgrade rollback does.
				rollback.report.PriorReleaseExecuted = true
				return rollback.phases(recoveryContext, priorBinary, snapshot.Release.Version,
					snapshotVerifyReceipt(snapshot), request, journal, resumed)
			}
			return rollback.release.withExecutable(recoveryContext, func(binary string) error {
				return rollback.phases(recoveryContext, binary, rollback.release.version(),
					rollback.release.verifyReceipt(), request, journal, resumed)
			})
		},
	)
	if err != nil {
		return err
	}
	if rollback.session.Record().Phase != lifecyclemutation.PhaseRollbackSucceeded {
		return errors.New("coordinated rollback did not reach its verified phase")
	}
	if err := rollback.session.Complete(lifecyclemutation.StatusRecovered); err != nil {
		return fmt.Errorf("complete recovered lifecycle mutation: %w", err)
	}
	rollback.report.Status = advancedrollback.StatusConverged
	return nil
}

func (rollback *coordinatedRollback) phases(
	ctx context.Context,
	binary string,
	binaryVersion string,
	verifyReceipt *releaseindex.Receipt,
	request advancedrollback.Request,
	journal advancedrollback.Journal,
	resumed bool,
) error {
	session := rollback.session
	snapshot := rollback.custody.Snapshot
	operationID := session.Record().OperationID
	runner := newPublicUpgradeTransactionRunner()
	common := publicUpgradeCommandPrefix(rollback.workspace, specFile)
	digest, err := hashPublicUpgradeExecutable(binary)
	if err != nil {
		return fmt.Errorf("hash rollback executable: %w", err)
	}
	componentVersion := architectureV2ComponentVersion(binaryVersion)

	if session.Record().Phase == lifecyclemutation.PhaseRollbackStarted {
		advancedRollbackEvent("generate", "started", nil)
		nonce, err := session.BeginJoin(
			lifecyclemutation.PhaseRollbackStarted, lifecyclemutation.PhaseRollbackGenerateStarted,
			"generate", componentVersion, digest,
		)
		if err != nil {
			return fmt.Errorf("authorize rollback generate: %w", err)
		}
		// The checkpoint plan records the exact Inventory it was generated
		// from; a plain generate would re-measure free disk and could never
		// reproduce the checkpoint plan hash its verify requires.
		generateCommand, cleanupInventory, err := advancedRollbackGenerateCommand(rollback.custody)
		if err != nil {
			return err
		}
		defer cleanupInventory()
		if _, err := runner.Run(ctx, binary, append(append(common,
			lifecycleChildFlags(operationID, lifecyclemutation.PhaseRollbackGenerateStarted, nonce)...), generateCommand...),
			rollback.workspace); err != nil {
			rolloutFailure(advancedRollbackRolloutPrefix+"generate", err)
			return fmt.Errorf("rollback generate: %w", err)
		}
		if err := session.Transition(lifecyclemutation.PhaseRollbackGenerateStarted, lifecyclemutation.PhaseRollbackGenerateDone); err != nil {
			return err
		}
		advancedRollbackEvent("generate", "succeeded", nil)
	}
	if session.Record().Phase == lifecyclemutation.PhaseRollbackGenerateDone {
		// The per-stack convergence runs in process after the checkpoint
		// generation exists and before the joined apply, so an interrupted
		// run resumes it here and skips the stacks that already converged.
		// The release being restored stages its own stackkit-server.
		request.Native = advancedRollbackNativeSteps{
			workspace: rollback.workspace, executable: binary, artifacts: rollback.custody.Artifacts,
		}
		report, err := advancedrollback.Execute(ctx, request, journal, resumed)
		rollback.mergeStackReport(report)
		if err != nil {
			rolloutFailure(advancedRollbackRolloutPrefix+"stacks", err)
			return err
		}
		// The regenerated checkpoint generation has a new generation
		// receipt, so the checkpoint's own Apply result no longer binds to
		// it. A joined `apply` of the converged stacks records the signed
		// Apply result the rollback verify, drift detection and backup
		// configuration read, as the upgrade rollback's joined apply does.
		advancedRollbackEvent("apply", "started", nil)
		nonce, err := session.BeginJoin(
			lifecyclemutation.PhaseRollbackGenerateDone, lifecyclemutation.PhaseRollbackApplyStarted,
			"apply", componentVersion, digest,
		)
		if err != nil {
			return fmt.Errorf("authorize rollback apply: %w", err)
		}
		if _, err := runner.Run(ctx, binary, append(append(common,
			lifecycleChildFlags(operationID, lifecyclemutation.PhaseRollbackApplyStarted, nonce)...), "apply", "--auto-approve"),
			rollback.workspace); err != nil {
			rolloutFailure(advancedRollbackRolloutPrefix+"apply", err)
			return fmt.Errorf("rollback apply: %w", err)
		}
		if err := session.Transition(lifecyclemutation.PhaseRollbackApplyStarted, lifecyclemutation.PhaseRollbackApplyDone); err != nil {
			return err
		}
		advancedRollbackEvent("apply", "succeeded", nil)
	} else if loaded, found, err := advancedrollback.LoadJournal(rollback.workspace, snapshot.ID); err == nil && found {
		// A rollback resumed after its stacks converged still reports them.
		report, _ := advancedrollback.Execute(ctx, request, loaded, true)
		rollback.mergeStackReport(report)
	}
	if session.Record().Phase == lifecyclemutation.PhaseRollbackApplyDone {
		advancedRollbackEvent("verify", "started", nil)
		nonce, err := session.BeginJoin(
			lifecyclemutation.PhaseRollbackApplyDone, lifecyclemutation.PhaseRollbackVerifyStarted,
			"verify", componentVersion, digest,
		)
		if err != nil {
			return fmt.Errorf("authorize rollback verify: %w", err)
		}
		raw, err := runner.Run(ctx, binary, append(append(common,
			lifecycleChildFlags(operationID, lifecyclemutation.PhaseRollbackVerifyStarted, nonce)...), "verify", "--json"),
			rollback.workspace)
		if err != nil {
			rolloutFailure(advancedRollbackRolloutPrefix+"verify", err)
			return fmt.Errorf("rollback verify: %w", err)
		}
		verified, err := decodeAndValidateVerifyReport(
			raw, snapshot.Lineage.Binding.PlanHash, verifyReceipt,
			snapshot.OwnerRef, snapshot.Lineage.OwnerBindingDigest,
		)
		if err != nil {
			rolloutFailure(advancedRollbackRolloutPrefix+"verify", err)
			return fmt.Errorf("validate rollback verification: %w", err)
		}
		rollback.verified = verified
		rollback.report.RuntimeVerified = true
		if err := session.Transition(lifecyclemutation.PhaseRollbackVerifyStarted, lifecyclemutation.PhaseRollbackVerifyDone); err != nil {
			return err
		}
		advancedRollbackEvent("verify", "succeeded", nil)
	}
	if session.Record().Phase == lifecyclemutation.PhaseRollbackVerifyDone {
		return session.Transition(lifecyclemutation.PhaseRollbackVerifyDone, lifecyclemutation.PhaseRollbackSucceeded)
	}
	return nil
}

// advancedRollbackGenerateCommand is the joined rollback generate, bound to
// the Inventory document of the checkpoint's own ResolvedPlan when the
// checkpoint captured one.
func advancedRollbackGenerateCommand(custody upgradelifecycle.ExecutorStateRollbackCustody) ([]string, func(), error) {
	plan, found := custody.Artifacts[nativehost.ResolvedPlanArtifactID]
	if !found {
		return []string{"generate"}, func() {}, nil
	}
	path, cleanup, err := materializePlanInventory(plan)
	if err != nil {
		return nil, nil, fmt.Errorf("materialize the checkpoint Inventory: %w", err)
	}
	if path == "" {
		return []string{"generate"}, cleanup, nil
	}
	return []string{"generate", "--inventory", path}, cleanup, nil
}

func (rollback *coordinatedRollback) mergeStackReport(report advancedrollback.Report) {
	rollback.report.Order = report.Order
	rollback.report.Stacks = report.Stacks
	if report.RollbackID != "" {
		rollback.report.RollbackID = report.RollbackID
	}
}

// executeAdvancedRollback is the standalone `advanced rollback run`. It
// resumes the unfinished rollback to the same checkpoint or begins a new
// upgrade-kind lifecycle mutation whose checkpoint is the target.
func executeAdvancedRollback(
	ctx context.Context,
	cmd *cobra.Command,
	request advancedRollbackRequest,
) (advancedrollback.Report, error) {
	workspace := request.workspace
	advancedRollbackEvent("resolve-target", "started", map[string]string{"to": request.to})
	snapshotID, changeSetID, err := resolveAdvancedRollbackTarget(workspace, request.to)
	report := newAdvancedRollbackReport(snapshotID, changeSetID)
	if err != nil {
		rolloutFailure(advancedRollbackRolloutPrefix+"resolve-target", err)
		return report, err
	}
	custody, err := (upgradelifecycle.ExecutorStateStore{}).LoadRollbackCustody(workspace, snapshotID)
	if err != nil {
		rolloutFailure(advancedRollbackRolloutPrefix+"resolve-target", err)
		return report, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return report, fmt.Errorf("verify local Owner custody: %w", err)
	}
	if custody.Snapshot.OwnerRef != owner.OwnerRef {
		return report, errors.New("rollback checkpoint belongs to another Owner")
	}
	target, err := advancedRollbackTargetLayout(custody, owner.Binding.SiteRef, owner.Binding.NodeRef)
	if err != nil {
		return report, err
	}
	advancedRollbackEvent("resolve-target", "succeeded", map[string]string{"targetSnapshotId": snapshotID})
	release, err := currentLifecycleReleaseAuthority(cmd, workspace)
	if err != nil {
		return report, err
	}

	rollback := &coordinatedRollback{
		workspace: workspace, release: &release, custody: custody, target: target,
		tools: request.tools, changeSetID: changeSetID, report: report,
	}
	journal, found, err := advancedrollback.LoadJournal(workspace, snapshotID)
	if err != nil {
		return report, err
	}
	var session publicUpgradeLifecycleSession
	if found && journal.InProgress() {
		recovered, _, openErr := lifecyclemutation.OpenUpgradeRecovery(workspace, journal.LifecycleOperationID)
		if openErr != nil {
			return report, fmt.Errorf("resume coordinated rollback %s: %w", journal.RollbackID, openErr)
		}
		session = recovered
		rollback.rollbackID = journal.RollbackID
		rollback.changeSetID = journal.ChangeSetID
		rollback.report.ChangeSetID = journal.ChangeSetID
		if session.Record().Status != lifecyclemutation.StatusActive {
			_ = session.Close()
			return report, fmt.Errorf("coordinated rollback %s has no active lifecycle mutation to resume", journal.RollbackID)
		}
	} else {
		current, layoutErr := currentAdvancedRollbackLayout(workspace, owner.Binding.SiteRef, owner.Binding.NodeRef)
		if layoutErr != nil {
			return report, layoutErr
		}
		rollback.current = &current
		// The plan is computed once without writing before the lifecycle
		// mutation begins, so an unplannable workspace fails before it can
		// leave an active mutation behind.
		if _, planErr := advancedrollback.Plan(advancedrollback.Request{
			WorkspaceRoot: workspace, Current: advancedRollbackStacks(current),
			Target: advancedRollbackStacks(target), TargetRoots: advancedRollbackTargetRoots(custody),
		}); planErr != nil {
			return report, planErr
		}
		rollback.rollbackID = "rollback-" + strings.TrimPrefix(snapshotID, "sha256:")[:16] + "-" +
			strings.ToLower(request.now.Format("20060102t150405z"))
		if err := release.withExecutable(ctx, func(string) error { return nil }); err != nil {
			return report, err
		}
		begun, beginErr := beginPublicUpgradeMutation(workspace, func() (lifecyclemutation.BeginRequest, error) {
			return lifecyclemutation.BeginRequest{
				OperationID: rollback.rollbackID, OwnerRef: custody.Snapshot.OwnerRef,
				Checkpoint: lifecyclemutation.CheckpointAuthority{
					ExecutorStateSnapshotID: custody.Snapshot.ID,
					KopiaAnchorID:           custody.Snapshot.KopiaSnapshotAnchor.ID,
				},
				Target: release.journal(release.record.SHA256),
				Prior:  priorReleaseAuthority(custody.Snapshot),
			}, nil
		})
		if beginErr != nil {
			return report, beginErr
		}
		session = begun
	}
	defer func() { _ = session.Close() }()
	rollback.session = session
	// The capability, trust and Owner scope are revalidated while the
	// lifecycle lock is held and before any Terramate run.
	revalidated, err := admitAdvancedRollback(workspace, request.capabilityPath, request.now)
	if err != nil {
		return rollback.report, err
	}
	if !equalAdvancedRollbackAdmission(request.admission, revalidated) {
		return rollback.report, errors.New("advanced rollback authority changed after admission")
	}
	operationCtx, cancel := lifecyclemutation.RecoveryContext(ctx)
	defer cancel()
	if err := rollback.run(operationCtx); err != nil {
		return rollback.report, err
	}
	_ = session.Close()
	sealAdvancedRollback(ctx, workspace, release.kit, release.resolution(), &rollback.report)
	return rollback.report, nil
}

// sealAdvancedRollback seals a new executor-state checkpoint of the rolled
// back runtime through the upgrade checkpoint path. The rollback itself has
// already converged, so a seal failure is reported, not raised.
//
// A rollback from a workspace with drifted runtime files needs no custody
// tolerance, and has none: it takes no pre-mutation snapshot (its anchor is
// the checkpoint sealed before the change set, loaded from the executor-state
// store without quiescing anything), every stack it keeps gets the
// checkpoint's captured Compose file, .env, main.tf and state written before
// its forced apply, and every stack the checkpoint lacks is destroyed with
// its project directory. The seal below therefore runs its strict custody
// check only after every drifted file was rewritten or removed. An Advanced
// drift reconcile follows the same order: it restores the governed runtime
// files of its forced stacks before its checkpoint
// (restoreAdvancedReconcileRuntimeFiles).
func sealAdvancedRollback(
	ctx context.Context,
	workspace, kit string,
	resolution releaseindex.Resolution,
	report *advancedrollback.Report,
) {
	advancedRollbackEvent("seal", "started", nil)
	err := withLifecycleMutation(workspace, advancedRollbackCommandName, func() error {
		checkpoint, sealErr := preparePublicUpgradeCheckpoint(ctx, workspace, kit, resolution)
		report.SealedSnapshotID = checkpoint.ExecutorStateSnapshotID
		return sealErr
	})
	switch {
	case err == nil && report.SealedSnapshotID != "":
		report.SealStatus = advancedrollback.SealSealed
		advancedRollbackEvent("seal", "succeeded", map[string]string{"sealedSnapshotId": report.SealedSnapshotID})
	case err != nil && strings.Contains(err.Error(), "unsupported_state_snapshot"):
		report.SealedSnapshotID = ""
		report.SealStatus = advancedrollback.SealUnsupported
		report.SealDetail = err.Error()
		advancedRollbackEvent("seal", "skipped", map[string]string{"reason": "unsupported_state_snapshot"})
	default:
		report.SealedSnapshotID = ""
		report.SealStatus = advancedrollback.SealFailed
		if err != nil {
			report.SealDetail = err.Error()
		}
		rolloutFailure(advancedRollbackRolloutPrefix+"seal", err)
	}
}

// advancedChangeSetRollbackIsCoordinated selects the rollback of a failed
// change set: a checkpoint of a Terramate-target install rolls back per
// stack; every other checkpoint keeps the existing upgrade rollback.
func advancedChangeSetRollbackIsCoordinated(snapshot upgradelifecycle.ExecutorStateSnapshot) bool {
	return snapshot.GenerationTarget == "terramate"
}

// selectAdvancedChangeSetRollback returns the rollback a failed change set
// runs for its checkpoint.
func selectAdvancedChangeSetRollback(
	snapshot upgradelifecycle.ExecutorStateSnapshot,
	existing, coordinated func() error,
) func() error {
	if advancedChangeSetRollbackIsCoordinated(snapshot) && coordinated != nil {
		return coordinated
	}
	return existing
}

// rollbackAdvancedChangeSetCoordinated is the coordinated rollback of a
// change set whose target generate, apply, Terramate convergence or verify
// failed. It runs under the change set's own lifecycle mutation.
func rollbackAdvancedChangeSetCoordinated(
	ctx context.Context,
	workspace string,
	release *lifecycleReleaseAuthority,
	checkpoint publicUpgradeCheckpoint,
	mutation publicUpgradeLifecycleSession,
	control *confinedfs.Transaction,
	verified verifiedAdvancedMutation,
	tools terramatehost.Tools,
	transaction *publicUpgradeTransaction,
) (*advancedrollback.Report, error) {
	report := newAdvancedRollbackReport(checkpoint.ExecutorStateSnapshotID, verified.record.ChangeSetID)
	transaction.FailedPhase = publicUpgradeFailurePhase(transaction.FailedPhase)
	emitPublicUpgradeTransactionEvent(checkpoint.OperationID, "rollback", "started")
	if mutation == nil {
		return &report, errors.New("rollback requires held lifecycle mutation authority")
	}
	ctx, cancel := lifecyclemutation.RecoveryContext(ctx)
	defer cancel()
	if err := removePublicUpgradeCommittedSuccess(control, checkpoint.OperationID); err != nil {
		return &report, fmt.Errorf("remove stale upgrade success authority before rollback: %w", err)
	}
	custody, err := (upgradelifecycle.ExecutorStateStore{}).LoadRollbackCustody(workspace, checkpoint.ExecutorStateSnapshotID)
	if err != nil {
		return &report, err
	}
	binding := verified.admission.owner.Binding
	target, err := advancedRollbackTargetLayout(custody, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return &report, err
	}
	current, err := terramatehost.PlanFromArtifacts(verified.candidate.Artifacts(), binding.SiteRef, binding.NodeRef)
	if err != nil {
		return &report, err
	}
	rollback := &coordinatedRollback{
		workspace: workspace, session: mutation, release: release, custody: custody,
		current: &current, target: target, tools: tools, changeSetID: verified.record.ChangeSetID,
		rollbackID: checkpoint.OperationID + "-rollback", report: report,
	}
	if err := rollback.run(ctx); err != nil {
		return &rollback.report, err
	}
	transaction.Rollback.PriorReleaseVersion = custody.Snapshot.Release.Version
	transaction.Rollback.PlanHash = rollback.verified.PlanHash
	transaction.Rollback.ApplyResultHash = rollback.verified.Apply.ResultHash
	return &rollback.report, nil
}

// advancedRollbackSameRelease reports whether the checkpoint's release is the
// release executing the rollback: the same verified archive for a release
// cache authority, the same executable digest for the running executable.
func advancedRollbackSameRelease(snapshot upgradelifecycle.ExecutorStateSnapshot, release *lifecycleReleaseAuthority) bool {
	if snapshot.Release.Version != release.version() {
		return false
	}
	if release.runningExecutable() {
		return snapshot.Executable.Blob.SHA256 == release.record.SHA256
	}
	return snapshot.Release.ArchiveSHA256 == "sha256:"+strings.TrimPrefix(release.receipt.ArchiveSHA256, "sha256:")
}

func advancedRollbackStacks(layout terramatehost.Layout) []advancedrollback.Stack {
	stacks := make([]advancedrollback.Stack, 0, len(layout.Host.RunOrder))
	for _, id := range layout.Host.RunOrder {
		stack, found := layout.Stack(id)
		if !found {
			continue
		}
		stacks = append(stacks, advancedrollback.Stack{ID: stack.ID, Role: string(stack.Role), RuntimeRoot: stack.RuntimeRoot})
	}
	return stacks
}

func advancedRollbackTargetRoots(custody upgradelifecycle.ExecutorStateRollbackCustody) []advancedrollback.TargetRoot {
	roots := make([]advancedrollback.TargetRoot, 0, len(custody.Roots))
	for _, root := range custody.Roots {
		roots = append(roots, advancedrollback.TargetRoot{RuntimeRoot: root.Root, Files: advancedrollback.RootFiles{
			State: root.State, Config: root.Config,
			Compose: root.Compose, HasCompose: root.HasCompose,
			Environment: root.Environment, HasEnvironment: root.HasEnvironment,
		}})
	}
	return roots
}

// advancedRollbackTargetLayout derives the checkpoint's host layout from its
// captured stack graph and stack files. Only a checkpoint of a
// Terramate-target install has them.
func advancedRollbackTargetLayout(
	custody upgradelifecycle.ExecutorStateRollbackCustody,
	siteRef, nodeRef string,
) (terramatehost.Layout, error) {
	raw, found := custody.Artifacts[terramatestackgraph.ArtifactID]
	if !found || custody.Snapshot.GenerationTarget != "terramate" {
		return terramatehost.Layout{}, errors.New("coordinated rollback requires a checkpoint of a generation.target=terramate install with its Terramate stack graph")
	}
	graph, err := terramatestackgraph.Parse(raw)
	if err != nil {
		return terramatehost.Layout{}, fmt.Errorf("parse the checkpoint stack graph: %w", err)
	}
	return terramatehost.Plan(graph, custody.Artifacts, siteRef, nodeRef)
}

// currentAdvancedRollbackLayout derives the host layout of the current
// generation from the verified artifact manifest of the current plan.
func currentAdvancedRollbackLayout(workspace, siteRef, nodeRef string) (terramatehost.Layout, error) {
	specRaw, sourceVersion, handled, err := classifyArchitectureV2ExecutionSpec(workspace, specFile)
	if err != nil {
		return terramatehost.Layout{}, err
	}
	if !handled || !sourceVersion.IsV2() {
		return terramatehost.Layout{}, errors.New("advanced rollback requires a canonical Architecture v2 StackSpec")
	}
	inventory, err := readArchitectureV2Inventory(workspace, "")
	if err != nil {
		return terramatehost.Layout{}, err
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return terramatehost.Layout{}, err
	}
	inventory, err = inventoryForPersistedGeneration(service, workspace, specRaw, inventory)
	if err != nil {
		return terramatehost.Layout{}, err
	}
	current, err := service.ResolveCurrent(architecturev2.ResolveInput{StackSpec: specRaw, Inventory: inventory})
	if err != nil {
		return terramatehost.Layout{}, err
	}
	result, err := current.Result()
	if err != nil {
		return terramatehost.Layout{}, err
	}
	plan, err := service.VerifyCanonicalPlan(result.CanonicalPlan)
	if err != nil {
		return terramatehost.Layout{}, err
	}
	_, manifestPath, _ := plan.MetadataPaths(workspace)
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return terramatehost.Layout{}, err
	}
	if manifest.Binding != plan.Binding() {
		return terramatehost.Layout{}, errors.New("the generated artifacts do not belong to the current plan; regenerate before a coordinated rollback")
	}
	artifacts := make(map[string][]byte, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(artifact.Path)))
		if err != nil {
			return terramatehost.Layout{}, fmt.Errorf("read generated artifact %s: %w", artifact.ID, err)
		}
		sum := sha256.Sum256(data)
		if "sha256:"+hex.EncodeToString(sum[:]) != artifact.SHA256 {
			return terramatehost.Layout{}, fmt.Errorf("generated artifact %s differs from its manifest", artifact.ID)
		}
		artifacts[artifact.ID] = data
	}
	raw, found := artifacts[terramatestackgraph.ArtifactID]
	if !found {
		return terramatehost.Layout{}, errors.New("the current generation has no Terramate stack graph; coordinated rollback requires generation.target=terramate")
	}
	graph, err := terramatestackgraph.Parse(raw)
	if err != nil {
		return terramatehost.Layout{}, err
	}
	return terramatehost.Plan(graph, artifacts, siteRef, nodeRef)
}

// advancedRollbackStackEnvironment supplies the process environment a
// stack's wrapper root needs for its local-exec: the native Compose
// interpolation environment of a Core payload and the Compose project name
// the root marker records.
func advancedRollbackStackEnvironment(workspace string) func(advancedrollback.Stack) ([]string, error) {
	var native nativehost.NativeComposeRuntime
	return func(stack advancedrollback.Stack) ([]string, error) {
		marker, err := opentofu.ReadRootMarker(filepath.Join(workspace, filepath.FromSlash(stack.RuntimeRoot)))
		if err != nil {
			// No executor root: nothing runs a Compose project.
			return nil, nil
		}
		environment := make([]string, 0, 8)
		if marker.Kind == "" {
			if native == nil {
				if native, err = nativehost.NewOSNativeComposeRuntime(workspace); err != nil {
					return nil, err
				}
			}
			core, err := native.ComposeEnvironment(marker.ModuleRef)
			if err != nil {
				return nil, err
			}
			environment = append(environment, core...)
		}
		if marker.ComposeProject != "" {
			environment = append(environment, "COMPOSE_PROJECT_NAME="+marker.ComposeProject)
		}
		return environment, nil
	}
}

// advancedRollbackNativeSteps runs the native Apply side steps around each
// restored or recreated stack's forced apply, through the same native code
// the runtime executor runs around its own `tofu apply`: the Core preparation
// and completion (stackkit-server staging and recreation, step-ca reload,
// PocketID owner realization, TinyAuth reconcile) for a Core root, and the
// workload completion (Wings recreation, readiness, origin backend) for a
// workload root, bound to the checkpoint's own workload bundle. Contract
// roots of edge and federation owners start no process and have none.
type advancedRollbackNativeSteps struct {
	workspace, executable string
	artifacts             map[string][]byte
}

func (n advancedRollbackNativeSteps) Prepare(ctx context.Context, stack advancedrollback.Stack) (func(context.Context) error, error) {
	marker, err := opentofu.ReadRootMarker(filepath.Join(n.workspace, filepath.FromSlash(stack.RuntimeRoot)))
	if err != nil {
		// No executor root: nothing runs a Compose project.
		return nil, nil
	}
	switch marker.Kind {
	case "":
		steps, err := nativehost.PrepareNativeComposeRoot(ctx, n.workspace, marker.ModuleRef, n.executable)
		if err != nil {
			return nil, err
		}
		return steps.Complete, nil
	case opentofu.RootKindWorkload:
		bundle, err := advancedRollbackWorkloadBundle(n.artifacts, marker.ModuleRef, marker.InstanceRef)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) error {
			return nativehost.CompleteRestoredWorkloadCompose(ctx, n.workspace, bundle)
		}, nil
	default:
		return nil, nil
	}
}

// advancedRollbackWorkloadBundle selects the checkpoint's one workload bundle
// of the restored root's module instance.
func advancedRollbackWorkloadBundle(artifacts map[string][]byte, moduleRef, instanceRef string) ([]byte, error) {
	var selected []byte
	for _, raw := range artifacts {
		bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(raw)
		if err != nil || bundle.ModuleRef != moduleRef || bundle.InstanceRef != instanceRef {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("the checkpoint holds several workload bundles of %s", instanceRef)
		}
		selected = raw
	}
	if selected == nil {
		return nil, fmt.Errorf("the checkpoint holds no workload bundle of %s", instanceRef)
	}
	return selected, nil
}

// resolveAdvancedRollbackTarget maps --to to the target checkpoint. A stored
// change set resolves to the checkpoint sealed before its apply: the one its
// own coordinated rollback journal or its Advanced mutation result records.
func resolveAdvancedRollbackTarget(workspace, to string) (snapshotID, changeSetID string, err error) {
	hexID := strings.TrimPrefix(to, "sha256:")
	changeSetPath := filepath.Join(workspace, ".stackkit", "advanced", "change-sets", hexID+".json")
	if info, statErr := os.Lstat(changeSetPath); statErr != nil || !info.Mode().IsRegular() {
		return to, "", nil
	}
	candidates := map[string]struct{}{}
	journals, _ := filepath.Glob(filepath.Join(workspace, filepath.FromSlash(advancedrollback.JournalDir), "*.json"))
	for _, name := range journals {
		var journal struct {
			TargetSnapshotID string `json:"targetSnapshotId"`
			ChangeSetID      string `json:"changeSetId"`
		}
		if readAdvancedRollbackJSON(name, &journal) == nil && journal.ChangeSetID == to &&
			advancedSHA256Pattern.MatchString(journal.TargetSnapshotID) {
			candidates[journal.TargetSnapshotID] = struct{}{}
		}
	}
	results, _ := filepath.Glob(filepath.Join(workspace, ".stackkit", "advanced", "results", "*.json"))
	for _, name := range results {
		var result struct {
			ChangeSetID string `json:"changeSetId"`
			Checkpoint  struct {
				ExecutorStateSnapshotID string `json:"executorStateSnapshotId"`
			} `json:"checkpoint"`
		}
		if readAdvancedRollbackJSON(name, &result) == nil && result.ChangeSetID == to &&
			advancedSHA256Pattern.MatchString(result.Checkpoint.ExecutorStateSnapshotID) {
			candidates[result.Checkpoint.ExecutorStateSnapshotID] = struct{}{}
		}
	}
	found := make([]string, 0, len(candidates))
	for candidate := range candidates {
		found = append(found, candidate)
	}
	sort.Strings(found)
	switch len(found) {
	case 1:
		return found[0], to, nil
	case 0:
		return "", to, errors.New("advanced_rollback_target_unresolved: no checkpoint is recorded for this change set; pass --to <executor-state snapshot ID>")
	default:
		return "", to, fmt.Errorf("advanced_rollback_target_unresolved: the change set has several recorded checkpoints %v; pass --to <executor-state snapshot ID>", found)
	}
}

func readAdvancedRollbackJSON(name string, target any) error {
	raw, err := readAdvancedRegular(name, maxAdvancedCandidateBytes, "Advanced record")
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
