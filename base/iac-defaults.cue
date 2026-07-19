// Package base — IaC-Defaults schema (kit-update-phase-1, ADR-0018).
//
// Host-local implementation-adapter versions, default tag-set, and backend-config templates are
// centralized here so that every kit reaches a consistent IaC starting point.
// The Go template renderer (`internal/template/renderer.go`) projects values
// from a kit's `iac:` field onto the `iac/defaults` Terraform module, which
// each kit imports as `module "defaults"`.
//
// Why a CUE schema instead of pure HCL `locals`:
//   - Type-safety + unification: a kit author cannot omit `kit_slug`/`kit_version`.
//   - Single Source-of-Truth across kits: bumping `provider_versions.docker`
//     here propagates without per-module edits.
//   - The audit trail of provider-version bumps lives in CUE git history.
package base

// =============================================================================
// IaCDefaults — top-level shape passed to the renderer
// =============================================================================

#IaCDefaults: {
	// Pinned host-local OpenTofu adapter versions. Server infrastructure
	// providers are intentionally absent; TechStack owns those drivers. Simulate
	// may consume the same neutral executor contract as an optional test harness.
	provider_versions: #ProviderVersions

	// Tags merged into every Docker resource via `module.defaults.tags`.
	// `kit_slug` + `kit_version` are required; `tenant_id` is set by the
	// generator when the kit is rendered for a specific tenant deployment.
	default_tags: #DefaultTags

	// Tofu/Terraform backend selection. `local` is the homelab default;
	// `s3` for self-hosted remote-state; `remote` for Terraform Cloud /
	// HCP Terraform Enterprise.
	backend: #BackendConfig
}

// =============================================================================
// Provider versions
// =============================================================================

#ProviderVersions: {
	// Mandatory baseline — every kit needs these
	docker: string | *"~> 3.0"
	local:  string | *"~> 2.5"
	random: string | *"~> 3.6"
	null:   string | *"~> 3.2"
}

// =============================================================================
// Default tags
// =============================================================================

#DefaultTags: {
	managed_by:  string | *"stackkit"
	kit_slug:    string
	kit_version: string

	// Set by generator when rendering for a specific tenant deployment
	tenant_id?: string

	// Free-form additions (e.g. `environment: "prod"`, `cost_center: "..."`)
	[string]: string
}

// =============================================================================
// Backend configuration — discriminated union
// =============================================================================

#BackendConfig: {
	type: "local" | "s3" | "remote"

	if type == "local" {
		local: {
			path: string | *"deploy/terraform.tfstate"
		}
	}

	if type == "s3" {
		s3: {
			bucket: string
			key:    string
			region: string | *"eu-central-1"

			// Optional — only for state-locking via DynamoDB
			dynamodb_table?: string

			// Optional — only when targeting non-AWS S3-compatible store
			endpoint?: string
		}
	}

	if type == "remote" {
		remote: {
			hostname:     string | *"app.terraform.io"
			organization: string
			workspace:    string
		}
	}
}
