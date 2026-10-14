package architecturev2renderer

import "sort"

// PrivateAIModelLicense is the license of every model in the reviewed table
// (privateAIModelTags). The owner types it to accept a pull made through the
// `ollama-models` setup action.
const PrivateAIModelLicense = "Apache-2.0"

// privateAIModelBytes is the declared on-disk size ceiling per reviewed tag
// (2026-10-13), rounded up from the summed layer sizes of the registry.ollama.ai
// manifests (each measured size is below its ceiling) so the disk budget
// check errs toward refusing. A tag without an entry cannot be pulled by hand:
// the budget check fails closed on an unknown size.
var privateAIModelBytes = map[string]int64{
	"qwen3.5:4b": 4e9, "qwen3.5:9b": 7e9, "qwen3.5:27b": 20e9,
	"qwen2.5-coder:1.5b": 1.5e9, "qwen2.5-coder:7b": 5e9, "qwen3-coder:30b": 20e9,
	"qwen3-vl:4b": 4e9, "qwen3-vl:8b": 7e9, "qwen3-vl:32b": 22e9,
}

// PrivateAIReviewedModel is one entry of the reviewed model allow-list.
type PrivateAIReviewedModel struct {
	Tag     string
	Pack    string
	License string
	// Bytes is the declared size ceiling; zero means unknown.
	Bytes int64
}

// PrivateAIReviewedModels returns every reviewed Ollama tag in a stable order.
func PrivateAIReviewedModels() []PrivateAIReviewedModel {
	var models []PrivateAIReviewedModel
	for pack, sizes := range privateAIModelTags {
		for _, tag := range sizes {
			models = append(models, PrivateAIReviewedModel{Tag: tag, Pack: pack, License: PrivateAIModelLicense, Bytes: privateAIModelBytes[tag]})
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Tag < models[j].Tag })
	return models
}

// ResolvePrivateAIReviewedModel returns the reviewed entry for a tag.
func ResolvePrivateAIReviewedModel(tag string) (PrivateAIReviewedModel, bool) {
	for _, model := range PrivateAIReviewedModels() {
		if model.Tag == tag {
			return model, true
		}
	}
	return PrivateAIReviewedModel{}, false
}
