package usecasecatalog

import (
	"encoding/json"
	"slices"
)

const (
	UseCaseSchema       = "stackkits-use-case-catalog/v1"
	EvidenceSchema      = "stackkits-use-case-evidence/v1"
	CompatibilitySchema = "stackkits-compatibility/v1"
)

type Component struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
	Kind string `json:"kind"`
	// Realization is derived for alternative components only: "install" when
	// the use case's Architecture v2 workload admits the alternative, so the
	// pinned release can install it; "recorded" when the choice is kept as the
	// owner's target preference for a later release. Never authored.
	Realization string `json:"realization,omitempty"`
}

type UseCaseLoad struct {
	Residency string `json:"residency"`
	Baseline  string `json:"baseline"`
	Burst     string `json:"burst"`
}

// UseCaseComputeTierFit is the Unifier-readable surface of one package on one
// install.computeTier graph. Apply does not read it.
type UseCaseComputeTierFit struct {
	Included   bool         `json:"included"`
	Functions  []string     `json:"functions,omitempty"`
	Load       *UseCaseLoad `json:"load,omitempty"`
	ModuleSlug string       `json:"moduleSlug,omitempty"`
	Reason     string       `json:"reason,omitempty"`
	Notes      []string     `json:"notes,omitempty"`
}

// SettingOption is one choice of a choice-kind setting.
type SettingOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note,omitempty"`
	// Realization overrides the setting's realization for this option.
	Realization string `json:"realization,omitempty"`
	// MinMemoryGB is the server memory this option needs to run well.
	MinMemoryGB int `json:"minMemoryGb,omitempty"`
	// NeedsAccelerator marks an option that needs a GPU in practice.
	NeedsAccelerator bool `json:"needsAccelerator,omitempty"`
}

// Setting is a decision an operator makes about a use case before install;
// the projection of foundation.#UseCaseSetting. `Default` is a string for
// choice and text settings and a bool for toggles, exactly as declared.
type Setting struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Group       string          `json:"group"`
	Depth       string          `json:"depth"`
	Help        string          `json:"help,omitempty"`
	Options     []SettingOption `json:"options,omitempty"`
	Default     any             `json:"default"`
	Placeholder string          `json:"placeholder,omitempty"`
	Realization string          `json:"realization"`
	// WorkloadRef names the Architecture v2 application workload a toggle
	// selects in the StackSpec when it is on; such a setting installs.
	WorkloadRef string `json:"workloadRef,omitempty"`
	// AcceleratorProfileOf names the Architecture v2 module whose accelerator
	// profile a non-default option selects; the default option selects none.
	AcceleratorProfileOf string `json:"acceleratorProfileOf,omitempty"`
}

// CapabilityOption is one tool that can realize a capability module; the
// projection of foundation.#UseCaseCapabilityOption. An installing option
// names the Architecture v2 workload alternative that realizes it; a recorded
// option is planned and refused by authoring.
type CapabilityOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Note        string `json:"note,omitempty"`
	Realization string `json:"realization"`
	// Pending names what a recorded option waits for; authoring repeats it
	// in the refusal so the owner learns the exact blocker.
	Pending        string `json:"pending,omitempty"`
	WorkloadRef    string `json:"workloadRef,omitempty"`
	AlternativeRef string `json:"alternativeRef,omitempty"`
	// AlternativeRefs lists every alternative of WorkloadRef that installs
	// this tool; AlternativeRef is the one used unless another capability of
	// the use case decides the workload. Empty means only AlternativeRef.
	AlternativeRefs []string `json:"alternativeRefs,omitempty"`
}

// InstallsWith reports whether the option's tool is part of the named
// alternative of its workload.
func (o CapabilityOption) InstallsWith(alternativeRef string) bool {
	return o.AlternativeRef == alternativeRef || slices.Contains(o.AlternativeRefs, alternativeRef)
}

// Capability is an independently selectable capability module of a use case;
// the projection of foundation.#UseCaseCapabilityModule.
type Capability struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Help                string            `json:"help,omitempty"`
	Required            bool              `json:"required"`
	EnabledByDefault    bool              `json:"enabledByDefault"`
	Requires            []string          `json:"requires"`
	RequiresAccelerator bool              `json:"requiresAccelerator"`
	Default             CapabilityOption  `json:"default"`
	Alternative         *CapabilityOption `json:"alternative,omitempty"`
}

// Options returns the default and, when declared, the alternative.
func (c Capability) Options() []CapabilityOption {
	if c.Alternative == nil {
		return []CapabilityOption{c.Default}
	}
	return []CapabilityOption{c.Default, *c.Alternative}
}

// Bundle groups capability modules of a use case under one customer-facing
// choice; the projection of foundation.#UseCaseBundle. Turning a bundle on
// selects Defaults through the ordinary capability authoring.
type Bundle struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Help                  string   `json:"help,omitempty"`
	Examples              []string `json:"examples,omitempty"`
	Capabilities          []string `json:"capabilities"`
	Defaults              []string `json:"defaults"`
	Required              bool     `json:"required"`
	EnabledByDefault      bool     `json:"enabledByDefault"`
	Advanced              bool     `json:"advanced"`
	RecommendsAccelerator bool     `json:"recommendsAccelerator"`
	RequiresSandbox       bool     `json:"requiresSandbox"`
}

