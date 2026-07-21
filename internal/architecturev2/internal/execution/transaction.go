// Package execution owns the held-root installation transaction used by the
// Architecture v2 authority boundary. It is internal so callers cannot bypass
// plan verification or redirect generated output through raw filesystem paths.
package execution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type Error = architecturev2renderer.Error

const (
	ErrInvalidPlan         = architecturev2renderer.ErrInvalidPlan
	ErrInvalidPath         = architecturev2renderer.ErrInvalidPath
	ErrOutputChanged       = architecturev2renderer.ErrOutputChanged
	ErrUnsafeOutputRoot    = architecturev2renderer.ErrUnsafeOutputRoot
	ErrOutputBusy          = architecturev2renderer.ErrOutputBusy
	ErrOutputTransaction   = architecturev2renderer.ErrOutputTransaction
	ErrTransactionRecovery = architecturev2renderer.ErrTransactionRecovery
	ErrTransactionCleanup  = architecturev2renderer.ErrTransactionCleanup
	ErrTransactionRollback = architecturev2renderer.ErrTransactionRollback
)

func fail(code architecturev2renderer.ErrorCode, location, format string, args ...any) error {
	return &architecturev2renderer.Error{Code: code, Path: location, Message: fmt.Sprintf(format, args...)}
}

func wrap(code architecturev2renderer.ErrorCode, location, message string, err error) error {
	return &architecturev2renderer.Error{Code: code, Path: location, Message: message, Err: err}
}

type installPreparation struct {
	plan      generationartifact.VerifiedPlan
	workspace *confinedfs.Transaction
	finalRoot string
}

type stagedOutput struct {
	container string
	root      string
	manifest  generationartifact.ArtifactManifest
	receipt   generationartifact.GenerationReceipt
}

type cleanupTreeFunc func(*confinedfs.Transaction, string) error

// ValidateWorkspaceRoot canonicalizes the path-name assertion used only to
// match an already-held authorization root. Installation itself never reopens
// this pathname.
func ValidateWorkspaceRoot(root string) (string, error) { return validateWorkspaceRoot(root) }

// InstallManagedOutput performs the held-root stage/verify/swap transaction.
// Go's nested internal boundary permits calls only from the architecturev2
// package tree; callers outside that authority cannot reach this raw mutation.
func InstallManagedOutput(plan generationartifact.VerifiedPlan, workspace *confinedfs.Transaction, result architecturev2renderer.RenderResult, options architecturev2renderer.InstallOptions) (architecturev2renderer.InstallResult, error) {
	return installManagedOutput(plan, workspace, result, options, removeSafeTree)
}

