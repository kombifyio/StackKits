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
		description: "Private chat with Ollama and Open WebUI (or AnythingLLM as the chat alternative) using persistent owner data and explicitly downloaded models. CPU runtime is implemented; live producer evidence remains pending."
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
		alternatives: [{
			moduleSlug: "anythingllm"
			role:       "primary"
			required:   false
			rationale:  "AnythingLLM replaces Open WebUI as the chat module (ai=anythingllm, capability ai.chat=anythingllm); Ollama and its model volume stay identical."
			capabilities: ["chat-interface", "model-selection"]
		}]
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
		notes: ["No model is downloaded during installation. Sign in with the owner credentials, then explicitly select and download a model in Open WebUI Admin Settings. Model files persist across restarts; chats and owner data are backup sources. GPU inference is opt-in through the nvidia or amd accelerator profile of the runtime module; GPU runtime evidence and live producer proof remain pending.", "With AnythingLLM as the chat module (ai.chat=anythingllm) the owner pulls a chat and an embedding model into Ollama on the node and selects them in AnythingLLM's settings; AnythingLLM has no model download surface of its own."]
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
		anythingllm: {
			moduleSlug: "anythingllm"
			role:       "supporting"
			required:   false
			rationale:  "Pinned AnythingLLM (MIT) as the chat alternative: workspaces with built-in RAG (LanceDB) and agents, owner password from custody, persistent storage volume, telemetry off. It parses documents with its own collector, so the Tika and Docling add-ons are refused with it."
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
		speechkit: {
			moduleSlug: "speechkit"
			role:       "supporting"
			required:   false
			rationale:  "Speech capability module: kombify SpeechKit server with local providers only (whisper.cpp STT, Kokoro TTS, the node's Ollama for Assist) on the private AI network behind its bearer token; wired into the chat module's OpenAI-compatible audio settings. Selection stays refused until a SpeechKit release ships the audio routes in a public image."
			capabilities: ["speech-to-text", "text-to-speech"]
		}
		hermes: {
			moduleSlug: "hermes"
			role:       "supporting"
			rationale:  "Assistant capability module (experimental, one owner): Hermes Agent on the node's local model with persistent memory and skills. StackKits pins the policy floor as Hermes managed scope; it inspects and drafts by default, and messaging, web search, schedules and external writes are toolsets the owner grants. Private dashboard behind the kit's login and its own password."
			capabilities: ["personal-assistant"]
		}
		openhands: {
			moduleSlug: "openhands"
			role:       "supporting"
			rationale:  "Agent-harness capability module: OpenHands runs its shell, editor and automations inside one gVisor-isolated container with its own workspace volume, no host Docker socket and the node's Ollama as model endpoint; a private route behind the kit's login. The host needs the runsc runtime."
			capabilities: ["agent-harness"]
		}
		paperclip: {
			moduleSlug: "paperclip"
			role:       "supporting"
			rationale:  "Agent-control-plane capability module (experimental): Paperclip organizes agents, budgets, approvals and tasks with its own PostgreSQL in one gVisor-isolated bundle, no host Docker socket, no egress and no provider key; budgets default to zero. The installed Hermes is reachable on the private AI network. A private route behind the kit's login and Paperclip's own login. The host needs the runsc runtime."
			capabilities: ["agent-control-plane"]
		}
	}
	setup: {
		defaultPolicy: "on_demand"
		drops: [{
			name:        "comfyui-model-download"
			policy:      "on_demand"
			description: "Download one reviewed ComfyUI model preset after the owner accepts its license; weights are verified by size and SHA-256, never bundled and excluded from backup."
		}, {
			name:        "kombify-ai-connect"
			policy:      "on_demand"
			description: "Connect Ollama to the owner's kombify AI as an own model endpoint with `stackkit setup ai-connect`: the connector token from Companion Studio stays in local custody and an outbound-only connector starts on the next apply; nothing inbound is opened."
		}]
	}
	lifecycle: foundation.#StandardUseCaseLifecycle
	evidence: {healthChecks: ["private-ai-http"], required: ["route", "backup", "runtime-owner", "removal"]}
}
