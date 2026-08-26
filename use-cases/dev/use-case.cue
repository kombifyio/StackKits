// Package dev defines the Developer Platform use case package.
//
// SK-M1 records the committed Gitea product intent only. The Gitea module,
// CI tool selection, runtime workload, and lifecycle evidence belong to SK-M5.
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
		lifecycle:   "draft"
		description: "Draft private source-control and developer-collaboration intent centered on Gitea, pending SK-M5 module, CI, and runtime authority."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "gitea"
			role:       "primary"
			required:   true
			rationale:  "Gitea is the committed SK-M5 source-control default, but its module and executable workload do not exist yet."
			capabilities: ["source-control", "git-hosting", "developer-collaboration"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "planned-self-hosted-dev"
	runtimeProfiles: "planned-self-hosted-dev": {
		displayName: "Planned Self-hosted Developer Platform"
		description: "SK-M5 will realize Gitea and a selected CI implementation on an Owner-selected node through the module facts pipeline."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["This draft profile is catalog intent; the removed legacy Woodpecker definitions were never executable module or runtime authority, and CI selection remains SK-M5 work."]
	}

	computeTiers: {
		low: {
			included: false
			reason: "Gitea+CI is not on the Basement low graph."
		}
		standard: {
			included: false
			reason: "SK-M5: Gitea module is not executable. Intended load is always-on idle git host with interactive and batch CI bursts."
		}
		high: {
			included: false
			reason: "Same as standard until SK-M5. CI batch bursts belong on high headroom, not on low."
		}
	}

	tools: gitea: {
		moduleSlug: "gitea"
		role:       "primary"
		required:   true
		rationale:  "Planned source-control implementation; module contract pending in SK-M5."
		capabilities: ["source-control", "git-hosting", "developer-collaboration"]
	}
}
