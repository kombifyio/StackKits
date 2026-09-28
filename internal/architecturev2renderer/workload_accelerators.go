package architecturev2renderer

import (
	"slices"
)

// The narrow exception to "GPUs are never passed" (workload_lan_rights.go):
// a component receives a GPU only when its module's selected accelerator
// profile names it, and only for the modules and components governed here.
// The grant is the vendor's container access, never a raw host device path:
// NVIDIA through a CDI device request, AMD ROCm through /dev/kfd and /dev/dri.
var governedAcceleratorComponents = map[string][]string{
	privateAIWorkloadModuleID:   {"ollama"},
	anythingLLMWorkloadModuleID: {"ollama"},
	comfyUIWorkloadModuleID:     {"comfyui"},
}

// selectedPaaSAccelerator is the GPU grant carried by one bundle component.
type selectedPaaSAccelerator struct {
	Profile string `json:"profile"`
	Vendor  string `json:"vendor"`
	Access  string `json:"access"`
}

// ApplicationDeliveryAccelerator is the validated GPU grant of a component.
type ApplicationDeliveryAccelerator struct {
	Profile string
	Vendor  string
	// Access is "cdi" (NVIDIA, device nvidia.com/gpu=all) or
	// "rocm-device-nodes" (AMD, /dev/kfd and /dev/dri).
	Access string
}

// applyModuleAccelerator grants the selected profile to exactly the governed
// components it names and swaps in a declared image variant. Catalog
// components never carry a grant themselves.
func applyModuleAccelerator(moduleRef string, components []selectedPaaSRuntimeComponent, accelerator ModuleAccelerator, path string) ([]selectedPaaSRuntimeComponent, error) {
	governed := governedAcceleratorComponents[moduleRef]
	for _, component := range accelerator.Components {
		if !slices.Contains(governed, component) {
			return nil, fail(ErrInvalidPlan, path, "accelerator profile %q grants a GPU to %q, which is not a governed accelerator component", accelerator.Profile, component)
		}
	}
	result := slices.Clone(components)
	granted := 0
	for index := range result {
		if result[index].Accelerator != nil {
			return nil, fail(ErrInvalidPlan, path, "catalog components never declare an accelerator grant")
		}
		if !slices.Contains(accelerator.Components, result[index].ID) {
			continue
		}
		result[index].Accelerator = &selectedPaaSAccelerator{Profile: accelerator.Profile, Vendor: accelerator.Vendor, Access: accelerator.Access}
		if image, ok := accelerator.Images[result[index].ID]; ok {
			result[index].Image.Ref, result[index].Image.Digest = image.Ref, image.Digest
		}
		granted++
	}
	if granted != len(accelerator.Components) {
		return nil, fail(ErrInvalidPlan, path, "accelerator profile %q names a component the module does not run", accelerator.Profile)
	}
	return result, nil
}

// parseAccelerator validates the GPU grant a rendered bundle carries.
func parseAccelerator(component selectedPaaSRuntimeComponent, moduleRef, path string) (*ApplicationDeliveryAccelerator, error) {
	grant := component.Accelerator
	if grant == nil {
		return nil, nil
	}
	if !slices.Contains(governedAcceleratorComponents[moduleRef], component.ID) || grant.Profile == "" ||
		!((grant.Vendor == "nvidia" && grant.Access == "cdi") || (grant.Vendor == "amd" && grant.Access == "rocm-device-nodes")) {
		return nil, fail(ErrInvalidPlan, path+".accelerator", "a GPU is granted only to a governed component through its vendor's container access")
	}
	return &ApplicationDeliveryAccelerator{Profile: grant.Profile, Vendor: grant.Vendor, Access: grant.Access}, nil
}
