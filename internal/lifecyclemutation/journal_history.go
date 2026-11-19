package lifecyclemutation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

func loadRecord(
	workspace string,
	transaction *confinedfs.Transaction,
) (Record, string, bool, error) {
	exists, info, err := transaction.Exists(activePath)
	if err != nil {
		return Record{}, "", false, err
	}
	if !exists {
		active, err := activeHistoryHeads(workspace, transaction)
		if err != nil {
			return Record{}, "", false, err
		}
		if len(active) != 0 {
			return Record{}, "", false, errors.New(
				"lifecycle mutation has active orphan history without a current pointer",
			)
		}
		return Record{}, "", false, nil
	}
	if !info.Mode().IsRegular() {
		return Record{}, "", false, errors.New("lifecycle mutation journal is not a private regular file")
	}
	if err := backupcustody.RequirePrivatePath(
		filepath.Join(workspace, filepath.FromSlash(activePath)), false,
	); err != nil {
		return Record{}, "", false, fmt.Errorf(
			"verify private lifecycle mutation journal: %w", err,
		)
	}
	raw, _, err := transaction.ReadStable(activePath)
	if err != nil {
		return Record{}, "", false, err
	}
	record, digest, err := decodeRecord(workspace, raw)
	if err != nil {
		return Record{}, "", false, err
	}
	latest, latestDigest, err := latestHistoryRecord(
		workspace, transaction, record.OperationID,
	)
	if err != nil {
		return Record{}, "", false, err
	}
	if latest.Sequence != record.Sequence || latestDigest != digest {
		return Record{}, "", false, errors.New(
			"lifecycle mutation active pointer is stale or replayed",
		)
	}
	active, err := activeHistoryHeads(workspace, transaction)
	if err != nil {
		return Record{}, "", false, err
	}
	switch {
	case recordBlocksMutation(record) &&
		(len(active) != 1 || active[record.OperationID] != digest):
		return Record{}, "", false, errors.New(
			"lifecycle mutation has divergent active operation histories",
		)
	case !recordBlocksMutation(record) && len(active) != 0:
		return Record{}, "", false, errors.New(
			"lifecycle mutation terminal pointer hides an active orphan history",
		)
	}
	return record, digest, true, nil
}

func decodeRecord(workspace string, raw []byte) (Record, string, error) {
	if len(raw) > maxRecordBytes {
		return Record{}, "", errors.New("lifecycle mutation journal exceeds its storage budget")
	}
	var record Record
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Record{}, "", fmt.Errorf("decode lifecycle mutation journal: %w", err)
	}
	if err := validateRecord(record); err != nil {
		return Record{}, "", err
	}
	canonical, err := signingBytes(record)
	if err != nil {
		return Record{}, "", err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(
		workspace, canonical, record.Signature,
	); err != nil {
		return Record{}, "", fmt.Errorf(
			"verify lifecycle mutation Owner signature: %w", err,
		)
	}
	if record.Signature.OwnerRef != record.OwnerRef ||
		record.WorkspaceHash != workspaceHash(workspace) {
		return Record{}, "", errors.New(
			"lifecycle mutation journal differs from Owner or workspace",
		)
	}
	expected, err := resolvedplan.CanonicalJSON(record)
	if err != nil || !bytes.Equal(raw, expected) {
		return Record{}, "", errors.New(
			"lifecycle mutation journal is not canonical",
		)
	}
	sum := sha256.Sum256(raw)
	return record, "sha256:" + hex.EncodeToString(sum[:]), nil
}

type historyRecord struct {
	record Record
	digest string
}

