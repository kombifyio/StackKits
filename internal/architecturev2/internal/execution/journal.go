package execution

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	transactionJournalVersion = "stackkit.architecture-v2-output-transaction.v1"
	transactionControlRoot    = ".stackkits-control"
	transactionJournalRoot    = transactionControlRoot + "/output-journals"
	transactionStagePrefix    = ".stackkit-v2-stage-"
	transactionBackupPrefix   = ".previous-managed-output-"
	transactionFailedPrefix   = ".failed-managed-output-"
	transactionIDHexLength    = sha256.Size
)

// transactionPhase is deliberately ordered. Its numeric rank is used only in
// journal record names; valid state-machine transitions remain explicit so a
// writer cannot skip a required write-ahead boundary merely by increasing the
// rank.
type transactionPhase string

const (
	transactionPhaseStaged                transactionPhase = "staged"
	transactionPhaseBackupIntent          transactionPhase = "backup-intent"
	transactionPhaseBackupComplete        transactionPhase = "backup-complete"
	transactionPhaseInstallIntent         transactionPhase = "install-intent"
	transactionPhaseInstallComplete       transactionPhase = "install-complete"
	transactionPhaseCommitCleanupIntent   transactionPhase = "commit-cleanup-intent"
	transactionPhaseRollbackIntent        transactionPhase = "rollback-intent"
	transactionPhaseRollbackComplete      transactionPhase = "rollback-complete"
	transactionPhaseRollbackCleanupIntent transactionPhase = "rollback-cleanup-intent"
	transactionPhaseComplete              transactionPhase = "complete"
)

var transactionPhaseRanks = map[transactionPhase]int{
	transactionPhaseStaged:                10,
	transactionPhaseBackupIntent:          20,
	transactionPhaseBackupComplete:        30,
	transactionPhaseInstallIntent:         40,
	transactionPhaseInstallComplete:       50,
	transactionPhaseCommitCleanupIntent:   60,
	transactionPhaseRollbackIntent:        70,
	transactionPhaseRollbackComplete:      80,
	transactionPhaseRollbackCleanupIntent: 90,
	transactionPhaseComplete:              100,
}

// transactionJournal is the complete, non-secret recovery authority for one
// managed-output swap. Every field is emitted in every phase. There are no
// timestamps or process-local identities: the caller-provided transaction ID
// and content digests are the only identities that survive a process crash.
type transactionJournal struct {
	Version            string           `json:"version"`
	TransactionID      string           `json:"transactionId"`
	OutputRoot         string           `json:"outputRoot"`
	StageContainer     string           `json:"stageContainer"`
	BackupRoot         string           `json:"backupRoot"`
	FailedRoot         string           `json:"failedRoot"`
	PlanDigest         string           `json:"planDigest"`
	ManifestDigest     string           `json:"manifestDigest"`
	ReceiptDigest      string           `json:"receiptDigest"`
	PreviousRootDigest string           `json:"previousRootDigest"`
	HadPrevious        bool             `json:"hadPrevious"`
	Phase              transactionPhase `json:"phase"`
}

// transactionJournalBinding is the immutable part of a journal. Keeping it a
// separate type makes caller-supplied expectations mandatory when a persisted
// record is read back; a self-consistent forged journal is not accepted merely
// because its JSON validates in isolation.
type transactionJournalBinding struct {
	TransactionID      string
	OutputRoot         string
	StageContainer     string
	BackupRoot         string
	FailedRoot         string
	PlanDigest         string
	ManifestDigest     string
	ReceiptDigest      string
	PreviousRootDigest string
	HadPrevious        bool
}

type transactionJournalErrorCode string

const (
	transactionJournalInvalid transactionJournalErrorCode = "journal-invalid"
	transactionJournalDrift   transactionJournalErrorCode = "journal-binding-drift"
	transactionJournalIO      transactionJournalErrorCode = "journal-io"
)

type transactionJournalError struct {
	Code  transactionJournalErrorCode
	Path  string
	Field string
	Want  string
	Got   string
	Err   error
}

