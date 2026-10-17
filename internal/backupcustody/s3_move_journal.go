package backupcustody

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const s3MoveJournalPath = custodyRelDir + "/s3-target-previous.json"

// MoveStoredS3TargetJournaled moves the stored target to a successor authority
// like RebindStoredS3Target, but first records the authority it moves away from
// in a private journal beside the custody. A process that dies after the move
// and before restore() leaves that journal behind, and SettleS3TargetMove moves
// custody back to it, so the custody never stays on a candidate authority
// whose change set did not commit. restore() moves custody back and drops the
// journal; call CommitS3TargetMove when the move becomes the new baseline.
//
// The journal holds only the authority (references and hashes), never target
// credentials. The caller must hold the owner-authorized lifecycle mutation.
func MoveStoredS3TargetJournaled(workspace string, successor S3TargetAuthority) (restore func() error, err error) {
	if pending, pendingErr := readS3MoveJournal(workspace); pendingErr != nil {
		return nil, pendingErr
	} else if pending != nil {
		return nil, errors.New("backupcustody: an earlier target move was not settled; recover it first")
	}
	previous, err := StoredS3TargetAuthority(workspace)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(previous)
	if err != nil {
		return nil, err
	}
	if err := writeS3MoveJournal(workspace, raw); err != nil {
		return nil, err
	}
	restore = func() error { return SettleS3TargetMove(workspace) }
	if err := RebindStoredS3Target(workspace, successor); err != nil {
		// The move may have landed before its read-back failed.
		return nil, errors.Join(err, restore())
	}
	return restore, nil
}

// SettleS3TargetMove completes a journaled move that did not commit: custody
// returns to the authority recorded before the move and the journal is removed.
// It changes nothing when no move is pending, and repeats harmlessly.
func SettleS3TargetMove(workspace string) error {
	previous, err := readS3MoveJournal(workspace)
	if err != nil {
		return err
	}
	if previous == nil {
		return nil
	}
	if err := RebindStoredS3Target(workspace, *previous); err != nil {
		return fmt.Errorf("backupcustody: restore the target authority recorded before the move: %w", err)
	}
	return removeS3MoveJournal(workspace)
}

// CommitS3TargetMove drops the journal once the move is the committed baseline.
// It changes nothing when no move is pending.
func CommitS3TargetMove(workspace string) error { return removeS3MoveJournal(workspace) }

func readS3MoveJournal(workspace string) (*S3TargetAuthority, error) {
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
	if _, err := transaction.Lstat(s3MoveJournalPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("backupcustody: inspect target move journal: %w", err)
	}
	raw, _, err := transaction.ReadStable(s3MoveJournalPath)
	if err != nil {
		return nil, fmt.Errorf("backupcustody: read target move journal: %w", err)
	}
	var authority S3TargetAuthority
	if err := json.Unmarshal(raw, &authority); err != nil || authority.Binding.BindingHash == "" {
		return nil, errors.New("backupcustody: target move journal is malformed")
	}
	return &authority, nil
}

func writeS3MoveJournal(workspace string, raw []byte) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(s3MoveJournalPath, raw)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("backupcustody: target move journal durability was not confirmed")
	}
	absolute := filepath.Join(workspace, filepath.FromSlash(s3MoveJournalPath))
	if err := ProtectPrivatePath(absolute, false); err != nil {
		return err
	}
	return RequirePrivatePath(absolute, false)
}

func removeS3MoveJournal(workspace string) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Close() }()
	info, err := transaction.Lstat(s3MoveJournalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return transaction.RemoveRegularFile(s3MoveJournalPath, info)
}