// MainUseCase is the owner-accepted main use case a catalog entry is grouped
// under; CUE constrains the id and derives the title.
type MainUseCase struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type AuthoringModule struct {
	ID                  string   `json:"id"`
	ComputeProfiles     []string `json:"computeProfiles"`
	StorageProfiles     []string `json:"storageProfiles,omitempty"`
	AcceleratorProfiles []string `json:"acceleratorProfiles,omitempty"`
	// ComputeProfileFacts carries the declared numeric footprint of each
	// profile in ComputeProfiles, keyed by profile ID, so Homelab can size a
	// selection without evaluating CUE. Recommendation stays with Homelab.
	ComputeProfileFacts map[string]ComputeProfileFacts `json:"computeProfileFacts,omitempty"`
	// AcceleratorProfileFacts is the device demand of each accelerator
	// profile the module declares; absent when it declares none.
	AcceleratorProfileFacts map[string]AcceleratorProfileFacts `json:"acceleratorProfileFacts,omitempty"`
}

// ResourceBudget is a declared resource envelope. Undeclared axes stay absent,
// never zero (ADR-0039).
type ResourceBudget struct {
	CPUCores  *float64 `json:"cpuCores,omitempty"`
	RAMGB     *float64 `json:"ramGB,omitempty"`
	StorageGB *float64 `json:"storageGB,omitempty"`
}

// HostFloor is the minimum a node must attest before the profile applies.
type HostFloor struct {
	MinCPUCores                    *float64 `json:"minCpuCores,omitempty"`
	MinRAMGB                       *float64 `json:"minRamGB,omitempty"`
	MinStorageGB                   *float64 `json:"minStorageGB,omitempty"`
	MinAMD64MicroarchitectureLevel *float64 `json:"minAMD64MicroarchitectureLevel,omitempty"`
	AllowedArchitectures           []string `json:"allowedArchitectures,omitempty"`
}

// ComputeProfileFacts projects one module compute profile's declared facts.
// Provenance is carried verbatim when the source declares it.
type ComputeProfileFacts struct {
	Maturity      string          `json:"maturity"`
	Executable    bool            `json:"executable"`
	Realization   string          `json:"realization,omitempty"`
	HostFloor     *HostFloor      `json:"hostFloor,omitempty"`
	Reservation   *ResourceBudget `json:"reservation,omitempty"`
	Recommended   *ResourceBudget `json:"recommended,omitempty"`
	Headroom      *ResourceBudget `json:"headroom,omitempty"`
	Architectures []string        `json:"architectures,omitempty"`
	Provenance    json.RawMessage `json:"provenance,omitempty"`
	MeasuredRef   string          `json:"measuredRef,omitempty"`
}

// AcceleratorProfileFacts projects one accelerator profile's device demand.
type AcceleratorProfileFacts struct {
	Vendor         string          `json:"vendor"`
	Access         string          `json:"access"`
	MinVramGiB     *float64        `json:"minVramGiB,omitempty"`
	MinDriverMajor *float64        `json:"minDriverMajor,omitempty"`
	Maturity       string          `json:"maturity"`
	Reservation    *ResourceBudget `json:"reservation,omitempty"`
}

type ConfigurationSetting struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Label   string `json:"label"`
	Options []any  `json:"options,omitempty"`
}

type ConfigurationInputs struct {
	Settings   []ConfigurationSetting `json:"settings"`
	SecretRefs []string               `json:"secretRefs"`
}

type AuthoringAlternative struct {
	Configuration             *ConfigurationInputs `json:"configuration,omitempty"`
	ID                        string               `json:"id"`
	Name                      string               `json:"name"`
	Modules                   []AuthoringModule    `json:"modules"`
	ProvidesRuntimeAdapterRef string               `json:"providesRuntimeAdapterRef,omitempty"`
}

type AuthoringWorkload struct {
	ID                 string                 `json:"id"`
	DefaultAlternative string                 `json:"defaultAlternative"`
	Alternatives       []AuthoringAlternative `json:"alternatives"`
}

type UseCase struct {
	WorkloadRef  string                           `json:"workloadRef,omitempty"`
	ID           string                           `json:"id"`
	Title        string                           `json:"title"`
	Description  string                           `json:"description"`
	Components   []Component                      `json:"components"`
	MainUseCase  MainUseCase                      `json:"mainUseCase"`
	ComputeTiers map[string]UseCaseComputeTierFit `json:"computeTiers,omitempty"`
	Settings     []Setting                        `json:"settings,omitempty"`
	// Capabilities are the independently selectable capability modules of
	// this use case (`stackkit init --use-case-capability`).
	Capabilities []Capability `json:"capabilities,omitempty"`
	// Bundles group the capability modules for consumer surfaces.
	Bundles            []Bundle               `json:"bundles,omitempty"`
	Docs               string                 `json:"docs,omitempty"`
	DefaultAlternative string                 `json:"defaultAlternative,omitempty"`
	Alternatives       []AuthoringAlternative `json:"alternatives,omitempty"`
	// AddOns are application workloads of this use case the owner selects in
	// addition to the primary workload, each with its own module profiles.
	AddOns []AuthoringWorkload `json:"addOns,omitempty"`
}

type sourceCatalog struct {
	UseCases                      []UseCase
	Packages                      map[string]map[string]any
	Workloads                     map[string]map[string]any
	AddOnWorkloads                map[string][]map[string]any
	Lifecycles                    map[string]map[string]any
	Modules                       map[string]bool
	NotApplicable                 map[string]map[string]string
	RuntimeEvidence               map[string]string
	RuntimeEvidencePresent        bool
	RuntimeEvidenceSourceMismatch bool
	OS                            []OSCompatibility
	KitCores                      []AuthoringWorkload
	moduleProfiles                map[string][]string
	catalogWorkloads              []map[string]any
	catalogModules                []map[string]any
}

func metadataID(value map[string]any) string {
	metadata, _ := value["metadata"].(map[string]any)
	return stringField(metadata, "id")
}

func stringField(value map[string]any, field string) string {
	result, _ := value[field].(string)
	return result
}

func boolField(value map[string]any, field string) bool {
	result, _ := value[field].(bool)
	return result
}
