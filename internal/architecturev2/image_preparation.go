package architecturev2

import (
	"encoding/json"
	"fmt"
	"sort"

	cueapi "cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/imagepreparation"
)

// NeutralImageAuthority supplies the existing verified CUE projection to the
// artifact transport. It adds no alternate catalog or installation authority.
type NeutralImageAuthority struct{}

func (NeutralImageAuthority) NeutralImageProfile(id string) ([]byte, error) {
	return NeutralImageProfile(id)
}

func (NeutralImageAuthority) ValidateNeutralImageManifest(raw []byte) error {
	return ValidateNeutralImageManifest(raw)
}

// NeutralImageProfile returns the CUE-generated core cache projection from the
// verified embedded authority. The caller never supplies catalog facts.
func NeutralImageProfile(id string) ([]byte, error) {
	manifest, err := readEmbeddedManifest()
	if err != nil {
		return nil, err
	}
	if err = verifyEmbeddedSourceHashesForRoot(embeddedProductBundleRoot, manifest); err != nil {
		return nil, err
	}
	if err = verifyEmbeddedDocumentHashesForRoot(embeddedProductBundleRoot, manifest); err != nil {
		return nil, err
	}
	document, err := readEmbeddedDocumentForRoot(embeddedProductBundleRoot, manifest.Documents["neutralImageProfiles"])
	if err != nil {
		return nil, err
	}
	profile, ok := document[id]
	if !ok {
		return nil, fmt.Errorf("unknown neutral image profile %q", id)
	}
	return json.Marshal(profile)
}

// ValidateNeutralImageManifest validates untrusted metadata against the exact
// schema embedded in this CLI. Admission additionally compares trusted pins.
func ValidateNeutralImageManifest(raw []byte) error {
	manifest, err := readEmbeddedManifest()
	if err != nil {
		return err
	}
	if err = verifyEmbeddedSourceHashesForRoot(embeddedProductBundleRoot, manifest); err != nil {
		return err
	}
	source, err := embeddedBundleFS.ReadFile(embeddedProductBundleRoot + "/foundation/image_preparation.cue")
	if err != nil {
		return err
	}
	// This source also refers to the catalog to build its projection. Supplying
	// the verified generated catalog keeps both schema and projection closed.
	catalog, err := embeddedBundleFS.ReadFile(embeddedProductBundleRoot + "/catalog.json")
	if err != nil {
		return err
	}
	ctx := cuecontext.New()
	scope := ctx.CompileBytes(append(append(source, []byte("\nArchitectureV2Catalog: ")...), catalog...))
	return scope.LookupPath(cueapi.ParsePath("#NeutralImageManifest")).Unify(ctx.CompileBytes(raw)).Validate(cueapi.Concrete(true))
}

// RequireSelectedWorkloadModule binds cache coverage to actual canonical
// workload selection, not to an unused module override in raw candidate intent.
// The same service-owned CUE catalog that validates init owns every moduleRef.
func (s *Service) RequireSelectedWorkloadModule(canonical []byte, moduleID string) error {
	spec, selected, err := s.selectedImageWorkloadModules(canonical)
	if err != nil {
		return err
	}
	return requireSelectedImageWorkloadModule(spec, selected, moduleID)
}

type imageProfileModuleIntent struct {
	Enabled            bool   `json:"enabled"`
	ComputeProfile     string `json:"computeProfile"`
	AcceleratorProfile string `json:"acceleratorProfile"`
}

type imageProfileWorkloadIntent struct {
	Alternative string         `json:"alternative"`
	Settings    map[string]any `json:"settings"`
}

type imageProfileSpec struct {
	DataProfile string                                `json:"dataProfile"`
	Modules     map[string]imageProfileModuleIntent   `json:"modules"`
	Workloads   map[string]imageProfileWorkloadIntent `json:"workloads"`
}

func (s *Service) selectedImageWorkloadModules(canonical []byte) (imageProfileSpec, map[string]imageProfileWorkloadIntent, error) {
	if s == nil || s.authority == nil {
		return imageProfileSpec{}, nil, fmt.Errorf("initialized CUE authority is required for cache selection")
	}
	var spec imageProfileSpec
	if err := json.Unmarshal(canonical, &spec); err != nil {
		return imageProfileSpec{}, nil, err
	}
	selected := map[string]imageProfileWorkloadIntent{}
	for _, contract := range s.authority.catalog.Workloads {
		metadata, _ := contract["metadata"].(map[string]any)
		id, _ := metadata["id"].(string)
		workload, ok := spec.Workloads[id]
		if !ok {
			continue
		}
		alternatives, _ := contract["alternatives"].([]any)
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			if alternative["id"] == workload.Alternative {
				moduleID, _ := alternative["moduleRef"].(string)
				selected[moduleID] = workload
			}
		}
	}
	return spec, selected, nil
}

func requireSelectedImageWorkloadModule(spec imageProfileSpec, selected map[string]imageProfileWorkloadIntent, moduleID string) error {
	if !spec.Modules[moduleID].Enabled {
		return fmt.Errorf("neutral cache module %s must be enabled", moduleID)
	}
	if _, exists := selected[moduleID]; exists {
		return nil
	}
	return fmt.Errorf("neutral cache module %s is not the module of a selected workload alternative", moduleID)
}