// installManagedOutput keeps cleanup injection private to this package so
// tests can prove the committed-with-cleanup-error state without exposing a
// production hook or mutating package-global filesystem behavior.
func installManagedOutput(plan generationartifact.VerifiedPlan, workspace *confinedfs.Transaction, result architecturev2renderer.RenderResult, options architecturev2renderer.InstallOptions, cleanupTree cleanupTreeFunc) (installed architecturev2renderer.InstallResult, returnErr error) {
	if workspace == nil {
		return architecturev2renderer.InstallResult{}, fail(architecturev2renderer.ErrAuthorization, "generation.authorization.workspaceRoot", "held workspace transaction is required")
	}
	if cleanupTree == nil {
		return architecturev2renderer.InstallResult{}, fail(architecturev2renderer.ErrOutputTransaction, "cleanup", "transaction cleanup implementation is required")
	}
	prepared, err := prepareManagedInstall(plan, workspace, result)
	if err != nil {
		return architecturev2renderer.InstallResult{}, err
	}
	outputLock, err := workspace.TryAcquireOutputLock(prepared.finalRoot)
	if err != nil {
		if errors.Is(err, confinedfs.ErrOutputLockBusy) {
			return architecturev2renderer.InstallResult{}, wrap(ErrOutputBusy, displayPath(workspace, prepared.finalRoot), "another process owns the managed output transaction", err)
		}
		return architecturev2renderer.InstallResult{}, wrap(ErrOutputTransaction, displayPath(workspace, prepared.finalRoot), "acquire managed output transaction lock", err)
	}
	defer func() {
		returnErr = mergeCleanupResult(installed, returnErr, outputLock.Release(), displayPath(workspace, prepared.finalRoot)+" (output lock)")
	}()
	if err := initializeManagedInstall(prepared); err != nil {
		return architecturev2renderer.InstallResult{}, err
	}
	if err := RequireNoPendingOutputTransaction(workspace, prepared.finalRoot); err != nil {
		return architecturev2renderer.InstallResult{}, err
	}
	staged, err := stageManagedOutput(prepared, result, options.GeneratedAt)
	if err != nil {
		return architecturev2renderer.InstallResult{}, err
	}
	preserveStage := false
	defer func() {
		if preserveStage {
			return
		}
		returnErr = mergeCleanupResult(installed, returnErr, cleanupTree(workspace, staged.container), displayPath(workspace, staged.container))
	}()
	journal, err := beginManagedOutputJournal(prepared, staged)
	if err != nil {
		preserveStage = true
		return architecturev2renderer.InstallResult{}, wrap(ErrTransactionRecovery, displayPath(workspace, staged.container), "persist initial managed output transaction journal", err)
	}
	if preserve, err := swapAndVerifyManagedOutput(prepared, staged, &journal); err != nil {
		preserveStage = preserve
		if !preserve {
			if cleanupErr := finishManagedOutputTransaction(workspace, staged.container, &journal, cleanupTree); cleanupErr != nil {
				preserveStage = true
				return architecturev2renderer.InstallResult{}, errors.Join(err, cleanupErr)
			}
			preserveStage = true
		}
		return architecturev2renderer.InstallResult{}, err
	}
	installed = architecturev2renderer.InstallResult{
		Committed:  true,
		OutputRoot: displayPath(workspace, prepared.finalRoot),
		Manifest:   staged.manifest,
		Receipt:    staged.receipt,
	}
	if err := finishManagedOutputTransaction(workspace, staged.container, &journal, cleanupTree); err != nil {
		preserveStage = true
		return installed, &Error{Code: ErrTransactionCleanup, Path: displayPath(workspace, staged.container), Message: "managed output is committed and verified, but durable transaction cleanup failed", Err: err, Committed: true}
	}
	preserveStage = true
	return installed, nil
}

// RequireNoPendingOutputTransaction is the shared fail-closed admission check
// for Generate, Plan, Apply, and Verify. The caller must already own the exact
// output lock; this function never acquires or weakens that lease.
func RequireNoPendingOutputTransaction(workspace *confinedfs.Transaction, outputRoot string) error {
	pending, exists, err := findPendingOutputTransaction(workspace, outputRoot)
	if err != nil {
		return wrap(ErrTransactionRecovery, displayPath(workspace, outputRoot), "inspect durable output transaction recovery state", err)
	}
	if !exists {
		return nil
	}
	return &Error{
		Code:    ErrTransactionRecovery,
		Path:    displayPath(workspace, outputRoot),
		Message: fmt.Sprintf("durable output transaction %s requires recovery from phase %s", pending.TransactionID, pending.Phase),
	}
}

