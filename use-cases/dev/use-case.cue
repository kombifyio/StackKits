// Package dev defines the Developer Platform use case package.
//
// Private Git hosting is explicit; CI runner selection remains separate.
package dev

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "dev"
		useCaseRef:  "dev"
		displayName: "Developer Platform"
		version:     "0.1.0"
		layer:       "application"
		category:    "dev"
		lifecycle:   "experimental"
		description: "Private repositories through native Git HTTPS and local owner custody. CI runners are not installed."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "gitea"
			role:       "primary"
			required:   true
			rationale:  "Gitea provides private source control with local account custody."
			capabilities: ["source-control", "git-hosting", "developer-collaboration"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-dev"
	runtimeProfiles: "self-hosted-dev": {
		displayName: "Self-hosted Git Hosting"
		description: "Gitea runs on one owner-selected node through Standalone Compose. SQLite, repositories, LFS data and configuration persist."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["Use the local owner account over the declared HTTPS route. Native Git authentication does not use a browser login gate. SSH and CI runners are separate future selections."]
	}

	computeTiers: {
		low: {included: false, reason: "The first Git hosting path uses the standard profile."}
		standard: {included: true, moduleSlug: "gitea", functions: ["source-control", "git-hosting", "developer-collaboration"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Repository growth requires a separate disk budget. No CI runner is included."]}
		high: {included: true, moduleSlug: "gitea", functions: ["source-control", "git-hosting", "developer-collaboration"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same Git hosting runtime as standard."]}
	}

	tools: gitea: {
		moduleSlug: "gitea"
		role:       "primary"
		required:   true
		rationale:  "Pinned rootless Gitea with native authentication and private repositories."
		capabilities: ["source-control", "git-hosting", "developer-collaboration"]
	}
	lifecycle: foundation.#StandardUseCaseLifecycle
	evidence: {healthChecks: ["gitea-http"], required: ["route", "backup", "runtime-owner", "removal"]}
}
