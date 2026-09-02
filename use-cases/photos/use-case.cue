// Package photos defines the Photos / Family Photo Vault use case package.
//
// Photos is a default kit use case (Basement and Cloud). The release path stays
// self-hosted and OSS-realized through Immich. ente-photos is post-1.0 and is
// not a catalog component until modules/ente-photos exists. There is intentionally no
// control-plane runtime profile: per PLACEMENT-MODE-STANDARD heavy stateful
// media apps such as Immich are local-only/standard and never
// managed-serverless. Managed value for this use case is realized as lease,
// routing, backup, and recovery-of-intent entitlements — not as a
// Control-Plane-hosted Immich.
package photos

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "photos"
		useCaseRef:  "photos"
		displayName: "Photos and Memories"
		version:     "0.14.0"
		layer:       "application"
		category:    "photos"
		lifecycle:   "beta"
		description: "Family photo and video vault centered on Immich with mobile backup, ML search, shared albums, and self-hosted or bring-your-own profiles."
	}

	selection: {
		role: "default"
		defaultTool: {
			moduleSlug: "immich"
			role:       "primary"
			required:   true
			rationale:  "Immich is the Google-Photos-class default with mobile backup, ML search, and multi-user family sharing."
			capabilities: ["photos", "photo-management", "media-backup", "ai-search"]
		}
	}

	defaultRuntimeProfile: "self-hosted-photos"
	runtimeProfiles: {
		"self-hosted-photos": {
			displayName: "Self-hosted Photo Vault"
			description: "StackKits deploys digest-pinned Immich through the graph-selected runtime (PaaS on standard/high, standalone lite on low)."
			realization: "oss"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane:      false
			requiresLocalBridge:       false
			notes: ["Managed-serverless is intentionally absent; provider lifecycle, leases, credentials, and endpoints are outside StackKits Standard Mode authority."]
		}
		"bring-your-own-photos": {
			displayName: "Bring Your Own Photo Service"
			description: "An existing Immich (or API-compatible) instance the user already operates is connected as the package backend."
			realization: "external"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane:      false
			requiresLocalBridge:       false
		}
	}

	computeTiers: {
		low: {
			included: true
			moduleSlug: "immich-lite"
			functions: ["photos", "photo-management", "media-backup"]
			load: {residency: "always-on", baseline: "idle-resident", burst: "ingest"}
			notes: ["No ML search. Catalog alternative immich-lite on standalone-compose. Kit graph also substitutes stackkits-immich-lite-runtime. Mobile backup is the spike; the library otherwise waits."]
		}
		standard: {
			included: true
			moduleSlug: "immich"
			functions: ["photos", "photo-management", "media-backup", "ai-search"]
			load: {residency: "always-on", baseline: "active-resident", burst: "ingest"}
			notes: ["ML worker is resident base load. Ingest and interactive search are bursts on top."]
		}
		high: {
			included: true
			moduleSlug: "immich"
			functions: ["photos", "photo-management", "media-backup", "ai-search"]
			load: {residency: "always-on", baseline: "active-resident", burst: "ingest"}
			notes: ["Same photo functions as standard until a high-graph substitution exists. Extra headroom is kit high, not extra Immich features."]
		}
	}

	tools: {
		immich: {
			moduleSlug: "immich"
			role:       "primary"
			required:   true
			rationale:  "Default photo vault implementation: server, ML, pgvecto-rs PostgreSQL, and Redis companions."
			capabilities: ["photos", "photo-management", "media-backup", "ai-search", "rest-api"]
		}
		"immich-lite": {
			moduleSlug: "immich-lite"
			role:       "primary"
			required:   false
			rationale:  "Low-graph Immich without ML, delivered through standalone Compose."
			capabilities: ["photos", "photo-management", "media-backup"]
		}
	}

	connectors: {
		stackkit: {
			kind:      "stackkit"
			name:      "stackkit"
			owner:     "stackkit"
			endpoint:  "/mcp"
			transport: "streamable-http"
			auth:      "stackkit-mcp-token"
			capabilities: ["lifecycle", "setup", "evidence", "handoff"]
		}
		photos: {
			kind:      "product-api"
			name:      "photos"
			owner:     "product"
			transport: "http"
			auth:      "product-auth"
			capabilities: ["asset-metadata", "album", "share", "user-management", "jobs"]
		}
	}

	productApis: {
		immich: {
			protocol: "rest"
			basePath: "/api"
			auth:     "immich-auth"
			purpose:  "Owner bootstrap (admin sign-up, server/user onboarding), health ping, user and album management, shared links, asset upload/download, and job status."
		}
	}

	ril: capabilities: {
		inventory: {
			mode:      "read"
			authority: "read-only"
			source:    "product-api"
			evidence:  "Album, user, shared-link, and storage-usage inventory."
		}
		status: {
			mode:      "read"
			authority: "read-only"
			source:    "product-api"
			evidence:  "Server ping, job queues, and ML pipeline health."
		}
		"album-plan": {
			mode:      "plan"
			authority: "read-only"
			source:    "product-api"
			evidence:  "Draft album or share organization plan without applying changes."
		}
		"user-invite": {
			mode:             "write"
			authority:        "gated-write"
			source:           "product-api"
			requiresApproval: true
			evidence:         "Audited family-member account creation."
		}
		"share-update": {
			mode:             "write"
			authority:        "gated-write"
			source:           "product-api"
			requiresApproval: true
			evidence:         "Audited shared-link or album-permission update."
		}
	}

	setup: {
		defaultPolicy: "on_demand"
		drops: [
			{
				name:        "immich-owner-bootstrap"
				policy:      "on_demand"
				description: "Create or verify the Immich owner from StackKit admin credentials and complete server/user onboarding (realized today by the immich-owner-bootstrap setup drop)."
			},
			{
				name:        "photos-family-onboarding"
				policy:      "on_demand"
				description: "Create family member accounts and initial shared albums; record invite evidence."
			},
		]
	}

	evidence: {
		healthChecks: ["photos-route", "photos-api", "photos-owner", "photos-backup"]
		required: ["route", "auth", "backup", "owner-bootstrap", "runtime-owner", "removal"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle & {
		referenceVertical: true
		stages: setup: {
			name:        "setup"
			operations:  ["stackkit.setup"]
			phases:      ["authorize", "configure", "verify"]
			surfaces:    ["cli", "mcp", "state-console"]
			evidence:    ["setup-result"]
			mutation:    true
			destructive: false
			ownerApproval: true
		}
	}

	agentSurface: {
		equipPolicy:  "on-generate"
		lifecycleMcp: {}
		productMcps: []
		apis: [{
			id:       "immich"
			protocol: "rest"
			purpose:  "Owner bootstrap, health, albums, shares, assets, and jobs. Immich has no native product MCP."
			auth:     "immich-auth"
		}]
		skills: [{
			id:       "family-vault"
			audience: "product-user"
			source:   "stackkits"
			path:     "use-cases/photos/agent/family-vault/SKILL.md"
		}]
		cliHelpers: [{
			command: "stackkit agent mcp-config"
			purpose: "Print the stackkit lifecycle MCP client connection."
		}]
		configBaseline: {
			status: "omitted"
			reason: "Immich runtime is the digest-pinned selected-PaaS bundle. StackKits does not author a separate Immich configuration file."
		}
	}
}
