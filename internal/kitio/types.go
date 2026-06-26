// Package kitio provides DB-shape <-> stackkit.yaml conversions plus
// reverse-generation of CUE / Terraform / Docker-Compose artifacts.
//
// This is the Go counterpart to the TS kit-import endpoint
// (kombify-Administration/.../kit-import/+server.ts). The endpoint accepts
// a request body whose JSON shape is preserved here as KitDefinition.
//
// One library, two consumers:
//   - cmd/stackkit kit import / export / roundtrip CLI
//   - tests in cmd/stackkit/commands/kit_*_test.go (golden + live)
//
// Kept DB-free: the library never opens a connection. The Admin API
// client (client.go) is used for live roundtrip tests against
// /api/v1/sk/registry/stackkits/{slug}/kit-export.
package kitio

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// jsonMarshal/jsonUnmarshal aliases keep MarshalJSON/UnmarshalJSON below
// independent of the struct's other deps.
func jsonMarshal(v interface{}) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v interface{}) error { return json.Unmarshal(b, v) }

// KitDefinition is the canonical DB-shape that round-trips through both
// kit-import (yaml -> POST body) and kit-export (GET response -> yaml).
// JSON tags match the TS endpoint's body shape verbatim.
type KitDefinition struct {
	APIVersion   string                    `yaml:"apiVersion,omitempty" json:"apiVersion,omitempty"`
	Kind         string                    `yaml:"kind,omitempty" json:"kind,omitempty"`
	Metadata     KitMetadata               `yaml:"metadata" json:"metadata"`
	SupportedOS  []string                  `yaml:"supportedOS,omitempty" json:"supportedOS,omitempty"`
	Requirements map[string]interface{}    `yaml:"requirements,omitempty" json:"requirements,omitempty"`
	Modes        map[string]ModeDef        `yaml:"modes,omitempty" json:"modes,omitempty"`
	AutoSelect   map[string]interface{}    `yaml:"autoSelect,omitempty" json:"autoSelect,omitempty"`
	Application  map[string]ApplicationDef `yaml:"application,omitempty" json:"application,omitempty"`
	Foundation   map[string]FoundationDef  `yaml:"foundation,omitempty" json:"foundation,omitempty"`
	Platform     PlatformField             `yaml:"platform,omitempty" json:"platform,omitempty"`
	Features     map[string]bool           `yaml:"features,omitempty" json:"features,omitempty"`
	ComputeTiers map[string]ComputeTierDef `yaml:"computeTiers,omitempty" json:"computeTiers,omitempty"`
	Outputs      map[string]interface{}    `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	Changelog    []ChangelogEntry          `yaml:"changelog,omitempty" json:"changelog,omitempty"`

	// Multi-node kit fields
	NodeTypes map[string]NodeTypeDef `yaml:"nodeTypes,omitempty" json:"nodeTypes,omitempty"`
	Addons    AddonsDef              `yaml:"addons,omitempty" json:"addons,omitempty"`
	Identity  map[string]interface{} `yaml:"identity,omitempty" json:"identity,omitempty"`
	Pattern   map[string]interface{} `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	PaaS      map[string]interface{} `yaml:"paas,omitempty" json:"paas,omitempty"`
	Secrets   map[string]interface{} `yaml:"secrets,omitempty" json:"secrets,omitempty"`

	// Cluster-oriented kit fields
	Swarm    map[string]interface{} `yaml:"swarm,omitempty" json:"swarm,omitempty"`
	Services []ServiceSpecDef       `yaml:"services,omitempty" json:"services,omitempty"`
	Extends  string                 `yaml:"extends,omitempty" json:"extends,omitempty"`

	// Common optional
	Architecture    string   `yaml:"architecture,omitempty" json:"architecture,omitempty"`
	TunnelOptions   []string `yaml:"tunnelOptions,omitempty" json:"tunnelOptions,omitempty"`
	SecretsProvider string   `yaml:"-" json:"secretsProvider,omitempty"`

	// Meta fields injected by kit-import CLI (not in YAML)
	CueSourcePath string `yaml:"-" json:"cueSourcePath,omitempty"`
	ImportedBy    string `yaml:"-" json:"importedBy,omitempty"`
	ContractHash  string `yaml:"-" json:"contractHash,omitempty"`
	DryRun        bool   `yaml:"-" json:"dryRun,omitempty"`
}