func (e *transactionJournalError) Error() string {
	if e == nil {
		return ""
	}
	location := e.Path
	if e.Field != "" {
		if location != "" {
			location += "."
		}
		location += e.Field
	}
	message := string(e.Code)
	if location != "" {
		message += " at " + location
	}
	if e.Want != "" || e.Got != "" {
		message += fmt.Sprintf(": want %q, got %q", e.Want, e.Got)
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *transactionJournalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newTransactionJournal(binding transactionJournalBinding, phase transactionPhase) (transactionJournal, error) {
	journal := transactionJournal{
		Version:            transactionJournalVersion,
		TransactionID:      binding.TransactionID,
		OutputRoot:         binding.OutputRoot,
		StageContainer:     binding.StageContainer,
		BackupRoot:         binding.BackupRoot,
		FailedRoot:         binding.FailedRoot,
		PlanDigest:         binding.PlanDigest,
		ManifestDigest:     binding.ManifestDigest,
		ReceiptDigest:      binding.ReceiptDigest,
		PreviousRootDigest: binding.PreviousRootDigest,
		HadPrevious:        binding.HadPrevious,
		Phase:              phase,
	}
	if err := validateTransactionJournal(journal); err != nil {
		return transactionJournal{}, err
	}
	return journal, nil
}

// transactionBindingForStage derives every transaction-owned name from the
// random transaction ID already contained in CreatePrivateDirectory's stage
// name. Callers cannot independently choose backup or failed-output paths.
func transactionBindingForStage(stageContainer, outputRoot, planDigest, manifestDigest, receiptDigest, previousRootDigest string, hadPrevious bool) (transactionJournalBinding, error) {
	transactionID, err := transactionIDFromStageContainer(stageContainer)
	if err != nil {
		return transactionJournalBinding{}, err
	}
	binding := transactionJournalBinding{
		TransactionID:      transactionID,
		OutputRoot:         outputRoot,
		StageContainer:     stageContainer,
		BackupRoot:         path.Join(stageContainer, transactionBackupPrefix+transactionID),
		FailedRoot:         path.Join(stageContainer, transactionFailedPrefix+transactionID),
		PlanDigest:         planDigest,
		ManifestDigest:     manifestDigest,
		ReceiptDigest:      receiptDigest,
		PreviousRootDigest: previousRootDigest,
		HadPrevious:        hadPrevious,
	}
	journal, err := newTransactionJournal(binding, transactionPhaseStaged)
	if err != nil {
		return transactionJournalBinding{}, err
	}
	return journal.binding(), nil
}

func transactionIDFromStageContainer(stageContainer string) (string, error) {
	if _, err := validatePortablePath(stageContainer); err != nil {
		return "", journalInvalid("stageContainer", stageContainer, "canonical transaction stage container", err)
	}
	if path.Dir(stageContainer) != "." || !strings.HasPrefix(stageContainer, transactionStagePrefix) {
		return "", journalInvalid("stageContainer", stageContainer, transactionStagePrefix+"<transaction-id>", nil)
	}
	transactionID := strings.TrimPrefix(stageContainer, transactionStagePrefix)
	if !validTransactionID(transactionID) {
		return "", journalInvalid("transactionId", transactionID, "32 lowercase hexadecimal characters", nil)
	}
	return transactionID, nil
}

func (j transactionJournal) binding() transactionJournalBinding {
	return transactionJournalBinding{
		TransactionID:      j.TransactionID,
		OutputRoot:         j.OutputRoot,
		StageContainer:     j.StageContainer,
		BackupRoot:         j.BackupRoot,
		FailedRoot:         j.FailedRoot,
		PlanDigest:         j.PlanDigest,
		ManifestDigest:     j.ManifestDigest,
		ReceiptDigest:      j.ReceiptDigest,
		PreviousRootDigest: j.PreviousRootDigest,
		HadPrevious:        j.HadPrevious,
	}
}

func (j transactionJournal) marshalCanonical() ([]byte, error) {
	if err := validateTransactionJournal(j); err != nil {
		return nil, err
	}
	data, err := resolvedplan.CanonicalJSON(j)
	if err != nil {
		return nil, &transactionJournalError{Code: transactionJournalInvalid, Path: "journal", Err: err}
	}
	return data, nil
}

func parseTransactionJournal(data []byte) (transactionJournal, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var journal transactionJournal
	if err := decoder.Decode(&journal); err != nil {
		return transactionJournal{}, &transactionJournalError{Code: transactionJournalInvalid, Path: "journal", Err: err}
	}
	if err := requireJSONEOF(decoder); err != nil {
		return transactionJournal{}, &transactionJournalError{Code: transactionJournalInvalid, Path: "journal", Err: err}
	}
	canonical, err := journal.marshalCanonical()
	if err != nil {
		return transactionJournal{}, err
	}
	if !bytes.Equal(data, canonical) {
		return transactionJournal{}, &transactionJournalError{Code: transactionJournalInvalid, Path: "journal", Err: errors.New("journal is not byte-for-byte canonical JSON")}
	}
	return journal, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("journal contains more than one JSON value")
		}
		return fmt.Errorf("decode trailing journal data: %w", err)
	}
	return nil
}