func beginManagedOutputJournal(prepared installPreparation, staged stagedOutput) (transactionJournal, error) {
	hadPrevious, _, err := prepared.workspace.Exists(prepared.finalRoot)
	if err != nil {
		return transactionJournal{}, err
	}
	manifestDigest, err := staged.manifest.Hash()
	if err != nil {
		return transactionJournal{}, err
	}
	receiptDigest, err := resolvedplan.CanonicalSHA256(staged.receipt)
	if err != nil {
		return transactionJournal{}, err
	}
	previousRootDigest := ""
	if hadPrevious {
		previousRootDigest, err = managedTreeDigest(prepared.workspace, prepared.finalRoot)
		if err != nil {
			return transactionJournal{}, err
		}
	}
	binding, err := transactionBindingForStage(staged.container, prepared.finalRoot, prepared.plan.Binding().PlanHash, manifestDigest, receiptDigest, previousRootDigest, hadPrevious)
	if err != nil {
		return transactionJournal{}, err
	}
	journal, err := newTransactionJournal(binding, transactionPhaseStaged)
	if err != nil {
		return transactionJournal{}, err
	}
	if err := writeTransactionJournal(prepared.workspace, nil, journal); err != nil {
		return transactionJournal{}, err
	}
	return journal, nil
}

func finishManagedOutputTransaction(workspace *confinedfs.Transaction, stageContainer string, journal *transactionJournal, cleanupTree cleanupTreeFunc) error {
	if journal == nil {
		return errors.New("managed output transaction journal is required")
	}
	if journal.Phase != transactionPhaseCommitCleanupIntent && journal.Phase != transactionPhaseRollbackCleanupIntent {
		return fmt.Errorf("managed output transaction cannot clean up from phase %s", journal.Phase)
	}
	if err := cleanupTree(workspace, stageContainer); err != nil {
		return err
	}
	if _, err := workspace.SyncDirectory(path.Dir(stageContainer)); err != nil {
		return err
	}
	if err := advanceAndWriteTransactionJournal(workspace, journal, transactionPhaseComplete); err != nil {
		return err
	}
	return removeTransactionJournal(workspace, journal.binding())
}

func mergeCleanupResult(installed architecturev2renderer.InstallResult, operationErr, cleanupErr error, cleanupPath string) error {
	if cleanupErr == nil {
		return operationErr
	}
	if operationErr != nil {
		return errors.Join(operationErr, cleanupErr)
	}
	if installed.Committed {
		return &Error{
			Code:      ErrTransactionCleanup,
			Path:      cleanupPath,
			Message:   "managed output is committed and verified, but transaction staging cleanup failed",
			Err:       cleanupErr,
			Committed: true,
		}
	}
	return wrap(ErrOutputTransaction, cleanupPath, "clean transaction staging tree", cleanupErr)
}

func prepareManagedInstall(plan generationartifact.VerifiedPlan, workspace *confinedfs.Transaction, result architecturev2renderer.RenderResult) (installPreparation, error) {
	outputRoot, err := architecturev2renderer.ValidateManagedOutput(plan, result)
	if err != nil {
		return installPreparation{}, err
	}
	if outputRoot == "." {
		return installPreparation{}, fail(architecturev2renderer.ErrUnsafeOutputRoot, "resolvedPlan.generation.outputRoot", "managed stage/swap installation requires a dedicated outputRoot, not current workspace")
	}
	return installPreparation{plan: plan, workspace: workspace, finalRoot: outputRoot}, nil
}

func initializeManagedInstall(prepared installPreparation) error {
	if err := prepared.workspace.MkdirAll(path.Dir(prepared.finalRoot), 0o750); err != nil {
		return wrap(ErrOutputTransaction, displayPath(prepared.workspace, path.Dir(prepared.finalRoot)), "create managed output parent under output lock", err)
	}
	if err := inspectExistingManagedRoot(prepared.workspace, prepared.finalRoot); err != nil {
		return err
	}
	return nil
}

