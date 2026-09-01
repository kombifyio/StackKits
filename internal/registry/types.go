// Package registry exposes the StackKits catalog (tools, module versions,
// curated stackkits) to the CLI in an OSS-safe way.
//
// The registry has one publication authority: the Git checkout. CUE owns the
// facts it models; the checked-in embedded snapshot owns remaining catalog
// facts and the baked projection consumed by the CLI. No account, database, or
// hosted service participates in publication. Manual snapshots remain a
// migration-only compatibility shape.
package registry

import (
	"encoding/json"
	"time"
)

// SnapshotVersion is the current schema version for registry_snapshot.json.
// Bump when the on-disk shape changes in an incompatible way.
//
// v3 (2026-06-10, WS-0 truth-consolidation): adds ContentHash, ServiceGroups,
// ToolDefaultConfigs and the StackKit sub-resources (service selections,
// spec-profile hashes, and tool configs. Those fields now remain versioned in
// the Git-owned embedded artifact used by emit-cue and the hash-parity gates.
//
// v4 (2026-06-13, CP-2 discovery-to-docs pipeline): extended Tool with vendor,
// license, documentation_url, repo_url, use_cases and a Content block carrying
// the published sk_tool_content sections. Powers emit-mintlify (CP-3).
const SnapshotVersion = 4

// Snapshot is the envelope serialized to registry_snapshot.json.
//
// All nested slices are sorted deterministically (by primary key) so that
// two snapshots with identical content produce byte-identical JSON --
// this keeps diffs readable and `goimports`-style re-bakes idempotent.
// Source values for Snapshot.Source. Constants instead of bare strings so
// callers agree on spelling.
const (
	SourceCUE    = "cue"
	SourceManual = "manual"
)

type Snapshot struct {
	// SchemaVersion pins the shape of this envelope. Must equal
	// SnapshotVersion at load time; mismatches fail-fast.
	SchemaVersion int `json:"schema_version"`

	// Source identifies where the snapshot was produced.
	// One of: "cue" (Git/CUE bake) or "manual" (migration fallback).
	Source string `json:"source"`

	// GeneratedAt is the UTC time the snapshot was baked.
	GeneratedAt time.Time `json:"generated_at"`

	// ContentHash is the SHA-256 (hex) over the canonical
	// catalog payload, excluding volatile envelope fields (generated_at,
	// content_hash itself). Two snapshots with identical
	// catalog content carry the same hash regardless of when or where they
	// were fetched. Empty for CUE-baked snapshots.
	ContentHash string `json:"content_hash,omitempty"`

	// Tools is the unified tool catalog (sk_tool).
	Tools []Tool `json:"tools"`

	// Services is the product-facing service catalog mirror (sk_service +
	// sk_stackkit_service). It carries canonical service keys, URL slugs,
	// legacy aliases, and owner/SSO readiness policy, but composition defaults
	// still come from CUE/StackKit contracts and must be parity-tested here.
	Services []Service `json:"services"`

	// Modules is the latest-version view of the module registry
	// (sk_module_version, one row per slug -- the "released" version).
	Modules []Module `json:"modules"`

	// StackKits is the curated composition catalog (sk_stackkit).
	StackKits []StackKit `json:"stackkits"`

	// ServiceGroups mirrors sk_service_group: the canonical service-group
	// taxonomy including the yaml_section/yaml_key mapping (ADR-0014) that
	// kitio consumes instead of hardcoded Go/TS maps (WS-5).
	ServiceGroups []ServiceGroup `json:"service_groups,omitempty"`

	// ToolDefaultConfigs mirrors sk_tool_default_config: the kit-agnostic
	// baseline config per tool (Phase B).
	ToolDefaultConfigs []ToolDefaultConfig `json:"tool_default_configs,omitempty"`
}

