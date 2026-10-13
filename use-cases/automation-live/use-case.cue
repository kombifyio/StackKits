// Package automation defines the public Automation use case package.
//
// The public export carries only the explicit ntfy live relay. The internal
// automation engine is not part of the public export.
package automation

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "automation"
		useCaseRef:  "automation"
		displayName: "Automation"
		version:     "0.1.0"
		layer:       "application"
		category:    "automation"
		lifecycle:   "experimental"
		description: "Explicit live notifications for connected clients on the owner's node through a private ntfy relay. Runs without a kombify plan."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "ntfy-live"
			role:       "primary"
			required:   true
			rationale:  "Explicit live-only ntfy relay for connected clients, with no queue, native accounts, attachments or Web Push; never a durable push substitute."
			capabilities: ["live-notifications"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-ntfy-live"
	runtimeProfiles: "self-hosted-ntfy-live": {
		displayName: "Self-hosted live notification relay"
		description: "The ntfy live relay runs as one workload on one owner-selected node through Standalone Compose or the OpenTofu wrapper."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: [
			"The relay forwards only to currently connected clients; offline notifications are lost.",
			"The route is administrator-only and private, never a public or edge route.",
		]
	}

	computeTiers: {
		low: {included: false, reason: "No independently measured low variant."}
		standard: {included: true, moduleSlug: "ntfy-live", functions: ["live-notifications"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Connected-client live relay only; resource reservations are configured policy."]}
		high: {included: false, reason: "No named measured high capability; standard is not copied as high."}
	}

	tools: "ntfy-live": {moduleSlug: "ntfy-live", role: "primary", required: true, rationale: "Explicit live-only ntfy relay for connected clients, with no queue, native accounts, attachments or Web Push; never a durable push substitute.", capabilities: ["live-notifications"]}

	connectors: stackkit: {
		kind:      "stackkit"
		name:      "stackkit"
		owner:     "stackkit"
		endpoint:  "/mcp"
		transport: "streamable-http"
		auth:      "stackkit-mcp-token"
		capabilities: ["lifecycle", "evidence"]
	}

	setup: {
		defaultPolicy: "manual"
		drops: [{name: "ntfy-live-delivery-check", policy: "on_demand", description: "Verify a connected live subscriber receives the private node delivery check and no cached replay remains; no upstream administrator login is claimed."}]
	}

	evidence: {
		healthChecks: ["ntfy-live-http"]
		required: ["route", "backup", "runtime-owner", "removal"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle

	agentSurface: {
		equipPolicy: "on-generate"
		lifecycleMcp: {}
		productMcps: []
		apis: [{id: "ntfy-live-api", protocol: "rest", purpose: "POST /topic and GET /topic/json for connected live delivery through the owner-only private route; no queued replay.", auth: "owner-private-route"}]
		skills: [{id: "ntfy-live", audience: "stack-operator", source: "stackkits", path: "use-cases/automation-live/agent/ntfy-live/SKILL.md"}]
		cliHelpers: [{
			command: "stackkit agent mcp-config"
			purpose: "Print the stackkit lifecycle MCP client connection."
		}]
		configBaseline: {
			status: "omitted"
			reason: "The relay keeps no message data; StackKits governs only the server configuration."
		}
	}
}
