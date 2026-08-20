// Package ai defines the Private AI use case package.
//
// SK-M1 records the committed product intent only. Ollama and Open WebUI
// modules, runtime workload, and lifecycle evidence belong to SK-M5.
package ai

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "ai"
		useCaseRef:  "ai"
		displayName: "Private AI"
		version:     "0.1.0"
		layer:       "application"
		category:    "ai"
		lifecycle:   "draft"
		description: "Draft private AI intent for local model serving through Ollama and a supporting Open WebUI, pending SK-M5 module and runtime authority."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "ollama"
			role:       "primary"
			required:   true
			rationale:  "Ollama is the committed SK-M5 default, but its module and executable workload do not exist yet."
			capabilities: ["model-serving", "local-inference"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "planned-self-hosted-ai"
	runtimeProfiles: "planned-self-hosted-ai": {
		displayName: "Planned Self-hosted Private AI"
		description: "SK-M5 will realize Ollama and Open WebUI on an Owner-selected GPU-capable node through the module facts pipeline."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		contexts: ["local", "cloud"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["This draft profile is catalog intent, not an executable module, generated workload, or runtime receipt."]
	}

	tools: {
		ollama: {
			moduleSlug: "ollama"
			role:       "primary"
			required:   true
			rationale:  "Planned local model-serving implementation; module contract pending in SK-M5."
			capabilities: ["model-serving", "local-inference"]
		}
		"open-webui": {
			moduleSlug: "open-webui"
			role:       "supporting"
			required:   true
			rationale:  "Planned user-facing interface for the Ollama runtime; module contract pending in SK-M5."
			capabilities: ["chat-interface", "model-selection"]
		}
	}
}
