package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
)

const (
	workloadCompanionsInputRef    = "companions"
	workloadCompanionsSourceRef   = "workloads.companions"
	workloadCompanionsValueType   = "workload-companion-list-v1"
	workloadCompanionsCardinality = "list"
)

// workloadCompanion is one selected add-on workload alternative that joined
// the primary workload's internal network on the same node.
type workloadCompanion struct {
	WorkloadRef    string `json:"workloadRef"`
	AlternativeRef string `json:"alternativeRef"`
}

// selectedPaaSCompanionEnvironment is one catalog-declared wiring of a
// primary component to a selected add-on alternative.
type selectedPaaSCompanionEnvironment struct {
	WorkloadRef    string            `json:"workloadRef"`
	AlternativeRef string            `json:"alternativeRef"`
	Environment    map[string]string `json:"environment"`
}

// openWebUIComfyUIWorkflow is the FLUX.1 schnell text-to-image graph in
// ComfyUI API format that Open WebUI queues; the node map tells Open WebUI
// where to put the model, prompt, size, count, steps and seed.
const (
	openWebUIComfyUIWorkflow      = `{"3":{"class_type":"KSampler","inputs":{"cfg":1,"denoise":1,"latent_image":["5",0],"model":["4",0],"negative":["7",0],"positive":["6",0],"sampler_name":"euler","scheduler":"simple","seed":0,"steps":4}},"4":{"class_type":"CheckpointLoaderSimple","inputs":{"ckpt_name":"flux1-schnell-fp8.safetensors"}},"5":{"class_type":"EmptySD3LatentImage","inputs":{"batch_size":1,"height":1024,"width":1024}},"6":{"class_type":"CLIPTextEncode","inputs":{"clip":["4",1],"text":""}},"7":{"class_type":"CLIPTextEncode","inputs":{"clip":["4",1],"text":""}},"8":{"class_type":"VAEDecode","inputs":{"samples":["3",0],"vae":["4",2]}},"9":{"class_type":"SaveImage","inputs":{"filename_prefix":"open-webui/image","images":["8",0]}}}`
	openWebUIComfyUIWorkflowNodes = `[{"type":"model","key":"ckpt_name","node_ids":["4"]},{"type":"prompt","key":"text","node_ids":["6"]},{"type":"width","key":"width","node_ids":["5"]},{"type":"height","key":"height","node_ids":["5"]},{"type":"n","key":"batch_size","node_ids":["5"]},{"type":"steps","key":"steps","node_ids":["3"]},{"type":"seed","key":"seed","node_ids":["3"]}]`
)

// governedCompanionEnvironments lists the only primary components that may be
// wired to their selected add-ons, and exactly how. The catalog declares the
// wiring; the renderer materializes an entry only while its add-on is selected
// on the node, so an unselected add-on renders nothing.
var governedCompanionEnvironments = map[string]struct {
	component string
	entries   []selectedPaaSCompanionEnvironment
}{
	privateAIWorkloadModuleID: {component: "open-webui", entries: []selectedPaaSCompanionEnvironment{
		{WorkloadRef: "ai-documents", AlternativeRef: "docling", Environment: map[string]string{
			"CONTENT_EXTRACTION_ENGINE": "docling", "DOCLING_SERVER_URL": "http://docling:5001",
		}},
		{WorkloadRef: "ai-documents", AlternativeRef: "tika", Environment: map[string]string{
			"CONTENT_EXTRACTION_ENGINE": "tika", "TIKA_SERVER_URL": "http://tika:9998", "TIKA_SERVER_VERSION": "4",
		}},
		{WorkloadRef: "ai-search", AlternativeRef: "searxng", Environment: map[string]string{
			"ENABLE_WEB_SEARCH": "true", "WEB_SEARCH_ENGINE": "searxng", "SEARXNG_QUERY_URL": "http://searxng:8080/search",
		}},
		// Open WebUI v0.11.3 image generation through ComfyUI. Private AI runs
		// with ENABLE_PERSISTENT_CONFIG=false, so these values apply at every
		// start, also when ComfyUI is added to an existing chat install.
		{WorkloadRef: "ai-image-video", AlternativeRef: "comfyui", Environment: map[string]string{
			"ENABLE_IMAGE_GENERATION": "true", "IMAGE_GENERATION_ENGINE": "comfyui", "COMFYUI_BASE_URL": "http://comfyui:8188",
			"IMAGE_GENERATION_MODEL": "flux1-schnell-fp8.safetensors", "IMAGE_SIZE": "1024x1024", "IMAGE_STEPS": "4",
			"COMFYUI_WORKFLOW": openWebUIComfyUIWorkflow, "COMFYUI_WORKFLOW_NODES": openWebUIComfyUIWorkflowNodes,
		}},
	}},
}

