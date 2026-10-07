// Package game defines the Game Server use case (ADR-0043, ADR-0048).
//
// Calagopus is the recommended platform; Pelican and Pterodactyl are
// selectable alternatives. StackKits installs and bootstraps the Panel and the
// Wings node, and the owner-approved setup action creates curated game
// servers. Wings alone owns the game-server containers.
package game

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "game"
		useCaseRef:  "game"
		displayName: "Game Server"
		version:     "0.3.0"
		layer:       "application"
		category:    "game"
		lifecycle:   "experimental"
		description: "Game servers for friends and family through Calagopus (or Pelican or Pterodactyl), with curated Minecraft Java, Paper and Bedrock, Terraria and Valheim profiles and secure defaults."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "calagopus"
			role:       "primary"
			required:   true
			rationale:  "Calagopus is the recommended game platform (ADR-0048): a stable Rust Panel with per-permission API keys plus the Wings daemon that runs each game server in its own container."
			capabilities: ["game-server-hosting", "game-server-management"]
		}
		alternatives: [{
			moduleSlug: "pelican"
			role:       "primary"
			required:   false
			rationale:  "Pelican Panel, the actively developed Pterodactyl successor (upstream beta), with its own Wings."
			capabilities: ["game-server-hosting", "game-server-management"]
		}, {
			moduleSlug: "pterodactyl"
			role:       "primary"
			required:   false
			rationale:  "Pterodactyl, the established platform; existing installations keep it."
			capabilities: ["game-server-hosting", "game-server-management"]
		}]
	}

	defaultRuntimeProfile: "self-hosted-game"
	runtimeProfiles: "self-hosted-game": {
		displayName: "Self-hosted Game Servers"
		description: "The selected Panel with its database and the Wings node daemon run on one owner-selected node through Standalone Compose; Wings publishes each game server's own ports on that node."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: [
			"A home node is reachable from the home network; internet players need a deliberately selected managed VPS or bridge. No port forwarding is created implicitly.",
			"Creating a server requires owner approval and the owner's own acceptance of the game's EULA.",
		]
	}

	computeTiers: {
		low: {included: false, reason: "Game servers need the standard profile and their own memory budget."}
		standard: {included: true, moduleSlug: "calagopus", functions: ["game-server-hosting", "game-server-management"], load: {residency: "on-demand", baseline: "idle-resident", burst: "interactive"}, notes: ["Each running world needs its own memory: about 2 GB for Minecraft Java, 3 GB for Paper, 1.5 GB for Bedrock and Terraria, and 4 GB for Valheim."]}
		high: {included: true, moduleSlug: "calagopus", functions: ["game-server-hosting", "game-server-management"], load: {residency: "on-demand", baseline: "idle-resident", burst: "interactive"}, notes: ["Same platform graph as standard; more worlds fit with more host memory."]}
	}

	tools: {
		calagopus: {
			moduleSlug: "calagopus"
			role:       "primary"
			required:   true
			rationale:  "Digest-pinned Calagopus Panel and Wings with owner bootstrap from custody, scoped API keys and curated game profiles."
			capabilities: ["game-server-hosting", "game-server-management", "rest-api"]
		}
		pelican: {
			moduleSlug: "pelican"
			role:       "primary"
			required:   false
			rationale:  "Digest-pinned Pelican Panel and Wings (upstream beta) with owner bootstrap from custody and curated game profiles."
			capabilities: ["game-server-hosting", "game-server-management", "rest-api"]
		}
		pterodactyl: {
			moduleSlug: "pterodactyl"
			role:       "primary"
			required:   false
			rationale:  "Digest-pinned Pterodactyl Panel and Wings with owner bootstrap from custody and curated game profiles."
			capabilities: ["game-server-hosting", "game-server-management", "rest-api"]
		}
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

	productApis: {
		"calagopus-client": {
			protocol: "rest"
			basePath: "/api/client"
			auth:     "calagopus-client-api-key"
			purpose:  "Routine owner operations on selected game servers with a key restricted to server read, power, console and files."
		}
		"calagopus-admin": {
			protocol: "rest"
			basePath: "/api/admin"
			auth:     "calagopus-setup-api-key"
			purpose:  "Administrative provisioning by the owner-approved setup action only; never handed to a conversational agent."
		}
		"pelican-client": {
			protocol: "rest"
			basePath: "/api/client"
			auth:     "pelican-client-api-key"
			purpose:  "Routine owner operations on selected game servers: state, resources, power, allow list and bounded file edits."
		}
		"pelican-application": {
			protocol: "rest"
			basePath: "/api/application"
			auth:     "pelican-application-api-key"
			purpose:  "Administrative provisioning by the owner-approved setup action only; never handed to a conversational agent."
		}
		"pterodactyl-client": {
			protocol: "rest"
			basePath: "/api/client"
			auth:     "pterodactyl-client-api-key"
			purpose:  "Routine owner operations on selected game servers: state, resources, power, allow list and bounded file edits."
		}
		"pterodactyl-application": {
			protocol: "rest"
			basePath: "/api/application"
			auth:     "pterodactyl-application-api-key"
			purpose:  "Administrative provisioning by the owner-approved setup action only; never handed to a conversational agent."
		}
	}

	setup: {
		defaultPolicy: "on_demand"
		drops: [{
			name:        "game-server"
			policy:      "on_demand"
			description: "Create a curated game server (Minecraft Java, Paper or Bedrock, Terraria or Valheim) with secure defaults after the owner approves and accepts the game's EULA."
		}]
	}

	evidence: {
		healthChecks: ["calagopus-panel-http", "pelican-panel-http", "pterodactyl-panel-http"]
		required: ["route", "backup", "owner-bootstrap", "runtime-owner", "removal"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle & {
		stages: setup: {}
	}

	agentSurface: {
		equipPolicy: "on-generate"
		lifecycleMcp: {}
		productMcps: []
		apis: [{
			id:       "calagopus-client"
			protocol: "rest"
			purpose:  "Scoped owner operations on selected game servers. Calagopus has no native product MCP; the admin API stays with the setup action."
			auth:     "calagopus-client-api-key"
		}, {
			id:       "pelican-client"
			protocol: "rest"
			purpose:  "Scoped owner operations on selected game servers. Pelican has no native product MCP; the Application API stays with the setup action."
			auth:     "pelican-client-api-key"
		}, {
			id:       "pterodactyl-client"
			protocol: "rest"
			purpose:  "Scoped owner operations on selected game servers. Pterodactyl has no native product MCP; the Application API stays with the setup action."
			auth:     "pterodactyl-client-api-key"
		}]
		skills: [{
			id:       "game-server"
			audience: "product-user"
			source:   "stackkits"
			path:     "use-cases/game/agent/game-server/SKILL.md"
		}]
		cliHelpers: [{
			command: "stackkit setup game"
			purpose: "Create a curated game server after owner approval and EULA acceptance."
		}, {
			command: "stackkit game list"
			purpose: "List the owner's game servers with state and port."
		}, {
			command: "stackkit game power"
			purpose: "Start, stop or restart one game server after owner approval."
		}, {
			command: "stackkit game allow"
			purpose: "Admit one player on an allow-list server after owner approval."
		}, {
			command: "stackkit agent mcp-config"
			purpose: "Print the stackkit lifecycle MCP client connection."
		}]
		configBaseline: {
			status: "omitted"
			reason: "Game servers are configured through the selected Panel's APIs by the setup action; StackKits does not author a separate game configuration file."
		}
	}
}