// PlatformField is a polymorphic yaml `platform:` value.
//
// In base-kit it is a map of platform service slots:
//
//	platform:
//	  traefik: { role: default }
//	  paas: { role: optional, defaultTool: coolify }
//
// In future kit definitions it may be represented as a single string:
//
//	platform: docker
//
// We keep both forms reachable.
type PlatformField struct {
	// AsString holds the scalar form, e.g. "docker".
	AsString string
	// AsMap holds the structured form (base-kit).
	AsMap map[string]PlatformDef
}

// IsEmpty reports whether the field carries no data.
func (p PlatformField) IsEmpty() bool {
	return p.AsString == "" && len(p.AsMap) == 0
}

// UnmarshalYAML accepts both yaml shapes.
func (p *PlatformField) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Decode(&p.AsString)
	case yaml.MappingNode:
		return node.Decode(&p.AsMap)
	default:
		return nil
	}
}

// MarshalYAML emits the string form when set, otherwise the map form, otherwise nothing.
func (p PlatformField) MarshalYAML() (interface{}, error) {
	if p.AsString != "" {
		return p.AsString, nil
	}
	if len(p.AsMap) > 0 {
		return p.AsMap, nil
	}
	return nil, nil
}

// MarshalJSON keeps the symmetry for kit-import POST body.
func (p PlatformField) MarshalJSON() ([]byte, error) {
	if p.AsString != "" {
		return jsonMarshal(p.AsString)
	}
	if len(p.AsMap) > 0 {
		return jsonMarshal(p.AsMap)
	}
	return []byte("null"), nil
}

// UnmarshalJSON accepts string-or-map for kit-export round-trip.
func (p *PlatformField) UnmarshalJSON(b []byte) error {
	// Try string first
	var s string
	if err := jsonUnmarshal(b, &s); err == nil {
		p.AsString = s
		return nil
	}
	var m map[string]PlatformDef
	if err := jsonUnmarshal(b, &m); err == nil {
		p.AsMap = m
		return nil
	}
	return nil // tolerate null
}

