// Package registry exposes the StackKits catalog (tools, module versions,
// curated stackkits) to the CLI in an OSS-safe way.
//
// The registry has two producers:
//
//  1. kombify-Administration (private, DB-first — ADR-0010). It is the
//     authoritative source for tool evaluations, module-version lineage,
//     and curated stackkit compositions. Internal callers reach it via
//     STACKKIT_ADMIN_ENDPOINT.
//
//  2. This package's embedded snapshot at internal/registry/data/
//     registry_snapshot.json. The snapshot is a frozen, DB-independent
//     projection used by OSS builds of the CLI. It is refreshed at release
//     time either from the Admin API (for the private build, baked into
//     goreleaser) or from the local CUE tree (for pure-OSS builds).
//
// At runtime the AutoClient selects between the two producers so the CLI
// behaves identically with or without an Admin endpoint configured.
// OSS users never need Postgres; internal users never work with stale
// data. Neither mode writes — release and verify operations stay on the
// existing Admin API clients in cmd/stackkit/commands/module.go.
package registry

import "time"

// SnapshotVersion is the current schema version for registry_snapshot.json.
// Bump when the on-disk shape changes in an incompatible way.
const SnapshotVersion = 2

// Snapshot is the envelope serialized to registry_snapshot.json.
//
// All nested slices are sorted deterministically (by primary key) so that
// two snapshots with identical content produce byte-identical JSON --
// this keeps diffs readable and `goimports`-style re-bakes idempotent.
// Source values for Snapshot.Source. Constants instead of bare strings so
// callers (RemoteClient, CUE bootstrap, manual fallback) agree on spelling.
const (
	SourceAdminAPI = "admin-api"
	SourceCUE      = "cue"
	SourceManual   = "manual"
)

type Snapshot struct {
	// SchemaVersion pins the shape of this envelope. Must equal
	// SnapshotVersion at load time; mismatches fail-fast.
	SchemaVersion int `json:"schema_version"`

	// Source identifies where the snapshot was produced.
	// One of: "admin-api" (private build), "cue" (OSS bootstrap),
	// "manual" (checked-in fallback).
	Source string `json:"source"`

	// GeneratedAt is the UTC time the snapshot was baked.
	GeneratedAt time.Time `json:"generated_at"`

	// AdminEndpoint records which API instance produced the snapshot
	// (when Source == "admin-api"). Empty for CUE-baked snapshots.
	AdminEndpoint string `json:"admin_endpoint,omitempty"`

	// Tools is the unified tool catalog (sk_tool).
	Tools []Tool `json:"tools"`

	// Services is the product-facing service catalog (sk_service +
	// sk_stackkit_service). It is authoritative for canonical service keys,
	// URL slugs, legacy aliases, and strict owner/SSO readiness policy.
	Services []Service `json:"services"`

	// Modules is the latest-version view of the module registry
	// (sk_module_version, one row per slug -- the "released" version).
	Modules []Module `json:"modules"`

	// StackKits is the curated composition catalog (sk_stackkit).
	StackKits []StackKit `json:"stackkits"`
}

// Tool mirrors the subset of sk_tool that the CLI needs: identity +
// evaluation status. Vulnerability and changelog fields are omitted
// intentionally -- OSS users have no use for them, and privates stay
// behind the Admin API.
type Tool struct {
	Slug        string   `json:"slug"`
	DisplayName string   `json:"display_name"`
	Category    string   `json:"category"`
	Layer       string   `json:"layer,omitempty"`
	Status      string   `json:"status"` // "evaluated" | "candidate" | "deprecated"
	Homepage    string   `json:"homepage,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
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
	LocalSlug               string   `json:"local_slug"`
	PublicSlug              string   `json:"public_slug"`
	LegacyAliases           []string `json:"legacy_aliases,omitempty"`
	IdentityPolicy          string   `json:"identity_policy"`
	OwnerProvisioningPolicy string   `json:"owner_provisioning_policy"`
	Icon                    string   `json:"icon,omitempty"`
	Badge                   string   `json:"badge,omitempty"`
	Section                 string   `json:"section,omitempty"`
	Order                   int      `json:"order,omitempty"`
	EnableVar               string   `json:"enable_var,omitempty"`
	Default                 bool     `json:"default"`
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
}

// StackKitModule is a module-in-a-stackkit reference: slug + pinned
// module-version plus the role the module plays in the composition.
type StackKitModule struct {
	Slug    string `json:"slug"`
	Version string `json:"version,omitempty"`
	Role    string `json:"role,omitempty"` // "ingress", "identity", "app", ...
}