func stageManagedOutput(prepared installPreparation, result architecturev2renderer.RenderResult, generatedAt string) (staged stagedOutput, returnErr error) {
	stageContainer, err := prepared.workspace.CreatePrivateDirectory(".stackkit-v2-stage-")
	if err != nil {
		return stagedOutput{}, wrap(ErrOutputTransaction, prepared.workspace.Name(), "create held staging directory", err)
	}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, removeSafeTree(prepared.workspace, stageContainer))
		}
	}()
	stageOutputRoot := path.Join(stageContainer, prepared.finalRoot)
	if overlapPortablePaths(stageContainer, prepared.finalRoot) {
		return stagedOutput{}, fail(ErrUnsafeOutputRoot, displayPath(prepared.workspace, prepared.finalRoot), "managed output root overlaps the staging container")
	}
	if err := prepared.workspace.MkdirAll(stageOutputRoot, 0o750); err != nil {
		return stagedOutput{}, wrap(ErrOutputTransaction, displayPath(prepared.workspace, stageOutputRoot), "create staged output root", err)
	}
	artifacts := result.Artifacts()
	artifactPaths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if err := writeStagedArtifact(prepared.workspace, stageContainer, artifact); err != nil {
			return stagedOutput{}, err
		}
		artifactPaths = append(artifactPaths, artifact.Path)
	}
	manifest, err := generationartifact.BuildManifestHeld(prepared.plan, prepared.workspace, stageContainer, artifactPaths)
	if err != nil {
		return stagedOutput{}, wrap(ErrOutputTransaction, displayPath(prepared.workspace, stageOutputRoot), "build generation manifest from held staged artifacts", err)
	}
	receipt, err := generationartifact.NewReceipt(prepared.plan, manifest, generatedAt)
	if err != nil {
		return stagedOutput{}, wrap(ErrOutputTransaction, displayPath(prepared.workspace, stageOutputRoot), "create generation receipt", err)
	}
	// Control metadata is deliberately last: no renderer artifact is written
	// after the manifest, and the receipt is the final staged write.
	if err := generationartifact.PersistManifestHeld(prepared.plan, prepared.workspace, stageContainer, manifest); err != nil {
		return stagedOutput{}, wrap(ErrOutputTransaction, displayPath(prepared.workspace, stageOutputRoot), "persist held staged generation manifest", err)
	}
	if err := generationartifact.PersistReceiptHeld(prepared.plan, prepared.workspace, stageContainer, manifest, receipt); err != nil {
		return stagedOutput{}, wrap(ErrOutputTransaction, displayPath(prepared.workspace, stageOutputRoot), "persist held staged generation receipt", err)
	}
	if err := inspectManagedTree(prepared.workspace, stageOutputRoot); err != nil {
		return stagedOutput{}, err
	}
	if err := generationartifact.VerifyManifestHeld(prepared.plan, prepared.workspace, stageContainer, manifest); err != nil {
		return stagedOutput{}, wrap(ErrOutputTransaction, displayPath(prepared.workspace, stageOutputRoot), "verify held staged artifact manifest", err)
	}
	if err := generationartifact.VerifyReceipt(prepared.plan, manifest, receipt); err != nil {
		return stagedOutput{}, wrap(ErrOutputTransaction, displayPath(prepared.workspace, stageOutputRoot), "verify staged generation receipt", err)
	}
	return stagedOutput{container: stageContainer, root: stageOutputRoot, manifest: manifest, receipt: receipt}, nil
}

