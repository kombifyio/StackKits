//go:build publisher

package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type journeyImageUpdate struct {
	path          string
	before, after []byte
	mode          os.FileMode
}

// prepareJourneyImageUpdate preserves a module-owned journey while advancing its
// catalog-owned image. A drifted or ambiguous image stops the whole bump.
func prepareJourneyImageUpdate(moduleDir, service, oldImage, newImage string) (*journeyImageUpdate, error) {
	testsDir := filepath.Join(moduleDir, "tests")
	info, err := os.Lstat(testsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("journey tests path is not a directory: %s", testsDir)
	}
	marker := filepath.Join(testsDir, ".custom-journey")
	info, err = os.Lstat(marker)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("custom journey marker is not a regular file: %s", marker)
	}
	script := filepath.Join(testsDir, "integration_test.sh")
	scriptInfo, err := os.Lstat(script)
	if err != nil {
		return nil, err
	}
	if !scriptInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("custom journey script is not a regular file: %s", script)
	}
	path := filepath.Join(testsDir, "reference-compose.yml")
	info, err = os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("custom journey compose is not a regular file: %s", path)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse custom journey compose: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("parse additional custom journey document: %w", err)
		}
		return nil, fmt.Errorf("custom journey compose must contain one document")
	}
	if len(doc.Content) != 1 {
		return nil, fmt.Errorf("custom journey compose has no document root")
	}
	mappingValue := func(n *yaml.Node, key string) *yaml.Node {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var result *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				if result != nil {
					return nil
				}
				result = n.Content[i+1]
			}
		}
		return result
	}
	services := mappingValue(doc.Content[0], "services")
	selected := mappingValue(services, service)
	image := mappingValue(selected, "image")
	if image == nil || image.Kind != yaml.ScalarNode || image.Tag != "!!str" || image.Value != oldImage {
		return nil, fmt.Errorf("custom journey %s service %s image differs from current catalog", path, service)
	}
	lines := strings.Split(string(source), "\n")
	if image.Line < 1 || image.Line > len(lines) || strings.Count(lines[image.Line-1], oldImage) != 1 {
		return nil, fmt.Errorf("custom journey image cannot be safely updated in %s", path)
	}
	lines[image.Line-1] = strings.Replace(lines[image.Line-1], oldImage, newImage, 1)
	return &journeyImageUpdate{path: path, before: source, after: []byte(strings.Join(lines, "\n")), mode: info.Mode().Perm()}, nil
}

// writeJourneyImage installs a complete file in the same directory, avoiding a
// truncated journey if a write fails before the rename.
func writeJourneyImage(update *journeyImageUpdate) error {
	file, err := os.CreateTemp(filepath.Dir(update.path), ".journey-image-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(update.mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(update.after); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), update.path)
}
