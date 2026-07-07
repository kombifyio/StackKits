package commands

import (
	"fmt"
	"path/filepath"
	"strings"
)

func resolveModuleArtifactPath(baseDir, artifactName string) (string, error) {
	if strings.Contains(baseDir, "\x00") || strings.Contains(artifactName, "\x00") {
		return "", fmt.Errorf("path contains invalid null byte")
	}
	if filepath.IsAbs(artifactName) {
		return "", fmt.Errorf("module artifact path %q must be relative", artifactName)
	}

	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve module directory: %w", err)
	}
	rel := filepath.Clean(filepath.FromSlash(artifactName))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("module artifact path %q escapes module directory", artifactName)
	}

	target := filepath.Clean(filepath.Join(baseAbs, rel))
	targetRel, err := filepath.Rel(baseAbs, target)
	if err != nil {
		return "", fmt.Errorf("resolve module artifact path: %w", err)
	}
	if targetRel == ".." || strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("module artifact path %q escapes module directory", artifactName)
	}
	return target, nil
}
