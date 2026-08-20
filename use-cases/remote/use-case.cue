// Package remote defines the Remote Desktop use case package.
//
// SK-M1 records the committed Apache Guacamole product intent only. The
// module, runtime workload, connection model, and lifecycle evidence belong
// to SK-M5.
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
		lifecycle:   "draft"
		description: "Draft private browser-accessible remote desktop intent centered on Apache Guacamole, pending SK-M5 module and runtime authority."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "guacamole"
			role:       "primary"
			required:   true
			rationale:  "Apache Guacamole is the committed SK-M5 default, but its module and executable workload do not exist yet."
			capabilities: ["browser-remote-access", "remote-session"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "planned-self-hosted-remote"
	runtimeProfiles: "planned-self-hosted-remote": {
		displayName: "Planned Self-hosted Remote Desktop"
		description: "SK-M5 will realize Apache Guacamole on an Owner-selected node through the module facts pipeline."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		contexts: ["local", "cloud"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["This draft profile is catalog intent; guacd, database, connection, authentication, recording, route, and secret contracts remain unselected SK-M5 implementation work."]
	}

	tools: guacamole: {
		moduleSlug: "guacamole"
		role:       "primary"
		required:   true
		rationale:  "Planned browser-based remote-access implementation; module contract pending in SK-M5."
		capabilities: ["browser-remote-access", "remote-session"]
	}
}