func validateTransactionJournal(j transactionJournal) error {
	if j.Version != transactionJournalVersion {
		return journalInvalid("version", j.Version, transactionJournalVersion, nil)
	}
	if !validTransactionID(j.TransactionID) {
		return journalInvalid("transactionId", j.TransactionID, "32 lowercase hexadecimal characters", nil)
	}
	expectedStage := transactionStagePrefix + j.TransactionID
	if j.StageContainer != expectedStage {
		return journalInvalid("stageContainer", j.StageContainer, expectedStage, nil)
	}
	outputRoot, err := validatePortablePath(j.OutputRoot)
	if err != nil || outputRoot != j.OutputRoot {
		return journalInvalid("outputRoot", j.OutputRoot, "canonical portable relative output root", err)
	}
	firstOutputSegment := strings.Split(j.OutputRoot, "/")[0]
	if firstOutputSegment == transactionControlRoot || strings.HasPrefix(firstOutputSegment, ".stackkit-v2-") {
		return journalInvalid("outputRoot", j.OutputRoot, "path outside reserved StackKits control namespaces", nil)
	}
	if overlapPortablePaths(j.OutputRoot, transactionJournalRoot) || overlapPortablePaths(j.OutputRoot, j.StageContainer) {
		return journalInvalid("outputRoot", j.OutputRoot, "path disjoint from journal and stage control trees", nil)
	}
	expectedBackup := path.Join(j.StageContainer, transactionBackupPrefix+j.TransactionID)
	if j.BackupRoot != expectedBackup {
		return journalInvalid("backupRoot", j.BackupRoot, expectedBackup, nil)
	}
	expectedFailed := path.Join(j.StageContainer, transactionFailedPrefix+j.TransactionID)
	if j.FailedRoot != expectedFailed {
		return journalInvalid("failedRoot", j.FailedRoot, expectedFailed, nil)
	}
	for field, value := range map[string]string{
		"planDigest": j.PlanDigest, "manifestDigest": j.ManifestDigest, "receiptDigest": j.ReceiptDigest,
	} {
		if !validJournalDigest(value) {
			return journalInvalid(field, value, "lowercase sha256:<64-hex> digest", nil)
		}
	}
	if j.HadPrevious {
		if !validJournalDigest(j.PreviousRootDigest) {
			return journalInvalid("previousRootDigest", j.PreviousRootDigest, "lowercase sha256:<64-hex> digest when hadPrevious is true", nil)
		}
	} else if j.PreviousRootDigest != "" {
		return journalInvalid("previousRootDigest", j.PreviousRootDigest, "empty when hadPrevious is false", nil)
	}
	if _, ok := transactionPhaseRanks[j.Phase]; !ok {
		return journalInvalid("phase", string(j.Phase), "known ordered transaction phase", nil)
	}
	return nil
}

func validateTransactionJournalBinding(j transactionJournal, expected transactionJournalBinding) error {
	if err := validateTransactionJournal(j); err != nil {
		return err
	}
	actual := j.binding()
	checks := []struct {
		field string
		want  string
		got   string
	}{
		{"transactionId", expected.TransactionID, actual.TransactionID},
		{"outputRoot", expected.OutputRoot, actual.OutputRoot},
		{"stageContainer", expected.StageContainer, actual.StageContainer},
		{"backupRoot", expected.BackupRoot, actual.BackupRoot},
		{"failedRoot", expected.FailedRoot, actual.FailedRoot},
		{"planDigest", expected.PlanDigest, actual.PlanDigest},
		{"manifestDigest", expected.ManifestDigest, actual.ManifestDigest},
		{"receiptDigest", expected.ReceiptDigest, actual.ReceiptDigest},
		{"previousRootDigest", expected.PreviousRootDigest, actual.PreviousRootDigest},
	}
	for _, check := range checks {
		if check.want != check.got {
			return &transactionJournalError{Code: transactionJournalDrift, Path: "journal", Field: check.field, Want: check.want, Got: check.got}
		}
	}
	if expected.HadPrevious != actual.HadPrevious {
		return &transactionJournalError{Code: transactionJournalDrift, Path: "journal", Field: "hadPrevious", Want: fmt.Sprint(expected.HadPrevious), Got: fmt.Sprint(actual.HadPrevious)}
	}
	return nil
}

func advanceTransactionJournal(current transactionJournal, next transactionPhase) (transactionJournal, error) {
	if err := validateTransactionJournal(current); err != nil {
		return transactionJournal{}, err
	}
	if !validTransactionPhaseTransition(current.Phase, next, current.HadPrevious) {
		return transactionJournal{}, journalInvalid("phase", string(next), "valid successor of "+string(current.Phase), nil)
	}
	current.Phase = next
	return current, nil
}