func swapAndVerifyManagedOutput(prepared installPreparation, staged stagedOutput, journal *transactionJournal) (bool, error) {
	if journal == nil {
		return true, wrap(ErrTransactionRecovery, displayPath(prepared.workspace, staged.container), "managed output transaction journal is required", nil)
	}
	if journal.HadPrevious {
		if err := advanceAndWriteTransactionJournal(prepared.workspace, journal, transactionPhaseBackupIntent); err != nil {
			return true, wrap(ErrTransactionRecovery, displayPath(prepared.workspace, journal.BackupRoot), "persist previous-output backup intent", err)
		}
		if preserve, err := movePreviousToBackup(prepared.workspace, prepared.finalRoot, journal.BackupRoot, journal.PreviousRootDigest); err != nil {
			if !preserve {
				if journalErr := markManagedOutputRolledBack(prepared.workspace, journal); journalErr != nil {
					return true, errors.Join(err, journalErr)
				}
			}
			return preserve, err
		}
		if err := advanceAndWriteTransactionJournal(prepared.workspace, journal, transactionPhaseBackupComplete); err != nil {
			return true, wrap(ErrTransactionRecovery, displayPath(prepared.workspace, journal.BackupRoot), "persist previous-output backup completion", err)
		}
	}
	if err := advanceAndWriteTransactionJournal(prepared.workspace, journal, transactionPhaseInstallIntent); err != nil {
		return true, wrap(ErrTransactionRecovery, displayPath(prepared.workspace, prepared.finalRoot), "persist managed output install intent", err)
	}
	installed, err := prepared.workspace.Rename(staged.root, prepared.finalRoot)
	if installed {
		if syncErr := syncRenameParents(prepared.workspace, staged.root, prepared.finalRoot); syncErr != nil {
			err = errors.Join(err, syncErr)
		}
	}
	if err != nil || !installed {
		operationErr := err
		if operationErr == nil {
			operationErr = errors.New("held rename returned without installing staged output")
		}
		if rollbackErr := rollbackManagedOutput(prepared.workspace, prepared.finalRoot, journal, installed); rollbackErr != nil {
			return true, &Error{Code: ErrTransactionRollback, Path: displayPath(prepared.workspace, prepared.finalRoot), Message: "managed output install failed and the previous state could not be restored", Err: errors.Join(operationErr, rollbackErr)}
		}
		return false, wrap(ErrOutputTransaction, displayPath(prepared.workspace, prepared.finalRoot), "install staged managed output", operationErr)
	}
	if err := advanceAndWriteTransactionJournal(prepared.workspace, journal, transactionPhaseInstallComplete); err != nil {
		return true, wrap(ErrTransactionRecovery, displayPath(prepared.workspace, prepared.finalRoot), "persist managed output install completion", err)
	}
	if err := verifyInstalledOutput(prepared.plan, prepared.workspace, prepared.finalRoot, staged.manifest, staged.receipt); err != nil {
		if rollbackErr := rollbackManagedOutput(prepared.workspace, prepared.finalRoot, journal, true); rollbackErr != nil {
			return true, &Error{Code: ErrTransactionRollback, Path: displayPath(prepared.workspace, prepared.finalRoot), Message: "installed output failed verification and rollback failed", Err: errors.Join(err, rollbackErr)}
		}
		return false, err
	}
	if err := advanceAndWriteTransactionJournal(prepared.workspace, journal, transactionPhaseCommitCleanupIntent); err != nil {
		return true, wrap(ErrTransactionRecovery, displayPath(prepared.workspace, prepared.finalRoot), "persist committed output cleanup intent", err)
	}
	return false, nil
}

func movePreviousToBackup(workspace *confinedfs.Transaction, finalRoot, backupRoot, expectedDigest string) (bool, error) {
	if err := inspectManagedTree(workspace, finalRoot); err != nil {
		return false, err
	}
	installed, err := workspace.Rename(finalRoot, backupRoot)
	if installed {
		err = errors.Join(err, syncRenameParents(workspace, finalRoot, backupRoot))
	}
	if err != nil {
		return installed, wrap(ErrOutputTransaction, displayPath(workspace, finalRoot), "move previous managed output into transaction backup", err)
	}
	if !installed {
		return true, fail(ErrTransactionRollback, displayPath(workspace, finalRoot), "previous managed output rename did not install its backup")
	}
	verificationErr := inspectManagedTree(workspace, backupRoot)
	if verificationErr == nil {
		verificationErr = requireManagedTreeDigest(workspace, backupRoot, expectedDigest)
	}
	if verificationErr != nil {
		restored, rollbackErr := workspace.Rename(backupRoot, finalRoot)
		if restored {
			rollbackErr = errors.Join(rollbackErr, syncRenameParents(workspace, backupRoot, finalRoot))
		}
		if restored && rollbackErr == nil {
			rollbackErr = requireManagedTreeDigest(workspace, finalRoot, expectedDigest)
		}
		if rollbackErr != nil || !restored {
			return true, &Error{Code: ErrTransactionRollback, Path: displayPath(workspace, finalRoot), Message: "transaction backup verification failed and previous output could not be restored", Err: errors.Join(verificationErr, rollbackErr)}
		}
		return false, wrap(ErrOutputTransaction, displayPath(workspace, backupRoot), "verify transaction backup after rename", verificationErr)
	}
	return false, nil
}

