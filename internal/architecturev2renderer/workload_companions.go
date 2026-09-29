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
// the primary workload's internal network on the same node. Custody lists
// the add-on's opaque references for exactly the slots the primary's catalog
// component binds through companionSecretEnvironment, sorted by slot; absent
// otherwise.
type workloadCompanion struct {
	WorkloadRef    string                     `json:"workloadRef"`
	AlternativeRef string                     `json:"alternativeRef"`
	Custody        []workloadCompanionCustody `json:"custody,omitempty"`
}

// workloadCompanionCustody is one add-on secret slot and its opaque reference.
type workloadCompanionCustody struct {
	Slot string `json:"slot"`
	Ref  string `json:"ref"`
}

// custodyRef returns the companion's opaque reference for a slot.
func (c workloadCompanion) custodyRef(slot string) (string, bool) {
	for _, entry := range c.Custody {
		if entry.Slot == slot {
			return entry.Ref, true
		}
	}
	return "", false
}

// selectedPaaSCompanionEnvironment is one catalog-declared wiring of a
// primary component to a selected add-on alternative.
type selectedPaaSCompanionEnvironment struct {
	WorkloadRef    string            `json:"workloadRef"`
	AlternativeRef string            `json:"alternativeRef"`
	Environment    map[string]string `json:"environment"`
}

