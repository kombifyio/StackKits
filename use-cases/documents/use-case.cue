// Package documents defines the upstream Paperless-ngx documents use case.
package documents

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "documents"
		useCaseRef:  "documents"
		displayName: "Documents"
		version:     "0.1.0"
		layer:       "application"
		category:    "documents"
		lifecycle:   "experimental"
		description: "Private document ingestion, OCR, indexing and search through upstream Paperless-ngx."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "paperless-ngx"
			role:       "primary"
			required:   true
			rationale:  "Paperless-ngx provides the document application, OCR pipeline, index and search interface."
			capabilities: ["document-ingestion", "ocr", "document-search", "document-management"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-documents"
	runtimeProfiles: "self-hosted-documents": {
		displayName: "Self-hosted Documents"
		description: "Paperless-ngx, PostgreSQL and Valkey run on one owner-selected node through Standalone Compose."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["StackKits configures the upstream runtime and owner account. Paperless-ngx remains the document, OCR, indexing and search authority."]
	}

	computeTiers: {
		low: {included: false, reason: "The first Paperless-ngx path uses the standard profile."}
		standard: {included: true, moduleSlug: "paperless-ngx", functions: ["document-ingestion", "ocr", "document-search", "document-management"], load: {residency: "always-on", baseline: "idle-resident", burst: "ingest"}, notes: ["Document and database growth require a separate storage budget."]}
		high: {included: true, moduleSlug: "paperless-ngx", functions: ["document-ingestion", "ocr", "document-search", "document-management"], load: {residency: "always-on", baseline: "idle-resident", burst: "ingest"}, notes: ["Same upstream service graph as standard."]}
	}

	tools: "paperless-ngx": {
		moduleSlug: "paperless-ngx"
		role:       "primary"
		required:   true
		rationale:  "Digest-pinned Paperless-ngx with native authentication and persistent document data."
		capabilities: ["document-ingestion", "ocr", "document-search", "document-management"]
	}
	lifecycle: foundation.#StandardUseCaseLifecycle
	evidence: {healthChecks: ["paperless-http"], required: ["route", "backup", "runtime-owner", "removal"]}
	setup: {
		defaultPolicy: "on_demand"
		drops: [
			{name: "paperless-owner-login", policy: "on_demand", description: "Verify the existing custodied administrator through the exact applied workload and record the plan-bound setup result."},
		]
	}
	agentSurface: {
		equipPolicy: "on-generate"
		lifecycleMcp: {}
		productMcps: []
		apis: [{
			id:       "paperless"
			protocol: "rest"
			purpose:  "Observe the existing administrator through the selected upstream application's admitted API; application features remain upstream-owned."
			auth:     "custodied-owner-login"
		}]
		skills: [{
			id:       "owner-setup"
			audience: "stack-operator"
			source:   "stackkits"
			path:     "use-cases/documents/agent/owner-setup/SKILL.md"
		}]
		cliHelpers: [{
			command: "stackkit setup documents --owner-approve --json"
			purpose: "Verify the existing local administrator after apply using the plan-bound setup flow and existing secret custody."
		}, {
			command: "stackkit agent mcp-config"
			purpose: "Connect to the existing StackKits lifecycle MCP."
		}]
		configBaseline: {
			status: "omitted"
			reason: "The selected upstream bundle owns its configuration; setup observes the custodied administrator and creates no separate initializer or credential store."
		}
	}

}
