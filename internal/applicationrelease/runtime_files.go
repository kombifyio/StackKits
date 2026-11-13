package applicationrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// RuntimeFile is one private runtime file of a native installation that a
// release checkpoint retains beside the application's volumes. For the
// Marketplace these are its Compose recipe, its environment (APP_KEY and
// database credentials), its custody (API token, server identity) and its
// SSH key: Coolify migrates its database forward on start, so a rollback
// restores the database together with the custody that matches it.
type RuntimeFile struct {
	// Path is relative to the workspace root.
	Path string `json:"path"`
	// SHA256 is the lowercase hex digest of the checkpointed bytes.
	SHA256 string `json:"sha256"`
}

const maxRuntimeFileBytes = 1 << 20

// retainedRuntimeFile is the operation-bound private copy of one file.
func retainedRuntimeFile(operation string, file RuntimeFile) string {
	return path.Join(Directory, operation+".files", path.Base(file.Path))
}

func runtimeFileDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validRuntimeFiles(files []RuntimeFile) bool {
	seen := map[string]bool{}
	for _, file := range files {
		clean := path.Clean(file.Path)
		base := path.Base(clean)
		if file.Path == "" || clean != file.Path || path.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
			len(file.SHA256) != 64 || seen[base] {
			return false
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return false
		}
		seen[base] = true
	}
	return true
}

func withTransaction(workspace string, run func(*confinedfs.Root, *confinedfs.Transaction) error) error {
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
	return run(root, transaction)
}

func readPrivate(transaction *confinedfs.Transaction, relative string) ([]byte, error) {
	data, info, err := transaction.ReadStableBounded(relative, maxRuntimeFileBytes)
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || len(data) == 0 {
		clear(data)
		return nil, fmt.Errorf("applicationrelease: private runtime file %s is unavailable or not private", relative)
	}
	return data, nil
}

func writePrivate(root *confinedfs.Root, transaction *confinedfs.Transaction, relative string, data []byte) error {
	directory := path.Dir(relative)
	if err := transaction.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	view, err := root.View(directory)
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600(path.Base(relative), data)
	return err
}

// CaptureRuntimeFiles reads the live private files and returns their
// checkpoint digests.
func CaptureRuntimeFiles(workspace string, paths []string) ([]RuntimeFile, error) {
	files := make([]RuntimeFile, 0, len(paths))
	err := withTransaction(workspace, func(_ *confinedfs.Root, transaction *confinedfs.Transaction) error {
		for _, relative := range paths {
			data, err := readPrivate(transaction, relative)
			if err != nil {
				return err
			}
			files = append(files, RuntimeFile{Path: relative, SHA256: runtimeFileDigest(data)})
			clear(data)
		}
		return nil
	})
	if err == nil && !validRuntimeFiles(files) {
		err = errors.New("applicationrelease: runtime file set is invalid")
	}
	return files, err
}

// RetainRuntimeFiles copies every live runtime file of the record into its
// operation-bound private copy. A copy already read back is kept; a live file
// that no longer has its checkpoint digest refuses, since nothing may change
// it while the checkpoint is open.
func RetainRuntimeFiles(workspace string, record Record) error {
	return withTransaction(workspace, func(root *confinedfs.Root, transaction *confinedfs.Transaction) error {
		for _, file := range record.RuntimeFiles {
			retained := retainedRuntimeFile(record.OperationID, file)
			if data, err := readPrivate(transaction, retained); err == nil {
				same := runtimeFileDigest(data) == file.SHA256
				clear(data)
				if same {
					continue
				}
			}
			live, err := readPrivate(transaction, file.Path)
			if err != nil {
				return err
			}
			if runtimeFileDigest(live) != file.SHA256 {
				clear(live)
				return fmt.Errorf("applicationrelease: %s changed after the release checkpoint started", file.Path)
			}
			err = writePrivate(root, transaction, retained, live)
			clear(live)
			if err != nil {
				return err
			}
			data, err := readPrivate(transaction, retained)
			if err != nil {
				return err
			}
			same := runtimeFileDigest(data) == file.SHA256
			clear(data)
			if !same {
				return fmt.Errorf("applicationrelease: the retained copy of %s did not read back", file.Path)
			}
		}
		return nil
	})
}

// VerifyRuntimeFilesAtCheckpoint reports whether every live runtime file of
// the record still has its checkpoint digest.
func VerifyRuntimeFilesAtCheckpoint(workspace string, record Record) error {
	return withTransaction(workspace, func(_ *confinedfs.Root, transaction *confinedfs.Transaction) error {
		for _, file := range record.RuntimeFiles {
			live, err := readPrivate(transaction, file.Path)
			if err != nil {
				return err
			}
			same := runtimeFileDigest(live) == file.SHA256
			clear(live)
			if !same {
				return fmt.Errorf("applicationrelease: %s differs from release checkpoint %s", file.Path, record.OperationID)
			}
		}
		return nil
	})
}

// VerifyRetainedRuntimeFiles reports whether every retained copy of the
// record still has its checkpoint digest. It writes nothing, so a rollback
// checks it before any destructive volume restore.
func VerifyRetainedRuntimeFiles(workspace string, record Record) error {
	return withTransaction(workspace, func(_ *confinedfs.Root, transaction *confinedfs.Transaction) error {
		for _, file := range record.RuntimeFiles {
			data, err := readPrivate(transaction, retainedRuntimeFile(record.OperationID, file))
			if err != nil {
				return err
			}
			same := runtimeFileDigest(data) == file.SHA256
			clear(data)
			if !same {
				return fmt.Errorf("applicationrelease: the retained copy of %s differs from its checkpoint; live data was not changed", file.Path)
			}
		}
		return nil
	})
}

// RestoreRuntimeFiles verifies every retained copy, then writes each back
// over its live file and reads it back.
func RestoreRuntimeFiles(workspace string, record Record) error {
	return withTransaction(workspace, func(root *confinedfs.Root, transaction *confinedfs.Transaction) error {
		values := make(map[string][]byte, len(record.RuntimeFiles))
		defer func() {
			for _, data := range values {
				clear(data)
			}
		}()
		for _, file := range record.RuntimeFiles {
			data, err := readPrivate(transaction, retainedRuntimeFile(record.OperationID, file))
			if err != nil {
				return err
			}
			values[file.Path] = data
			if runtimeFileDigest(data) != file.SHA256 {
				return fmt.Errorf("applicationrelease: the retained copy of %s differs from its checkpoint; nothing was restored", file.Path)
			}
		}
		for _, file := range record.RuntimeFiles {
			if err := writePrivate(root, transaction, file.Path, values[file.Path]); err != nil {
				return fmt.Errorf("applicationrelease: restore %s: %w", file.Path, err)
			}
			live, err := readPrivate(transaction, file.Path)
			if err != nil {
				return err
			}
			same := runtimeFileDigest(live) == file.SHA256
			clear(live)
			if !same {
				return fmt.Errorf("applicationrelease: restored %s differs from its checkpoint", file.Path)
			}
		}
		return nil
	})
}

// RemoveRetainedRuntimeFiles removes the operation-bound copies.
func RemoveRetainedRuntimeFiles(workspace string, record Record) error {
	if len(record.RuntimeFiles) == 0 {
		return nil
	}
	return withTransaction(workspace, func(_ *confinedfs.Root, transaction *confinedfs.Transaction) error {
		directory := path.Join(Directory, record.OperationID+".files")
		exists, _, err := transaction.Exists(directory)
		if err != nil || !exists {
			return err
		}
		return transaction.RemoveTree(directory)
	})
}