type managedTreeIdentityEntry struct {
	Path          string `json:"path"`
	Kind          string `json:"kind"`
	Mode          string `json:"mode"`
	ContentDigest string `json:"contentDigest,omitempty"`
}

func managedTreeDigest(workspace *confinedfs.Transaction, root string) (string, error) {
	entries, err := workspace.Walk(root)
	if err != nil {
		return "", err
	}
	identity := make([]managedTreeIdentityEntry, 0, len(entries))
	for _, entry := range entries {
		relative := "."
		if entry.Path != root {
			relative = strings.TrimPrefix(entry.Path, root+"/")
		}
		item := managedTreeIdentityEntry{Path: relative, Mode: fmt.Sprintf("%04o", entry.Info.Mode().Perm())}
		if entry.Info.IsDir() {
			item.Kind = "directory"
		} else {
			item.Kind = "file"
			data, _, readErr := workspace.ReadStable(entry.Path)
			if readErr != nil {
				return "", readErr
			}
			digest := sha256.Sum256(data)
			item.ContentDigest = "sha256:" + hex.EncodeToString(digest[:])
		}
		identity = append(identity, item)
	}
	return resolvedplan.CanonicalSHA256(identity)
}

func requireManagedTreeDigest(workspace *confinedfs.Transaction, root, expected string) error {
	actual, err := managedTreeDigest(workspace, root)
	if err != nil {
		return err
	}
	if actual != expected {
		return fail(ErrOutputChanged, displayPath(workspace, root), "managed tree identity changed: want %s, got %s", expected, actual)
	}
	return nil
}

func writeStagedArtifact(workspace *confinedfs.Transaction, stageContainer string, artifact architecturev2renderer.Artifact) error {
	if workspace == nil {
		return fail(ErrOutputTransaction, "staging", "held workspace transaction is required")
	}
	if _, err := validatePortablePath(artifact.Path); err != nil {
		return wrap(ErrInvalidPath, artifact.Path, "invalid staged artifact path", err)
	}
	target := path.Join(stageContainer, artifact.Path)
	if err := workspace.MkdirAll(path.Dir(target), 0o750); err != nil {
		return wrap(ErrOutputTransaction, displayPath(workspace, path.Dir(target)), "create staged artifact parent", err)
	}
	mode, err := parseMode(artifact.Mode)
	if err != nil {
		return wrap(ErrInvalidPlan, artifact.Path, "invalid governed artifact mode", err)
	}
	if err := workspace.WriteFileExclusive(target, artifact.Bytes, mode); err != nil {
		return wrap(ErrOutputTransaction, displayPath(workspace, target), "write staged artifact through held workspace", err)
	}
	return nil
}