// RequireSelectedImageProfile admits the complete Full Local workload closure
// against the trusted CUE image profile. It does not import assets, resolve host
// inventory or change intent. Standard retains its existing core-only check.
func (s *Service) RequireSelectedImageProfile(canonical []byte, profile imagepreparation.Profile) error {
	if profile.DataProfile != "full-local" {
		return s.RequireSelectedWorkloadModule(canonical, profile.Module)
	}
	spec, selected, err := s.selectedImageWorkloadModules(canonical)
	if err != nil {
		return err
	}
	if spec.DataProfile != profile.DataProfile || len(profile.Modules) == 0 {
		return fmt.Errorf("Full Local cache requires matching canonical dataProfile and a declared module closure")
	}
	allowed := map[string]bool{}
	for _, moduleID := range profile.Modules {
		if err := requireSelectedImageWorkloadModule(spec, selected, moduleID); err != nil {
			return err
		}
		allowed[moduleID] = true
	}
	images := map[imagepreparation.Image]bool{}
	for _, image := range profile.Images {
		images[image] = true
	}
	models := map[string]architecturev2renderer.OfflineModelPin{}
	for _, pack := range profile.ModelPacks {
		pin, err := architecturev2renderer.OfflinePrivateAIModel(pack.Component, pack.Size)
		if err != nil {
			return err
		}
		models[pack.Component] = pin
	}
	var moduleIDs []string
	for moduleID := range selected {
		moduleIDs = append(moduleIDs, moduleID)
	}
	sort.Strings(moduleIDs)
	usedModels := map[string]bool{}
	for _, moduleID := range moduleIDs {
		if !allowed[moduleID] {
			return fmt.Errorf("selected workload module %s is outside the preinstalled profile closure", moduleID)
		}
		intent := spec.Modules[moduleID]
		if intent.AcceleratorProfile != "" {
			return fmt.Errorf("preinstalled profile has no accelerator closure for %s", moduleID)
		}
		var module map[string]any
		for _, contract := range s.authority.catalog.Modules {
			metadata, _ := contract["metadata"].(map[string]any)
			if metadata["id"] == moduleID {
				module = contract
				break
			}
		}
		components, err := selectedImageProfileComponents(module, intent.ComputeProfile)
		if err != nil {
			return fmt.Errorf("preinstalled module %s: %w", moduleID, err)
		}
		for _, component := range components {
			id, _ := component["id"].(string)
			if setting, _ := component["enabledBySetting"].(string); setting != "" {
				if selected[moduleID].Settings[setting] != true {
					continue
				}
			}
			image, _ := component["image"].(map[string]any)
			ref, _ := image["ref"].(string)
			digest, _ := image["digest"].(string)
			if pin, model := models[id]; model {
				if selected[moduleID].Settings["model-size"] != pin.Size {
					return fmt.Errorf("selected model %s size differs from preinstalled profile size %s", id, pin.Size)
				}
				if digest != pin.ImporterImageDigest || !profileHasImageBytes(profile.Images, ref, digest) {
					return fmt.Errorf("selected model %s importer image is outside the preinstalled profile closure", id)
				}
				usedModels[id] = true
				continue
			}
			if !images[imagepreparation.Image{Component: id, Ref: ref, Digest: digest}] {
				return fmt.Errorf("selected component %s image is outside the preinstalled profile closure", id)
			}
		}
	}
	for _, pack := range profile.ModelPacks {
		if !usedModels[pack.Component] {
			return fmt.Errorf("preinstalled model pack %s/%s must be selected", pack.Component, pack.Size)
		}
	}
	return nil
}

func selectedImageProfileComponents(module map[string]any, computeProfile string) ([]map[string]any, error) {
	runtime, _ := module["runtime"].(map[string]any)
	rawComponents, _ := runtime["components"].([]any)
	if len(rawComponents) == 0 {
		return nil, fmt.Errorf("selected module has no catalog runtime component closure")
	}
	var included map[string]bool
	if profiles, hasProfiles := module["computeProfiles"].(map[string]any); hasProfiles {
		if computeProfile == "" {
			computeProfile, _ = module["defaultComputeProfile"].(string)
		}
		body, _ := profiles[computeProfile].(map[string]any)
		refs, _ := body["components"].([]any)
		if len(refs) == 0 {
			return nil, fmt.Errorf("selected compute profile %s has no catalog component closure", computeProfile)
		}
		included = map[string]bool{}
		for _, raw := range refs {
			id, _ := raw.(string)
			included[id] = true
		}
	}
	var components []map[string]any
	for _, raw := range rawComponents {
		component, _ := raw.(map[string]any)
		id, _ := component["id"].(string)
		if included == nil || included[id] {
			components = append(components, component)
		}
	}
	return components, nil
}

func profileHasImageBytes(images []imagepreparation.Image, ref, digest string) bool {
	for _, image := range images {
		if image.Ref == ref && image.Digest == digest {
			return true
		}
	}
	return false
}
