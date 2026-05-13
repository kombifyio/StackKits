// Package aiworkloads - AI/LLM Add-On
//
// Local AI inference stack:
//   - Ollama: LLM inference engine (supports many models)
//   - Open WebUI: ChatGPT-like web interface for Ollama
//
// License:
//   - Ollama: MIT
//   - Open WebUI: BSD-3-Clause
//
// Placement: Local node (GPU required for reasonable performance)
//
// Usage:
//   addons: "ai-workloads": aiworkloads.#Config & {
//       ollama: models: ["llama3.2", "codellama"]
//   }

package aiworkloads

// #Config defines AI workloads add-on configuration
#Config: {
	_addon: {
		name:        "ai-workloads"
		displayName: "AI / LLM"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Ollama + Open WebUI for local AI inference"
	}

	enabled: bool | *true

	// Ollama configuration
	ollama: #OllamaConfig

	// Open WebUI configuration
	openwebui: #OpenWebUIConfig
}

#OllamaConfig: {
	// GPU acceleration
	gpu: {
		enabled: bool | *true
		vendor:  *"nvidia" | "amd" | "intel" | "cpu"
	}

	// Models to pre-pull
	models: [...string] | *["llama3.2"]

	// Model storage path
	modelsPath: string | *"/data/ollama/models"

	// Resource limits
	resources: {
		memory: string | *"8192m"
		cpus:   number | *4.0
		// GPU memory managed by NVIDIA runtime
	}
}

#OpenWebUIConfig: {
	enabled: bool | *true

	// External API keys (optional, for cloud LLMs)
	openaiApiKey?: =~"^secret://"

	// Resource limits
	resources: {
		memory: string | *"512m"
		cpus:   number | *1.0
	}
}

// Service definitions

#OllamaService: {
	name:        "ollama"
	displayName: "Ollama"
	image:       "ollama/ollama:0.5"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 11434, host: 11434, protocol: "tcp", name: "api"},
	]

	volumes: [
		{name: "ollama-data", path: "/root/.ollama", type: "volume"},
	]

	// GPU runtime (NVIDIA)
	runtime: "nvidia"
	gpuCapabilities: ["gpu"]
}

#OpenWebUIService: {
	name:        "open-webui"
	displayName: "Open WebUI"
	image:       "ghcr.io/open-webui/open-webui:main"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 8080, host: 8085, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "open-webui-data", path: "/app/backend/data", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`ai.{{.domain}}`)"
	}

	environment: {
		OLLAMA_BASE_URL: "http://ollama:11434"
	}
}

#Outputs: {
	ollamaUrl:   string | *"http://ollama:11434"
	webUIUrl:    string | *"https://ai.{{.domain}}"
	gpuEnabled:  bool
}