func latestHistoryRecord(
	workspace string,
	transaction *confinedfs.Transaction,
	operationID string,
) (Record, string, error) {
	operationRoot := operationsRoot + "/" + operationID
	exists, info, err := transaction.Exists(operationRoot)
	if err != nil || !exists {
		if err != nil {
			return Record{}, "", err
		}
		// Callers distinguish an operation that never journaled (for example a
		// transfer abort before any import) through os.ErrNotExist.
		return Record{}, "", fmt.Errorf("lifecycle mutation immutable history is missing: %w", os.ErrNotExist)
	}
	if !info.IsDir() {
		return Record{}, "", errors.New(
			"lifecycle mutation immutable history is not a directory",
		)
	}
	entries, err := transaction.Walk(operationRoot)
	if err != nil {
		return Record{}, "", err
	}
	if len(entries) > 257 {
		return Record{}, "", errors.New(
			"lifecycle mutation history exceeds the bounded phase limit",
		)
	}
	history := make([]historyRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.Info.IsDir() {
			continue
		}
		base := filepath.Base(filepath.FromSlash(entry.Path))
		if strings.HasPrefix(base, ".stackkit-tmp-") {
			if !entry.Info.Mode().IsRegular() || entry.Info.Size() > maxRecordBytes {
				return Record{}, "", errors.New(
					"lifecycle mutation atomic temporary artifact is invalid",
				)
			}
			if err := backupcustody.RequirePrivatePath(
				filepath.Join(workspace, filepath.FromSlash(entry.Path)), false,
			); err != nil {
				return Record{}, "", fmt.Errorf(
					"verify lifecycle mutation atomic temporary artifact: %w",
					err,
				)
			}
			continue
		}
		raw, _, err := transaction.ReadStable(entry.Path)
		if err != nil {
			return Record{}, "", err
		}
		if err := backupcustody.RequirePrivatePath(
			filepath.Join(workspace, filepath.FromSlash(entry.Path)), false,
		); err != nil {
			return Record{}, "", err
		}
		record, digest, err := decodeRecord(workspace, raw)
		if err != nil {
			return Record{}, "", err
		}
		if record.OperationID != operationID {
			return Record{}, "", errors.New(
				"lifecycle mutation history contains a substituted operation",
			)
		}
		expectedName := fmt.Sprintf(
			"%020d-%s.json",
			record.Sequence, strings.TrimPrefix(digest, "sha256:"),
		)
		if filepath.Base(filepath.FromSlash(entry.Path)) != expectedName {
			return Record{}, "", errors.New(
				"lifecycle mutation history filename differs from signed record",
			)
		}
		history = append(history, historyRecord{record: record, digest: digest})
	}
	sort.Slice(history, func(i, j int) bool {
		if history[i].record.Sequence == history[j].record.Sequence {
			return history[i].digest < history[j].digest
		}
		return history[i].record.Sequence < history[j].record.Sequence
	})
	if len(history) == 0 || history[0].record.Sequence != 1 ||
		history[0].record.PreviousRecordDigest != "" {
		return Record{}, "", errors.New(
			"lifecycle mutation history does not start at the signed origin",
		)
	}
	for index := 1; index < len(history); index++ {
		previous := history[index-1]
		current := history[index]
		if current.record.Sequence != previous.record.Sequence+1 ||
			current.record.PreviousRecordDigest != previous.digest {
			return Record{}, "", errors.New(
				"lifecycle mutation history is divergent or non-linear",
			)
		}
	}
	latest := history[len(history)-1]
	return latest.record, latest.digest, nil
}

func recoverLatestRecord(
	workspace, operationID string,
	transaction *confinedfs.Transaction,
	view confinedfs.View,
) (Record, string, error) {
	if !operationPattern.MatchString(operationID) {
		return Record{}, "", errors.New(
			"explicit lifecycle recovery operation ID is invalid",
		)
	}
	latest, latestDigest, err := latestHistoryRecord(
		workspace, transaction, operationID,
	)
	if err != nil {
		return Record{}, "", err
	}
	activeHeads, err := activeHistoryHeads(workspace, transaction)
	if err != nil {
		return Record{}, "", err
	}
	if recordBlocksMutation(latest) {
		if len(activeHeads) != 1 ||
			activeHeads[operationID] != latestDigest {
			return Record{}, "", errors.New(
				"explicit lifecycle recovery requires one unique requested active history",
			)
		}
	} else if len(activeHeads) != 0 {
		return Record{}, "", errors.New(
			"explicit lifecycle recovery terminal history is hidden by another active operation",
		)
	}
	exists, info, err := transaction.Exists(activePath)
	if err != nil {
		return Record{}, "", err
	}
	if exists && !info.Mode().IsRegular() {
		return Record{}, "", errors.New(
			"explicit lifecycle recovery requires a private regular active pointer",
		)
	}
	if exists {
		if err := backupcustody.RequirePrivatePath(
			filepath.Join(workspace, filepath.FromSlash(activePath)), false,
		); err != nil {
			return Record{}, "", fmt.Errorf("verify private recovery pointer: %w", err)
		}
		raw, _, readErr := transaction.ReadStable(activePath)
		if readErr != nil {
			return Record{}, "", readErr
		}
		active, activeDigest, decodeErr := decodeRecord(workspace, raw)
		if decodeErr != nil {
			return Record{}, "", errors.New(
				"explicit lifecycle recovery refuses a tampered active pointer",
			)
		}
		if recordBlocksMutation(active) &&
			active.OperationID != operationID {
			return Record{}, "", errors.New(
				"explicit lifecycle recovery cannot replace another active operation",
			)
		}
		if active.OperationID == operationID &&
			activeDigest == latestDigest {
			return latest, latestDigest, nil
		}
	}
	latestRaw, err := resolvedplan.CanonicalJSON(latest)
	if err != nil {
		return Record{}, "", err
	}
	if _, err := view.WriteAtomic0600(activePath, latestRaw); err != nil {
		return Record{}, "", fmt.Errorf(
			"repair stale lifecycle pointer during explicit recovery: %w", err,
		)
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(activePath)), false,
	); err != nil {
		return Record{}, "", err
	}
	return latest, latestDigest, nil
}