// KitMetadata mirrors stackkit.yaml `metadata:` block.
type KitMetadata struct {
	Name        string   `yaml:"name" json:"name"`
	Version     string   `yaml:"version" json:"version"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Summary     string   `yaml:"summary,omitempty" json:"summary,omitempty"`
	Author      string   `yaml:"author,omitempty" json:"author,omitempty"`
	License     string   `yaml:"license,omitempty" json:"license,omitempty"`
	Repository  string   `yaml:"repository,omitempty" json:"repository,omitempty"`
	Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// ModeDef captures an install/lifecycle mode such as bare, bootstrapped, or advanced.
type ModeDef struct {
	Description    string                   `yaml:"description,omitempty" json:"description,omitempty"`
	TemplateDir    string                   `yaml:"templateDir,omitempty" json:"templateDir,omitempty"`
	Engine         string                   `yaml:"engine,omitempty" json:"engine,omitempty"`
	Recommended    bool                     `yaml:"recommended,omitempty" json:"recommended,omitempty"`
	Features       []string                 `yaml:"features,omitempty" json:"features,omitempty"`
	Requires       []string                 `yaml:"requires,omitempty" json:"requires,omitempty"`
	RecommendedFor []string                 `yaml:"recommended_for,omitempty" json:"recommended_for,omitempty"`
	Stacks         []map[string]interface{} `yaml:"stacks,omitempty" json:"stacks,omitempty"`
}

// ApplicationDef is a user-facing application category (photos, media, ...).
// Lives under stackkit.yaml `application:` key (canonical L3 layer per ADR-0012).
// Pre-2026-04 this was named UseCaseDef under `useCases:` — see migration 000084.
type ApplicationDef struct {
	Role                  string                                  `yaml:"role" json:"role"`
	DefaultTool           string                                  `yaml:"defaultTool,omitempty" json:"defaultTool,omitempty"`
	Alternatives          []string                                `yaml:"alternatives,omitempty" json:"alternatives,omitempty"`
	Description           string                                  `yaml:"description,omitempty" json:"description,omitempty"`
	Package               string                                  `yaml:"package,omitempty" json:"package,omitempty"`
	DefaultRuntimeProfile string                                  `yaml:"defaultRuntimeProfile,omitempty" json:"defaultRuntimeProfile,omitempty"`
	RuntimeProfiles       map[string]ApplicationRuntimeProfileDef `yaml:"runtimeProfiles,omitempty" json:"runtimeProfiles,omitempty"`
	Connectors            map[string]ApplicationConnectorDef      `yaml:"connectors,omitempty" json:"connectors,omitempty"`
	ProductAPIs           map[string]ApplicationProductAPIDef     `yaml:"productApis,omitempty" json:"productApis,omitempty"`
	RIL                   *ApplicationRILDef                      `yaml:"ril,omitempty" json:"ril,omitempty"`
}

type ApplicationRuntimeProfileDef struct {
	DisplayName               string   `yaml:"displayName,omitempty" json:"displayName,omitempty"`
	Description               string   `yaml:"description,omitempty" json:"description,omitempty"`
	Realization               string   `yaml:"realization,omitempty" json:"realization,omitempty"`
	PlacementModes            []string `yaml:"placementModes,omitempty" json:"placementModes,omitempty"`
	Contexts                  []string `yaml:"contexts,omitempty" json:"contexts,omitempty"`
	ManagedServerlessEligible bool     `yaml:"managedServerlessEligible,omitempty" json:"managedServerlessEligible,omitempty"`
	RequiresControlPlane      bool     `yaml:"requiresControlPlane,omitempty" json:"requiresControlPlane,omitempty"`
	RequiresLocalBridge       bool     `yaml:"requiresLocalBridge,omitempty" json:"requiresLocalBridge,omitempty"`
	Notes                     []string `yaml:"notes,omitempty" json:"notes,omitempty"`
}

type ApplicationConnectorDef struct {
	Kind          string   `yaml:"kind,omitempty" json:"kind,omitempty"`
	Name          string   `yaml:"name,omitempty" json:"name,omitempty"`
	Owner         string   `yaml:"owner,omitempty" json:"owner,omitempty"`
	Endpoint      string   `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	Transport     string   `yaml:"transport,omitempty" json:"transport,omitempty"`
	Auth          string   `yaml:"auth,omitempty" json:"auth,omitempty"`
	NativeProduct bool     `yaml:"nativeProduct,omitempty" json:"nativeProduct,omitempty"`
	Capabilities  []string `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

type ApplicationProductAPIDef struct {
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	BasePath string `yaml:"basePath,omitempty" json:"basePath,omitempty"`
	Auth     string `yaml:"auth,omitempty" json:"auth,omitempty"`
	Purpose  string `yaml:"purpose,omitempty" json:"purpose,omitempty"`
}

type ApplicationRILDef struct {
	Capabilities map[string]ApplicationRILCapabilityDef `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

type ApplicationRILCapabilityDef struct {
	Mode             string `yaml:"mode,omitempty" json:"mode,omitempty"`
	Authority        string `yaml:"authority,omitempty" json:"authority,omitempty"`
	Source           string `yaml:"source,omitempty" json:"source,omitempty"`
	RequiresApproval bool   `yaml:"requiresApproval,omitempty" json:"requiresApproval,omitempty"`
	Evidence         string `yaml:"evidence,omitempty" json:"evidence,omitempty"`
}

// FoundationDef is a foundation-layer service slot.
type FoundationDef struct {
	Role         string   `yaml:"role" json:"role"`
	Alternatives []string `yaml:"alternatives,omitempty" json:"alternatives,omitempty"`
	Description  string   `yaml:"description,omitempty" json:"description,omitempty"`
}

// PlatformDef is a platform-layer service slot.
type PlatformDef struct {
	Role         string   `yaml:"role" json:"role"`
	DefaultTool  string   `yaml:"defaultTool,omitempty" json:"defaultTool,omitempty"`
	Alternatives []string `yaml:"alternatives,omitempty" json:"alternatives,omitempty"`
	Description  string   `yaml:"description,omitempty" json:"description,omitempty"`
}

// ComputeTierDef is a tier sizing entry (low/standard/high).
type ComputeTierDef struct {
	Requirements       ResourceRequirements   `yaml:"requirements,omitempty" json:"requirements,omitempty"`
	AdditionalServices []string               `yaml:"additionalServices,omitempty" json:"additionalServices,omitempty"`
	ServiceOverrides   map[string]interface{} `yaml:"serviceOverrides,omitempty" json:"serviceOverrides,omitempty"`
	PaasOverride       string                 `yaml:"paasOverride,omitempty" json:"paasOverride,omitempty"`
}

// ResourceRequirements captures cpu/memory/disk + optional disk type.
type ResourceRequirements struct {
	CPU      int    `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory   int    `yaml:"memory,omitempty" json:"memory,omitempty"`
	Disk     int    `yaml:"disk,omitempty" json:"disk,omitempty"`
	DiskType string `yaml:"diskType,omitempty" json:"diskType,omitempty"`
	// Multi-node
	ManagerNodes int `yaml:"managerNodes,omitempty" json:"managerNodes,omitempty"`
	WorkerNodes  int `yaml:"workerNodes,omitempty" json:"workerNodes,omitempty"`
	Nodes        int `yaml:"nodes,omitempty" json:"nodes,omitempty"`
}

// ChangelogEntry mirrors a YAML changelog item.
type ChangelogEntry struct {
	Version string   `yaml:"version" json:"version"`
	Date    string   `yaml:"date,omitempty" json:"date,omitempty"`
	Changes []string `yaml:"changes,omitempty" json:"changes,omitempty"`
}

// NodeTypeDef describes local/cloud node roles in multi-node kit definitions.
type NodeTypeDef struct {
	Description  string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Access       string                 `yaml:"access,omitempty" json:"access,omitempty"`
	Role         string                 `yaml:"role,omitempty" json:"role,omitempty"`
	Requirements map[string]interface{} `yaml:"requirements,omitempty" json:"requirements,omitempty"`
	Providers    []string               `yaml:"providers,omitempty" json:"providers,omitempty"`
}

// AddonsDef holds autoActivated + optional addon slug lists.
type AddonsDef struct {
	AutoActivated []string `yaml:"autoActivated,omitempty" json:"autoActivated,omitempty"`
	Optional      []string `yaml:"optional,omitempty" json:"optional,omitempty"`
}

// ServiceSpecDef declares deployment overrides per service.
type ServiceSpecDef struct {
	Name        string                 `yaml:"name" json:"name"`
	Required    bool                   `yaml:"required,omitempty" json:"required,omitempty"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Status      string                 `yaml:"status,omitempty" json:"status,omitempty"`
	Replicas    *int                   `yaml:"replicas,omitempty" json:"replicas,omitempty"`
	SwarmMode   bool                   `yaml:"swarmMode,omitempty" json:"swarmMode,omitempty"`
	Deploy      map[string]interface{} `yaml:"deploy,omitempty" json:"deploy,omitempty"`
}

// RoundTripReport captures the structural diff between the original kit
// definition and the reconstructed one.
type RoundTripReport struct {
	Slug              string            `json:"slug"`
	OriginalHash      string            `json:"originalHash"`
	ReconstructedHash string            `json:"reconstructedHash"`
	HashesEqual       bool              `json:"hashesEqual"`
	Differences       []FieldDifference `json:"differences,omitempty"`
	CosmeticOnly      bool              `json:"cosmeticOnly"`
	Formats           []string          `json:"formats"`
}

// FieldDifference is a single field-level diff entry.
type FieldDifference struct {
	Path          string      `json:"path"`
	Severity      string      `json:"severity"` // "critical" | "cosmetic"
	Original      interface{} `json:"original,omitempty"`
	Reconstructed interface{} `json:"reconstructed,omitempty"`
	Note          string      `json:"note,omitempty"`
}
