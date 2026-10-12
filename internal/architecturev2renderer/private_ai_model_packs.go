package architecturev2renderer

import (
	"encoding/json"
	"slices"
	"sort"
)

// Private AI model packs: the use case settings model-pack-general,
// model-pack-coding and model-pack-vision choose which models Apply pulls into
// the node's Ollama, model-size chooses their size. Each selected pack renders
// one one-shot component that pulls exactly one tag from the reviewed table
// below and verifies it in Ollama's model list; no setting means no download.
const (
	privateAIModelSizeSetting = "model-size"
	privateAIModelSizeDefault = "small"
)

// privateAIModelPack is one model pack: its workload setting and the
// component that pulls it.
type privateAIModelPack struct {
	Setting   string
	Component string
}

// privateAIModelPacks lists the packs in a stable order.
var privateAIModelPacks = []privateAIModelPack{
	{Setting: "model-pack-general", Component: "ollama-model-general"},
	{Setting: "model-pack-coding", Component: "ollama-model-coding"},
	{Setting: "model-pack-vision", Component: "ollama-model-vision"},
}

// privateAIModelTags is the one reviewed model table (2026-10-11): pack
// component -> model-size -> Ollama library tag. Every model is Apache-2.0.
// small fits about 4 GB of memory, medium about 8 GB (both fit the 10 GB
// Ollama cap on the CPU), large needs 16 GB or more and in practice a GPU.
// general follows the plan's general-assistant model qwen3.5:9b (Hermes and
// OpenHands default); docs/use-case-expansion/ai-agents.md names the family.
// Tags are upstream names, not digests: the pull verifies presence in
// /api/tags, not a manifest digest.
var privateAIModelTags = map[string]map[string]string{
	"ollama-model-general": {"small": "qwen3.5:4b", "medium": "qwen3.5:9b", "large": "qwen3.5:27b"},
	"ollama-model-coding":  {"small": "qwen2.5-coder:1.5b", "medium": "qwen2.5-coder:7b", "large": "qwen3-coder:30b"},
	"ollama-model-vision":  {"small": "qwen3-vl:4b", "medium": "qwen3-vl:8b", "large": "qwen3-vl:32b"},
}

// privateAIModelSelection maps each selected pack component to its tag.
func privateAIModelSelection(settings map[string]json.RawMessage, path string) (map[string]string, error) {
	size := privateAIModelSizeDefault
	if raw, set := settings[privateAIModelSizeSetting]; set {
		if err := json.Unmarshal(raw, &size); err != nil || privateAIModelTags["ollama-model-general"][size] == "" {
			return nil, fail(ErrInvalidPlan, path+"."+privateAIModelSizeSetting, "must be small, medium or large")
		}
	}
	selection := map[string]string{}
	for _, pack := range privateAIModelPacks {
		raw, set := settings[pack.Setting]
		if !set {
			continue
		}
		var on bool
		if err := json.Unmarshal(raw, &on); err != nil {
			return nil, fail(ErrInvalidPlan, path+"."+pack.Setting, "must be a boolean")
		}
		if on {
			selection[pack.Component] = privateAIModelTags[pack.Component][size]
		}
	}
	return selection, nil
}

func privateAIModelPackComponent(id string) bool {
	_, known := privateAIModelTags[id]
	return known
}

// privateAIModelPullCommand is the governed pull script and its $0, as the
// catalog declares it; the renderer appends the model tag as $1.
func privateAIModelPullCommand() ([]string, bool) {
	var command []string
	return command, json.Unmarshal([]byte(ollamaModelPullCommandJSON), &command) == nil && len(command) == 2
}

// validPrivateAIModelPull admits only the governed pull: the pinned Ollama
// client with the reviewed script, one reviewed tag of its own pack, on the
// internal network after Ollama, with no egress, data, secret or host right.
func validPrivateAIModelPull(c selectedPaaSRuntimeComponent) bool {
	governed, ok := privateAIModelPullCommand()
	if !ok || len(c.Command) != len(governed)+1 || !exactStringList(c.Command[:len(governed)], governed) ||
		!slices.Contains(sortedValues(privateAIModelTags[c.ID]), c.Command[len(governed)]) ||
		c.Role != "application" || c.Lifecycle != "one-shot" || c.EnabledBySetting != "" || c.HealthFailure != "" ||
		c.Image.Ref != ollamaImageRef || c.Image.Digest != ollamaImageDigest ||
		ollamaModelPullImageRef != ollamaImageRef || ollamaModelPullImageDigest != ollamaImageDigest ||
		!exactStringList(c.Entrypoint, []string{"/bin/sh", "-c"}) ||
		!exactStringList(c.DependsOn, []string{"ollama"}) || !exactStringList(c.NetworkRefs, []string{"private-ai-internal"}) || c.Egress ||
		len(c.Environment) != 1 || c.Environment["OLLAMA_HOST"] != "http://ollama:11434" ||
		len(c.SecretEnvironment) != 0 || len(c.OwnerEnvironment) != 0 || len(c.CompanionEnvironment) != 0 || len(c.CompanionSecretEnvironment) != 0 ||
		len(c.SecretFiles) != 0 || len(c.RestoreActivationEnvironment) != 0 || len(c.Volumes) != 0 || c.Accelerator != nil ||
		c.Health.Kind != "completion" || c.Resources == nil || c.Resources.MemoryLimit != "256m" || c.Resources.MemoryReservation != "" || c.Resources.CPUs != 0 {
		return false
	}
	return len(c.PublishedPorts) == 0 && len(c.LANListeners) == 0 && c.DevicePassthrough == nil && len(c.Devices) == 0 &&
		len(c.PeerNetworks) == 0 && !c.RouteHostLoopback && c.DockerLifecycleOwner == nil && len(c.RouteHostEnvironment) == 0 && c.AcmeTLSALPNPort == 0 &&
		c.HomeIdentityAccess == nil && c.PocketIDClient == nil && c.JellyfinSSOPlugin == nil && c.HomeAssistantOIDC == nil && c.HomeAssistantNotify == nil &&
		c.StopSignal == "" && !c.Init && c.SandboxRuntime == ""
}

func sortedValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
