package generationartifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func PersistManifest(filePath string, plan VerifiedPlan, manifest ArtifactManifest) error {
	if err := plan.RequireReady(ExecutionPhaseGeneration); err != nil {
		return err
	}
	if manifest.Binding != plan.Binding() {
		return fail(ErrBindingMismatch, "manifest.binding", "does not match the current ResolvedPlan")
	}
	if err := validateManifestAgainstPlan(plan, manifest); err != nil {
		return err
	}
	data, err := manifest.MarshalCanonical()
	if err != nil {
		return err
	}
	return persist0600(filePath, data)
}

func PersistReceipt(filePath string, plan VerifiedPlan, manifest ArtifactManifest, receipt GenerationReceipt) error {
	if err := plan.RequireReady(ExecutionPhaseGeneration); err != nil {
		return err
	}
	if err := VerifyReceipt(plan, manifest, receipt); err != nil {
		return err
	}
	data, err := receipt.MarshalCanonical()
	if err != nil {
		return err
	}
	return persist0600(filePath, data)
}

func ReadManifest(filePath string) (ArtifactManifest, error) {
	data, err := readCanonicalControl0600(filePath, "artifact manifest")
	if err != nil {
		return ArtifactManifest{}, err
	}
	var manifest ArtifactManifest
	if err := decodeStrictJSON(data, &manifest); err != nil {
		return ArtifactManifest{}, wrap(ErrInvalidContract, filePath, "decode artifact manifest", err)
	}
	canonical, err := manifest.MarshalCanonical()
	if err != nil {
		return ArtifactManifest{}, err
	}
	if !bytes.Equal(data, canonical) {
		return ArtifactManifest{}, fail(ErrNonCanonical, filePath, "artifact manifest is not byte-for-byte canonical JSON")
	}
	return manifest, nil
}

func ReadReceipt(filePath string) (GenerationReceipt, error) {
	data, err := readCanonicalControl0600(filePath, "generation receipt")
	if err != nil {
		return GenerationReceipt{}, err
	}
	var receipt GenerationReceipt
	if err := decodeStrictJSON(data, &receipt); err != nil {
		return GenerationReceipt{}, wrap(ErrInvalidContract, filePath, "decode generation receipt", err)
	}
	canonical, err := receipt.MarshalCanonical()
	if err != nil {
		return GenerationReceipt{}, err
	}
	if !bytes.Equal(data, canonical) {
		return GenerationReceipt{}, fail(ErrNonCanonical, filePath, "generation receipt is not byte-for-byte canonical JSON")
	}
	return receipt, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("multiple JSON values")
	} else if err != io.EOF {
		return fmt.Errorf("invalid trailing data: %w", err)
	}
	return nil
}

func persist0600(target string, data []byte) error {
	if target == "" {
		return fail(ErrInvalidPath, "output", "target path is required")
	}
	directory := filepath.Dir(target)
	// Inspect the existing ancestor chain before MkdirAll: otherwise a symlinked
	// parent could cause directory creation outside the caller's intended tree.
	if err := rejectAnySymlinkInChain(target); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return wrap(ErrIO, directory, "create output directory", err)
	}
	if err := rejectSymlinkChain(target); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return wrap(ErrIO, target, "create temporary artifact", err)
	}
	temporaryPath := temporary.Name()
	installed := false
	defer func() {
		if installed {
			return
		}
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return wrap(ErrIO, temporaryPath, "set temporary artifact permissions", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return wrap(ErrIO, temporaryPath, "write temporary artifact", err)
	}
	if err := temporary.Sync(); err != nil {
		return wrap(ErrIO, temporaryPath, "sync temporary artifact", err)
	}
	if err := temporary.Close(); err != nil {
		return wrap(ErrIO, temporaryPath, "close temporary artifact", err)
	}
	// Re-check immediately before replacement so a parent or target swapped to
	// a symlink during the write window is rejected instead of followed.
	if err := rejectSymlinkChain(target); err != nil {
		return err
	}
	if err := atomicReplace0600(temporaryPath, target); err != nil {
		return wrap(ErrIO, target, "atomically install artifact", err)
	}
	installed = true
	if err := os.Chmod(target, 0o600); err != nil {
		return wrap(ErrIO, target, "enforce artifact permissions", err)
	}
	return nil
}