// Tool mirrors the subset of sk_tool that the CLI needs: identity +
// evaluation status + vendor/license metadata + published content.
// Vulnerability and changelog fields are omitted intentionally because they
// are not part of the embedded StackKits contract.
type Tool struct {
	Slug        string   `json:"slug"`
	DisplayName string   `json:"display_name"`
	Category    string   `json:"category"`
	Layer       string   `json:"layer,omitempty"`
	Status      string   `json:"status"` // sk_tool.maturity: "unknown" | "experimental" | "beta" | "ga"
	Homepage    string   `json:"homepage,omitempty"`
	Description string   `json:"description,omitempty"`
	LogoURL     string   `json:"logo_url,omitempty"`
	ImageURL    string   `json:"image_url,omitempty"`
	Tags        []string `json:"tags,omitempty"`

	// v4 fields: vendor/license metadata from sk_tool and published content
	// from sk_tool_content (CP-2, discovery-to-docs pipeline).
	Vendor           string       `json:"vendor,omitempty"`
	License          string       `json:"license,omitempty"`
	LicenseSPDX      string       `json:"license_spdx,omitempty"`
	DocumentationURL string       `json:"documentation_url,omitempty"`
	RepoURL          string       `json:"repo_url,omitempty"`
	UseCases         []string     `json:"use_cases,omitempty"`
	Content          *ToolContent `json:"content,omitempty"`
}

// ToolContent carries the published tool documentation content from
// sk_tool_content. Only content_kind=tool_doc with status=published
// is embedded in the snapshot.
type ToolContent struct {
	ContentKind   string              `json:"content_kind"`
	Version       int                 `json:"version"`
	ContentHash   string              `json:"content_hash"`
	PromptVersion string              `json:"prompt_version,omitempty"`
	Sections      ToolContentSections `json:"sections"`
}

// ToolContentSections maps the structured documentation sections of a
// tool_doc content entry retained in the versioned snapshot wire shape.
type ToolContentSections struct {
	Overview        string            `json:"overview,omitempty"`
	VendorInfo      string            `json:"vendor_info,omitempty"`
	UseCases        []string          `json:"use_cases,omitempty"`
	EnableSnippet   string            `json:"enable_snippet,omitempty"`
	Steps           []ToolContentStep `json:"steps,omitempty"`
	Verify          []string          `json:"verify,omitempty"`
	Troubleshooting []ToolContentStep `json:"troubleshooting,omitempty"`
}

// ToolContentStep is a titled step or troubleshooting entry.
type ToolContentStep struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Service mirrors the CLI-visible projection of sk_service joined with the
// current StackKit binding. It intentionally separates user-facing service
// identity from tool/module implementation names.
type Service struct {
	Key                     string   `json:"key"`
	DisplayName             string   `json:"display_name"`
	Description             string   `json:"description,omitempty"`
	ToolName                string   `json:"tool_name"`
	ModuleSlug              string   `json:"module_slug"`
	Role                    string   `json:"role,omitempty"`
	DefaultTool             string   `json:"default_tool,omitempty"`
	Alternatives            []string `json:"alternatives,omitempty"`
	LocalSlug               string   `json:"local_slug"`
	PublicSlug              string   `json:"public_slug"`
	LegacyAliases           []string `json:"legacy_aliases,omitempty"`
	IdentityPolicy          string   `json:"identity_policy"`
	OwnerProvisioningPolicy string   `json:"owner_provisioning_policy"`
	Icon                    string   `json:"icon,omitempty"`
	LogoURL                 string   `json:"logo_url,omitempty"`
	Badge                   string   `json:"badge,omitempty"`
	Layer                   string   `json:"layer,omitempty"`
	Section                 string   `json:"section,omitempty"`
	Order                   int      `json:"order,omitempty"`
	EnableVar               string   `json:"enable_var,omitempty"`
	GuideURL                string   `json:"guide_url,omitempty"`
	SetupPolicy             string   `json:"setup_policy,omitempty"`
	SetupActionLabel        string   `json:"setup_action_label,omitempty"`
	Delivery                Delivery `json:"delivery,omitempty"`
	BootstrapProvider       string   `json:"bootstrap_provider,omitempty"`
	Default                 bool     `json:"default"`
}

type Delivery struct {
	ManagedBy string `json:"managedBy,omitempty"`
}

// Module is the CLI-visible projection of one released sk_module_version.
// ContractHash is the SHA256 parity anchor (ADR-0010 §Parity contract).
type Module struct {
	Slug              string   `json:"slug"`
	DisplayName       string   `json:"display_name,omitempty"`
	Version           string   `json:"version"`
	Layer             string   `json:"layer,omitempty"`
	Description       string   `json:"description,omitempty"`
	ContractHash      string   `json:"contract_hash"`
	SupportedContexts []string `json:"supported_contexts,omitempty"`
	Core              bool     `json:"core,omitempty"`
}

