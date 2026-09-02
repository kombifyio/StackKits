// Package files defines the File Storage / Document Management use case package.
//
// Files is a default BaseKit use case. The release path stays self-hosted and
// OSS-realized through Cloudreve, while heavier collaboration and DMS profiles
// remain product intent until their native workload and lifecycle contracts exist.
package files

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "files"
		useCaseRef:  "files"
		displayName: "File Storage and Documents"
		version:     "0.13.0"
		layer:       "application"
		category:    "files"
		lifecycle:   "beta"
		description: "File storage, sharing, document collaboration, and document-management workflows with local, managed, and bring-your-own profiles."
	}

	selection: {
		role: "default"
		defaultTool: {
			moduleSlug: "cloudreve"
			role:       "primary"
			required:   true
			rationale:  "Cloudreve is the lightweight BaseKit default for file storage and sharing."
			capabilities: ["files", "document-storage", "file-sharing"]
		}
		alternatives: [
			{
				moduleSlug: "nextcloud"
				role:       "primary"
				required:   false
				rationale:  "Planned collaboration and WebDAV alternative. Native StackKits does not currently accept or deploy Nextcloud."
				capabilities: ["files", "document-storage", "file-sharing", "collaboration", "webdav"]
			},
		]
	}

	defaultRuntimeProfile: "self-hosted-lightweight"
	runtimeProfiles: {
		"self-hosted-lightweight": {
			displayName: "Self-hosted Lightweight Files"
			description: "StackKit deploys Cloudreve as the default file storage and sharing app on the user's node."
			realization: "oss"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane:      false
			requiresLocalBridge:       false
		}
		"self-hosted-collaboration": {
			displayName: "Self-hosted Collaboration Suite"
			description: "Planned Nextcloud files provider for collaboration and WebDAV-heavy use; not available through native StackKits init, resolve, or apply."
			realization: "oss"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane:      false
			requiresLocalBridge:       false
			notes: ["Product intent only. Cloudreve is the only native Files alternative. Nextcloud needs a workload, runtime adapter, owner setup, routing/auth, backup/restore, and migration contract before admission."]
		}
		"kombify-managed-files": {
			displayName: "Kombify Managed Files"
			description: "Kombify operates storage, route, auth handoff, backup, sharing policy, and API wiring for the Files use case."
			realization: "control-plane"
			placementModes: ["managed-serverless"]
			managedServerlessEligible: true
			requiresControlPlane:      true
			requiresLocalBridge:       false
		}
		"kombify-managed-dms": {
			displayName: "Kombify Managed Document Management"
			description: "Kombify operates document ingestion, OCR/indexing, retention policy, search, sharing, backup, and RIL evidence for document-management workflows."
			realization: "control-plane"
			placementModes: ["managed-serverless"]
			managedServerlessEligible: true
			requiresControlPlane:      true
			requiresLocalBridge:       false
			notes: ["DMS realization may combine storage, OCR/indexing workers, and document metadata services behind the Control Plane catalog."]
		}
		"bring-your-own-storage": {
			displayName: "Bring Your Own Storage"
			description: "An existing Nextcloud, WebDAV, S3-compatible bucket, or file service is connected as the package backend."
			realization: "external"
			placementModes: ["local-only", "standard", "managed-serverless"]
			managedServerlessEligible: true
			requiresControlPlane:      false
			requiresLocalBridge:       false
		}
	}

	computeTiers: {
		low: {
			included:   true
			moduleSlug: "cloudreve"
			functions: ["files", "document-storage", "file-sharing"]
			load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}
			notes: ["Lightweight sharing. Nextcloud is not a native workload on any profile."]
		}
		standard: {
			included:   true
			moduleSlug: "cloudreve"
			functions: ["files", "document-storage", "file-sharing"]
			load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}
			notes: ["Nextcloud remains planned product intent, not a selectable native alternative."]
		}
		high: {
			included:   true
			moduleSlug: "cloudreve"
			functions: ["files", "document-storage", "file-sharing"]
			load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}
			notes: ["A high profile does not add collaboration or DMS. Those need their own admitted workload and lifecycle contracts."]
		}
	}

	tools: {
		cloudreve: {
			moduleSlug: "cloudreve"
			role:       "primary"
			required:   true
			rationale:  "Lightweight default file storage and sharing implementation."
			capabilities: ["files", "document-storage", "file-sharing", "rest-api"]
		}
		nextcloud: {
			moduleSlug: "nextcloud"
			role:       "primary"
			required:   false
			rationale:  "Planned collaboration alternative with WebDAV and richer document workflows; native rollout is unsupported."
			capabilities: ["files", "document-storage", "file-sharing", "collaboration", "webdav"]
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
		files: {
			kind:      "product-api"
			name:      "files"
			owner:     "product"
			transport: "http"
			auth:      "product-auth"
			capabilities: ["file-list", "download", "upload", "share", "metadata"]
		}
	}

	productApis: {
		cloudreve: {
			protocol: "rest"
			basePath: "/api/v4"
			auth:     "cloudreve-auth"
			purpose:  "Owner bootstrap, health, file metadata, upload/download, and sharing policy checks."
		}
		nextcloud: {
			protocol: "webdav"
			basePath: "/remote.php/dav/files"
			auth:     "nextcloud-auth"
			purpose:  "Document and file access for collaboration-oriented provider profiles."
		}
		"managed-dms": {
			protocol: "rest"
			basePath: "/api/documents"
			auth:     "control-plane-auth"
			purpose:  "Managed document ingestion, OCR/index evidence, search, metadata, retention, and sharing workflows."
		}
	}

	ril: capabilities: {
		inventory: {
			mode:      "read"
			authority: "read-only"
			source:    "product-api"
			evidence:  "Folder, file, document, share, and storage-quota inventory."
		}
		"document-search": {
			mode:      "read"
			authority: "read-only"
			source:    "product-api"
			evidence:  "Searchable document index results and metadata provenance."
		}
		"classification-plan": {
			mode:      "plan"
			authority: "read-only"
			source:    "product-api"
			evidence:  "Draft folder, retention, tag, or document-classification plan without applying changes."
		}
		"share-update": {
			mode:             "write"
			authority:        "gated-write"
			source:           "product-api"
			requiresApproval: true
			evidence:         "Audited share, permission, or retention-policy update."
		}
		"ingest-document": {
			mode:             "write"
			authority:        "gated-write"
			source:           "product-api"
			requiresApproval: true
			evidence:         "Audited document upload/import with OCR/index evidence when DMS is enabled."
		}
	}

	setup: {
		defaultPolicy: "on_demand"
		drops: [
			{
				name:        "cloudreve-owner-bootstrap"
				policy:      "on_demand"
				description: "Create or verify the app-local Files administrator through the pinned Cloudreve API and revoke temporary setup sessions. Storage and sharing choices remain in the application."
			},
			{
				name:        "document-index-bootstrap"
				policy:      "on_demand"
				description: "Verify OCR/index/search wiring for managed DMS profiles."
			},
		]
	}

	evidence: {
		healthChecks: ["files-route", "files-api", "files-owner", "files-backup"]
		required: ["route", "auth", "backup", "owner-bootstrap", "runtime-owner", "removal"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle

	agentSurface: {
		equipPolicy: "on-generate"
		lifecycleMcp: {}
		productMcps: []
		skills: [{
			id:       "owner-setup"
			audience: "product-user"
			source:   "stackkits"
			path:     "use-cases/files/agent/owner-setup/SKILL.md"
		}]
		apis: [{
			id:       "cloudreve"
			protocol: "rest"
			purpose:  "Owner bootstrap, health, file metadata, upload/download, and sharing. Cloudreve has no native product MCP."
			auth:     "cloudreve-auth"
		}]
		cliHelpers: [{
			command: "stackkit agent mcp-config"
			purpose: "Print the stackkit lifecycle MCP client connection."
		}]
		configBaseline: {
			status: "omitted"
			reason: "Files runtime is the digest-pinned selected-PaaS bundle. StackKits does not author a separate Cloudreve configuration file."
		}
	}
}
