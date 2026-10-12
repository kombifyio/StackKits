// Package marketplace declares the owner's optional app-store intent.
// Runtime admission belongs to Architecture v2. Until its pinned application,
// bootstrap, lifecycle and current-pin receipt exist, this package stays draft
// and declares no selected tool or installable compute tier.
package marketplace

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "marketplace"
		useCaseRef:  "marketplace"
		displayName: "Marketplace"
		version:     "0.1.0"
		layer:       "application"
		category:    "marketplace"
		lifecycle:   "draft"
		description: "Optional Coolify app store on the owner's homelab. StackKits owns integration lifecycle; upstream applications retain their own features."
	}
	selection: {role: "optional", alternatives: []}
	tools: {}
	defaultRuntimeProfile: "self-hosted-marketplace"
	runtimeProfiles: "self-hosted-marketplace": {
		displayName: "Self-hosted Marketplace"
		description: "The target is an optional application on a Basement or Cloud node, alongside the StackKits router. Native application admission and runtime proof are pending."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: [
			"Coolify is an application, not the selected runtime target or the default core.",
			"The owner signs in through PocketID; household users receive explicit rights. The UI is LAN-only or behind TinyAuth.",
			"One execution and routing owner controls each workload. Curated kombify apps use the Coolify API; the upstream store is unchanged.",
			"Users run third-party applications on their own homelab. kombify does not host them; a separately named managed serverless variant is the platform exception.",
			"Package14 runtime/bootstrap/backup/restore admission is tracked in kombify-StackKits-23yki.7. No compatibility or support claim follows from this declaration.",
		]
	}
	computeTiers: {
		low: {included: false, reason: "The pinned Marketplace application and lifecycle have no current-pin runtime receipt."}
		standard: {included: false, reason: "The pinned Marketplace application and lifecycle have no current-pin runtime receipt."}
		high: {included: false, reason: "The pinned Marketplace application and lifecycle have no current-pin runtime receipt."}
	}
	evidence: required: ["admin-bootstrap", "pocketid-login", "private-ui", "route-owner", "backup", "restore", "runtime-owner", "removal"]
	agentSurface: {
		equipPolicy:  "on-generate"
		lifecycleMcp: {}
		productMcps: []
		apis: [{
			id:       "coolify"
			protocol: "rest"
			purpose:  "The native Marketplace executor uses the held own-host server binding for admitted app deployments. API and SSH credentials remain in node-private custody."
			auth:     "stackkit-marketplace-node-custody"
		}]
		skills: [{
			id:       "marketplace-operations"
			audience: "stack-operator"
			source:   "stackkits"
			path:     "use-cases/marketplace/agent/operations/SKILL.md"
		}]
		cliHelpers: [{
			command: "stackkit agent mcp-config"
			purpose: "Configure the existing StackKits lifecycle MCP endpoint. Write actions retain native Owner authorization and sealed execution admission."
		}, {
			command: "stackkit status"
			purpose: "Read the applied Marketplace runtime and custody through the current resolved Plan."
		}, {
			command: "stackkit verify"
			purpose: "Verify the admitted Marketplace runtime using the exact compiler and resolved Plan binding."
		}, {
			command: "stackkit service"
			purpose: "Run native Marketplace start, stop, restart or bounded logs with the current Owner authorization."
		}, {
			command: "stackkit backup"
			purpose: "Use the native Kopia snapshot, staged restore and activation contracts; keep crash-consistent volume evidence distinct from application-consistent backup."
		}, {
			command: "stackkit remove"
			purpose: "Remove only the admitted own-host Marketplace resources. Data remains by default; explicit deletion requires the canonical signed removal contract."
		}]
		configBaseline: {
			status: "omitted"
			reason: "The native executor renders the digest-pinned Marketplace artifacts and private custody from the resolved Plan. This package declares no competing product configuration file."
		}
	}
}
