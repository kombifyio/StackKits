package cue

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

// Support levels of a mode-matrix cell (base/mode_matrix.cue #SupportLevel).
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
	Evidence  []string
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
		iter, _ := evidence.List()
		for iter.Next() {
			if s, err := iter.Value().String(); err == nil && s != "" {
				m.Evidence = append(m.Evidence, s)
			}
		}
	}
	return m, nil
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