// selectedPaaSCompanionSecretEnvironment binds variables of a primary
// component to custody secret slots of a selected add-on alternative. The
// renderer turns each binding into an ordinary secretEnvironment entry whose
// bundle slot (companionSecretSlot) references the add-on's own secret, so
// the value exists only in the executor's private secret environment.
type selectedPaaSCompanionSecretEnvironment struct {
	WorkloadRef       string            `json:"workloadRef"`
	AlternativeRef    string            `json:"alternativeRef"`
	SecretEnvironment map[string]string `json:"secretEnvironment"`
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
		// The assistant talks to Ollama itself; Open WebUI gets no variable.
		{WorkloadRef: "ai-assistant", AlternativeRef: "hermes", Environment: map[string]string{}},
		// The agent harness reaches Ollama on this network and needs no variable.
		{WorkloadRef: "ai-harness", AlternativeRef: "openhands", Environment: map[string]string{}},
		// The control plane reaches Hermes and Ollama on this network itself and needs no variable.
		{WorkloadRef: "ai-control-plane", AlternativeRef: "paperclip", Environment: map[string]string{}},
		// Open WebUI v0.11.3 speech through SpeechKit's OpenAI-compatible audio
		// routes (backend/open_webui/config.py AUDIO_*). The bearer token is a
		// custody secret of the ai-speech workload; companion wiring carries
		// no secret, so AUDIO_*_OPENAI_API_KEY stays a documented gap.
		{WorkloadRef: "ai-speech", AlternativeRef: "speechkit", Environment: map[string]string{
			"AUDIO_STT_ENGINE": "openai", "AUDIO_STT_OPENAI_API_BASE_URL": "http://speechkit:8080/v1", "AUDIO_STT_MODEL": "whisper-1",
			"AUDIO_TTS_ENGINE": "openai", "AUDIO_TTS_OPENAI_API_BASE_URL": "http://speechkit:8080/v1", "AUDIO_TTS_MODEL": "tts-1", "AUDIO_TTS_VOICE": "af_bella",
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
	// AnythingLLM 1.16.2 (server/utils/agents/aibitat/plugins/web-browsing.js):
	// the agent's SearXNG engine reads AGENT_SEARXNG_API_URL and appends
	// q and format=json. ComfyUI coexists on its own route without wiring:
	// AnythingLLM has no image generation. Document parsing has no entry,
	// because AnythingLLM uses its own collector; authoring refuses it.
	anythingLLMWorkloadModuleID: {component: "anythingllm", entries: []selectedPaaSCompanionEnvironment{
		{WorkloadRef: "ai-search", AlternativeRef: "searxng", Environment: map[string]string{
			"AGENT_SEARXNG_API_URL": "http://searxng:8080/search",
		}},
		{WorkloadRef: "ai-image-video", AlternativeRef: "comfyui", Environment: map[string]string{}},
		// The assistant talks to Ollama itself; AnythingLLM gets no variable.
		{WorkloadRef: "ai-assistant", AlternativeRef: "hermes", Environment: map[string]string{}},
		// The agent harness reaches Ollama on this network and needs no variable.
		{WorkloadRef: "ai-harness", AlternativeRef: "openhands", Environment: map[string]string{}},
		// The control plane reaches Hermes and Ollama on this network itself and needs no variable.
		{WorkloadRef: "ai-control-plane", AlternativeRef: "paperclip", Environment: map[string]string{}},
		// AnythingLLM 1.16.2 speech through SpeechKit's OpenAI-compatible audio
		// routes: server/utils/SpeechToText/openAiGeneric (STT_*),
		// TextToSpeech/openAiGeneric (TTS_*) and the collector's
		// GenericOpenAiWhisper for uploaded audio (WHISPER_*). The *_KEY
		// values are the ai-speech custody token the owner enters in Settings.
		{WorkloadRef: "ai-speech", AlternativeRef: "speechkit", Environment: map[string]string{
			"STT_PROVIDER": "generic-openai", "STT_OPEN_AI_COMPATIBLE_ENDPOINT": "http://speechkit:8080/v1", "STT_OPEN_AI_COMPATIBLE_MODEL": "whisper-1",
			"WHISPER_PROVIDER": "generic-openai", "WHISPER_GENERIC_OPEN_AI_BASE_URL": "http://speechkit:8080/v1", "WHISPER_GENERIC_OPEN_AI_MODEL": "whisper-1",
			"TTS_PROVIDER": "generic-openai", "TTS_OPEN_AI_COMPATIBLE_ENDPOINT": "http://speechkit:8080/v1", "TTS_OPEN_AI_COMPATIBLE_MODEL": "tts-1", "TTS_OPEN_AI_COMPATIBLE_VOICE_MODEL": "af_bella",
		}},
	}},
}

// governedCompanionSecretEnvironments lists the only primary components that
// may receive a selected add-on's custody secret, and exactly which slot
// reaches which variable. The catalog declares the binding; the renderer
// materializes it only while the add-on is selected on the node and its
// reference travels with the companion, so an unselected add-on leaves no
// slot, no reference and no variable behind.
var governedCompanionSecretEnvironments = map[string]struct {
	component string
	entries   []selectedPaaSCompanionSecretEnvironment
}{
	// Open WebUI v0.11.3 (backend/open_webui/config.py) authenticates its
	// OpenAI-compatible audio routes with AUDIO_*_OPENAI_API_KEY.
	privateAIWorkloadModuleID: {component: "open-webui", entries: []selectedPaaSCompanionSecretEnvironment{
		{WorkloadRef: "ai-speech", AlternativeRef: "speechkit", SecretEnvironment: map[string]string{
			"AUDIO_STT_OPENAI_API_KEY": "server-token", "AUDIO_TTS_OPENAI_API_KEY": "server-token",
		}},
	}},
	// AnythingLLM 1.16.2 (server/.env.example) generic-openai providers read
	// *_OPEN_AI_COMPATIBLE_KEY and WHISPER_GENERIC_OPEN_AI_API_KEY.
	anythingLLMWorkloadModuleID: {component: "anythingllm", entries: []selectedPaaSCompanionSecretEnvironment{
		{WorkloadRef: "ai-speech", AlternativeRef: "speechkit", SecretEnvironment: map[string]string{
			"STT_OPEN_AI_COMPATIBLE_KEY": "server-token", "TTS_OPEN_AI_COMPATIBLE_KEY": "server-token", "WHISPER_GENERIC_OPEN_AI_API_KEY": "server-token",
		}},
	}},
}

// companionSecretSlot is the bundle secret slot that carries an add-on's
// secret in the primary's bundle; it names the add-on so it can never collide
// with, or be mistaken for, one of the primary's own slots.
func companionSecretSlot(workloadRef, slot string) string {
	return workloadRef + "-" + slot
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
		for entryIndex, entry := range companion.Custody {
			if !contractIDPattern.MatchString(entry.Slot) || !validSecretReference(entry.Ref) ||
				(entryIndex > 0 && companion.Custody[entryIndex-1].Slot >= entry.Slot) {
				return nil, fail(ErrInvalidPlan, path, "workload companion %s carries an invalid or unsorted custody reference", companion.WorkloadRef)
			}
		}
	}
	return companions, nil
}

// validateApplicationDeliveryInputsWithCompanions accepts exactly the
// compiler-owned delivery route and workload companion bindings plus the named
// owner settings of the workload, and returns the raw values of the settings
// the owner set.
func validateApplicationDeliveryInputsWithCompanions(unit RenderUnit, moduleRef, serviceRef string, targetPort int, settings []string, path string) (*applicationDeliveryRoute, []workloadCompanion, map[string]json.RawMessage, error) {
	if !sameStringSet(unit.PublicInputRefs(), append([]string{applicationDeliveryRouteInputRef, workloadCompanionsInputRef}, settings...)) ||
		len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.PlanInputsJSON()) {
		return nil, nil, nil, fail(ErrInvalidPlan, path, "requires only the delivery route, workload companion and declared owner setting inputs")
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
		return nil, nil, nil, fail(ErrInvalidPlan, path+".inputBindings", "delivery route and companion bindings differ from the compiler-owned contract")
	}
	for _, binding := range bindings {
		switch binding.TargetRef {
		case applicationDeliveryRouteInputRef:
			if binding.SourceRef != applicationDeliveryRouteSourceRef || binding.ValueType != applicationDeliveryRouteValueType ||
				binding.Cardinality != applicationDeliveryRouteCardinality || binding.Required ||
				!bytes.Equal(bytes.TrimSpace(binding.DefaultValue), []byte("null")) {
				return nil, nil, nil, fail(ErrInvalidPlan, path+".inputBindings", "delivery route binding identity differs from the compiler-owned contract")
			}
		case workloadCompanionsInputRef:
			if binding.SourceRef != workloadCompanionsSourceRef || binding.ValueType != workloadCompanionsValueType ||
				binding.Cardinality != workloadCompanionsCardinality || binding.Required ||
				!bytes.Equal(bytes.TrimSpace(binding.DefaultValue), []byte("[]")) {
				return nil, nil, nil, fail(ErrInvalidPlan, path+".inputBindings", "workload companion binding identity differs from the compiler-owned contract")
			}
		default:
			return nil, nil, nil, fail(ErrInvalidPlan, path+".inputBindings", "carries an undeclared input binding")
		}
	}
	var values map[string]json.RawMessage
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return nil, nil, nil, wrap(ErrInvalidPlan, path+".values", "decode delivery route, companions and owner settings", err)
	}
	var route *applicationDeliveryRoute
	var rawCompanions json.RawMessage
	settingValues := map[string]json.RawMessage{}
	for key, raw := range values {
		switch {
		case key == applicationDeliveryRouteInputRef:
			if string(bytes.TrimSpace(raw)) == "null" {
				continue
			}
			route = &applicationDeliveryRoute{}
			if err := decodeStrict(raw, route); err != nil {
				return nil, nil, nil, wrap(ErrInvalidPlan, path+".values.delivery-route", "decode exact delivery route", err)
			}
		case key == workloadCompanionsInputRef:
			rawCompanions = raw
		case slices.Contains(settings, key):
			settingValues[key] = raw
		default:
			return nil, nil, nil, fail(ErrInvalidPlan, path+".values", "carries an undeclared input")
		}
	}
	companions, err := decodeWorkloadCompanions(rawCompanions, path+".values.companions")
	if err != nil {
		return nil, nil, nil, err
	}
	if route != nil {
		if err := validateParsedApplicationDeliveryRoute(*route, moduleRef, serviceRef, targetPort, path+".values.delivery-route"); err != nil {
			return nil, nil, nil, err
		}
	}
	return route, companions, settingValues, nil
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

