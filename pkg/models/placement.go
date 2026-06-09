package models

import "strings"

// Placement axis (PLACEMENT-MODE-STANDARD §4 / STACKKITS-SPEC-PIPELINE §10).
// StackKits-OSS realizes only S1 (local-only, standard+cloudless);
// managed-serverless/coupled are S2/S3 (TechStack/Control-Plane), not realized here.
const (
	PlacementLocalOnly = "local-only"
	PlacementStandard  = "standard"
	PlacementManaged   = "managed-serverless"
	PlacementDefault   = PlacementStandard

	ExposurePrivate   = "private"
	ExposurePublic    = "public"
	CouplingCloudless = "cloudless"
	CouplingCoupled   = "coupled"
)

// PlacementSpec is the user's placement intent (mirrors CUE #PlacementIntent).
type PlacementSpec struct {
	Mode     string `yaml:"mode,omitempty" json:"mode,omitempty"`
	Exposure string `yaml:"exposure,omitempty" json:"exposure,omitempty"`
	Coupling string `yaml:"coupling,omitempty" json:"coupling,omitempty"`
}

// NormalizePlacementMode maps blank to the default; otherwise trims/lowercases.
func NormalizePlacementMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		return PlacementDefault
	}
	return m
}

// IsKnownPlacementMode reports whether mode is one of the three axis values.
func IsKnownPlacementMode(mode string) bool {
	switch NormalizePlacementMode(mode) {
	case PlacementLocalOnly, PlacementStandard, PlacementManaged:
		return true
	default:
		return false
	}
}

// EffectivePlacementMode resolves the effective mode: spec value → default.
func (s *StackSpec) EffectivePlacementMode() string {
	if s == nil {
		return PlacementDefault
	}
	return NormalizePlacementMode(s.Placement.Mode)
}
