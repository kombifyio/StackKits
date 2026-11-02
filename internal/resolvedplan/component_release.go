package resolvedplan

import (
	"fmt"
	"strings"
)

// A catalog runtime component may admit further upstream releases beside its
// default image (#ModuleRuntimeComponentV2.releases). A workload selects one
// through its declared `release` setting. The selection is projected into the
// canonical module runtime before it is fingerprinted, so generation, Apply and
// Verify consume one exact image; the plan verifier rebuilds the same runtime
// from the bound workload settings. The release list itself is catalog-only
// data and never enters a resolved plan.
const componentReleaseSetting = "release"

// componentReleaseAdapter is the only runtime adapter whose Apply guard and
// retained-data rollback govern a release change.
const componentReleaseAdapter = "standalone-compose"

func requireComponentReleaseAdapter(workloadID string, workload *resolvedWorkloadSelection) error {
	if _, selected := workload.settings[componentReleaseSetting]; !selected {
		return nil
	}
	if workload.runtimeAdapterID != componentReleaseAdapter {
		return fail(ErrInvalidInput, "spec.workloads."+workloadID+".settings."+componentReleaseSetting,
			"release selection requires the %s runtime adapter", componentReleaseAdapter)
	}
	return nil
}

func projectComponentRelease(moduleID string, rawIntent any, runtime map[string]any) error {
	selected, present, err := intentComponentRelease(moduleID, rawIntent)
	if err != nil {
		return err
	}
	return applyComponentRelease(moduleID, selected, present, runtime)
}

func projectBoundComponentRelease(plan ResolvedPlan, moduleID string, runtime map[string]any) error {
	workloads, err := objectListOptional(map[string]any(plan), "workloads")
	if err != nil {
		return fail(ErrInvalidInput, "resolvedPlan.workloads", "%v", err)
	}
	for _, workload := range workloads {
		alternative, ok := workload["alternative"].(map[string]any)
		if !ok || alternative["moduleRef"] != moduleID {
			continue
		}
		settings, _ := workload["settings"].(map[string]any)
		selected, present, err := intentComponentRelease(moduleID, map[string]any{"settings": settings})
		if err != nil {
			return err
		}
		runtimeAlternative, _ := alternative["runtime"].(map[string]any)
		adapter, _ := runtimeAlternative["adapter"].(map[string]any)
		if present && adapter["id"] != componentReleaseAdapter {
			return fail(ErrInvalidInput, "resolvedPlan.workloads."+fmt.Sprint(workload["id"])+".settings."+componentReleaseSetting,
				"release selection requires the %s runtime adapter", componentReleaseAdapter)
		}
		return applyComponentRelease(moduleID, selected, present, runtime)
	}
	return applyComponentRelease(moduleID, "", false, runtime)
}

func intentComponentRelease(moduleID string, rawIntent any) (string, bool, error) {
	intent, ok := rawIntent.(map[string]any)
	if !ok {
		return "", false, nil
	}
	settings, ok := intent["settings"].(map[string]any)
	if !ok {
		return "", false, nil
	}
	raw, present := settings[componentReleaseSetting]
	if !present {
		return "", false, nil
	}
	selected, ok := raw.(string)
	if !ok || strings.TrimSpace(selected) == "" || selected != strings.TrimSpace(selected) {
		return "", false, fail(ErrInvalidInput, "catalog.modules."+moduleID+".settings."+componentReleaseSetting, "requires one exact release name")
	}
	return selected, true, nil
}

// applyComponentRelease removes every catalog release list and, for a
// selection, replaces the entry component image and the runtime image with the
// admitted pin whose tag names the selected release.
func applyComponentRelease(moduleID, selected string, present bool, runtime map[string]any) error {
	path := "catalog.modules." + moduleID + ".runtime"
	components, _ := runtime["components"].([]any)
	entryRef, _ := runtime["entryComponentRef"].(string)
	var admitted []map[string]any
	var entry map[string]any
	for _, raw := range components {
		component, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrContractConflict, path+".components", "runtime component is invalid")
		}
		if component["id"] == entryRef {
			entry = component
		}
		releases, declared := component["releases"]
		if !declared {
			continue
		}
		delete(component, "releases")
		if component["id"] != entryRef {
			return fail(ErrContractConflict, path+".components", "releases are admitted only on the entry component")
		}
		list, ok := releases.([]any)
		if !ok || len(list) == 0 {
			return fail(ErrContractConflict, path+".components.releases", "release list is invalid")
		}
		image, ok := component["image"].(map[string]any)
		if !ok {
			return fail(ErrContractConflict, path+".components.image", "entry component image is invalid")
		}
		admitted = append(admitted, image)
		for _, rawRelease := range list {
			release, ok := rawRelease.(map[string]any)
			image, imageOK := release["image"].(map[string]any)
			if !ok || !imageOK {
				return fail(ErrContractConflict, path+".components.releases", "admitted release image is invalid")
			}
			admitted = append(admitted, image)
		}
	}
	if !present {
		return nil
	}
	if entry == nil || len(admitted) == 0 {
		return fail(ErrInvalidInput, path, "selected release %q has no admitted release choice", selected)
	}
	names := make([]string, 0, len(admitted))
	for _, image := range admitted {
		ref, _ := image["ref"].(string)
		name := componentImageRelease(ref)
		names = append(names, name)
		if name != selected {
			continue
		}
		pin := map[string]any{"ref": ref, "digest": image["digest"]}
		entry["image"] = pin
		runtime["image"] = map[string]any{"ref": ref, "digest": image["digest"]}
		return nil
	}
	return fail(ErrInvalidInput, path, "release %q is not admitted; admitted releases: %s", selected, strings.Join(names, ", "))
}

// componentImageRelease returns the exact tag of a tag-pinned image ref.
func componentImageRelease(ref string) string {
	separator := strings.LastIndex(ref, ":")
	if separator <= strings.LastIndex(ref, "/") || strings.Contains(ref, "@") {
		return ""
	}
	return ref[separator+1:]
}

// AdmittedComponentReleases names the releases a catalog module admits for its
// entry component, default first; it is empty without a release choice.
func AdmittedComponentReleases(catalog Catalog, moduleRef string) []string {
	for _, module := range catalog.Modules {
		metadata, _ := module["metadata"].(map[string]any)
		if metadata["id"] != moduleRef {
			continue
		}
		runtime, _ := module["runtime"].(map[string]any)
		entryRef, _ := runtime["entryComponentRef"].(string)
		components, _ := runtime["components"].([]any)
		for _, raw := range components {
			component, _ := raw.(map[string]any)
			releases, _ := component["releases"].([]any)
			if component["id"] != entryRef || len(releases) == 0 {
				continue
			}
			image, _ := component["image"].(map[string]any)
			ref, _ := image["ref"].(string)
			names := []string{componentImageRelease(ref)}
			for _, rawRelease := range releases {
				release, _ := rawRelease.(map[string]any)
				image, _ := release["image"].(map[string]any)
				ref, _ := image["ref"].(string)
				names = append(names, componentImageRelease(ref))
			}
			return names
		}
	}
	return nil
}

// ComponentReleaseSetting is the workload setting that selects a release.
const ComponentReleaseSetting = componentReleaseSetting