func verifyInstalledOutput(plan generationartifact.VerifiedPlan, workspace *confinedfs.Transaction, finalRoot string, manifest generationartifact.ArtifactManifest, receipt generationartifact.GenerationReceipt) error {
	if err := inspectManagedTree(workspace, finalRoot); err != nil {
		return err
	}
	if err := generationartifact.VerifyManifestHeld(plan, workspace, ".", manifest); err != nil {
		return wrap(ErrOutputTransaction, displayPath(workspace, finalRoot), "verify installed artifact manifest through held workspace", err)
	}
	installedManifest, err := generationartifact.ReadManifestHeld(plan, workspace, ".")
	if err != nil {
		return wrap(ErrOutputTransaction, displayPath(workspace, finalRoot), "read installed generation manifest through held workspace", err)
	}
	wantManifest, err := manifest.MarshalCanonical()
	if err != nil {
		return wrap(ErrOutputTransaction, displayPath(workspace, finalRoot), "canonicalize expected installed manifest", err)
	}
	gotManifest, err := installedManifest.MarshalCanonical()
	if err != nil {
		return wrap(ErrOutputTransaction, displayPath(workspace, finalRoot), "canonicalize installed manifest", err)
	}
	if !bytes.Equal(gotManifest, wantManifest) {
		return fail(ErrOutputChanged, displayPath(workspace, finalRoot), "installed manifest changed")
	}
	installedReceipt, err := generationartifact.ReadReceiptHeld(plan, workspace, ".")
	if err != nil {
		return wrap(ErrOutputTransaction, displayPath(workspace, finalRoot), "read installed generation receipt through held workspace", err)
	}
	if err := generationartifact.VerifyReceipt(plan, installedManifest, installedReceipt); err != nil {
		return wrap(ErrOutputTransaction, displayPath(workspace, finalRoot), "verify installed generation receipt", err)
	}
	if installedReceipt != receipt {
		return fail(ErrOutputChanged, displayPath(workspace, finalRoot), "installed receipt changed")
	}
	// This check is deliberately last. All prior operations were handle-relative,
	// so a renamed workspace cannot redirect output; identity failure now drives
	// the same held-root rollback path as any other verification failure.
	if err := workspace.VerifyPathIdentity(); err != nil {
		return wrap(ErrOutputTransaction, workspace.Name(), "workspace pathname no longer identifies held installation root", err)
	}
	return nil
}

func rollbackManagedOutput(workspace *confinedfs.Transaction, finalRoot string, journal *transactionJournal, installed bool) error {
	if err := advanceAndWriteTransactionJournal(workspace, journal, transactionPhaseRollbackIntent); err != nil {
		return err
	}
	if installed {
		if err := inspectManagedTree(workspace, finalRoot); err != nil {
			return err
		}
		moved, err := workspace.Rename(finalRoot, journal.FailedRoot)
		if moved {
			err = errors.Join(err, syncRenameParents(workspace, finalRoot, journal.FailedRoot))
		}
		if err != nil || !moved {
			return fmt.Errorf("move failed installed output aside: %w", err)
		}
	}
	if journal.HadPrevious {
		if err := inspectManagedTree(workspace, journal.BackupRoot); err != nil {
			return err
		}
		if err := requireManagedTreeDigest(workspace, journal.BackupRoot, journal.PreviousRootDigest); err != nil {
			return err
		}
		restored, err := workspace.Rename(journal.BackupRoot, finalRoot)
		if restored {
			err = errors.Join(err, syncRenameParents(workspace, journal.BackupRoot, finalRoot))
		}
		if err != nil || !restored {
			return fmt.Errorf("restore previous managed output: %w", err)
		}
		if err := requireManagedTreeDigest(workspace, finalRoot, journal.PreviousRootDigest); err != nil {
			return fmt.Errorf("verify restored previous managed output: %w", err)
		}
	}
	return markManagedOutputRolledBack(workspace, journal)
}

func markManagedOutputRolledBack(workspace *confinedfs.Transaction, journal *transactionJournal) error {
	if journal.Phase != transactionPhaseRollbackIntent {
		if err := advanceAndWriteTransactionJournal(workspace, journal, transactionPhaseRollbackIntent); err != nil {
			return err
		}
	}
	if err := advanceAndWriteTransactionJournal(workspace, journal, transactionPhaseRollbackComplete); err != nil {
		return err
	}
	return advanceAndWriteTransactionJournal(workspace, journal, transactionPhaseRollbackCleanupIntent)
}

