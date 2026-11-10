package homegraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// MappingSchema identifies the stored mapping document.
const MappingSchema = "kombify.home_graph.mapping.v1"

// Mapping ties kombify ids to Home Assistant ids. The node agent persists it;
// it holds no secret.
type Mapping struct {
	Schema string `json:"schema"`
	// LabelID is the Home Assistant id of the label "kombify".
	LabelID string `json:"label_id,omitempty"`
	// Floors, Areas and Devices map a kombify floor, room and device id to the
	// Home Assistant floor_id, area_id and device registry id.
	Floors  map[string]string `json:"floors"`
	Areas   map[string]string `json:"areas"`
	Devices map[string]string `json:"devices"`
	// Created lists the Home Assistant objects kombify created itself
	// ("floor:<id>", "area:<id>"). Only these may ever be removed, and only with
	// the person's confirmation.
	Created map[string]bool `json:"created"`
	// Synced holds the value each synchronized field had after the last
	// successful write or comparison; it tells a change made in kombify from a
	// change made in Home Assistant.
	Synced map[string]string `json:"synced"`
}

func (m *Mapping) normalize() {
	m.Schema = MappingSchema
	if m.Floors == nil {
		m.Floors = map[string]string{}
	}
	if m.Areas == nil {
		m.Areas = map[string]string{}
	}
	if m.Devices == nil {
		m.Devices = map[string]string{}
	}
	if m.Created == nil {
		m.Created = map[string]bool{}
	}
	if m.Synced == nil {
		m.Synced = map[string]string{}
	}
}

// Clone returns a deep copy.
func (m Mapping) Clone() Mapping {
	c := Mapping{LabelID: m.LabelID}
	c.Floors = cloneMap(m.Floors)
	c.Areas = cloneMap(m.Areas)
	c.Devices = cloneMap(m.Devices)
	c.Synced = cloneMap(m.Synced)
	c.Created = make(map[string]bool, len(m.Created))
	for k, v := range m.Created {
		c.Created[k] = v
	}
	c.normalize()
	return c
}

func cloneMap(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// ParseMapping decodes a stored mapping document.
func ParseMapping(b []byte) (Mapping, error) {
	var m Mapping
	if err := json.Unmarshal(b, &m); err != nil {
		return Mapping{}, fmt.Errorf("homegraph: decode mapping: %w", err)
	}
	if m.Schema != "" && m.Schema != MappingSchema {
		return Mapping{}, fmt.Errorf("homegraph: unknown mapping schema %q", m.Schema)
	}
	m.normalize()
	return m, nil
}

// Marshal encodes the mapping document.
func (m Mapping) Marshal() ([]byte, error) {
	m.normalize()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("homegraph: encode mapping: %w", err)
	}
	return b, nil
}

// MappingStore persists a Mapping as a JSON file (mode 0600, atomic write).
type MappingStore struct{ Path string }

// Load reads the mapping; a missing file yields an empty mapping.
func (s MappingStore) Load() (Mapping, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		var m Mapping
		m.normalize()
		return m, nil
	}
	if err != nil {
		return Mapping{}, fmt.Errorf("homegraph: read mapping: %w", err)
	}
	return ParseMapping(b)
}

// Save writes the mapping atomically: a temporary file in the same directory,
// synced, then renamed over the target.
func (s MappingStore) Save(m Mapping) error {
	b, err := m.Marshal()
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("homegraph: mapping dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".mapping-*.tmp")
	if err != nil {
		return fmt.Errorf("homegraph: mapping temp file: %w", err)
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, fs.ErrPermission) {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("homegraph: mapping chmod: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("homegraph: write mapping: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("homegraph: sync mapping: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("homegraph: close mapping: %w", err)
	}
	if err := os.Rename(name, s.Path); err != nil {
		cleanup()
		return fmt.Errorf("homegraph: replace mapping: %w", err)
	}
	return nil
}