// StackKit is a curated composition (sk_stackkit) with its module roster
// frozen at the version the kit was pinned against.
type StackKit struct {
	Slug        string           `json:"slug"`
	DisplayName string           `json:"display_name"`
	Description string           `json:"description,omitempty"`
	Layers      []string         `json:"layers,omitempty"`
	Modules     []StackKitModule `json:"modules"`

	// Version and ReleaseChannel mirror sk_stackkit (ADR-0018): the kit
	// version and its promotion channel ("edge" | "beta" | "stable").
	// The WS-2 release gate compares against the stable-channel truth.
	Version        string `json:"version,omitempty"`
	ReleaseChannel string `json:"release_channel,omitempty"`

	// ServiceSelections mirrors sk_stackkit_service_selection: which module
	// is selected (and which alternatives are allowed) per service group.
	ServiceSelections []StackKitServiceSelection `json:"service_selections,omitempty"`

	// SpecProfiles mirrors sk_stackkit_spec_profile (default/saved kinds
	// only): the drift-detection hashes generate/verify compare against.
	// The full definition stays in the Git-owned Kit CUE/YAML source.
	SpecProfiles []StackKitSpecProfile `json:"spec_profiles,omitempty"`

	// ToolConfigs mirrors sk_stackkit_tool_config: the kit-specific,
	// schema-validated config per service group + module.
	ToolConfigs []StackKitToolConfig `json:"tool_configs,omitempty"`
}

// StackKitModule is a module-in-a-stackkit reference: slug + pinned
// module-version plus the role the module plays in the composition.
type StackKitModule struct {
	Slug    string `json:"slug"`
	Version string `json:"version,omitempty"`
	Role    string `json:"role,omitempty"` // "ingress", "identity", "app", ...
}

// StackKitServiceSelection mirrors sk_stackkit_service_selection.
type StackKitServiceSelection struct {
	ServiceGroupSlug       string   `json:"service_group_slug"`
	Role                   string   `json:"role,omitempty"` // "required" | "recommended" | "optional"
	SelectedModuleSlug     string   `json:"selected_module_slug,omitempty"`
	AlternativeModuleSlugs []string `json:"alternative_module_slugs,omitempty"`
}

// StackKitSpecProfile mirrors the hash-relevant subset of
// Legacy spec-profile projection. The materialized definition remains in the
// Git-owned Kit CUE/YAML source; this snapshot carries only parity anchors.
type StackKitSpecProfile struct {
	Slug               string `json:"slug"`
	Kind               string `json:"kind,omitempty"` // "default" | "saved"
	IsDefault          bool   `json:"is_default,omitempty"`
	SpecHash           string `json:"spec_hash,omitempty"`
	KitDefinitionHash  string `json:"kit_definition_hash,omitempty"`
	ModuleContractHash string `json:"module_contract_hash,omitempty"`
}

// StackKitToolConfig mirrors sk_stackkit_tool_config: the validated config
// payload generate consumes as kit-level defaults (WS-2).
type StackKitToolConfig struct {
	ServiceGroupSlug string          `json:"service_group_slug"`
	ModuleSlug       string          `json:"module_slug,omitempty"`
	ModuleVersion    string          `json:"module_version,omitempty"`
	Config           json.RawMessage `json:"config,omitempty"`
}

// ServiceGroup mirrors sk_service_group: canonical service-group taxonomy
// plus the ADR-0014 yaml-section mapping consumed by kitio (WS-5).
type ServiceGroup struct {
	Slug              string `json:"slug"`
	DisplayName       string `json:"display_name,omitempty"`
	LayerSlug         string `json:"layer_slug,omitempty"`
	SelectionType     string `json:"selection_type,omitempty"` // "single" | "multi"
	DefaultModuleSlug string `json:"default_module_slug,omitempty"`
	YAMLSection       string `json:"yaml_section,omitempty"` // "foundation" | "platform" | "application"
	YAMLKey           string `json:"yaml_key,omitempty"`
}

// ToolDefaultConfig mirrors sk_tool_default_config: kit-agnostic baseline
// config per tool. Copied into StackKitToolConfig when a tool joins a kit.
type ToolDefaultConfig struct {
	ToolSlug        string          `json:"tool_slug"`
	Config          json.RawMessage `json:"config,omitempty"`
	ConfigSchemaRef string          `json:"config_schema_ref,omitempty"`
}
