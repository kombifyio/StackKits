package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// Every catalog module's entry image is a release choice (resolvedplan
// component_release.go). The plan carries the selected entry pin; the
// renderers themselves only know the catalog default. This file is the single
// point between the two: renderUnit hands every module renderer a unit whose
// entry image is the catalog default, and records an applied override in the
// rendered workload bundle as workload.releaseOverride. The generic delivery
// parser (ParseApplicationDeliveryWorkloadBundle) puts the override back on
// the entry component, so Compose, Verify, the Apply release guard and
// observation all run and check the override pin. Companion images stay with
// the kit and are never overridden.

// workloadReleaseImage is one exact release pin of a module's entry component.
type workloadReleaseImage struct {
	Release, Ref, Digest string
}

// workloadEntryRelease is the generated catalog authority of one module's
// entry component: its default pin first, then every tested release.
type workloadEntryRelease struct {
	Component string
	Images    []workloadReleaseImage
}

// WorkloadRelease is one release pin as lifecycle adapters see it.
type WorkloadRelease struct {
	Release string
	Ref     string
	Digest  string
}

// WorkloadReleaseOverride is the effective entry pin of a workload bundle
// whose plan selected another release than the catalog default.
type WorkloadReleaseOverride struct {
	Release string `json:"release"`
	Ref     string `json:"ref"`
	Digest  string `json:"digest"`
}

// AdmittedWorkloadReleases returns the default release first, then every
// further release the catalog has tested for the module's entry component.
// It is empty for a module without a tag-pinned entry image.
func AdmittedWorkloadReleases(moduleRef string) []WorkloadRelease {
	images := workloadEntryReleases[moduleRef].Images
	releases := make([]WorkloadRelease, len(images))
	for index, image := range images {
		releases[index] = WorkloadRelease(image)
	}
	return releases
}

// WorkloadReleaseChoice is the catalog authority of a module's entry
// component: its tested pins (default first) and its release track. ok is
// false for a module without a tag-pinned entry image.
func WorkloadReleaseChoice(moduleRef string) (resolvedplan.ComponentReleaseChoice, bool) {
	entry, ok := workloadEntryReleases[moduleRef]
	if !ok || len(entry.Images) == 0 {
		return resolvedplan.ComponentReleaseChoice{}, false
	}
	choice := resolvedplan.ComponentReleaseChoice{Track: resolvedplan.ComponentReleaseTrack(moduleRef)}
	for _, image := range entry.Images {
		choice.Admitted = append(choice.Admitted, resolvedplan.ComponentReleaseImage(image))
	}
	return choice, true
}

// AdmittedWorkloadReleaseForImage names the tested release whose exact pin is
// ref and digest; ok is false for any other image.
func AdmittedWorkloadReleaseForImage(moduleRef, ref, digest string) (string, bool) {
	for _, image := range workloadEntryReleases[moduleRef].Images {
		if image.Ref == ref && image.Digest == digest {
			return image.Release, true
		}
	}
	return "", false
}

// WorkloadReleaseInTrack reports a stable release that a tested or untested
// override of the module's entry image may run: the default, or a later
// release inside the module's track.
func WorkloadReleaseInTrack(moduleRef, release string) bool {
	choice, ok := WorkloadReleaseChoice(moduleRef)
	if !ok {
		return false
	}
	def := choice.Admitted[0].Release
	if release == def {
		return true
	}
	later, comparable := resolvedplan.ComponentReleaseLater(release, def)
	return comparable && later && resolvedplan.StableComponentRelease(release) && resolvedplan.ComponentReleaseInTrack(choice.Track, def, release)
}

// WorkloadEntryImageRelease resolves an entry image pin of a module to its
// release: the default pin, a tested pin, or an untested in-track release of
// the same image that an applied override may run. isDefault reports the
// catalog default; ok is false for every other image.
func WorkloadEntryImageRelease(moduleRef, ref, digest string) (release string, isDefault, ok bool) {
	choice, found := WorkloadReleaseChoice(moduleRef)
	if !found {
		return "", false, false
	}
	def := choice.Admitted[0]
	if ref == def.Ref && digest == def.Digest {
		return def.Release, true, true
	}
	_, tested := AdmittedWorkloadReleaseForImage(moduleRef, ref, digest)
	separator := strings.LastIndex(ref, ":")
	if separator <= strings.LastIndex(ref, "/") {
		return "", false, false
	}
	override := resolvedplan.ComponentReleaseOverride{Version: ref[separator+1:], Ref: ref, Digest: digest, Tested: tested}
	state, err := resolvedplan.EvaluateComponentRelease(choice, override)
	if err != nil || state != resolvedplan.ComponentReleaseApplied {
		return "", false, false
	}
	return override.Version, false, true
}

