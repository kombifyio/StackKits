package hostsecurity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// SaveEvidence writes the evidence under the workspace's .stackkit directory
// for local use and returns the path. The file is owner-readable only: it
// describes the host's exposure.
func (e Engine) SaveEvidence(workspaceRoot string, evidence Evidence) (string, error) {
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode host security evidence: %w", err)
	}
	path := EvidencePath(workspaceRoot)
	if err := e.Host.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write host security evidence: %w", err)
	}
	return path, nil
}

// LoadEvidence reads evidence written by SaveEvidence. The result must be read
// through AtTime: an old observation is unknown, whatever it once said.
func (e Engine) LoadEvidence(workspaceRoot string) (Evidence, error) {
	raw, err := e.Host.ReadFile(EvidencePath(workspaceRoot))
	if err != nil {
		return Evidence{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if err := decoder.Decode(&evidence); err != nil {
		return Evidence{}, fmt.Errorf("stored host security evidence is unreadable: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Evidence{}, errors.New("stored host security evidence has trailing data")
	}
	if evidence.SchemaVersion != EvidenceSchemaVersion {
		return Evidence{}, fmt.Errorf("stored host security evidence has schema %q, want %q", evidence.SchemaVersion, EvidenceSchemaVersion)
	}
	return evidence, nil
}
