// Package placement resolves the S1 (StackKit-Standalone) capability bindings
// for a StackSpec. It is the OSS-side implementation of the shared
// resolver-output contract (STACKKITS-SPEC-PIPELINE §10): each abstract
// capability (relational, vector, ai_inference, …) is bound to a concrete
// provider+target for the realizable placement modes.
//
// StackKits-OSS realizes only S1 — local-only and standard+cloudless. The
// managed-serverless mode and any coupled placement are S2/S3 (TechStack /
// Control-Plane) and are rejected here with ErrNotS1; their per-deployment
// bindings live in the kombify-admin catalog, never in this repo.
package placement

import (
	"fmt"

	"github.com/kombifyio/stackkits/pkg/models"
)

// CapabilityBinding is the resolved provider+target for one abstract capability.
type CapabilityBinding struct {
	Provider string `json:"provider"`
	Target   string `json:"target"`
}

// Result is the resolved placement block (STACKKITS-SPEC-PIPELINE §10 shape).
type Result struct {
	Mode         string                       `json:"mode"`
	Exposure     string                       `json:"exposure"`
	Coupling     string                       `json:"coupling"`
	Capabilities map[string]CapabilityBinding `json:"capabilities"`
}

// ErrNotS1 signals a placement that StackKits-OSS does not realize
// (managed-serverless mode or coupled coupling → TechStack/SaaS, S2/S3).
type ErrNotS1 struct{ Mode, Coupling string }

func (e ErrNotS1) Error() string {
	return fmt.Sprintf("placement %q (coupling %q) requires TechStack/SaaS (S2/S3); not realized by StackKits-OSS", e.Mode, e.Coupling)
}

// s1Capabilities maps each realizable mode to its capability bindings.
// local-only targets the developer machine; standard targets self-provisioned
// infrastructure the operator runs (still cloudless — no managed substrate).
var s1Capabilities = map[string]map[string]CapabilityBinding{
	models.PlacementLocalOnly: {
		"relational":     {Provider: "sqlite", Target: "local"},
		"vector":         {Provider: "sqlite-vec", Target: "embedded"},
		"ai_inference":   {Provider: "ollama", Target: "on-device"},
		"object_storage": {Provider: "local-fs", Target: "local"},
		"state_cache":    {Provider: "in-memory", Target: "local"},
		"secrets":        {Provider: "file", Target: "local"},
	},
	models.PlacementStandard: {
		"relational":     {Provider: "postgres", Target: "self-provisioned"},
		"vector":         {Provider: "sqlite-vec", Target: "self-provisioned"},
		"ai_inference":   {Provider: "ollama", Target: "self-provisioned"},
		"object_storage": {Provider: "local-fs", Target: "self-provisioned"},
		"state_cache":    {Provider: "valkey", Target: "self-provisioned"},
		"secrets":        {Provider: "file", Target: "self-provisioned"},
	},
}

// ResolveS1 resolves the S1 capability bindings for spec. A nil spec resolves
// to the default (standard) placement. managed-serverless or coupled placements
// return ErrNotS1 — the caller surfaces that as a warning, not a hard failure.
func ResolveS1(spec *models.StackSpec) (*Result, error) {
	if spec == nil {
		spec = &models.StackSpec{}
	}
	mode := spec.EffectivePlacementMode()
	coupling := spec.Placement.Coupling
	if coupling == "" {
		coupling = models.CouplingCloudless
	}
	if mode == models.PlacementManaged || coupling == models.CouplingCoupled {
		return nil, ErrNotS1{Mode: mode, Coupling: coupling}
	}
	caps, ok := s1Capabilities[mode]
	if !ok {
		return nil, fmt.Errorf("unknown placement mode %q", mode)
	}
	exposure := spec.Placement.Exposure
	if mode == models.PlacementLocalOnly || exposure == "" {
		exposure = models.ExposurePrivate
	}
	return &Result{
		Mode:         mode,
		Exposure:     exposure,
		Coupling:     models.CouplingCloudless,
		Capabilities: caps,
	}, nil
}