// entryReleaseUnit returns the unit a module renderer receives: for a plan
// that selected another entry release, a copy whose runtime image and entry
// component image are the catalog default, together with the override. A
// unit without an entry image, or at its default pin, is returned unchanged.
func entryReleaseUnit(unit RenderUnit) (RenderUnit, *WorkloadReleaseOverride, error) {
	path := "resolvedPlan.modules." + unit.moduleID + ".renderUnits." + unit.id + ".runtime"
	entryRef, hasEntry := unit.RuntimeEntryComponentRef()
	ref, hasRef := unit.ContainerImageRef()
	digest, hasDigest := unit.ContainerImageDigest()
	entry, known := workloadEntryReleases[unit.moduleID]
	if !hasEntry || !hasRef || !hasDigest || !known || len(entry.Images) == 0 || entry.Component != entryRef {
		return unit, nil, nil
	}
	release, isDefault, ok := WorkloadEntryImageRelease(unit.moduleID, ref, digest)
	if isDefault {
		return unit, nil, nil
	}
	if !ok {
		return RenderUnit{}, nil, fail(ErrInvalidPlan, path, "entry image %s is neither the catalog default nor a tested or in-track release of %s", ref, entry.Images[0].Ref)
	}
	def := entry.Images[0]
	decoder := json.NewDecoder(bytes.NewReader(unit.runtimeComponentsJSON))
	decoder.UseNumber()
	var components []map[string]any
	if err := decoder.Decode(&components); err != nil {
		return RenderUnit{}, nil, wrap(ErrInvalidPlan, path+".components", "decode component graph", err)
	}
	replaced := 0
	for _, component := range components {
		if component["id"] != entryRef {
			continue
		}
		image, _ := component["image"].(map[string]any)
		if image["ref"] != ref || image["digest"] != digest {
			return RenderUnit{}, nil, fail(ErrInvalidPlan, path+".components", "entry component image differs from the runtime image")
		}
		component["image"] = map[string]any{"ref": def.Ref, "digest": def.Digest}
		replaced++
	}
	if replaced != 1 {
		return RenderUnit{}, nil, fail(ErrInvalidPlan, path+".components", "entry component %q is absent", entryRef)
	}
	encoded, err := json.Marshal(components)
	if err != nil {
		return RenderUnit{}, nil, wrap(ErrRendererFailure, path+".components", "encode default component graph", err)
	}
	normalized := unit
	normalized.containerImageRef, normalized.containerImageDigest = def.Ref, def.Digest
	normalized.runtimeComponentsJSON = encoded
	return normalized, &WorkloadReleaseOverride{Release: release, Ref: ref, Digest: digest}, nil
}