func validTransactionPhaseTransition(current, next transactionPhase, hadPrevious bool) bool {
	if _, ok := transactionPhaseRanks[next]; !ok {
		return false
	}
	if transactionPhaseRanks[next] <= transactionPhaseRanks[current] {
		return false
	}
	switch current {
	case transactionPhaseStaged:
		if next == transactionPhaseRollbackIntent {
			return true
		}
		if hadPrevious {
			return next == transactionPhaseBackupIntent
		}
		return next == transactionPhaseInstallIntent
	case transactionPhaseBackupIntent:
		return hadPrevious && (next == transactionPhaseBackupComplete || next == transactionPhaseRollbackIntent)
	case transactionPhaseBackupComplete:
		return hadPrevious && (next == transactionPhaseInstallIntent || next == transactionPhaseRollbackIntent)
	case transactionPhaseInstallIntent:
		return next == transactionPhaseInstallComplete || next == transactionPhaseRollbackIntent
	case transactionPhaseInstallComplete:
		return next == transactionPhaseCommitCleanupIntent || next == transactionPhaseRollbackIntent
	case transactionPhaseCommitCleanupIntent:
		return next == transactionPhaseComplete
	case transactionPhaseRollbackIntent:
		return next == transactionPhaseRollbackComplete
	case transactionPhaseRollbackComplete:
		return next == transactionPhaseRollbackCleanupIntent
	case transactionPhaseRollbackCleanupIntent:
		return next == transactionPhaseComplete
	default:
		return false
	}
}

func transactionJournalDirectory(transactionID string) (string, error) {
	if !validTransactionID(transactionID) {
		return "", journalInvalid("transactionId", transactionID, "32 lowercase hexadecimal characters", nil)
	}
	return path.Join(transactionJournalRoot, transactionID), nil
}

func transactionJournalRecordPath(transactionID string, phase transactionPhase) (string, error) {
	directory, err := transactionJournalDirectory(transactionID)
	if err != nil {
		return "", err
	}
	rank, ok := transactionPhaseRanks[phase]
	if !ok {
		return "", journalInvalid("phase", string(phase), "known ordered transaction phase", nil)
	}
	return path.Join(directory, fmt.Sprintf("%03d-%s.json", rank, phase)), nil
}

// writeTransactionJournal appends one immutable phase record. Transaction does
// not currently expose View.WriteAtomic0600, so replacement of one mutable
// journal file would reopen a pathname or create a remove/rename gap. Instead,
// an exclusive, synced 0600 pending record is atomically renamed to its final
// phase name through the already-held Transaction. Completed records are never
// overwritten, which also makes phase rollback and digest drift observable.
func writeTransactionJournal(workspace *confinedfs.Transaction, previous *transactionJournal, next transactionJournal) error {
	if workspace == nil {
		return &transactionJournalError{Code: transactionJournalIO, Path: transactionJournalRoot, Err: errors.New("held workspace transaction is required")}
	}
	if err := validateTransactionJournal(next); err != nil {
		return err
	}
	if previous == nil {
		if next.Phase != transactionPhaseStaged {
			return journalInvalid("phase", string(next.Phase), string(transactionPhaseStaged), nil)
		}
	} else {
		if err := validateTransactionJournalBinding(next, previous.binding()); err != nil {
			return err
		}
		if !validTransactionPhaseTransition(previous.Phase, next.Phase, next.HadPrevious) {
			return journalInvalid("phase", string(next.Phase), "valid successor of "+string(previous.Phase), nil)
		}
	}
	data, err := next.marshalCanonical()
	if err != nil {
		return err
	}
	directory, _ := transactionJournalDirectory(next.TransactionID)
	if err := workspace.MkdirAll(directory, 0o700); err != nil {
		return &transactionJournalError{Code: transactionJournalIO, Path: directory, Err: err}
	}
	recordPath, _ := transactionJournalRecordPath(next.TransactionID, next.Phase)
	if exists, _, err := workspace.Exists(recordPath); err != nil {
		return &transactionJournalError{Code: transactionJournalIO, Path: recordPath, Err: err}
	} else if exists {
		persisted, readErr := readJournalRecord0600(workspace, recordPath)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(persisted, data) {
			return &transactionJournalError{Code: transactionJournalDrift, Path: recordPath, Err: errors.New("existing phase record differs from requested canonical journal")}
		}
		return nil
	}
	pendingPath := recordPath + ".pending"
	if exists, _, err := workspace.Exists(pendingPath); err != nil {
		return &transactionJournalError{Code: transactionJournalIO, Path: pendingPath, Err: err}
	} else if exists {
		persisted, readErr := readJournalRecord0600(workspace, pendingPath)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(persisted, data) {
			return &transactionRecoveryAmbiguityError{TransactionID: next.TransactionID, Phase: next.Phase, Reason: "pending journal record differs from requested canonical phase"}
		}
	} else if err := workspace.WriteFileExclusive(pendingPath, data, 0o600); err != nil {
		return &transactionJournalError{Code: transactionJournalIO, Path: pendingPath, Err: err}
	}
	installed, err := workspace.Rename(pendingPath, recordPath)
	if err != nil {
		return &transactionJournalError{Code: transactionJournalIO, Path: recordPath, Err: err}
	}
	if !installed {
		return &transactionJournalError{Code: transactionJournalIO, Path: recordPath, Err: errors.New("held rename did not install journal record")}
	}
	syncDirectories := []string{directory}
	if previous == nil {
		// The first record may also have created every control-directory
		// ancestor. Flush each parent from the record directory back to the
		// held workspace root so the recovery authority itself survives a
		// power loss, not merely the record bytes.
		syncDirectories = append(syncDirectories, transactionJournalRoot, transactionControlRoot, ".")
	}
	for _, syncDirectory := range syncDirectories {
		if _, err := workspace.SyncDirectory(syncDirectory); err != nil {
			return &transactionJournalError{Code: transactionJournalIO, Path: syncDirectory, Err: fmt.Errorf("sync installed journal phase metadata: %w", err)}
		}
	}
	return nil
}

