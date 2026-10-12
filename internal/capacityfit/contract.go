// Package capacityfit is the shared numerical authority for selected module
// demand and capacity assessment. Callers bind profile facts and policy to the
// owning CUE catalog; this package neither selects profiles nor attests hosts.
package capacityfit

import (
	"fmt"
	"math"
)

const SchemaVersion = "stackkit.capacity-assessment/v1"

type Axis string

const (
	CPU         Axis = "cpuCores"
	RAM         Axis = "ramGB"
	Storage     Axis = "storageGB"
	DataStorage Axis = "dataStorageGiB"
)

var axes = [...]Axis{CPU, RAM, Storage}

// Vector retains unknown axes as nil. Missing facts never become zero.
type Vector struct {
	CPUCores  *float64 `json:"cpuCores,omitempty"`
	RAMGB     *float64 `json:"ramGB,omitempty"`
	StorageGB *float64 `json:"storageGB,omitempty"`
}

func (v Vector) Value(axis Axis) *float64 {
	switch axis {
	case CPU:
		return v.CPUCores
	case RAM:
		return v.RAMGB
	case Storage:
		return v.StorageGB
	}
	return nil
}

func (v *Vector) Set(axis Axis, value float64) {
	switch axis {
	case CPU:
		v.CPUCores = &value
	case RAM:
		v.RAMGB = &value
	case Storage:
		v.StorageGB = &value
	}
}

func (v Vector) Map() map[string]any {
	var result map[string]any
	for _, axis := range axes {
		if value := v.Value(axis); value != nil {
			if result == nil {
				result = map[string]any{}
			}
			result[string(axis)] = *value
		}
	}
	return result
}

func validateVector(v Vector, allowZero bool) error {
	for _, axis := range axes {
		value := v.Value(axis)
		if value == nil {
			continue
		}
		if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || (!allowZero && *value == 0) {
			return fmt.Errorf("%s must be finite and %s", axis, map[bool]string{true: "nonnegative", false: "positive"}[allowZero])
		}
	}
	return nil
}

type Profile struct {
	HostFloor   Vector `json:"hostFloor"`
	Reservation Vector `json:"reservation"`
	Recommended Vector `json:"recommended"`
	Headroom    Vector `json:"headroom"`
}

type Load struct {
	Residency string `json:"residency"`
	Baseline  string `json:"baseline"`
	Burst     string `json:"burst"`
}

type Module struct {
	ID string `json:"id"`
	// Existing projections may explicitly label a profile undeclared even
	// while carrying partial numerical facts. Known failures still take precedence.
	CapacityDeclaration    string   `json:"capacityDeclaration,omitempty"`
	NodeRefs               []string `json:"nodeRefs"`
	Compute                Profile  `json:"compute"`
	OrthogonalReservations []Vector `json:"orthogonalReservations,omitempty"`
	Load                   *Load    `json:"load,omitempty"`
}

type Origin string

const (
	Declared    Origin = "declared"
	Inventory   Origin = "inventory"
	Unavailable Origin = "unavailable"
)

// Inventory identifies the supplied inventory boundary, not cryptographic or
// current host attestation. The compiler and Apply retain those separate gates.
type NodeCapacity struct {
	NodeRef string `json:"nodeRef"`
	Origin  Origin `json:"origin"`
	Values  Vector `json:"values"`
}

// Policy has no defaults: every supplied threshold must come from the caller's
// bound CUE policy. Ref preserves that source; it does not authenticate it.
type Policy struct {
	Ref               string   `json:"ref"`
	HeadroomFactor    *float64 `json:"headroomFactor,omitempty"`
	MaxAlwaysOnPerCPU *float64 `json:"maxAlwaysOnPerCPU,omitempty"`
}

type Input struct {
	Modules    []Module       `json:"modules"`
	Capacities []NodeCapacity `json:"capacities"`
	Policy     *Policy        `json:"policy,omitempty"`
}

type Demand struct {
	NodeRef              string   `json:"nodeRef"`
	ModuleRefs           []string `json:"moduleRefs"`
	UnverifiedModuleRefs []string `json:"unverifiedModuleRefs"`
	UnverifiedAxes       []Axis   `json:"unverifiedAxes"`
	HostFloor            Vector   `json:"hostFloor"`
	Reservation          Vector   `json:"reservation"`
	Recommended          Vector   `json:"recommended"`
	Headroom             Vector   `json:"headroom"`
}

type Verdict string

const (
	Pass       Verdict = "pass"
	Warning    Verdict = "warning"
	Unverified Verdict = "unverified"
	Fail       Verdict = "fail"
)

type Check struct {
	Axis        Axis     `json:"axis"`
	Observed    *float64 `json:"observed,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Recommended *float64 `json:"recommended,omitempty"`
	Status      Verdict  `json:"status"`
	ReasonCode  string   `json:"reasonCode"`
}

type NodeAssessment struct {
	NodeRef     string   `json:"nodeRef"`
	Origin      Origin   `json:"origin"`
	Demand      Demand   `json:"demand"`
	Checks      []Check  `json:"checks"`
	Overall     Verdict  `json:"overall"`
	ReasonCodes []string `json:"reasonCodes"`
	Advice      []string `json:"advice"`
}

type Assessment struct {
	SchemaVersion string           `json:"schemaVersion"`
	Nodes         []NodeAssessment `json:"nodes"`
	Overall       Verdict          `json:"overall"`
	PolicyRef     string           `json:"policyRef,omitempty"`
}