// materializeCompanionSecretEnvironment checks the catalog declaration of a
// component against its governed companion secret bindings and, for every
// selected companion with such a binding, adds its variables to the
// component's secretEnvironment under a companion slot whose reference in
// secretRefs is the companion's own opaque secret reference. It fails when
// the declaration is not the governed one, when a selected companion lacks a
// governed plain wiring, when the companion does not carry the referenced
// slot, or when a variable is already bound.
func materializeCompanionSecretEnvironment(moduleRef string, component *selectedPaaSRuntimeComponent, companions []workloadCompanion, secretRefs map[string]string, path string) error {
	declared := component.CompanionSecretEnvironment
	component.CompanionSecretEnvironment = nil
	if len(declared) == 0 {
		return nil
	}
	rights, governed := governedCompanionSecretEnvironments[moduleRef]
	plain, plainGoverned := governedCompanionEnvironments[moduleRef]
	if !governed || !plainGoverned || rights.component != component.ID || !sameCompanionSecretEnvironments(declared, rights.entries) {
		return fail(ErrInvalidPlan, path+".companionSecretEnvironment", "companion secret wiring is admitted only for its governed component")
	}
	for _, companion := range companions {
		entry, ok := companionSecretEntry(rights.entries, companion)
		if !ok {
			continue
		}
		if _, wired := companionEntry(plain.entries, companion); !wired {
			return fail(ErrInvalidPlan, path+".companionSecretEnvironment", "selected companion %s=%s receives a secret without a governed wiring", companion.WorkloadRef, companion.AlternativeRef)
		}
		if component.SecretEnvironment == nil {
			component.SecretEnvironment = map[string]string{}
		}
		for _, key := range slices.Sorted(maps.Keys(entry.SecretEnvironment)) {
			slot := entry.SecretEnvironment[key]
			ref, carried := companion.custodyRef(slot)
			if !carried {
				return fail(ErrInvalidPlan, path+".companionSecretEnvironment", "selected companion %s does not carry secret slot %s", companion.WorkloadRef, slot)
			}
			_, plainBound := component.Environment[key]
			_, secretBound := component.SecretEnvironment[key]
			if plainBound || secretBound {
				return fail(ErrInvalidPlan, path+".companionSecretEnvironment", "companion secret wiring overrides %s", key)
			}
			bundleSlot := companionSecretSlot(companion.WorkloadRef, slot)
			if existing, exists := secretRefs[bundleSlot]; exists && existing != ref {
				return fail(ErrInvalidPlan, path+".companionSecretEnvironment", "companion secret slot %s is already bound", bundleSlot)
			}
			secretRefs[bundleSlot] = ref
			component.SecretEnvironment[key] = bundleSlot
		}
	}
	return nil
}