func syncRenameParents(workspace *confinedfs.Transaction, source, destination string) error {
	parents := []string{path.Dir(source), path.Dir(destination)}
	if parents[0] == parents[1] {
		parents = parents[:1]
	}
	var syncErr error
	for _, parent := range parents {
		if _, err := workspace.SyncDirectory(parent); err != nil {
			syncErr = errors.Join(syncErr, err)
		}
	}
	return syncErr
}

func validateWorkspaceRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fail(ErrUnsafeOutputRoot, "workspaceRoot", "workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", wrap(ErrUnsafeOutputRoot, root, "resolve workspace root", err)
	}
	abs = filepath.Clean(abs)
	if err := rejectAnySymlinkInAbsoluteChain(abs); err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", wrap(ErrUnsafeOutputRoot, abs, "inspect workspace root", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fail(ErrUnsafeOutputRoot, abs, "workspace root must be a non-symlink directory")
	}
	return abs, nil
}

func inspectExistingManagedRoot(workspace *confinedfs.Transaction, root string) error {
	exists, info, err := workspace.Exists(root)
	if err != nil {
		return wrap(ErrUnsafeOutputRoot, displayPath(workspace, root), "inspect managed output root", err)
	}
	if !exists {
		return nil
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail(ErrUnsafeOutputRoot, displayPath(workspace, root), "existing managed output root must be a non-symlink directory")
	}
	return inspectManagedTree(workspace, root)
}

func inspectManagedTree(workspace *confinedfs.Transaction, root string) error {
	if _, err := workspace.Walk(root); err != nil {
		return wrap(ErrUnsafeOutputRoot, displayPath(workspace, root), "inspect held managed output tree", err)
	}
	return nil
}

func rejectAnySymlinkInAbsoluteChain(value string) error {
	current := filepath.Clean(value)
	for {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fail(ErrUnsafeOutputRoot, current, "path and ancestors must not be symlinks")
		}
		if err != nil && !os.IsNotExist(err) {
			return wrap(ErrUnsafeOutputRoot, current, "inspect path ancestor", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func removeSafeTree(workspace *confinedfs.Transaction, root string) error {
	if workspace == nil || strings.TrimSpace(root) == "" || root == "." {
		return fail(ErrUnsafeOutputRoot, "cleanup", "held cleanup root is required")
	}
	if err := workspace.RemoveTree(root); err != nil {
		return wrap(ErrOutputTransaction, displayPath(workspace, root), "remove held transaction staging tree", err)
	}
	return nil
}

func displayPath(workspace *confinedfs.Transaction, portableRelative string) string {
	if workspace == nil {
		return portableRelative
	}
	name := workspace.Name()
	if name == "" || portableRelative == "." {
		return name
	}
	return filepath.Join(name, filepath.FromSlash(portableRelative))
}

func parseMode(value string) (os.FileMode, error) {
	if len(value) != 4 || value[0] != '0' {
		return 0, fmt.Errorf("mode %q must use four-digit octal form", value)
	}
	parsed, err := strconv.ParseUint(value, 8, 9)
	if err != nil {
		return 0, fmt.Errorf("mode %q is not octal: %w", value, err)
	}
	return os.FileMode(parsed), nil
}

func validatePortablePath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, `\`) || strings.ContainsAny(value, `<>:"|?*`) {
		return "", fmt.Errorf("must be a non-empty portable slash-separated relative path")
	}
	if strings.HasPrefix(value, "/") || (len(value) >= 2 && value[1] == ':') {
		return "", fmt.Errorf("absolute, drive-relative, and UNC paths are forbidden")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return "", fmt.Errorf("path must be canonical and remain beneath its root")
	}
	for _, segment := range strings.Split(clean, "/") {
		if strings.TrimRight(segment, ". ") != segment || windowsReservedSegment(segment) {
			return "", fmt.Errorf("path is not portable to Windows")
		}
	}
	return clean, nil
}

func windowsReservedSegment(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func overlapPortablePaths(left, right string) bool {
	left, right = path.Clean(left), path.Clean(right)
	if left == right {
		return true
	}
	return strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
