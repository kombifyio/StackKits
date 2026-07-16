package generationartifact

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// verifyClosedExecutorTree proves that outputRoot is a closed executor input
// set. Only manifest artifacts, the canonical manifest, the canonical receipt,
// and the directories required to reach those files may exist. Mutable
// executor state deliberately has no implicit exception here: it belongs in a
// separately governed root.
func verifyClosedExecutorTree(plan VerifiedPlan, root string, manifest ArtifactManifest) error {
	absRoot, err := validateArtifactRoot(root)
	if err != nil {
		return err
	}
	expected, err := newClosedExecutorTree(plan, manifest)
	if err != nil {
		return err
	}
	if err := requireControlFiles(absRoot, expected.controlFiles); err != nil {
		return err
	}
	outputPath, err := governedOutputTreePath(absRoot, plan.outputRoot)
	if err != nil {
		return err
	}

	seenFiles := make(map[string]string, len(expected.files))
	seenDirectories := make(map[string]string, len(expected.directories))
	err = filepath.WalkDir(outputPath, func(filePath string, _ fs.DirEntry, walkErr error) error {
		return expected.observe(absRoot, filePath, walkErr, seenFiles, seenDirectories)
	})
	if err != nil {
		return err
	}
	return expected.requireComplete(seenFiles, seenDirectories)
}

type closedExecutorTree struct {
	files        map[string]string
	directories  map[string]string
	controlFiles []string
}

func governedOutputTreePath(absRoot, outputRoot string) (string, error) {
	if outputRoot == "." {
		return absRoot, nil
	}
	outputPath, err := validateArtifactComponents(absRoot, outputRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(outputPath)
	if err != nil {
		return "", wrap(ErrIO, outputRoot, "inspect governed output root", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fail(ErrInvalidPath, outputRoot, "governed output root must be a non-symlink directory")
	}
	return outputPath, nil
}

func (t closedExecutorTree) observe(absRoot, filePath string, walkErr error, seenFiles, seenDirectories map[string]string) error {
	if walkErr != nil {
		return wrap(ErrIO, filePath, "walk governed executor input tree", walkErr)
	}
	portable, err := workspaceRelativePortablePath(absRoot, filePath)
	if err != nil {
		return err
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return wrap(ErrIO, portable, "inspect executor input entry", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fail(ErrPathEscape, portable, "executor input tree must not contain symlinks")
	}
	if info.IsDir() {
		return observeClosedEntry(t.directories, seenDirectories, portable, "directory")
	}
	if !info.Mode().IsRegular() {
		return fail(ErrInvalidPath, portable, "executor input tree may contain only declared regular files and required directories")
	}
	return observeClosedEntry(t.files, seenFiles, portable, "regular file")
}

func observeClosedEntry(allowed, seen map[string]string, portable, entryKind string) error {
	key := portablePathKey(portable)
	if _, exists := allowed[key]; !exists {
		return fail(ErrArtifactChanged, portable, "undeclared %s exists in the closed executor input tree", entryKind)
	}
	if previous, duplicate := seen[key]; duplicate {
		return fail(ErrDuplicateArtifact, portable, "%s aliases already-seen path %q", entryKind, previous)
	}
	seen[key] = portable
	return nil
}

func (t closedExecutorTree) requireComplete(seenFiles, seenDirectories map[string]string) error {
	for key, expectedPath := range t.files {
		if _, found := seenFiles[key]; !found {
			return fail(ErrArtifactMissing, expectedPath, "declared executor input is absent from the closed output tree")
		}
	}
	for key, expectedPath := range t.directories {
		if _, found := seenDirectories[key]; !found {
			return fail(ErrArtifactMissing, expectedPath, "required executor input directory is absent from the closed output tree")
		}
	}
	return nil
}

func newClosedExecutorTree(plan VerifiedPlan, manifest ArtifactManifest) (closedExecutorTree, error) {
	result := closedExecutorTree{
		files:       make(map[string]string, len(manifest.Artifacts)+2),
		directories: make(map[string]string),
	}
	for index, artifact := range manifest.Artifacts {
		if err := result.addFile(plan.outputRoot, artifact.Path, fmt.Sprintf("manifest.artifacts[%d].path", index)); err != nil {
			return closedExecutorTree{}, err
		}
	}
	result.controlFiles = metadataControlPaths(plan.outputRoot)
	for _, controlPath := range result.controlFiles {
		if err := result.addFile(plan.outputRoot, controlPath, "generation.controlFiles"); err != nil {
			return closedExecutorTree{}, err
		}
	}
	return result, nil
}

func (t *closedExecutorTree) addFile(outputRoot, filePath, valuePath string) error {
	if _, err := validatePortablePath(filePath); err != nil {
		return wrap(ErrInvalidPath, valuePath, "invalid closed executor input path", err)
	}
	if !pathWithinOutputRoot(outputRoot, filePath) {
		return fail(ErrInvalidPlan, valuePath, "executor input %q is outside governed outputRoot %q", filePath, outputRoot)
	}
	key := portablePathKey(filePath)
	if previous, exists := t.files[key]; exists {
		return fail(ErrDuplicateArtifact, valuePath, "executor input %q aliases %q", filePath, previous)
	}
	if previous, exists := t.directories[key]; exists {
		return fail(ErrDuplicateArtifact, valuePath, "executor input file %q aliases required directory %q", filePath, previous)
	}
	t.files[key] = filePath

	directory := path.Dir(filePath)
	for {
		directoryKey := portablePathKey(directory)
		if previous, exists := t.files[directoryKey]; exists {
			return fail(ErrDuplicateArtifact, valuePath, "required directory %q aliases executor input file %q", directory, previous)
		}
		t.directories[directoryKey] = directory
		if directory == outputRoot || (outputRoot == "." && directory == ".") {
			return nil
		}
		parent := path.Dir(directory)
		if parent == directory || (parent == "." && outputRoot != ".") {
			return fail(ErrInvalidPlan, valuePath, "executor input %q does not descend from outputRoot %q", filePath, outputRoot)
		}
		directory = parent
	}
}

func metadataControlPaths(outputRoot string) []string {
	metadataRoot := path.Join(outputRoot, ".stackkit")
	if outputRoot == "." {
		metadataRoot = ".stackkit"
	}
	return []string{
		path.Join(metadataRoot, ArtifactManifestFileName),
		path.Join(metadataRoot, GenerationReceiptFileName),
	}
}

func pathWithinOutputRoot(outputRoot, filePath string) bool {
	if outputRoot == "." {
		return true
	}
	return strings.HasPrefix(filePath, outputRoot+"/")
}

func requireControlFiles(absRoot string, paths []string) error {
	for _, controlPath := range paths {
		candidate, err := validateArtifactComponents(absRoot, controlPath)
		if err != nil {
			return err
		}
		info, err := os.Lstat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				return wrap(ErrArtifactMissing, controlPath, "generation control file does not exist", err)
			}
			return wrap(ErrIO, controlPath, "inspect generation control file", err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fail(ErrInvalidPath, controlPath, "generation control file must be a regular non-symlink file")
		}
	}
	return nil
}

func workspaceRelativePortablePath(absRoot, filePath string) (string, error) {
	relative, err := filepath.Rel(absRoot, filePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fail(ErrPathEscape, filePath, "executor input entry escapes the workspace root")
	}
	if relative == "." {
		return ".", nil
	}
	portable := filepath.ToSlash(relative)
	if _, err := validatePortablePath(portable); err != nil {
		return "", wrap(ErrInvalidPath, portable, "executor input path is not portable", err)
	}
	return portable, nil
}