func readLatestTransactionJournal(workspace *confinedfs.Transaction, expected transactionJournalBinding) (transactionJournal, error) {
	if workspace == nil {
		return transactionJournal{}, &transactionJournalError{Code: transactionJournalIO, Path: transactionJournalRoot, Err: errors.New("held workspace transaction is required")}
	}
	directory, err := transactionJournalDirectory(expected.TransactionID)
	if err != nil {
		return transactionJournal{}, err
	}
	entries, err := workspace.Walk(directory)
	if err != nil {
		return transactionJournal{}, &transactionJournalError{Code: transactionJournalIO, Path: directory, Err: err}
	}
	records := make([]transactionJournal, 0, len(entries))
	for _, entry := range entries {
		if entry.Path == directory {
			if !entry.Info.IsDir() {
				return transactionJournal{}, &transactionRecoveryAmbiguityError{TransactionID: expected.TransactionID, Reason: "journal transaction path is not a directory"}
			}
			continue
		}
		if entry.Info.IsDir() || path.Dir(entry.Path) != directory {
			return transactionJournal{}, &transactionRecoveryAmbiguityError{TransactionID: expected.TransactionID, Reason: "journal transaction directory contains an unexpected nested entry"}
		}
		if strings.HasSuffix(entry.Path, ".pending") {
			return transactionJournal{}, &transactionRecoveryAmbiguityError{TransactionID: expected.TransactionID, Reason: "journal contains an interrupted pending phase record"}
		}
		data, readErr := readJournalRecord0600(workspace, entry.Path)
		if readErr != nil {
			return transactionJournal{}, readErr
		}
		journal, parseErr := parseTransactionJournal(data)
		if parseErr != nil {
			return transactionJournal{}, parseErr
		}
		wantPath, pathErr := transactionJournalRecordPath(journal.TransactionID, journal.Phase)
		if pathErr != nil || wantPath != entry.Path {
			return transactionJournal{}, &transactionRecoveryAmbiguityError{TransactionID: expected.TransactionID, Phase: journal.Phase, Reason: "journal phase record name does not match canonical content"}
		}
		if bindingErr := validateTransactionJournalBinding(journal, expected); bindingErr != nil {
			return transactionJournal{}, bindingErr
		}
		records = append(records, journal)
	}
	if len(records) == 0 {
		return transactionJournal{}, &transactionRecoveryAmbiguityError{TransactionID: expected.TransactionID, Reason: "journal contains no completed phase record"}
	}
	sort.Slice(records, func(i, j int) bool {
		return transactionPhaseRanks[records[i].Phase] < transactionPhaseRanks[records[j].Phase]
	})
	if records[0].Phase != transactionPhaseStaged {
		return transactionJournal{}, &transactionRecoveryAmbiguityError{TransactionID: expected.TransactionID, Phase: records[0].Phase, Reason: "journal does not begin at staged"}
	}
	for i := 1; i < len(records); i++ {
		if !validTransactionPhaseTransition(records[i-1].Phase, records[i].Phase, expected.HadPrevious) {
			return transactionJournal{}, &transactionRecoveryAmbiguityError{TransactionID: expected.TransactionID, Phase: records[i].Phase, Reason: "journal phase series contains an invalid transition"}
		}
	}
	return records[len(records)-1], nil
}

