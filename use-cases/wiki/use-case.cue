package wiki

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {name: "wiki", useCaseRef: "wiki", displayName: "Personal IT Wiki", version: "0.1.0", layer: "application", category: "wiki", lifecycle: "draft", description: "The person's IT source of truth on their own server: a SQLite file, browser reader and native MCP. Agents read it before the Board."}
	selection: {role: "optional", defaultTool: {moduleSlug: "wiki", role: "primary", required: true, rationale: "kombify.wiki.v1 is the same format as the cloud Wiki.", capabilities: ["wiki-read", "wiki-search", "wiki-graph"]}, alternatives: []}
	defaultRuntimeProfile: "local-wiki"
	runtimeProfiles: "local-wiki": {displayName: "Local Wiki", description: "Local writer with no account; a node-custodied connection can later select the person's cloud replica.", realization: "oss", placementModes: ["local-only", "standard"], managedServerlessEligible: false, requiresControlPlane: false, requiresLocalBridge: false, notes: ["Off by default. Reader access uses the server's private HTTPS route and a custodied read bearer.", "Replica connection remains empty until the account-scoped Gateway bundle route exists."]}
	computeTiers: {
		low: {included: false, reason: "No measured small-device profile."}
		standard: {included: true, moduleSlug: "wiki", functions: ["wiki-read", "wiki-search", "wiki-graph"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Policy allocation, not a measured capacity claim. linux/amd64 only at the current artifact pin."]}
		high: {included: false, reason: "No separately measured high capability."}
	}
	tools: wiki: {moduleSlug: "wiki", role: "primary", required: true, rationale: "The Wiki binary comes from the existing exact-main knowledge-node Delivery image.", capabilities: ["wiki-read", "wiki-search", "wiki-graph"]}
	connectors: wiki: {kind: "product-api", name: "wiki", owner: "product", endpoint: "/mcp", transport: "streamable-http", auth: "node-custodied-read-bearer", nativeProduct: true}
	lifecycle: foundation.#StandardUseCaseLifecycle & {stages: setup: {}}
	evidence: {healthChecks: ["wiki-http"], required: ["route", "backup", "runtime-owner", "removal"]}
	setup: {defaultPolicy: "on_demand", drops: []}
	journey: {outcome: "Open a local Wiki, read its IT page, back it up and restore the same SQLite data.", invariantData: ["wiki/state/wiki.db"]}
	// The committed cell and the recovery step are declared together, because the
	// recovery validator needs a committed alternative whose backed-up volume
	// (wiki/state) it can check. The committed cell records the catalog-admitted
	// exact image pin on the base home kit; installed-artifact, reader, MCP and
	// backup/restore proof on a rack lab guest remain open.
	committed: [
		{kit: "basement-kit", alternative: "wiki", adapter: "standalone-compose", tier: "standard"},
	]
	recovery: sequence: ["wiki/state"]
	agentSurface: {
		equipPolicy: "on-generate", lifecycleMcp: {}
		productMcps: [{id: "wiki", owner: "product", endpoint: "/mcp", transport: "streamable-http", auth: "node-custodied-read-bearer", generateClientConfig: false, reason: "The server's StackKits MCP registers wiki_read and resolves its read credential locally; no credential reaches a generated client file."}]
		apis: [{id: "wiki", protocol: "rest", purpose: "Read, search and graph the person's Wiki.", auth: "node-custodied-read-bearer"}]
		skills: [{id: "wiki-first", audience: "stack-operator", source: "stackkits", path: "use-cases/wiki/agent/wiki-first/SKILL.md"}], cliHelpers: []
		configBaseline: {status: "omitted", reason: "The native workload owns the closed environment and uses opaque node-custody references."}
	}
}
