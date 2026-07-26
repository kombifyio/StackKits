package execution

import (
	"errors"
	"fmt"
	"path"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// RetiredOutputGCAction is the one bounded mutation which may be taken for a
// retired output-transaction tombstone. Its value is intentionally echoed by
// the caller before mutation so inspection cannot accidentally become cleanup.
type RetiredOutputGCAction string

const (
	RetiredOutputGCRemoveStage   RetiredOutputGCAction = "remove-retired-stage"
	RetiredOutputGCRemoveJournal RetiredOutputGCAction = "remove-retired-journal"
)

// RetiredOutputGCInspection is a non-secret, exact report for one retired
// transaction namespace. Active journals and generated output are never read.
type RetiredOutputGCInspection struct {
	TransactionID string                `json:"transactionId"`
	Action        RetiredOutputGCAction `json:"action"`
}

// InspectRetiredOutputGC identifies exactly one permitted cleanup action for
// a deterministic retired transaction namespace. A journal is authority only
// when it is a complete immutable journal; malformed or partial evidence fails
// closed instead of being treated as deletion permission.
func InspectRetiredOutputGC(workspace *confinedfs.Transaction, transactionID string) (RetiredOutputGCInspection, error) {
	if workspace == nil {
		return RetiredOutputGCInspection{}, errors.New("held workspace transaction is required")
	}
	if !validTransactionID(transactionID) {
		return RetiredOutputGCInspection{}, journalInvalid("transactionId", transactionID, "32 lowercase hexadecimal characters", nil)
	}
	stagePath := path.Join(retiredTransactionStageRoot, transactionID)
	journalPath := path.Join(retiredTransactionJournalRoot, transactionID)
	stageExists, stageInfo, err := workspace.Exists(stagePath)
	if err != nil {
		return RetiredOutputGCInspection{}, &transactionJournalError{Code: transactionJournalIO, Path: stagePath, Err: err}
	}
	if stageExists && !stageInfo.IsDir() {
		return RetiredOutputGCInspection{}, &transactionRecoveryAmbiguityError{TransactionID: transactionID, Reason: "retired stage path is not a directory"}
	}
	journalExists, journalInfo, err := workspace.Exists(journalPath)
	if err != nil {
		return RetiredOutputGCInspection{}, &transactionJournalError{Code: transactionJournalIO, Path: journalPath, Err: err}
	}
	if journalExists && !journalInfo.IsDir() {
		return RetiredOutputGCInspection{}, &transactionRecoveryAmbiguityError{TransactionID: transactionID, Reason: "retired journal path is not a directory"}
	}
	if !stageExists && !journalExists {
		return RetiredOutputGCInspection{}, &transactionRecoveryAmbiguityError{TransactionID: transactionID, Reason: "retired transaction tombstone does not exist"}
	}
	if journalExists {
		stagedPath, _ := transactionJournalRecordPathAt(retiredTransactionJournalRoot, transactionID, transactionPhaseStaged)
		stagedData, readErr := readJournalRecord0600(workspace, stagedPath)
		if readErr != nil {
			return RetiredOutputGCInspection{}, readErr
		}
		staged, parseErr := parseTransactionJournal(stagedData)
		if parseErr != nil || staged.TransactionID != transactionID || staged.Phase != transactionPhaseStaged {
			return RetiredOutputGCInspection{}, &transactionRecoveryAmbiguityError{TransactionID: transactionID, Reason: "retired journal has no canonical staged authority"}
		}
		latest, latestErr := readLatestTransactionJournalAt(workspace, retiredTransactionJournalRoot, staged.binding())
		if latestErr != nil {
			return RetiredOutputGCInspection{}, latestErr
		}
		if latest.Phase != transactionPhaseComplete {
			return RetiredOutputGCInspection{}, &transactionRecoveryAmbiguityError{TransactionID: transactionID, Phase: latest.Phase, Reason: "retired journal is not complete"}
		}
	}
	if stageExists {
		if _, err := workspace.Walk(stagePath); err != nil {
			return RetiredOutputGCInspection{}, fmt.Errorf("inspect retired stage %s: %w", stagePath, err)
		}
		return RetiredOutputGCInspection{TransactionID: transactionID, Action: RetiredOutputGCRemoveStage}, nil
	}
	return RetiredOutputGCInspection{TransactionID: transactionID, Action: RetiredOutputGCRemoveJournal}, nil
}

// ApplyRetiredOutputGC executes at most the exact action returned by inspect.
// It re-inspects under the same held transaction and therefore cannot mutate a
// different tombstone after an operator copied an old action string.
func ApplyRetiredOutputGC(workspace *confinedfs.Transaction, expected RetiredOutputGCInspection) error {
	actual, err := InspectRetiredOutputGC(workspace, expected.TransactionID)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("retired output GC action changed: expected %q, current %q", expected.Action, actual.Action)
	}
	var target string
	switch actual.Action {
	case RetiredOutputGCRemoveStage:
		target = path.Join(retiredTransactionStageRoot, actual.TransactionID)
	case RetiredOutputGCRemoveJournal:
		target = path.Join(retiredTransactionJournalRoot, actual.TransactionID)
	default:
		return fmt.Errorf("unsupported retired output GC action %q", actual.Action)
	}
	if err := workspace.RemoveTree(target); err != nil {
		return fmt.Errorf("remove %s: %w", target, err)
	}
	if _, err := workspace.SyncDirectory(path.Dir(target)); err != nil {
		return fmt.Errorf("sync retired output GC metadata: %w", err)
	}
	return nil
}