// findPendingOutputTransaction discovers an earlier durable transaction for
// the same governed output root. The output lock must already be held. Any
// malformed control entry fails closed because silently skipping a forged or
// partial journal would allow a new swap to destroy recovery evidence.
func findPendingOutputTransaction(workspace *confinedfs.Transaction, outputRoot string) (transactionJournal, bool, error) {
	if workspace == nil {
		return transactionJournal{}, false, &transactionJournalError{Code: transactionJournalIO, Path: transactionJournalRoot, Err: errors.New("held workspace transaction is required")}
	}
	exists, info, err := workspace.Exists(transactionJournalRoot)
	if err != nil {
		return transactionJournal{}, false, &transactionJournalError{Code: transactionJournalIO, Path: transactionJournalRoot, Err: err}
	}
	if !exists {
		return transactionJournal{}, false, nil
	}
	if !info.IsDir() {
		return transactionJournal{}, false, &transactionRecoveryAmbiguityError{Reason: "journal control root is not a directory"}
	}
	entries, err := workspace.Walk(transactionJournalRoot)
	if err != nil {
		return transactionJournal{}, false, &transactionJournalError{Code: transactionJournalIO, Path: transactionJournalRoot, Err: err}
	}
	transactionIDs := make([]string, 0)
	for _, entry := range entries {
		if entry.Path == transactionJournalRoot {
			continue
		}
		relative := strings.TrimPrefix(entry.Path, transactionJournalRoot+"/")
		if strings.Contains(relative, "/") {
			continue
		}
		if !entry.Info.IsDir() || !validTransactionID(relative) {
			return transactionJournal{}, false, &transactionRecoveryAmbiguityError{TransactionID: relative, Reason: "journal control root contains an unexpected entry"}
		}
		transactionIDs = append(transactionIDs, relative)
	}

	var found *transactionJournal
	for _, transactionID := range transactionIDs {
		stagedPath, _ := transactionJournalRecordPath(transactionID, transactionPhaseStaged)
		data, readErr := readJournalRecord0600(workspace, stagedPath)
		if readErr != nil {
			return transactionJournal{}, false, &transactionRecoveryAmbiguityError{TransactionID: transactionID, Reason: "journal has no readable canonical staged record"}
		}
		staged, parseErr := parseTransactionJournal(data)
		if parseErr != nil {
			return transactionJournal{}, false, parseErr
		}
		if staged.TransactionID != transactionID || staged.Phase != transactionPhaseStaged {
			return transactionJournal{}, false, &transactionRecoveryAmbiguityError{TransactionID: transactionID, Reason: "staged journal identity does not match its control directory"}
		}
		latest, latestErr := readLatestTransactionJournal(workspace, staged.binding())
		if latestErr != nil {
			return transactionJournal{}, false, latestErr
		}
		sameOutput, compareErr := confinedfs.OutputLockRootsEqual(latest.OutputRoot, outputRoot)
		if compareErr != nil {
			return transactionJournal{}, false, &transactionRecoveryAmbiguityError{TransactionID: transactionID, Phase: latest.Phase, Reason: "journal output root cannot be compared with the governed lock identity"}
		}
		if !sameOutput {
			continue
		}
		if found != nil {
			return transactionJournal{}, false, &transactionRecoveryAmbiguityError{TransactionID: transactionID, Phase: latest.Phase, Reason: "more than one durable transaction claims the governed output root"}
		}
		candidate := latest
		found = &candidate
	}
	if found == nil {
		return transactionJournal{}, false, nil
	}
	return *found, true, nil
}

func advanceAndWriteTransactionJournal(workspace *confinedfs.Transaction, current *transactionJournal, next transactionPhase) error {
	if current == nil {
		return &transactionJournalError{Code: transactionJournalInvalid, Path: "journal", Err: errors.New("current transaction journal is required")}
	}
	advanced, err := advanceTransactionJournal(*current, next)
	if err != nil {
		return err
	}
	if err := writeTransactionJournal(workspace, current, advanced); err != nil {
		return err
	}
	*current = advanced
	return nil
}

func removeTransactionJournal(workspace *confinedfs.Transaction, binding transactionJournalBinding) error {
	directory, err := transactionJournalDirectory(binding.TransactionID)
	if err != nil {
		return err
	}
	if err := workspace.RemoveTree(directory); err != nil {
		return &transactionJournalError{Code: transactionJournalIO, Path: directory, Err: err}
	}
	if _, err := workspace.SyncDirectory(transactionJournalRoot); err != nil {
		return &transactionJournalError{Code: transactionJournalIO, Path: transactionJournalRoot, Err: fmt.Errorf("sync removed transaction journal metadata: %w", err)}
	}
	return nil
}