// splitCompanionSecretEnvironment separates a rendered secretEnvironment into
// the component's own slots and the governed companion secret bindings it
// carries, returning the companion bundle slots in use. It fails unless every
// companion binding is present completely or not at all, and every variable
// of a binding names that binding's companion slot.
func splitCompanionSecretEnvironment(moduleRef, componentID string, secretEnvironment map[string]string) (map[string]string, []string, error) {
	rights, governed := governedCompanionSecretEnvironments[moduleRef]
	base := maps.Clone(secretEnvironment)
	if !governed || rights.component != componentID {
		return base, nil, nil
	}
	var slots []string
	for _, entry := range rights.entries {
		present := 0
		for key, slot := range entry.SecretEnvironment {
			if base[key] == companionSecretSlot(entry.WorkloadRef, slot) {
				present++
			}
		}
		if present == 0 {
			continue
		}
		if present != len(entry.SecretEnvironment) {
			return nil, nil, fail(ErrInvalidPlan, "secretEnvironment", "carries a partial companion secret wiring")
		}
		for key, slot := range entry.SecretEnvironment {
			delete(base, key)
			bundleSlot := companionSecretSlot(entry.WorkloadRef, slot)
			if !slices.Contains(slots, bundleSlot) {
				slots = append(slots, bundleSlot)
			}
		}
	}
	for _, entry := range rights.entries {
		for key := range entry.SecretEnvironment {
			if _, leftover := base[key]; leftover {
				return nil, nil, fail(ErrInvalidPlan, "secretEnvironment", "carries a conflicting companion secret wiring")
			}
		}
	}
	slices.Sort(slots)
	return base, slots, nil
}

// companionSecretSlotsOf returns the companion bundle slots the governed
// component of a rendered bundle uses, so an exact secretRefs validator can
// require them and nothing else.
func companionSecretSlotsOf(moduleRef string, components []selectedPaaSRuntimeComponent) ([]string, error) {
	for _, component := range components {
		_, slots, err := splitCompanionSecretEnvironment(moduleRef, component.ID, component.SecretEnvironment)
		if err != nil {
			return nil, err
		}
		if len(slots) > 0 {
			return slots, nil
		}
	}
	return nil, nil
}

func companionSecretEntry(entries []selectedPaaSCompanionSecretEnvironment, companion workloadCompanion) (selectedPaaSCompanionSecretEnvironment, bool) {
	for _, entry := range entries {
		if entry.WorkloadRef == companion.WorkloadRef && entry.AlternativeRef == companion.AlternativeRef {
			return entry, true
		}
	}
	return selectedPaaSCompanionSecretEnvironment{}, false
}

func sameCompanionSecretEnvironments(left, right []selectedPaaSCompanionSecretEnvironment) bool {
	key := func(entry selectedPaaSCompanionSecretEnvironment) string {
		return entry.WorkloadRef + "/" + entry.AlternativeRef
	}
	sortedLeft := slices.SortedFunc(slices.Values(left), func(a, b selectedPaaSCompanionSecretEnvironment) int { return compareStrings(key(a), key(b)) })
	sortedRight := slices.SortedFunc(slices.Values(right), func(a, b selectedPaaSCompanionSecretEnvironment) int { return compareStrings(key(a), key(b)) })
	return slices.EqualFunc(sortedLeft, sortedRight, func(a, b selectedPaaSCompanionSecretEnvironment) bool {
		return key(a) == key(b) && maps.Equal(a.SecretEnvironment, b.SecretEnvironment)
	})
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
