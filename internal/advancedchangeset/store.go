package advancedchangeset

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const storeRelative = ".stackkit/advanced/change-sets"

type Store struct {
	WorkspaceRoot string
}

// Publish verifies before mutation, creates only owner-private directories,
// and atomically installs a content-addressed record without replacement.
func (store Store) Publish(record Record, request VerificationRequest) (string, error) {
	raw, err := record.MarshalCanonical()
	if err != nil {
		return "", wrap(ErrInvalid, "document", "cannot canonicalize record", err)
	}
	verified, err := Verify(raw, request)
	if err != nil {
		return "", err
	}
	fileName, err := fileNameForID(verified.ChangeSetID)
	if err != nil {
		return "", err
	}
	root, transaction, err := openStoreWorkspace(store.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = transaction.Close()
		_ = root.Close()
	}()
	for _, directory := range []string{".stackkit/advanced", storeRelative} {
		if err := transaction.MkdirAll(directory, 0o700); err != nil {
			return "", wrap(ErrIO, directory, "create private change-set directory", err)
		}
		absolute := filepath.Join(root.Name(), filepath.FromSlash(directory))
		if err := backupcustody.ProtectPrivatePath(absolute, true); err != nil {
			return "", wrap(ErrIO, directory, "apply owner-only directory permissions", err)
		}
	}
	view, err := root.View(storeRelative)
	if err != nil {
		return "", wrap(ErrIO, storeRelative, "open confined change-set view", err)
	}
	result, err := view.WriteAtomic0600NoReplace(fileName, raw)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			relative := storeRelative + "/" + fileName
			existing, _, readErr := transaction.ReadStable(relative)
			if readErr == nil && bytes.Equal(existing, raw) {
				absolute := filepath.Join(root.Name(), filepath.FromSlash(relative))
				if privateErr := backupcustody.RequirePrivatePath(absolute, false); privateErr != nil {
					return "", wrap(ErrIO, relative, "existing identical record is not owner-only", privateErr)
				}
				return relative, nil
			}
		}
		return "", wrap(ErrIO, fileName, "atomically publish without replacement", err)
	}
	if !result.Installed {
		return "", fail(ErrIO, fileName, "atomic publication did not install the record")
	}
	relative := storeRelative + "/" + fileName
	absolute := filepath.Join(root.Name(), filepath.FromSlash(relative))
	if err := backupcustody.ProtectPrivatePath(absolute, false); err != nil {
		return "", wrap(ErrIO, relative, "apply owner-only file permissions", err)
	}
	installed, _, err := transaction.ReadStable(relative)
	if err != nil {
		return "", wrap(ErrIO, relative, "re-read published record", err)
	}
	if !bytes.Equal(installed, raw) {
		return "", fail(ErrIO, relative, "published bytes differ from the verified record")
	}
	return relative, nil
}

// Load reads through the confined filesystem, verifies owner-only storage, and
// revalidates canonical identity, scope, freshness, and current owner custody.
func (store Store) Load(changeSetID string, request VerificationRequest) (Record, error) {
	fileName, err := fileNameForID(changeSetID)
	if err != nil {
		return Record{}, err
	}
	root, transaction, err := openStoreWorkspace(store.WorkspaceRoot)
	if err != nil {
		return Record{}, err
	}
	defer func() {
		_ = transaction.Close()
		_ = root.Close()
	}()
	relative := storeRelative + "/" + fileName
	absoluteStore := filepath.Join(root.Name(), filepath.FromSlash(storeRelative))
	absoluteFile := filepath.Join(root.Name(), filepath.FromSlash(relative))
	if err := backupcustody.RequirePrivatePath(absoluteStore, true); err != nil {
		return Record{}, wrap(ErrIO, storeRelative, "change-set directory is not owner-only", err)
	}
	if err := backupcustody.RequirePrivatePath(absoluteFile, false); err != nil {
		return Record{}, wrap(ErrIO, relative, "change-set file is not owner-only", err)
	}
	info, err := transaction.Lstat(relative)
	if err != nil {
		return Record{}, wrap(ErrIO, relative, "inspect stored record", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxDocumentBytes {
		return Record{}, fail(ErrInvalid, "document", "stored record has an invalid type or size")
	}
	raw, stableInfo, err := transaction.ReadStable(relative)
	if err != nil {
		return Record{}, wrap(ErrIO, relative, "read stable stored record", err)
	}
	if stableInfo.Size() != int64(len(raw)) || len(raw) > maxDocumentBytes {
		return Record{}, fail(ErrInvalid, "document", "stored record changed size while loading")
	}
	if err := backupcustody.RequirePrivatePath(absoluteFile, false); err != nil {
		return Record{}, wrap(ErrIO, relative, "change-set ACL changed while loading", err)
	}
	record, err := Verify(raw, request)
	if err != nil {
		return Record{}, err
	}
	if record.ChangeSetID != changeSetID {
		return Record{}, fail(ErrInvalid, "changeSetId", "does not match the requested content address")
	}
	return record, nil
}

func openStoreWorkspace(workspaceRoot string) (*confinedfs.Root, *confinedfs.Transaction, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil, nil, fail(ErrIO, "workspaceRoot", "is required")
	}
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, nil, wrap(ErrIO, "workspaceRoot", "cannot resolve absolute workspace", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, wrap(ErrIO, "workspaceRoot", "must be an existing plain directory", err)
	}
	root, err := confinedfs.Open(absolute)
	if err != nil {
		return nil, nil, wrap(ErrIO, "workspaceRoot", "open confined workspace", err)
	}
	transaction, err := root.BeginTransaction()
	if err != nil {
		_ = root.Close()
		return nil, nil, wrap(ErrIO, "workspaceRoot", "begin confined transaction", err)
	}
	return root, transaction, nil
}

func fileNameForID(changeSetID string) (string, error) {
	if !hashPattern.MatchString(changeSetID) {
		return "", fail(ErrInvalid, "changeSetId", "must be a canonical SHA-256 content address")
	}
	fileName := strings.TrimPrefix(changeSetID, "sha256:") + ".json"
	if strings.ContainsAny(fileName, `/\`) {
		return "", fail(ErrInvalid, "changeSetId", "must not contain path separators")
	}
	return fileName, nil
}