func readJournalRecord0600(workspace *confinedfs.Transaction, relative string) ([]byte, error) {
	data, info, err := workspace.ReadStable(relative)
	if err != nil {
		return nil, &transactionJournalError{Code: transactionJournalIO, Path: relative, Err: err}
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != os.FileMode(0o600) {
		return nil, &transactionJournalError{Code: transactionJournalInvalid, Path: relative, Field: "mode", Want: "0600", Got: fmt.Sprintf("%04o", info.Mode().Perm())}
	}
	return data, nil
}

func validTransactionID(value string) bool {
	if len(value) != transactionIDHexLength {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == transactionIDHexLength/2 && value == strings.ToLower(value)
}

func validJournalDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func journalInvalid(field, got, want string, err error) error {
	return &transactionJournalError{Code: transactionJournalInvalid, Path: "journal", Field: field, Want: want, Got: got, Err: err}
}

type transactionObservedObject string

const (
	transactionObjectAbsent     transactionObservedObject = "absent"
	transactionObjectPresent    transactionObservedObject = "present"
	transactionObjectMatching   transactionObservedObject = "matching"
	transactionObjectMismatched transactionObservedObject = "mismatched"
	transactionObjectUnsafe     transactionObservedObject = "unsafe"
)

type transactionRecoveryObservation struct {
	Stage  transactionObservedObject
	Output transactionObservedObject
	Backup transactionObservedObject
	Failed transactionObservedObject
}

type transactionRecoveryAction string

const (
	transactionRecoveryDiscardUncommitted    transactionRecoveryAction = "discard-uncommitted"
	transactionRecoveryMovePreviousToBackup  transactionRecoveryAction = "move-previous-to-backup"
	transactionRecoveryInstallStaged         transactionRecoveryAction = "install-staged"
	transactionRecoveryVerifyInstalled       transactionRecoveryAction = "verify-installed"
	transactionRecoveryBeginRollback         transactionRecoveryAction = "begin-rollback"
	transactionRecoveryMoveInstalledToFailed transactionRecoveryAction = "move-installed-to-failed"
	transactionRecoveryRestoreBackup         transactionRecoveryAction = "restore-backup"
	transactionRecoveryFinalizeRollback      transactionRecoveryAction = "finalize-rollback"
	transactionRecoveryCleanupCommitted      transactionRecoveryAction = "cleanup-committed"
	transactionRecoveryCleanupRolledBack     transactionRecoveryAction = "cleanup-rolled-back"
	transactionRecoveryRemoveJournal         transactionRecoveryAction = "remove-journal"
)

// transactionRecoveryAmbiguityError is intentionally distinct from ordinary
// validation and IO errors. Recovery integration must surface it to the
// operator and leave every controlled path untouched.
type transactionRecoveryAmbiguityError struct {
	TransactionID string
	Phase         transactionPhase
	Reason        string
}

func (e *transactionRecoveryAmbiguityError) Error() string {
	if e == nil {
		return ""
	}
	message := "transaction recovery is ambiguous"
	if e.TransactionID != "" {
		message += " for " + e.TransactionID
	}
	if e.Phase != "" {
		message += " at " + string(e.Phase)
	}
	if e.Reason != "" {
		message += ": " + e.Reason
	}
	return message
}

// classifyTransactionRecovery is pure: it performs no filesystem operation
// and returns only the single next deterministic action. Unknown, unsafe, or
// contradictory observations always produce a typed ambiguity error.
func classifyTransactionRecovery(journal transactionJournal, observed transactionRecoveryObservation) (transactionRecoveryAction, error) {
	if err := validateTransactionJournal(journal); err != nil {
		return "", err
	}
	if err := validateRecoveryObservation(observed); err != nil {
		return "", ambiguity(journal, err.Error())
	}
	if observed.Stage == transactionObjectUnsafe || observed.Output == transactionObjectUnsafe || observed.Backup == transactionObjectUnsafe || observed.Failed == transactionObjectUnsafe {
		return "", ambiguity(journal, "a transaction-owned path is unsafe")
	}

	switch journal.Phase {
	case transactionPhaseStaged:
		if observed.Stage == transactionObjectMatching && observed.Backup == transactionObjectAbsent && observed.Failed == transactionObjectAbsent && previousRootUntouched(journal, observed.Output) {
			return transactionRecoveryDiscardUncommitted, nil
		}
	case transactionPhaseBackupIntent:
		if !journal.HadPrevious || observed.Stage != transactionObjectMatching || observed.Failed != transactionObjectAbsent {
			break
		}
		if observed.Output == transactionObjectPresent && observed.Backup == transactionObjectAbsent {
			return transactionRecoveryMovePreviousToBackup, nil
		}
		if observed.Output == transactionObjectAbsent && observed.Backup == transactionObjectPresent {
			return transactionRecoveryInstallStaged, nil
		}
	case transactionPhaseBackupComplete:
		if journal.HadPrevious && observed.Stage == transactionObjectMatching && observed.Output == transactionObjectAbsent && observed.Backup == transactionObjectPresent && observed.Failed == transactionObjectAbsent {
			return transactionRecoveryInstallStaged, nil
		}
	case transactionPhaseInstallIntent, transactionPhaseInstallComplete:
		if observed.Failed != transactionObjectAbsent || !expectedBackupPresence(journal, observed.Backup) {
			break
		}
		if observed.Stage == transactionObjectMatching && observed.Output == transactionObjectAbsent {
			return transactionRecoveryInstallStaged, nil
		}
		if observed.Stage == transactionObjectAbsent && observed.Output == transactionObjectMatching {
			return transactionRecoveryVerifyInstalled, nil
		}
		if observed.Stage == transactionObjectAbsent && observed.Output == transactionObjectMismatched {
			return transactionRecoveryBeginRollback, nil
		}
	case transactionPhaseCommitCleanupIntent:
		if observed.Stage == transactionObjectAbsent && observed.Output == transactionObjectMatching && expectedBackupPresence(journal, observed.Backup) && observed.Failed == transactionObjectAbsent {
			return transactionRecoveryCleanupCommitted, nil
		}
	case transactionPhaseRollbackIntent:
		return classifyRollbackRecovery(journal, observed)
	case transactionPhaseRollbackComplete, transactionPhaseRollbackCleanupIntent:
		if rollbackIsComplete(journal, observed) {
			return transactionRecoveryCleanupRolledBack, nil
		}
	case transactionPhaseComplete:
		if observed.Stage == transactionObjectAbsent && observed.Backup == transactionObjectAbsent && observed.Failed == transactionObjectAbsent {
			return transactionRecoveryRemoveJournal, nil
		}
	}
	return "", ambiguity(journal, fmt.Sprintf("phase/path state is contradictory (stage=%s output=%s backup=%s failed=%s)", observed.Stage, observed.Output, observed.Backup, observed.Failed))
}

func classifyRollbackRecovery(journal transactionJournal, observed transactionRecoveryObservation) (transactionRecoveryAction, error) {
	if journal.HadPrevious {
		if observed.Backup == transactionObjectPresent && observed.Failed == transactionObjectAbsent {
			switch observed.Output {
			case transactionObjectMatching, transactionObjectMismatched:
				return transactionRecoveryMoveInstalledToFailed, nil
			case transactionObjectAbsent:
				return transactionRecoveryRestoreBackup, nil
			}
		}
		if observed.Backup == transactionObjectPresent && observed.Output == transactionObjectAbsent && observed.Failed == transactionObjectPresent {
			return transactionRecoveryRestoreBackup, nil
		}
		if observed.Backup == transactionObjectAbsent && observed.Output == transactionObjectPresent && observed.Failed == transactionObjectPresent {
			return transactionRecoveryFinalizeRollback, nil
		}
	} else {
		if observed.Backup != transactionObjectAbsent {
			return "", ambiguity(journal, "transaction without previous output owns a backup")
		}
		if observed.Failed == transactionObjectAbsent && (observed.Output == transactionObjectMatching || observed.Output == transactionObjectMismatched) {
			return transactionRecoveryMoveInstalledToFailed, nil
		}
		if observed.Output == transactionObjectAbsent && observed.Failed == transactionObjectPresent {
			return transactionRecoveryFinalizeRollback, nil
		}
		if observed.Output == transactionObjectAbsent && observed.Failed == transactionObjectAbsent && observed.Stage == transactionObjectMatching {
			return transactionRecoveryFinalizeRollback, nil
		}
	}
	return "", ambiguity(journal, "rollback path identities do not determine one safe next action")
}

func rollbackIsComplete(journal transactionJournal, observed transactionRecoveryObservation) bool {
	if observed.Backup != transactionObjectAbsent {
		return false
	}
	if journal.HadPrevious {
		return observed.Output == transactionObjectPresent && (observed.Stage == transactionObjectAbsent || observed.Stage == transactionObjectMatching) && (observed.Failed == transactionObjectAbsent || observed.Failed == transactionObjectPresent)
	}
	return observed.Output == transactionObjectAbsent && (observed.Stage == transactionObjectAbsent || observed.Stage == transactionObjectMatching) && (observed.Failed == transactionObjectAbsent || observed.Failed == transactionObjectPresent)
}

func previousRootUntouched(journal transactionJournal, output transactionObservedObject) bool {
	if journal.HadPrevious {
		return output == transactionObjectPresent
	}
	return output == transactionObjectAbsent
}

func expectedBackupPresence(journal transactionJournal, backup transactionObservedObject) bool {
	if journal.HadPrevious {
		return backup == transactionObjectPresent
	}
	return backup == transactionObjectAbsent
}

func validateRecoveryObservation(observed transactionRecoveryObservation) error {
	validContent := func(value transactionObservedObject) bool {
		switch value {
		case transactionObjectAbsent, transactionObjectPresent, transactionObjectMatching, transactionObjectMismatched, transactionObjectUnsafe:
			return true
		default:
			return false
		}
	}
	validPresence := func(value transactionObservedObject) bool {
		return value == transactionObjectAbsent || value == transactionObjectPresent || value == transactionObjectUnsafe
	}
	if !validContent(observed.Stage) || !validContent(observed.Output) {
		return errors.New("stage or output observation is unknown")
	}
	if !validPresence(observed.Backup) || !validPresence(observed.Failed) {
		return errors.New("backup or failed observation is not a presence state")
	}
	return nil
}

func ambiguity(journal transactionJournal, reason string) error {
	return &transactionRecoveryAmbiguityError{TransactionID: journal.TransactionID, Phase: journal.Phase, Reason: reason}
}
