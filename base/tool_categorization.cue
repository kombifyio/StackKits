// Package base — Tool Categorization Enums.
//
// Introduced 2026-05-08 by ADR-0018 / kit-update-phase-1.
// These enums classify tools beyond the Layer model (foundation / platform /
// application from ADR-0015). Layer answers "where in the stack?", these enums
// answer "what kind of tool?" (#ToolType) and "which functional slot?"
// (#ToolCategory).
//
// Wizard filtering, doc generation, and the Compatibility-Resolver (ADR-0018)
// consume these. The auto-generated `base/generated/tool_catalog.cue` keeps its
// `category: string` schema for now; a follow-up regeneration of the
// kombify-Administration CUE-emitter (`scripts/generate-cue.ts`) should refine
// `#CatalogTool.category` to `#ToolCategory`. Until then the enum is advisory
// for hand-written CUE and for downstream consumers (CLI, Admin-API).
package base

// =============================================================================
// TOOL TYPE — operational sourcing model
// =============================================================================

// #ToolType describes how a tool is operationally provided.
//   - oss     : self-hosted open-source (e.g. PostgreSQL, Traefik, Vaultwarden)
//   - managed : a managed/SaaS offering (e.g. Render Postgres, Auth0, Doppler)
//   - hybrid  : OSS core with optional managed backing (e.g. Sentry self-hosted
//               + sentry.io, Postgres self-hosted + Render-managed)
#ToolType: "oss" | "managed" | "hybrid"

// =============================================================================
// TOOL CATEGORY — functional slot
// =============================================================================

// #ToolCategory groups tools by the functional role they fill in a stack.
// Curated set as of 2026-05-08; extending the set requires a coordinated change
// here + in the kombify-Administration seed (sk_tool_category) + a CUE
// regeneration. ADR-0018 §1 holds the source-of-truth for this list.
#ToolCategory:
	"database" |
	"queue" |
	"webserver" |
	"auth" |
	"observability" |
	"backup" |
	"ingress" |
	"dns" |
	"media" |
	"storage" |
	"secrets" |
	"vpn" |
	"chat" |
	"automation" |
	"dev-platform" |
	"ai-workload" |
	"compute" |
	"messaging"