func activeHistoryHeads(
	workspace string,
	transaction *confinedfs.Transaction,
) (map[string]string, error) {
	entries, err := transaction.Walk(operationsRoot)
	if err != nil {
		return nil, err
	}
	operationIDs := map[string]struct{}{}
	prefix := operationsRoot + "/"
	for _, entry := range entries {
		if entry.Path == operationsRoot {
			continue
		}
		remainder := strings.TrimPrefix(entry.Path, prefix)
		operationID := strings.SplitN(remainder, "/", 2)[0]
		if operationID != "" {
			operationIDs[operationID] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(operationIDs))
	for operationID := range operationIDs {
		ordered = append(ordered, operationID)
	}
	sort.Strings(ordered)
	active := make(map[string]string)
	for _, operationID := range ordered {
		record, digest, err := latestHistoryRecord(
			workspace, transaction, operationID,
		)
		if err != nil {
			return nil, err
		}
		if recordBlocksMutation(record) {
			active[operationID] = digest
		}
	}
	if len(active) > 1 {
		return nil, errors.New(
			"lifecycle mutation history contains multiple active operations",
		)
	}
	return active, nil
}

func persistRecord(
	workspace string,
	transaction *confinedfs.Transaction,
	view confinedfs.View,
	record *Record,
	previousDigest string,
) (string, error) {
	if record == nil {
		return "", errors.New("lifecycle mutation record is required")
	}
	if previousDigest != "" {
		prior, info, readErr := transaction.ReadStable(activePath)
		if readErr != nil || !info.Mode().IsRegular() {
			return "", errors.New("read previous lifecycle mutation journal before commit")
		}
		previousSum := sha256.Sum256(prior)
		if "sha256:"+hex.EncodeToString(previousSum[:]) != previousDigest {
			return "", errors.New("previous lifecycle mutation journal digest changed before commit")
		}
	}
	record.PreviousRecordDigest = previousDigest
	record.Signature = localevidence.OwnerLifecycleMutationSignature{}
	canonical, err := signingBytes(*record)
	if err != nil {
		return "", err
	}
	record.Signature, err = localevidence.SignOwnerLifecycleMutation(workspace, canonical)
	if err != nil {
		return "", err
	}
	encoded, err := resolvedplan.CanonicalJSON(*record)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxRecordBytes {
		return "", errors.New("lifecycle mutation journal exceeds its storage budget")
	}
	sum := sha256.Sum256(encoded)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	operationRoot := operationsRoot + "/" + record.OperationID
	if err := transaction.MkdirAll(operationRoot, 0o700); err != nil {
		return "", err
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(operationRoot)), true,
	); err != nil {
		return "", err
	}
	historyPath := fmt.Sprintf(
		"%s/%020d-%s.json",
		operationRoot, record.Sequence, strings.TrimPrefix(digest, "sha256:"),
	)
	result, err := view.WriteAtomic0600NoReplace(historyPath, encoded)
	if err := settleJournalWrite(transaction, historyPath, encoded, result, err); err != nil {
		return "", fmt.Errorf("commit immutable lifecycle mutation phase: %w", err)
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(historyPath)), false,
	); err != nil {
		return digest, err
	}
	result, err = view.WriteAtomic0600(activePath, encoded)
	if err := settleJournalWrite(transaction, activePath, encoded, result, err); err != nil {
		return digest, err
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(workspace, filepath.FromSlash(activePath)), false,
	); err != nil {
		return digest, err
	}
	loaded, loadedDigest, exists, err := loadRecord(workspace, transaction)
	if err != nil || !exists || loaded.OperationID != record.OperationID ||
		loaded.Sequence != record.Sequence || loaded.Phase != record.Phase ||
		loaded.Status != record.Status || loadedDigest != digest {
		if err != nil {
			return digest, err
		}
		return digest, errors.New("re-read lifecycle mutation journal differs after commit")
	}
	return digest, nil
}

// An atomic writer can report a post-install durability or identity error after
// the signed bytes became visible. Re-read the exact file through the held
// transaction and flush its parent before deciding whether the journal commit
// is retryable. This also repairs a retry whose immutable history entry was
// installed before the previous process stopped.
func settleJournalWrite(transaction *confinedfs.Transaction, relative string, expected []byte, result confinedfs.AtomicWriteResult, writeErr error) error {
	if writeErr == nil {
		return nil
	}
	var confined *confinedfs.Error
	if errors.As(writeErr, &confined) && (confined.Code == confinedfs.ErrRootChanged || confined.Code == confinedfs.ErrIdentityUnsupported || confined.Code == confinedfs.ErrUnsafeEntry) {
		return writeErr
	}
	if result.Installed {
		writeErr = fmt.Errorf("journal bytes were installed before durability verification failed: %w", writeErr)
	}
	actual, info, readErr := transaction.ReadStable(relative)
	if readErr != nil || !info.Mode().IsRegular() || !bytes.Equal(actual, expected) {
		return writeErr
	}
	if _, syncErr := transaction.SyncDirectory(path.Dir(relative)); syncErr != nil {
		return errors.Join(writeErr, syncErr)
	}
	return nil
}

func signingBytes(record Record) ([]byte, error) {
	record.Signature = localevidence.OwnerLifecycleMutationSignature{}
	return resolvedplan.CanonicalJSON(record)
}
