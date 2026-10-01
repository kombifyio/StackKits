package architecturev2

import (
	"encoding/json"
	"fmt"

	cueapi "cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

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
	if s == nil || s.authority == nil {
		return fmt.Errorf("initialized CUE authority is required for cache selection")
	}
	var spec struct {
		Modules map[string]struct {
			Enabled bool `json:"enabled"`
		} `json:"modules"`
		Workloads map[string]struct {
			Alternative string `json:"alternative"`
		} `json:"workloads"`
	}
	if err := json.Unmarshal(canonical, &spec); err != nil {
		return err
	}
	if !spec.Modules[moduleID].Enabled {
		return fmt.Errorf("neutral cache module %s must be enabled", moduleID)
	}
	for _, contract := range s.authority.catalog.Workloads {
		metadata, _ := contract["metadata"].(map[string]any)
		id, _ := metadata["id"].(string)
		selected, ok := spec.Workloads[id]
		if !ok {
			continue
		}
		alternatives, _ := contract["alternatives"].([]any)
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			if alternative["id"] == selected.Alternative && alternative["moduleRef"] == moduleID {
				return nil
			}
		}
	}
	return fmt.Errorf("neutral cache module %s is not the module of a selected workload alternative", moduleID)
}
