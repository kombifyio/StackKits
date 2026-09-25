package advancedrollback

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var snapshotIDPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

const maxJournalBytes = 1 << 20

// JournalPath is the workspace-relative journal of one target checkpoint.
func JournalPath(snapshotID string) (string, error) {
	if !snapshotIDPattern.MatchString(snapshotID) {
		return "", &Error{Code: ErrInvalid, Detail: "target checkpoint must be a sha256:<hex> executor-state snapshot ID"}
	}
	return path.Join(JournalDir, strings.TrimPrefix(snapshotID, "sha256:")+".json"), nil
}

// LoadJournal returns the journal of the rollback to snapshotID, if any.
func LoadJournal(workspaceRoot, snapshotID string) (Journal, bool, error) {
	relative, err := JournalPath(snapshotID)
	if err != nil {
		return Journal{}, false, err
	}
	absolute, err := confinedFile(workspaceRoot, relative, false)
	if err != nil {
		return Journal{}, false, err
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return Journal{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxJournalBytes {
		return Journal{}, false, &Error{Code: ErrInvalid, Detail: "rollback journal is not a bounded regular file"}
	}
	raw, err := os.ReadFile(absolute)
	if err != nil {
		return Journal{}, false, fmt.Errorf("read rollback journal: %w", err)
	}
	var journal Journal
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return Journal{}, false, &Error{Code: ErrInvalid, Detail: "rollback journal is not a stackkit.rollback-journal/v1 document", Err: err}
	}
	if journal.SchemaVersion != JournalSchemaVersion || journal.TargetSnapshotID != snapshotID ||
		journal.RollbackID == "" || journal.Results == nil {
		return Journal{}, false, &Error{Code: ErrInvalid, Detail: "rollback journal does not belong to the target checkpoint"}
	}
	for _, step := range journal.Steps {
		if err := validateStep(step); err != nil {
			return Journal{}, false, err
		}
	}
	return journal, true, nil
}

// SaveJournal atomically replaces the journal of journal.TargetSnapshotID.
func SaveJournal(workspaceRoot string, journal Journal) error {
	relative, err := JournalPath(journal.TargetSnapshotID)
	if err != nil {
		return err
	}
	absolute, err := confinedFile(workspaceRoot, relative, true)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(absolute, append(raw, '\n'), 0o600)
}

func validateStep(step Step) error {
	clean := path.Clean(step.RuntimeRoot)
	if clean != step.RuntimeRoot || !strings.HasPrefix(clean, ".stackkit/runtime/") || path.Base(clean) != "opentofu" {
		return &Error{Code: ErrInvalid, Detail: fmt.Sprintf("stack runtime root %q is outside the runtime tree", step.RuntimeRoot)}
	}
	switch step.Action {
	case ActionDestroyed, ActionRestored, ActionRecreated, ActionUnchanged:
		return nil
	default:
		return &Error{Code: ErrInvalid, Detail: fmt.Sprintf("stack %s has unknown action %q", step.StackID, step.Action)}
	}
}

// confinedFile resolves a workspace-relative path below .stackkit and refuses
// symlinks and non-directories on the way. create makes missing directories
// owner-only.
func confinedFile(workspaceRoot, relative string, create bool) (string, error) {
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	clean := path.Clean(relative)
	if clean != relative || !strings.HasPrefix(clean, ".stackkit/") {
		return "", &Error{Code: ErrInvalid, Detail: fmt.Sprintf("path %q is outside the workspace .stackkit tree", relative)}
	}
	directory := workspace
	for _, segment := range strings.Split(path.Dir(clean), "/") {
		directory = filepath.Join(directory, segment)
		info, err := os.Lstat(directory)
		switch {
		case errors.Is(err, os.ErrNotExist) && create:
			if err := os.Mkdir(directory, 0o700); err != nil {
				return "", fmt.Errorf("create %s: %w", directory, err)
			}
		case errors.Is(err, os.ErrNotExist):
			return filepath.Join(workspace, filepath.FromSlash(clean)), nil
		case err != nil:
			return "", fmt.Errorf("inspect %s: %w", directory, err)
		case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
			return "", fmt.Errorf("%s must be a plain directory", directory)
		}
	}
	return filepath.Join(workspace, filepath.FromSlash(clean)), nil
}

func writeAtomic(target string, data []byte, mode os.FileMode) error {
	if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", target)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("stage %s: %w", target, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s: %w", target, err)
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect %s: %w", target, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync %s: %w", target, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", target, err)
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("install %s: %w", target, err)
	}
	return nil
}