// withEntryReleaseOverride records an applied override in the one workload
// bundle a module renderer produced. Its bytes otherwise stay exactly the
// renderer's own; a renderer whose output is no workload bundle at the
// default entry pin delivers no override and fails closed.
func withEntryReleaseOverride(unit RenderUnit, override WorkloadReleaseOverride, outputs []UnitOutput) ([]UnitOutput, error) {
	path := "resolvedPlan.modules." + unit.moduleID + ".renderUnits." + unit.id + ".outputs"
	def := workloadEntryReleases[unit.moduleID].Images[0]
	recorded := 0
	result := make([]UnitOutput, len(outputs))
	for index, output := range outputs {
		result[index] = output
		var bundle selectedPaaSWorkloadBundle
		if decodeStrict(output.Bytes, &bundle) != nil || bundle.Workload.ModuleRef != unit.moduleID {
			continue
		}
		canonical, err := json.Marshal(bundle)
		if err != nil || !bytes.Equal(append(canonical, '\n'), output.Bytes) {
			return nil, fail(ErrRendererFailure, path, "workload bundle %q is not in canonical form for a release override", output.Ref)
		}
		entryAtDefault := false
		for _, component := range bundle.Components {
			if component.ID == bundle.Workload.EntryComponent {
				entryAtDefault = component.Image.Ref == def.Ref && component.Image.Digest == def.Digest
			}
		}
		if !entryAtDefault || bundle.Workload.Release != def.Release || bundle.Workload.ReleaseOverride != nil {
			return nil, fail(ErrRendererFailure, path, "workload bundle %q does not render the catalog default entry release", output.Ref)
		}
		recorded++
		bundle.Workload.ReleaseOverride = &override
		data, err := json.Marshal(bundle)
		if err != nil {
			return nil, wrap(ErrRendererFailure, path, "marshal workload bundle release override", err)
		}
		result[index] = UnitOutput{Ref: output.Ref, Bytes: append(data, '\n')}
	}
	if recorded != 1 {
		return nil, fail(ErrRendererFailure, path, "module %s delivers no single workload bundle for its entry release override", unit.moduleID)
	}
	return result, nil
}

// applyWorkloadReleaseOverride puts a bundle's recorded override on its entry
// component after checking it against the generated catalog authority.
func applyWorkloadReleaseOverride(descriptor *ApplicationDeliveryBundleDescriptor, override *WorkloadReleaseOverride, path string) error {
	if override == nil {
		return nil
	}
	release, isDefault, ok := WorkloadEntryImageRelease(descriptor.ModuleRef, override.Ref, override.Digest)
	entry := workloadEntryReleases[descriptor.ModuleRef]
	if !ok || isDefault || release != override.Release || entry.Component != descriptor.EntryComponent || descriptor.Release != entry.Images[0].Release {
		return fail(ErrInvalidPlan, path, "release override is not a tested or in-track release of the entry image")
	}
	for index := range descriptor.Components {
		component := &descriptor.Components[index]
		if component.ID != descriptor.EntryComponent {
			continue
		}
		if component.ImageRef != entry.Images[0].Ref || component.ImageDigest != entry.Images[0].Digest {
			return fail(ErrInvalidPlan, path, "entry component image differs from the catalog default the override replaces")
		}
		component.ImageRef, component.ImageDigest = override.Ref, override.Digest
		descriptor.Release = override.Release
		return nil
	}
	return fail(ErrInvalidPlan, path, "entry component is absent")
}

// EffectiveWorkloadRelease reads the effective release and component pins of
// a closed workload bundle for a module-specific executor whose own parser
// reports the catalog default: with a recorded override, the entry
// component's pin and the release are the override's.
func EffectiveWorkloadRelease(data []byte, release string, components []SelectedPaaSWorkloadComponentDescriptor) (string, []SelectedPaaSWorkloadComponentDescriptor, error) {
	bundle, err := ParseApplicationDeliveryWorkloadBundle(data)
	if err != nil {
		return "", nil, err
	}
	effective := append([]SelectedPaaSWorkloadComponentDescriptor(nil), components...)
	for index := range effective {
		for _, component := range bundle.Components {
			if component.ID == effective[index].ID {
				effective[index].ImageRef, effective[index].ImageDigest = component.ImageRef, component.ImageDigest
			}
		}
	}
	// The generic parser admitted any difference only as a checked override.
	if bundle.Release != release {
		if _, isDefault, ok := WorkloadEntryImageRelease(bundle.ModuleRef, overriddenEntryRef(bundle), overriddenEntryDigest(bundle)); !ok || isDefault {
			return "", nil, fail(ErrInvalidPlan, "workloadBundle.workload.release", "release differs from the module's own bundle contract")
		}
	}
	return bundle.Release, effective, nil
}

func overriddenEntryRef(bundle ApplicationDeliveryBundleDescriptor) string {
	for _, component := range bundle.Components {
		if component.ID == bundle.EntryComponent {
			return component.ImageRef
		}
	}
	return ""
}

func overriddenEntryDigest(bundle ApplicationDeliveryBundleDescriptor) string {
	for _, component := range bundle.Components {
		if component.ID == bundle.EntryComponent {
			return component.ImageDigest
		}
	}
	return ""
}