func decodeWorkloadCompanions(raw []byte, path string) ([]workloadCompanion, error) {
	var companions []workloadCompanion
	if err := decodeStrict(raw, &companions); err != nil || companions == nil {
		return nil, fail(ErrInvalidPlan, path, "workload companions must be an exact list")
	}
	for index, companion := range companions {
		if !contractIDPattern.MatchString(companion.WorkloadRef) || !contractIDPattern.MatchString(companion.AlternativeRef) ||
			(index > 0 && companions[index-1].WorkloadRef >= companion.WorkloadRef) {
			return nil, fail(ErrInvalidPlan, path, "workload companions must be unique and sorted by workload")
		}
	}
	return companions, nil
}

// validateApplicationDeliveryInputsWithCompanions accepts exactly the
// compiler-owned delivery route and workload companion bindings.
func validateApplicationDeliveryInputsWithCompanions(unit RenderUnit, moduleRef, serviceRef string, targetPort int, path string) (*applicationDeliveryRoute, []workloadCompanion, error) {
	if !sameStringSet(unit.PublicInputRefs(), []string{applicationDeliveryRouteInputRef, workloadCompanionsInputRef}) ||
		len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.PlanInputsJSON()) {
		return nil, nil, fail(ErrInvalidPlan, path, "requires only the delivery route and workload companion inputs")
	}
	var bindings []struct {
		TargetRef    string          `json:"targetRef"`
		SourceRef    string          `json:"sourceRef"`
		ValueType    string          `json:"valueType"`
		Cardinality  string          `json:"cardinality"`
		Required     bool            `json:"required"`
		DefaultValue json.RawMessage `json:"defaultValue"`
	}
	if err := decodeStrict(unit.InputBindingsJSON(), &bindings); err != nil || len(bindings) != 2 {
		return nil, nil, fail(ErrInvalidPlan, path+".inputBindings", "delivery route and companion bindings differ from the compiler-owned contract")
	}
	for _, binding := range bindings {
		switch binding.TargetRef {
		case applicationDeliveryRouteInputRef:
			if binding.SourceRef != applicationDeliveryRouteSourceRef || binding.ValueType != applicationDeliveryRouteValueType ||
				binding.Cardinality != applicationDeliveryRouteCardinality || binding.Required ||
				!bytes.Equal(bytes.TrimSpace(binding.DefaultValue), []byte("null")) {
				return nil, nil, fail(ErrInvalidPlan, path+".inputBindings", "delivery route binding identity differs from the compiler-owned contract")
			}
		case workloadCompanionsInputRef:
			if binding.SourceRef != workloadCompanionsSourceRef || binding.ValueType != workloadCompanionsValueType ||
				binding.Cardinality != workloadCompanionsCardinality || binding.Required ||
				!bytes.Equal(bytes.TrimSpace(binding.DefaultValue), []byte("[]")) {
				return nil, nil, fail(ErrInvalidPlan, path+".inputBindings", "workload companion binding identity differs from the compiler-owned contract")
			}
		default:
			return nil, nil, fail(ErrInvalidPlan, path+".inputBindings", "carries an undeclared input binding")
		}
	}
	var values struct {
		Route      *applicationDeliveryRoute `json:"delivery-route"`
		Companions json.RawMessage           `json:"companions"`
	}
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return nil, nil, wrap(ErrInvalidPlan, path+".values", "decode delivery route and companions", err)
	}
	companions, err := decodeWorkloadCompanions(values.Companions, path+".values.companions")
	if err != nil {
		return nil, nil, err
	}
	if values.Route != nil {
		if err := validateParsedApplicationDeliveryRoute(*values.Route, moduleRef, serviceRef, targetPort, path+".values.delivery-route"); err != nil {
			return nil, nil, err
		}
	}
	return values.Route, companions, nil
}

