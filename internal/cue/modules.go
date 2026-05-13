package cue

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// modulePath holds the name and full path of a discovered CUE module.
type modulePath struct {
	Name string
	Path string
}

// discoverModulePaths scans a directory for subdirectories containing module.cue.
// Directories starting with "_" are skipped.
func discoverModulePaths(modulesDir string) ([]modulePath, error) {
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read modules directory: %w", err)
	}

	var paths []modulePath
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), "_") {
			continue
		}

		modPath := filepath.Join(modulesDir, entry.Name())
		modCue := filepath.Join(modPath, "module.cue")
		if _, statErr := os.Stat(modCue); os.IsNotExist(statErr) {
			continue
		}

		paths = append(paths, modulePath{
			Name: entry.Name(),
			Path: modPath,
		})
	}

	return paths, nil
}
