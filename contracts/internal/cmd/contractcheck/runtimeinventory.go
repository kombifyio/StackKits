package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type runtimeInventoryManifest struct {
	Source struct {
		SHA256 string `json:"sha256"`
	} `json:"source"`
	Outputs map[string]string `json:"outputs"`
}

// verifyRuntimeInventoryManifest binds the checked-in Runtime Inventory schema
// and generated types to the digests its generation manifest recorded. The
// generator lives in Techstack, so this is the drift check on this side.
func verifyRuntimeInventoryManifest(root string) error {
	directory := filepath.Join(root, "runtimeinventory")
	payload, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return err
	}
	var manifest runtimeInventoryManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return err
	}
	checks := []struct {
		label    string
		file     string
		expected string
	}{
		{label: "source", file: filepath.Join(directory, "schema.json"), expected: manifest.Source.SHA256},
		{label: "types.go", file: filepath.Join(directory, "types.go"), expected: manifest.Outputs["types.go"]},
	}
	for _, check := range checks {
		content, err := os.ReadFile(check.file)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		if check.expected == "" || hex.EncodeToString(sum[:]) != check.expected {
			return fmt.Errorf("runtimeinventory %s digest does not match its generation manifest", check.label)
		}
	}
	return nil
}
