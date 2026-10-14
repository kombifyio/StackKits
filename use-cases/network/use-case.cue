package network

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {name: "network", useCaseRef: "network", displayName: "Network", version: "0.1.0", layer: "application", category: "network", lifecycle: "experimental", description: "Filter explicitly selected private clients through AdGuard Home; the existing local DNS authority and DHCP remain independent."}
	selection: {role: "optional", defaultTool: {moduleSlug: "adguard-home", role: "primary", required: true, rationale: "AdGuard Home owns filtering; the existing kit resolver owns local names.", capabilities: ["dns-filtering", "selected-client-dns"]}, alternatives: []}
	defaultRuntimeProfile: "home-dns-filter"
	runtimeProfiles: "home-dns-filter": {displayName: "Selected-client DNS filter", description: "A private-client filter beside the existing local DNS resolver.", realization: "oss", placementModes: ["local-only", "standard"], managedServerlessEligible: false, requiresControlPlane: false, requiresLocalBridge: false, notes: ["Clients or an explicit router rule must address DNS port 53535. LAN publication is opt-in; no DHCP, router change, mesh or resolver takeover occurs. The plan re-applies the explicit configuration on each activation; edit client/rule policy in the StackSpec. Query logs and statistics are disabled."]}
	computeTiers: {
		low: {included: true, moduleSlug: "adguard-home", functions: ["dns-filtering", "selected-client-dns"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same DNS/setup recipe; smaller policy allocation. Physical small-device acceptance remains pending."]}
		standard: {included: true, moduleSlug: "adguard-home", functions: ["dns-filtering", "selected-client-dns"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Existing standard RAM allocation; CPU envelope is explicit. Measurements and policy are distinct."]}
		high: {included: true, moduleSlug: "adguard-home", functions: ["dns-filtering", "selected-client-dns"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same DNS/setup recipe; larger policy allocation. No measured high benefit or full V1 support is inferred."]}
	}
	tools: "adguard-home": {moduleSlug: "adguard-home", role: "primary", required: true, rationale: "Digest-pinned upstream application with explicit private clients, node-custodied owner and no query retention.", capabilities: ["dns-filtering", "selected-client-dns"]}
	lifecycle: foundation.#StandardUseCaseLifecycle & {stages: setup: {}}
	evidence: {healthChecks: ["adguard-home-http"], required: ["route", "runtime-owner", "removal"]}
	setup: {defaultPolicy: "on_demand", drops: [{name: "adguard-home-policy-check", policy: "on_demand", description: "Verify installed selected-client filtering and local resolver through the existing custodied owner API. No policy change or client DNS reachability claim."}]}
	agentSurface: {equipPolicy: "on-generate", lifecycleMcp: {}, productMcps: [], apis: [{id: "adguard-home", protocol: "rest", purpose: "Observe the upstream DNS filter using the existing custodied owner.", auth: "custodied-owner-login"}], skills: [{id: "dns-policy", audience: "stack-operator", source: "stackkits", path: "use-cases/network/agent/dns-policy/SKILL.md"}], cliHelpers: [], configBaseline: {status: "omitted", reason: "The canonical module renders explicit client policy and the native owner supplies only the private bcrypt verifier."}}
}
