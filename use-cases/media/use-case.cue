// Package media defines the Media Library use case package.
//
// The package binds existing Jellyfin intent without claiming the retired
// add-on's unimplemented *arr services or an Architecture v2 lifecycle.
package media

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "media"
		useCaseRef:  "media"
		displayName: "Media Library"
		version:     "0.1.0"
		layer:       "application"
		category:    "media"
		lifecycle:   "experimental"
		description: "Private household media library and streaming intent centered on the existing opt-in Jellyfin module."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "jellyfin"
			role:       "primary"
			required:   true
			rationale:  "Jellyfin is the only shipped media module and already owns the container, route, storage, and health contract."
			capabilities: ["media-server", "video-stream"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-media"
	runtimeProfiles: "self-hosted-media": {
		displayName: "Self-hosted Media Library"
		description: "StackKits records Jellyfin for selected-PaaS delivery on the Owner-selected node; an Architecture v2 workload and application lifecycle remain pending."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		contexts: ["local", "pi", "cloud"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["Media storage is Owner-custodied; this package does not claim the removed add-on's Sonarr, Radarr, Prowlarr, or Bazarr services."]
	}

	tools: jellyfin: {
		moduleSlug: "jellyfin"
		role:       "primary"
		required:   true
		rationale:  "The existing opt-in Jellyfin module is the sole shipped implementation for this package."
		capabilities: ["media-server", "video-stream"]
	}
}
