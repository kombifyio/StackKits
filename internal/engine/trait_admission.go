package engine

import "slices"

// Trait names of #AppModuleV3Traits (plus the governed stop behavior), as
// the engine admits them per module.
const (
	TraitHostNetwork          = "hostNetwork"
	TraitLANListeners         = "lanListeners"
	TraitPublishedPorts       = "publishedPorts"
	TraitDevicePassthrough    = "devicePassthrough"
	TraitAccelerator          = "accelerator"
	TraitSandboxRuntime       = "sandboxRuntime"
	TraitPeerNetworks         = "peerNetworks"
	TraitBundledPlugin        = "bundledPlugin"
	TraitDockerLifecycleOwner = "dockerLifecycleOwner"
	TraitOwnHostSSH           = "ownHostSSH"
	TraitStopSignal           = "stopSignal"
	TraitInit                 = "init"
)

// traitAdmission lists, per trait, the only module components that may
// declare it. It mirrors the governed tables of the legacy bundle renderers
// (architecturev2renderer workload_lan_rights.go, workload_accelerators.go,
// workload_sandbox_runtime.go, workload_peer_networks.go,
// workload_stop_behavior.go, game_node.go, jellyfin_sso.go,
// home_assistant_oidc.go) and the standalone Compose owner's host bindings;
// architecturev2renderer/engine_trait_admission_test.go keeps both in step
// until Phase 2 deletes the legacy tables.
var traitAdmission = map[string]map[string][]string{
	TraitHostNetwork: {"stackkits-speechkit-runtime": {"speechkit-discovery"}},
	TraitLANListeners: {
		"stackkits-adguard-home-runtime": {"adguard-home"},
		"stackkits-mosquitto-runtime":    {"mosquitto"},
		"stackkits-speechkit-runtime":    {"speechkit"},
	},
	TraitDevicePassthrough: {"stackkits-zigbee2mqtt-runtime": {"zigbee2mqtt"}},
	// ADR-0046 mail-node rights: ports published on every host address.
	TraitPublishedPorts: {"stackkits-stalwart-runtime": {"stalwart"}},
	TraitAccelerator: {
		"stackkits-private-ai-runtime":  {"ollama"},
		"stackkits-anythingllm-runtime": {"ollama"},
		"stackkits-comfyui-runtime":     {"comfyui"},
	},
	TraitSandboxRuntime: {
		"stackkits-openhands-runtime": {"openhands"},
		"stackkits-paperclip-runtime": {"paperclip"},
	},
	TraitPeerNetworks: {
		"stackkits-mosquitto-runtime":           {"mosquitto"},
		"stackkits-immich-public-proxy-runtime": {"immich-public-proxy"},
		"stackkits-immich-kiosk-runtime":        {"immich-kiosk"},
		"stackkits-immich-power-tools-runtime":  {"immich-power-tools"},
		"stackkits-searxng-runtime":             {"searxng"},
		"stackkits-tika-runtime":                {"tika"},
		"stackkits-docling-runtime":             {"docling"},
		"stackkits-litellm-runtime":             {"litellm"},
		"stackkits-qdrant-runtime":              {"qdrant"},
		"stackkits-comfyui-runtime":             {"comfyui"},
		"stackkits-openhands-runtime":           {"openhands"},
		"stackkits-paperclip-runtime":           {"paperclip"},
		"stackkits-hermes-runtime":              {"hermes"},
		"stackkits-speechkit-runtime":           {"speechkit"},
	},
	TraitBundledPlugin: {
		"stackkits-home-assistant-runtime": {"home-assistant"},
		"stackkits-jellyfin-runtime":       {"jellyfin"},
	},
	// ADR-0043 game node: only Wings owns the Docker lifecycle.
	TraitDockerLifecycleOwner: {
		"stackkits-pterodactyl-runtime": {"wings"},
		"stackkits-pelican-runtime":     {"wings"},
		"stackkits-calagopus-runtime":   {"wings"},
	},
	// The Marketplace Coolify manager owns host SSH to its own node.
	TraitOwnHostSSH: {"stackkits-marketplace-coolify-runtime": {"coolify"}},
	TraitStopSignal: {
		"stackkits-comfyui-runtime":     {"comfyui"},
		"stackkits-roundcube-runtime":   {"roundcube"},
		"stackkits-pterodactyl-runtime": {"panel", "panel-bootstrap"},
		"stackkits-pelican-runtime":     {"panel", "panel-bootstrap"},
	},
	TraitInit: {
		"stackkits-anythingllm-runtime":    {"anythingllm"},
		"stackkits-workbench-host-runtime": {"workbench-host"},
	},
}

// TraitAdmitted reports whether moduleRef's component may declare trait.
func TraitAdmitted(trait, moduleRef, componentID string) bool {
	return slices.Contains(traitAdmission[trait][moduleRef], componentID)
}

// TraitAdmissions returns a copy of the admission table of one trait.
func TraitAdmissions(trait string) map[string][]string {
	result := make(map[string][]string, len(traitAdmission[trait]))
	for module, components := range traitAdmission[trait] {
		result[module] = append([]string(nil), components...)
	}
	return result
}
