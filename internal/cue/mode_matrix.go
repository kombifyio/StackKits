package cue

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

// KitDeclaresModeMatrix reports whether <kitDir> ships a mode_matrix.cue file.
// It lets callers fail closed: a kit that DECLARES a matrix but whose matrix
// fails to load is a real error, whereas a legacy exported kit cache without the
// file legitimately skips enforcement.
func KitDeclaresModeMatrix(kitDir string) bool {
	_, err := os.Stat(filepath.Join(kitDir, "mode_matrix.cue"))
	return err == nil
}

// Support levels of a mode-matrix cell (foundation/mode_matrix.cue #SupportLevel).
const (
	SupportSupported    = "supported"
	SupportScaffolding  = "scaffolding"
	SupportUnsupported  = "unsupported"
	SupportControlPlane = "control-plane"
)

// KitModeMatrix is the Go projection of a kit's #KitModeSupport declaration.
type KitModeMatrix struct {
	Kit       string
	Placement map[string]string
	Install   map[string]string
	Context   map[string]string
	Paas      map[string]string
	// Evidence maps a cell path ("<axis>.<key>", e.g. "install.advanced") to
	// its citations. No consumer of KitModeMatrix reads it today; it exists
	// so callers that want the raw CUE declaration (docs generation, the
	// citation validator's Go-side parity checks) do not need a second
	// loader. See foundation/mode_matrix.cue for the citation contract.
	Evidence map[string][]string
}

// LoadKitModeMatrix loads <kitDir>'s CUE package and decodes its modeMatrix.
// Kits without a declaration (e.g. older exported kit caches) return an error;
// callers treat that as "matrix enforcement unavailable", not as a failure.
func LoadKitModeMatrix(kitDir string) (*KitModeMatrix, error) {
	cfg := cueLoadConfig(kitDir, kitDir)
	instances := load.Instances([]string{"."}, cfg)
	if len(instances) == 0 {
		return nil, fmt.Errorf("no CUE instances in %s", kitDir)
	}
	if instances[0].Err != nil {
		return nil, fmt.Errorf("load %s: %w", kitDir, instances[0].Err)
	}
	value := cuecontext.New().BuildInstance(instances[0])
	if err := value.Err(); err != nil {
		return nil, fmt.Errorf("build %s: %w", kitDir, err)
	}
	matrix := value.LookupPath(cue.ParsePath("modeMatrix"))
	if !matrix.Exists() {
		return nil, fmt.Errorf("kit %s declares no modeMatrix", kitDir)
	}

	m := &KitModeMatrix{
		Placement: map[string]string{},
		Install:   map[string]string{},
		Context:   map[string]string{},
		Paas:      map[string]string{},
	}
	if s, err := matrix.LookupPath(cue.ParsePath("kit")).String(); err == nil {
		m.Kit = s
	}
	for axis, dst := range map[string]map[string]string{
		"placement": m.Placement,
		"install":   m.Install,
		"context":   m.Context,
		"paas":      m.Paas,
	} {
		axisVal := matrix.LookupPath(cue.ParsePath(axis))
		if !axisVal.Exists() {
			continue
		}
		iter, err := axisVal.Fields(cue.Optional(true))
		if err != nil {
			return nil, fmt.Errorf("iterate %s axis: %w", axis, err)
		}
		for iter.Next() {
			key := strings.Trim(iter.Selector().String(), "\"")
			if s, serr := iter.Value().String(); serr == nil {
				dst[key] = s
			}
		}
	}
	if evidence := matrix.LookupPath(cue.ParsePath("evidence")); evidence.Exists() {
		iter, err := evidence.Fields(cue.Optional(true))
		if err != nil {
			return nil, fmt.Errorf("iterate evidence: %w", err)
		}
		for iter.Next() {
			cell := strings.Trim(iter.Selector().String(), "\"")
			citations, err := stringListValues(iter.Value())
			if err != nil {
				return nil, fmt.Errorf("iterate evidence[%s]: %w", cell, err)
			}
			if len(citations) == 0 {
				continue
			}
			if m.Evidence == nil {
				m.Evidence = map[string][]string{}
			}
			m.Evidence[cell] = citations
		}
	}
	return m, nil
}

// stringListValues reads a CUE list of strings, skipping empty entries.
func stringListValues(v cue.Value) ([]string, error) {
	iter, err := v.List()
	if err != nil {
		return nil, err
	}
	var values []string
	for iter.Next() {
		s, err := iter.Value().String()
		if err != nil {
			return nil, err
		}
		if s != "" {
			values = append(values, s)
		}
	}
	return values, nil
}

// CellVerdict grades the (placement, install, context) cell of this kit.
// Returned level is the worst across the three axes
// (unsupported > control-plane > scaffolding > supported); details name the
// axes that caused a non-supported verdict.
func (m *KitModeMatrix) CellVerdict(placementMode, installMode, nodeContext string) (level string, details []string) {
	rank := map[string]int{
		SupportSupported:    0,
		SupportScaffolding:  1,
		SupportControlPlane: 2,
		SupportUnsupported:  3,
	}
	level = SupportSupported
	consider := func(axis, key string, grades map[string]string) {
		grade, ok := grades[key]
		if !ok {
			// An undeclared axis value is unknown territory: grade it unsupported.
			grade = SupportUnsupported
		}
		if grade != SupportSupported {
			details = append(details, fmt.Sprintf("%s %q is %s for kit %q", axis, key, grade, m.Kit))
		}
		if rank[grade] > rank[level] {
			level = grade
		}
	}
	consider("placement mode", placementMode, m.Placement)
	consider("install mode", installMode, m.Install)
	consider("node context", nodeContext, m.Context)
	return level, details
}
