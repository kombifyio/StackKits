package architecturev2renderer

import (
	"encoding/json"
	"regexp"
	"slices"
)

// ModuleAccelerator is the selected accelerator profile of a module as bound
// in the ResolvedPlan (modules.<id>.acceleratorProfile and its hash-bound
// profile body). It is the only authority that lets a renderer grant a GPU.
type ModuleAccelerator struct {
	Profile    string
	Vendor     string
	Access     string
	Components []string
	// Images replaces a component image with the vendor build the profile
	// names (for example Ollama's ROCm image).
	Images map[string]ModuleAcceleratorImage
}

// ModuleAcceleratorImage is one CUE-owned, digest-pinned image variant.
type ModuleAcceleratorImage struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

var acceleratorImageDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// parseModuleAccelerator reads the optional selected accelerator profile of a
// resolved module. Absence is the CPU runtime.
func parseModuleAccelerator(object map[string]json.RawMessage, modulePath string) (*ModuleAccelerator, error) {
	rawBinding, bound := object["acceleratorProfileBinding"]
	rawProfile, selected := object["acceleratorProfile"]
	if !bound && !selected {
		return nil, nil
	}
	path := modulePath + ".acceleratorProfileBinding"
	var profile string
	var binding struct {
		Components  []string `json:"components"`
		Accelerator *struct {
			Vendor string                            `json:"vendor"`
			Access string                            `json:"access"`
			Images map[string]ModuleAcceleratorImage `json:"images"`
		} `json:"accelerator"`
	}
	if !bound || !selected || json.Unmarshal(rawProfile, &profile) != nil || profile == "" ||
		json.Unmarshal(rawBinding, &binding) != nil || binding.Accelerator == nil {
		return nil, fail(ErrInvalidPlan, path, "a selected accelerator profile requires its bound device requirement")
	}
	accelerator := binding.Accelerator
	if !(accelerator.Vendor == "nvidia" && accelerator.Access == "cdi") &&
		!(accelerator.Vendor == "amd" && accelerator.Access == "rocm-device-nodes") {
		return nil, fail(ErrInvalidPlan, path+".accelerator", "vendor %q with access %q is not a governed accelerator", accelerator.Vendor, accelerator.Access)
	}
	if len(binding.Components) == 0 {
		return nil, fail(ErrInvalidPlan, path+".components", "an accelerator profile names the components that receive the device")
	}
	for component, image := range accelerator.Images {
		if !slices.Contains(binding.Components, component) || image.Ref == "" || !acceleratorImageDigestPattern.MatchString(image.Digest) {
			return nil, fail(ErrInvalidPlan, path+".accelerator.images", "image variant for %q must be a digest-pinned image of a device component", component)
		}
	}
	return &ModuleAccelerator{
		Profile: profile, Vendor: accelerator.Vendor, Access: accelerator.Access,
		Components: slices.Clone(binding.Components), Images: accelerator.Images,
	}, nil
}
