// Package ai defines the Private AI use case package.
//
// CPU inference with explicit owner model selection. Live producer evidence remains pending.
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
		lifecycle:   "experimental"
		description: "Private chat with Ollama and Open WebUI using persistent owner data and explicitly downloaded models. CPU runtime is implemented; live producer evidence remains pending."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "ollama"
			role:       "primary"
			required:   true
			rationale:  "Ollama serves owner-selected models on the internal application network."
			capabilities: ["model-serving", "local-inference"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-ai"
	runtimeProfiles: "self-hosted-ai": {
		displayName: "Self-hosted Private AI"
		description: "StackKits realizes Ollama and Open WebUI on one selected node through the existing application adapter. The CPU profile requires 4 cores, 12 GiB RAM and 40 GiB storage; model weights and context may require more."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["No model is downloaded during installation. Sign in with the owner credentials, then explicitly select and download a model in Open WebUI Admin Settings. Model files persist across restarts; chats and owner data are backup sources. GPU inference is opt-in through the nvidia or amd accelerator profile of the runtime module; GPU runtime evidence and live producer proof remain pending."]
	}

	computeTiers: {
		low: {included: false, reason: "CPU inference needs the explicit standard hardware floor."}
		standard: {included: true, moduleSlug: "ollama", functions: ["model-serving", "local-inference", "chat-interface"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["No model is installed automatically. Model weights and context determine additional RAM and disk."]}
		high: {included: true, moduleSlug: "ollama", functions: ["model-serving", "local-inference", "chat-interface"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same CPU runtime as standard. A GPU is a separate accelerator profile, not a compute tier."]}
	}

	tools: {
		ollama: {
			moduleSlug: "ollama"
			role:       "primary"
			required:   true
			rationale:  "Pinned Ollama runtime with a persistent model volume."
			capabilities: ["model-serving", "local-inference"]
		}
		"open-webui": {
			moduleSlug: "open-webui"
			role:       "supporting"
			required:   true
			rationale:  "Pinned Open WebUI with owner provisioning, disabled public signup and persistent chat data."
			capabilities: ["chat-interface", "model-selection"]
		}
		searxng: {
			moduleSlug: "searxng"
			role:       "supporting"
			rationale:  "Web-search capability module: private meta search wired into Open WebUI; stateless and reached only over the private AI network."
			capabilities: ["web-search"]
		}
		tika: {
			moduleSlug: "tika"
			role:       "supporting"
			rationale:  "Document-parsing capability module: text extraction for Open WebUI documents; stateless and without OCR."
			capabilities: ["document-parsing"]
		}
		docling: {
			moduleSlug: "docling"
			role:       "supporting"
			rationale:  "Document-parsing alternative for scanned and complex layouts, with built-in OCR models; stateless."
			capabilities: ["document-parsing"]
		}
		comfyui: {
			moduleSlug: "comfyui"
			role:       "supporting"
			rationale:  "Image-video capability module on an NVIDIA GPU: reviewed workflow templates, no ComfyUI-Manager or custom nodes, a private route behind the kit's login, and owner-approved model downloads; wired into Open WebUI image generation when chat is selected."
			capabilities: ["image-generation", "video-generation"]
		}
	}
	setup: {
		defaultPolicy: "on_demand"
		drops: [{
			name:        "comfyui-model-download"
			policy:      "on_demand"
			description: "Download one reviewed ComfyUI model preset after the owner accepts its license; weights are verified by size and SHA-256, never bundled and excluded from backup."
		}]
	}
	lifecycle: foundation.#StandardUseCaseLifecycle
	evidence: {healthChecks: ["private-ai-http"], required: ["route", "backup", "runtime-owner", "removal"]}
}
