// Package game defines the Game Server use case (ADR-0043).
//
// Pterodactyl is the owner-decided platform: StackKits installs and
// bootstraps the Panel and the Wings node, and the owner-approved setup action
// creates curated game servers. Wings alone owns the game-server containers.
package game

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "game"
		useCaseRef:  "game"
		displayName: "Game Server"
		version:     "0.2.0"
		layer:       "application"
		category:    "game"
		lifecycle:   "experimental"
		description: "Game servers for friends and family through Pterodactyl, with curated Minecraft Java, Paper and Bedrock, Terraria and Valheim profiles and secure defaults."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "pterodactyl"
			role:       "primary"
			required:   true
			rationale:  "Pterodactyl is the owner-decided game platform: a web Panel for players and worlds plus the Wings daemon that runs each game server in its own container."
			capabilities: ["game-server-hosting", "game-server-management"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-game"
	runtimeProfiles: "self-hosted-game": {
		displayName: "Self-hosted Game Servers"
		description: "Pterodactyl Panel, MariaDB, Valkey and Wings run on one owner-selected node through Standalone Compose; Wings publishes each game server's own ports on that node."
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
		standard: {included: true, moduleSlug: "pterodactyl", functions: ["game-server-hosting", "game-server-management"], load: {residency: "on-demand", baseline: "idle-resident", burst: "interactive"}, notes: ["Each running world needs its own memory: about 2 GB for Minecraft Java, 3 GB for Paper, 1.5 GB for Bedrock and Terraria, and 4 GB for Valheim."]}
		high: {included: true, moduleSlug: "pterodactyl", functions: ["game-server-hosting", "game-server-management"], load: {residency: "on-demand", baseline: "idle-resident", burst: "interactive"}, notes: ["Same platform graph as standard; more worlds fit with more host memory."]}
	}

	tools: pterodactyl: {
		moduleSlug: "pterodactyl"
		role:       "primary"
		required:   true
		rationale:  "Digest-pinned Pterodactyl Panel and Wings with owner bootstrap from custody and curated game profiles."
		capabilities: ["game-server-hosting", "game-server-management", "rest-api"]
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
		healthChecks: ["pterodactyl-panel-http"]
		required: ["route", "backup", "owner-bootstrap", "runtime-owner", "removal"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle & {
		stages: setup: {}
	}

	agentSurface: {
		equipPolicy:  "on-generate"
		lifecycleMcp: {}
		productMcps: []
		apis: [{
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
			reason: "Game servers are configured through the Pterodactyl APIs by the setup action; StackKits does not author a separate game configuration file."
		}
	}
}
