// Package vault defines the Password Vault use case package.
//
// Vault is a default Basement and Cloud Kit use case. StackKits realizes it
// through the existing Vaultwarden module and Architecture v2 application
// lifecycle; provider and account lifecycle remain outside Standard Mode.
package vault

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "vault"
		useCaseRef:  "vault"
		displayName: "Password Vault"
		version:     "0.14.0"
		layer:       "application"
		category:    "vault"
		lifecycle:   "beta"
		description: "Private password and secure-note vault with owner-controlled bootstrap, backup, recovery, upgrade, drift, and removal through Vaultwarden."
	}

	selection: {
		role: "default"
		defaultTool: {
			moduleSlug: "vaultwarden"
			role:       "primary"
			required:   true
			rationale:  "Vaultwarden is the lightweight Bitwarden-compatible default already owned by the native Application Kit runtime."
			capabilities: ["password-manager", "secure-notes", "totp"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-vault"
	runtimeProfiles: {
		"self-hosted-vault": {
			displayName: "Self-hosted Password Vault"
			description: "StackKits deploys digest-pinned Vaultwarden through the selected application adapter on the Owner-selected node."
			realization: "oss"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane:      false
			requiresLocalBridge:       false
		}
	}

	computeTiers: {
		low: {
			included: true
			moduleSlug: "vaultwarden"
			functions: ["password-manager", "secure-notes", "totp"]
			load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}
			notes: ["Always resident for unlock/sync; almost no idle CPU. Fits the low graph."]
		}
		standard: {
			included: true
			moduleSlug: "vaultwarden"
			functions: ["password-manager", "secure-notes", "totp"]
			load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}
		}
		high: {
			included: true
			moduleSlug: "vaultwarden"
			functions: ["password-manager", "secure-notes", "totp"]
			load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}
			notes: ["No extra vault functions on high until a graph substitution exists."]
		}
	}

	tools: vaultwarden: {
		moduleSlug: "vaultwarden"
		role:       "primary"
		required:   true
		rationale:  "The existing Vaultwarden module owns the container, route, persistent data, health, and secret-reference contract."
		capabilities: ["password-manager", "secure-notes", "totp", "rest-api"]
	}

	connectors: stackkit: {
		kind:      "stackkit"
		name:      "stackkit"
		owner:     "stackkit"
		endpoint:  "/mcp"
		transport: "streamable-http"
		auth:      "stackkit-mcp-token"
		capabilities: ["lifecycle", "setup", "evidence"]
	}

	productApis: vaultwarden: {
		protocol: "rest"
		basePath: "/api"
		auth:     "vaultwarden-owner-auth"
		purpose:  "Owner invitation preparation plus manual encrypted account setup and bounded health/readback for the Bitwarden-compatible Vaultwarden service."
	}

	setup: {
		defaultPolicy: "on_demand"
		drops: [{
			name:        "vault-owner-invite"
			policy:      "on_demand"
			description: "Prepare or verify the Vaultwarden owner invitation using owner-custodied administration; the official client completes the encrypted personal account."
		}]
	}

	evidence: {
		healthChecks: ["vault-route", "vault-api", "vault-owner", "vault-backup"]
		required: ["route", "auth", "backup", "owner-bootstrap", "runtime-owner", "removal"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle

	agentSurface: {
		equipPolicy:  "on-generate"
		lifecycleMcp: {}
		productMcps: []
		apis: [{
			id:       "vaultwarden"
			protocol: "rest"
			purpose:  "Owner invitation preparation and bounded health/readback. The official client completes the encrypted personal account; Vaultwarden has no native product MCP."
			auth:     "vaultwarden-owner-auth"
		}]
		skills: [{
			id:       "owner-setup"
			audience: "product-user"
			source:   "stackkits"
			path:     "use-cases/vault/agent/owner-setup/SKILL.md"
		}]
		cliHelpers: [{
			command: "stackkit secrets materialize"
			purpose: "Establish or reuse owner-bound custody for declared local secret references; never print or export the Vaultwarden admin token."
		}, {
			command: "stackkit agent mcp-config"
			purpose: "Print the stackkit lifecycle MCP client connection."
		}]
		configBaseline: {
			status: "omitted"
			reason: "Vault runtime is the digest-pinned selected-PaaS bundle. StackKits does not author a separate Vaultwarden configuration file."
		}
	}
}