func rejectSymlinkChain(target string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return wrap(ErrIO, target, "resolve persistence target", err)
	}
	if info, err := os.Lstat(absolute); err == nil && info.IsDir() {
		return fail(ErrInvalidPath, absolute, "persistence target is a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return wrap(ErrIO, absolute, "inspect persistence target", err)
	}
	return rejectAnySymlinkInChain(absolute)
}

func rejectAnySymlinkInChain(value string) error {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return wrap(ErrIO, value, "resolve path for symlink inspection", err)
	}
	current := filepath.Clean(absolute)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fail(ErrPathEscape, current, "persistence target and parent components must not be symlinks")
			}
		} else if !os.IsNotExist(err) {
			return wrap(ErrIO, current, "inspect persistence path", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return nil
}

func readRegularNoSymlink(filePath, label string) ([]byte, error) {
	return readRegularNoSymlinkWithModeAfterOpen(filePath, label, nil, nil)
}

func readRegularNoSymlinkAfterOpen(filePath, label string, afterOpen func()) ([]byte, error) {
	return readRegularNoSymlinkWithModeAfterOpen(filePath, label, nil, afterOpen)
}

func readCanonicalControl0600(filePath, label string) ([]byte, error) {
	requiredMode := os.FileMode(0o600)
	return readRegularNoSymlinkWithModeAfterOpen(filePath, label, &requiredMode, nil)
}

func readRegularNoSymlinkWithModeAfterOpen(filePath, label string, requiredMode *os.FileMode, afterOpen func()) ([]byte, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, fail(ErrInvalidPath, label, "path is required")
	}
	if err := rejectAnySymlinkInChain(filePath); err != nil {
		return nil, err
	}
	expectedInfo, err := os.Lstat(filePath)
	if err != nil {
		return nil, wrap(ErrIO, filePath, "read "+label, err)
	}
	if !expectedInfo.Mode().IsRegular() {
		return nil, fail(ErrInvalidPath, filePath, "%s must be a regular file", label)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, wrap(ErrIO, filePath, "open "+label, err)
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, wrap(ErrIO, filePath, "stat opened "+label, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(expectedInfo, openedInfo) {
		return nil, fail(ErrArtifactChanged, filePath, "%s changed between path validation and open", label)
	}
	if err := requireReadMode(filePath, label, openedInfo, requiredMode); err != nil {
		return nil, err
	}
	if afterOpen != nil {
		afterOpen()
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, wrap(ErrIO, filePath, "read opened "+label, err)
	}
	if err := verifyStableReadPath(filePath, label, file, openedInfo, requiredMode); err != nil {
		return nil, err
	}
	return data, nil
}

func verifyStableReadPath(filePath, label string, file *os.File, openedInfo os.FileInfo, requiredMode *os.FileMode) error {
	afterReadInfo, err := file.Stat()
	if err != nil {
		return wrap(ErrIO, filePath, "restat opened "+label, err)
	}
	if !os.SameFile(openedInfo, afterReadInfo) || openedInfo.Size() != afterReadInfo.Size() || !openedInfo.ModTime().Equal(afterReadInfo.ModTime()) {
		return fail(ErrArtifactChanged, filePath, "%s changed while it was read", label)
	}
	if err := requireReadMode(filePath, label, afterReadInfo, requiredMode); err != nil {
		return err
	}
	if err := rejectAnySymlinkInChain(filePath); err != nil {
		return err
	}
	currentInfo, err := os.Lstat(filePath)
	if err != nil {
		return wrap(ErrIO, filePath, "restat "+label+" path", err)
	}
	if !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		return fail(ErrArtifactChanged, filePath, "%s path changed while it was read", label)
	}
	if err := requireReadMode(filePath, label, currentInfo, requiredMode); err != nil {
		return err
	}
	return nil
}

func requireReadMode(filePath, label string, info os.FileInfo, requiredMode *os.FileMode) error {
	if requiredMode == nil || runtime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm() != requiredMode.Perm() {
		return fail(ErrArtifactChanged, filePath, "%s permission mode is %04o, required %04o", label, info.Mode().Perm(), requiredMode.Perm())
	}
	return nil
}