// materializeCompanionEnvironment checks the catalog declaration of a
// component against its governed wiring and adds the environment of every
// selected companion. A selected companion without a governed wiring fails.
func materializeCompanionEnvironment(moduleRef string, component *selectedPaaSRuntimeComponent, companions []workloadCompanion, path string) error {
	declared := component.CompanionEnvironment
	component.CompanionEnvironment = nil
	rights, governed := governedCompanionEnvironments[moduleRef]
	if len(declared) == 0 {
		return nil
	}
	if !governed || rights.component != component.ID || !sameCompanionEnvironments(declared, rights.entries) {
		return fail(ErrInvalidPlan, path+".companionEnvironment", "companion wiring is admitted only for its governed component")
	}
	for _, companion := range companions {
		entry, ok := companionEntry(rights.entries, companion)
		if !ok {
			return fail(ErrInvalidPlan, path+".companionEnvironment", "selected companion %s=%s has no governed wiring", companion.WorkloadRef, companion.AlternativeRef)
		}
		if component.Environment == nil {
			component.Environment = map[string]string{}
		}
		for key, value := range entry.Environment {
			if _, exists := component.Environment[key]; exists {
				return fail(ErrInvalidPlan, path+".companionEnvironment", "companion wiring overrides %s", key)
			}
			component.Environment[key] = value
		}
	}
	return nil
}

// splitCompanionEnvironment separates a rendered environment into its base
// variables and the governed companion wirings it carries. It fails unless
// the remainder is exactly the union of wirings of distinct add-on workloads.
func splitCompanionEnvironment(moduleRef, componentID string, environment map[string]string) (map[string]string, error) {
	rights, governed := governedCompanionEnvironments[moduleRef]
	base := maps.Clone(environment)
	if !governed || rights.component != componentID {
		return base, nil
	}
	used := map[string]bool{}
	for _, entry := range rights.entries {
		present := 0
		for key, value := range entry.Environment {
			if base[key] == value {
				present++
			}
		}
		if present != len(entry.Environment) || used[entry.WorkloadRef] {
			continue
		}
		used[entry.WorkloadRef] = true
		for key := range entry.Environment {
			delete(base, key)
		}
	}
	for _, entry := range rights.entries {
		for key := range entry.Environment {
			if _, leftover := base[key]; leftover {
				return nil, fail(ErrInvalidPlan, "environment", "carries a partial or conflicting companion wiring")
			}
		}
	}
	return base, nil
}

func companionEntry(entries []selectedPaaSCompanionEnvironment, companion workloadCompanion) (selectedPaaSCompanionEnvironment, bool) {
	for _, entry := range entries {
		if entry.WorkloadRef == companion.WorkloadRef && entry.AlternativeRef == companion.AlternativeRef {
			return entry, true
		}
	}
	return selectedPaaSCompanionEnvironment{}, false
}

func sameCompanionEnvironments(left, right []selectedPaaSCompanionEnvironment) bool {
	key := func(entry selectedPaaSCompanionEnvironment) string {
		return entry.WorkloadRef + "/" + entry.AlternativeRef
	}
	sortedLeft := slices.SortedFunc(slices.Values(left), func(a, b selectedPaaSCompanionEnvironment) int { return compareStrings(key(a), key(b)) })
	sortedRight := slices.SortedFunc(slices.Values(right), func(a, b selectedPaaSCompanionEnvironment) int { return compareStrings(key(a), key(b)) })
	return slices.EqualFunc(sortedLeft, sortedRight, func(a, b selectedPaaSCompanionEnvironment) bool {
		return key(a) == key(b) && maps.Equal(a.Environment, b.Environment)
	})
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
