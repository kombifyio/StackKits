// Package remote defines the Remote Desktop use case package.
//
// Apache Guacamole owns application behavior. This initial runtime uses its
// explicit static XML profile with transient owner authentication and one
// required destination; SSO, households and wider acceptance remain separate.
package remote

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "remote"
		useCaseRef:  "remote"
		displayName: "Remote Desktop"
		version:     "0.1.0"
		layer:       "application"
		category:    "remote"
		lifecycle:   "experimental"
		description: "Browser access to one explicit SSH, RDP or VNC destination through Apache Guacamole and an internal guacd daemon."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "guacamole"
			role:       "primary"
			required:   true
			rationale:  "Apache Guacamole owns browser remote access; the native module renders an explicit destination and node-custodied owner authentication."
			capabilities: ["browser-remote-access", "remote-session"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-remote"
	runtimeProfiles: "self-hosted-remote": {
		displayName: "Self-hosted Remote Desktop"
		description: "Guacamole and guacd run on one selected node through Standalone Compose, with the explicit static XML authentication profile."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["No database, target credential persistence or recordings are selected. SSH requires an exact known_hosts key; the upstream application prompts for destination credentials. SSO and household clients remain pending."]
	}

	computeTiers: {
		low: {included: true, moduleSlug: "guacamole", functions: ["browser-remote-access", "remote-session"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same normal remote-session recipe with a smaller aggregate policy envelope. Physical small-device and whole-case acceptance remain pending."]}
		standard: {included: true, moduleSlug: "guacamole", functions: ["browser-remote-access", "remote-session"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Aggregate Guacamole/guacd demand is measured; declared allocation remains separate. Destination/browser resources belong to the destination/client."]}
		high: {included: true, moduleSlug: "guacamole", functions: ["browser-remote-access", "remote-session"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same normal remote-session recipe with a larger aggregate policy envelope. No named measured high benefit or physical/V1 support is inferred."]}
	}

	tools: guacamole: {
		moduleSlug: "guacamole"
		role:       "primary"
		required:   true
		rationale:  "Digest-pinned Guacamole with required destination intent, an internal guacd daemon and a custodied owner secret."
		capabilities: ["browser-remote-access", "remote-session"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle & {stages: setup: {}}
	evidence: {healthChecks: ["guacamole-http"], required: ["route", "runtime-owner", "removal"]}
	setup: {defaultPolicy: "on_demand", drops: [{name: "guacamole-owner-login", policy: "on_demand", description: "Authenticate the existing custodied owner on the exact applied Remote instance, revoke the temporary session, and record the plan-bound result."}]}
	agentSurface: {
		equipPolicy: "on-generate", lifecycleMcp: {}, productMcps: []
		apis: [{id: "guacamole", protocol: "rest", purpose: "Observe the exact upstream XML owner and revoke the temporary authentication session.", auth: "custodied-owner-login"}]
		skills: [{id: "owner-setup", audience: "stack-operator", source: "stackkits", path: "use-cases/remote/agent/owner-setup/SKILL.md"}]
		cliHelpers: [{command: "stackkit setup remote --owner-approve --json", purpose: "Run the admitted owner login observation on the exact applied Remote workload."}]
		configBaseline: {status: "omitted", reason: "The selected upstream bundle owns the explicit connection and transient authentication; setup observes the existing custodied owner and retains no target credentials."}
	}
}
