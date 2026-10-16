// Package media defines the Media Library use case package.
//
// Native Architecture v2 ships digest-pinned Jellyfin with its own standard or
// high module profile, independently of the Core profile. Only the retained
// v2alpha1 low graph omits Media. This package does not claim the retired
// add-on's unimplemented *arr services.
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
			rationale:  "Jellyfin owns video playback; separately selected Navidrome and Audiobookshelf add music and audiobook libraries."
			capabilities: ["media-server", "video-stream"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-media"
	runtimeProfiles: "self-hosted-media": {
		displayName: "Self-hosted Media Library"
		description: "StackKits deploys digest-pinned Jellyfin through the explicitly selected runtime with its own standard or high module profile, independently of the Core profile. The retained v2alpha1 low graph omits Media."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["Media storage is Owner-custodied; this package does not claim the removed add-on's Sonarr, Radarr, Prowlarr, or Bazarr services."]
	}

	computeTiers: {
		low: {
			included: false
			reason:   "Jellyfin library + transcode is not on the Basement low graph. Needs a lite substitution before it can be included."
		}
		standard: {
			included:   true
			moduleSlug: "jellyfin"
			functions: ["media-server", "video-stream"]
			load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}
			notes: ["Library waits; playback and transcode are the spike. Catalog alternative jellyfin on standard/high."]
		}
		high: {
			included:   true
			moduleSlug: "jellyfin"
			functions: ["media-server", "video-stream"]
			load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}
			notes: ["Same functions as standard until a high-graph media substitution exists."]
		}
	}

	tools: jellyfin: {
		moduleSlug: "jellyfin"
		role:       "primary"
		required:   true
		rationale:  "The opt-in Jellyfin module owns the primary video library; supporting workloads are selected independently."
		capabilities: ["media-server", "video-stream"]
	}
	tools: navidrome: {
		moduleSlug: "navidrome"
		role: "supporting"
		required: false
		rationale: "The separately selected media-music workload streams the owner-custodied music directory through Navidrome."
		capabilities: ["music-stream"]
	}
	tools: audiobookshelf: {
		moduleSlug: "audiobookshelf"
		role: "supporting"
		required: false
		rationale: "The separately selected media-audiobooks workload owns audiobook listening and progress over the owner-custodied library."
		capabilities: ["audiobook-stream", "podcast-library"]
	}

	setup: {
		defaultPolicy: "on_demand"
		drops: [{
			name:        "jellyfin-owner-bootstrap"
			policy:      "on_demand"
			description: "Prepare or verify the native Jellyfin administrator using private owner credentials. Explicit completion closes the startup wizard; library, household and playback choices remain owner-managed."
		}, {
			name: "navidrome-owner-bootstrap"
			policy: "on_demand"
			description: "Prepare or verify the exact Navidrome administrator and its admitted read-only music library, then request the native scan."
		}, {
			name: "audiobookshelf-owner-bootstrap"
			policy: "on_demand"
			description: "Prepare or verify the exact Audiobookshelf administrator, reuse or create the admitted read-only audiobook library, then request the native scan."
		}]
	}

	evidence: {
		healthChecks: ["jellyfin-http"]
		required: ["route", "backup", "runtime-owner", "removal"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle & {
		stages: setup: {}
	}

	agentSurface: {
		equipPolicy: "on-generate"
		lifecycleMcp: {}
		productMcps: []
		skills: [{
			id:       "owner-setup"
			audience: "product-user"
			source:   "stackkits"
			path:     "use-cases/media/agent/owner-setup/SKILL.md"
		}, {id: "music-library", audience: "stack-operator", source: "stackkits", path: "use-cases/media/agent/music-library/SKILL.md"}, {id: "audiobook-library", audience: "stack-operator", source: "stackkits", path: "use-cases/media/agent/audiobook-library/SKILL.md"}]
		apis: [{
			id:       "jellyfin"
			protocol: "rest"
			purpose:  "HTTP health and library access. Jellyfin has no native product MCP."
			auth:     "jellyfin-auth"
		}, {id: "navidrome", protocol: "rest", purpose: "Native administrator/library setup and Subsonic-compatible music playback. No native product MCP.", auth: "navidrome-self-auth"}, {id: "audiobookshelf", protocol: "rest", purpose: "Native administrator/library setup, audiobook streaming and per-user listening progress. No native product MCP.", auth: "audiobookshelf-self-auth"}]
		cliHelpers: [{
			command: "stackkit agent mcp-config"
			purpose: "Print the stackkit lifecycle MCP client connection."
		}]
		configBaseline: {
			status: "omitted"
			reason: "Jellyfin runtime is the digest-pinned selected-PaaS bundle. Library volume is owner-custodied. StackKits does not author a separate Jellyfin configuration file."
		}
	}
}
