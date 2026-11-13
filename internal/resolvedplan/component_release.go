package resolvedplan

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Every catalog module's entry component is a release choice: its default
// image, plus the further upstream releases the catalog has tested
// (#ModuleRuntimeComponentV2.releases, optional). Any workload moves its entry
// component to another release through the generic `release` setting, an
// exact override {version, ref, digest, tested}, without a declaration in its
// workload contract. A tested override names a release the catalog admits
// with exactly that pin; an untested override names another stable upstream
// release of the same image inside the module's release track (same major,
// and the same minor for a "patch" track), accepted explicitly by the owner.
// The track is the module's `upstream.track` (modules/<slug>/module.cue),
// projected by bundlegen into componentReleaseTracks. Companion images
// (databases, caches) stay with the kit. The selection is projected into the
// canonical module runtime before it is fingerprinted, so generation, Apply
// and Verify consume one exact image; the plan verifier rebuilds the same
// runtime from the bound workload settings. An override at or below the
// catalog's default release is superseded: the default pin runs and the
// override is inert. The tested release list is catalog-only data and never
// enters a resolved plan.
const componentReleaseSetting = "release"

// ComponentReleaseTrack returns the release track of a module's entry
// component: "patch" where its upstream.track says so, otherwise "minor".
func ComponentReleaseTrack(moduleID string) string {
	return normalizedTrack(componentReleaseTracks[moduleID])
}

// withoutComponentRelease returns workload settings without the generic
// `release` override, which every workload admits beside its declared inputs.
func withoutComponentRelease(settings map[string]any) map[string]any {
	if _, selected := settings[componentReleaseSetting]; !selected {
		return settings
	}
	declared := make(map[string]any, len(settings)-1)
	for key, value := range settings {
		if key != componentReleaseSetting {
			declared[key] = value
		}
	}
	return declared
}

// componentReleaseAdapter is the runtime adapter whose Apply guard and
// retained-data rollback govern a release change.
const componentReleaseAdapter = "standalone-compose"

// componentReleaseNativeModules are node-owned host installations without a
// runtime adapter whose own executor carries the same Apply guard and
// retained-data rollback: the Marketplace Coolify (one per node, also the
// Layer-2 platform) moves its Coolify release independently of a StackKits
// release (D-2026-11-12-06).
var componentReleaseNativeModules = map[string]bool{"stackkits-marketplace-coolify-runtime": true}

// componentReleaseGoverned reports whether a workload's runtime governs a
// release change: the Compose adapter, or a native installation listed above
// that selects no adapter.
func componentReleaseGoverned(moduleID, adapterID string) bool {
	return adapterID == componentReleaseAdapter || (adapterID == "" && componentReleaseNativeModules[moduleID])
}

// ComponentReleaseOverride is the persisted `release` workload setting.
type ComponentReleaseOverride struct {
	Version string `json:"version"`
	Ref     string `json:"ref"`
	Digest  string `json:"digest"`
	Tested  bool   `json:"tested"`
}

// Setting returns the exact StackSpec value of the override.
func (o ComponentReleaseOverride) Setting() map[string]any {
	return map[string]any{"version": o.Version, "ref": o.Ref, "digest": o.Digest, "tested": o.Tested}
}

// ComponentReleaseImage is one admitted release pin of an entry component.
type ComponentReleaseImage struct {
	Release, Ref, Digest string
}

// ComponentReleaseChoice is the catalog authority of one entry component:
// its default pin first, every further admitted pin, and its release track.
type ComponentReleaseChoice struct {
	Admitted []ComponentReleaseImage
	Track    string
}

// ComponentReleaseState is the projection of an override.
type ComponentReleaseState string

const (
	// ComponentReleaseApplied: the override pin runs.
	ComponentReleaseApplied ComponentReleaseState = "applied"
	// ComponentReleaseSuperseded: the catalog default is at or above the
	// override; the default pin runs.
	ComponentReleaseSuperseded ComponentReleaseState = "superseded"
)

// Component release tracks. A major release moves only with a StackKits release.
const (
	ComponentReleaseTrackMinor = "minor"
	ComponentReleaseTrackPatch = "patch"
)

// EvaluateComponentRelease checks one override against its catalog authority
// and reports whether its pin or the catalog default runs.
func EvaluateComponentRelease(choice ComponentReleaseChoice, override ComponentReleaseOverride) (ComponentReleaseState, error) {
	if len(choice.Admitted) == 0 {
		return "", fmt.Errorf("the component admits no release choice")
	}
	def := choice.Admitted[0]
	repository, ok := imageRepository(def.Ref)
	if !ok {
		return "", fmt.Errorf("the default image %q has no exact release tag", def.Ref)
	}
	if !StableComponentRelease(override.Version) {
		return "", fmt.Errorf("release %q is not a stable release tag", override.Version)
	}
	if override.Ref != repository+":"+override.Version {
		return "", fmt.Errorf("release ref %q is not %s:%s", override.Ref, repository, override.Version)
	}
	if !validSHA256Digest(override.Digest) {
		return "", fmt.Errorf("release digest %q is not a lowercase sha256 OCI digest", override.Digest)
	}
	admitted := false
	for _, image := range choice.Admitted {
		if image.Ref != override.Ref {
			continue
		}
		if image.Digest != override.Digest {
			return "", fmt.Errorf("release %s is admitted only with digest %s", override.Version, image.Digest)
		}
		admitted = true
	}
	if admitted != override.Tested {
		if admitted {
			return "", fmt.Errorf("release %s is a tested catalog release and must be recorded as tested", override.Version)
		}
		return "", fmt.Errorf("release %s is not a catalog release and must be recorded as untested", override.Version)
	}
	later, comparable := ComponentReleaseLater(override.Version, def.Release)
	if !comparable {
		if admitted {
			return ComponentReleaseApplied, nil
		}
		return "", fmt.Errorf("release %s is not on the release line of the default %s", override.Version, def.Release)
	}
	if !later {
		return ComponentReleaseSuperseded, nil
	}
	if !admitted && !ComponentReleaseInTrack(choice.Track, def.Release, override.Version) {
		return "", fmt.Errorf("release %s leaves the %s track of %s; a major release moves only with a StackKits release", override.Version, normalizedTrack(choice.Track), def.Release)
	}
	return ComponentReleaseApplied, nil
}

// componentReleaseTag is an ordered image tag: an optional "v", a numeric
// core of two to four parts and an optional variant suffix such as "-apache",
// "-alpine", "-rootless" or "-1-ce-non-root". Two tags are on one release line
// only with the same prefix and the same suffix, so an update never leaves the
// variant the catalog pins.
type componentReleaseTag struct {
	prefix, suffix string
	core           []int
}

var (
	componentReleaseTagPattern = regexp.MustCompile(`^(v?)(\d+)\.(\d+)(?:\.(\d+))?(?:\.(\d+))?(-[A-Za-z0-9][A-Za-z0-9._-]*)?$`)
	componentPreReleaseMarker  = regexp.MustCompile(`(?i)(alpha|beta|rc|pre|preview|dev|nightly|snapshot|canary)`)
)

func parseComponentReleaseTag(release string) (componentReleaseTag, bool) {
	match := componentReleaseTagPattern.FindStringSubmatch(release)
	if match == nil {
		return componentReleaseTag{}, false
	}
	tag := componentReleaseTag{prefix: match[1], suffix: match[6]}
	for _, part := range match[2:6] {
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return componentReleaseTag{}, false
		}
		tag.core = append(tag.core, value)
	}
	return tag, true
}

func (t componentReleaseTag) part(index int) int {
	if index < len(t.core) {
		return t.core[index]
	}
	return 0
}

// StableComponentRelease reports a release tag with a numeric core of two to
// four parts, optionally prefixed with v and followed by a variant suffix
// that names no pre-release (alpha, beta, rc and the like).
func StableComponentRelease(release string) bool {
	tag, ok := parseComponentReleaseTag(release)
	return ok && !componentPreReleaseMarker.MatchString(tag.suffix)
}

// ComponentReleaseLater orders two release tags by their numeric core;
// comparable is false unless both are on one release line (the same prefix
// and variant suffix).
func ComponentReleaseLater(candidate, current string) (later, comparable bool) {
	a, aOK := parseComponentReleaseTag(candidate)
	b, bOK := parseComponentReleaseTag(current)
	if !aOK || !bOK || a.prefix != b.prefix || a.suffix != b.suffix {
		return false, false
	}
	for index := 0; index < 4; index++ {
		if a.part(index) != b.part(index) {
			return a.part(index) > b.part(index), true
		}
	}
	return false, true
}

// ComponentReleaseInTrack reports a release on the line of a base release
// inside its track: the same major, and for a patch track the same minor.
func ComponentReleaseInTrack(track, base, release string) bool {
	a, aOK := parseComponentReleaseTag(base)
	b, bOK := parseComponentReleaseTag(release)
	if !aOK || !bOK || a.prefix != b.prefix || a.suffix != b.suffix || a.part(0) != b.part(0) {
		return false
	}
	return normalizedTrack(track) != ComponentReleaseTrackPatch || a.part(1) == b.part(1)
}

func normalizedTrack(track string) string {
	if track == ComponentReleaseTrackPatch {
		return track
	}
	return ComponentReleaseTrackMinor
}

func requireComponentReleaseAdapter(workloadID string, workload *resolvedWorkloadSelection) error {
	if _, selected := workload.settings[componentReleaseSetting]; !selected {
		return nil
	}
	if !componentReleaseGoverned(workload.moduleID, workload.runtimeAdapterID) {
		return fail(ErrInvalidInput, "spec.workloads."+workloadID+".settings."+componentReleaseSetting,
			"release selection requires the %s runtime adapter", componentReleaseAdapter)
	}
	return nil
}

func projectComponentRelease(moduleID string, rawIntent any, runtime map[string]any) error {
	selected, err := intentComponentRelease(moduleID, rawIntent)
	if err != nil {
		return err
	}
	return applyComponentRelease(moduleID, selected, runtime)
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
		selected, err := intentComponentRelease(moduleID, map[string]any{"settings": settings})
		if err != nil {
			return err
		}
		runtimeAlternative, _ := alternative["runtime"].(map[string]any)
		adapter, _ := runtimeAlternative["adapter"].(map[string]any)
		adapterID, _ := adapter["id"].(string)
		if selected != nil && !componentReleaseGoverned(moduleID, adapterID) {
			return fail(ErrInvalidInput, "resolvedPlan.workloads."+fmt.Sprint(workload["id"])+".settings."+componentReleaseSetting,
				"release selection requires the %s runtime adapter", componentReleaseAdapter)
		}
		return applyComponentRelease(moduleID, selected, runtime)
	}
	return applyComponentRelease(moduleID, nil, runtime)
}

func intentComponentRelease(moduleID string, rawIntent any) (*ComponentReleaseOverride, error) {
	intent, ok := rawIntent.(map[string]any)
	if !ok {
		return nil, nil
	}
	settings, ok := intent["settings"].(map[string]any)
	if !ok {
		return nil, nil
	}
	raw, present := settings[componentReleaseSetting]
	if !present {
		return nil, nil
	}
	override, ok := DecodeComponentReleaseSetting(raw)
	if !ok {
		return nil, fail(ErrInvalidInput, "catalog.modules."+moduleID+".settings."+componentReleaseSetting, "requires exactly {version, ref, digest, tested}")
	}
	return &override, nil
}

// DecodeComponentReleaseSetting reads the exact `release` setting value.
func DecodeComponentReleaseSetting(raw any) (ComponentReleaseOverride, bool) {
	value, ok := raw.(map[string]any)
	if !ok || len(value) != 4 {
		return ComponentReleaseOverride{}, false
	}
	version, versionOK := value["version"].(string)
	ref, refOK := value["ref"].(string)
	digest, digestOK := value["digest"].(string)
	tested, testedOK := value["tested"].(bool)
	if !versionOK || !refOK || !digestOK || !testedOK {
		return ComponentReleaseOverride{}, false
	}
	return ComponentReleaseOverride{Version: version, Ref: ref, Digest: digest, Tested: tested}, true
}

// applyComponentRelease removes every catalog release list and, for an
// applied override, replaces the entry component image and the runtime image
// with its exact pin.
func applyComponentRelease(moduleID string, selected *ComponentReleaseOverride, runtime map[string]any) error {
	path := "catalog.modules." + moduleID + ".runtime"
	components, _ := runtime["components"].([]any)
	entryRef, _ := runtime["entryComponentRef"].(string)
	choice := ComponentReleaseChoice{Track: ComponentReleaseTrack(moduleID)}
	var entry map[string]any
	for _, raw := range components {
		component, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrContractConflict, path+".components", "runtime component is invalid")
		}
		releases, declared := component["releases"]
		delete(component, "releases")
		if component["id"] != entryRef {
			if declared {
				return fail(ErrContractConflict, path+".components", "releases are admitted only on the entry component")
			}
			continue
		}
		entry = component
		image, ok := component["image"].(map[string]any)
		if !ok {
			return fail(ErrContractConflict, path+".components.image", "entry component image is invalid")
		}
		choice.Admitted = append(choice.Admitted, catalogReleaseImage(image))
		if !declared {
			continue
		}
		list, ok := releases.([]any)
		if !ok || len(list) == 0 {
			return fail(ErrContractConflict, path+".components.releases", "release list is invalid")
		}
		for _, rawRelease := range list {
			release, ok := rawRelease.(map[string]any)
			image, imageOK := release["image"].(map[string]any)
			if !ok || !imageOK {
				return fail(ErrContractConflict, path+".components.releases", "admitted release image is invalid")
			}
			choice.Admitted = append(choice.Admitted, catalogReleaseImage(image))
		}
	}
	if selected == nil {
		return nil
	}
	if entry == nil {
		return fail(ErrInvalidInput, path, "selected release %q has no entry component", selected.Version)
	}
	state, err := EvaluateComponentRelease(choice, *selected)
	if err != nil {
		return fail(ErrInvalidInput, path, "%v", err)
	}
	if state == ComponentReleaseApplied {
		entry["image"] = map[string]any{"ref": selected.Ref, "digest": selected.Digest}
		// A native host installation declares no runtime image; its entry
		// component alone carries the pin.
		if _, declared := runtime["image"]; declared || !componentReleaseNativeModules[moduleID] {
			runtime["image"] = map[string]any{"ref": selected.Ref, "digest": selected.Digest}
		}
	}
	return nil
}

func catalogReleaseImage(image map[string]any) ComponentReleaseImage {
	ref, _ := image["ref"].(string)
	digest, _ := image["digest"].(string)
	return ComponentReleaseImage{Release: componentImageRelease(ref), Ref: ref, Digest: digest}
}

// componentImageRelease returns the exact tag of a tag-pinned image ref.
func componentImageRelease(ref string) string {
	separator := strings.LastIndex(ref, ":")
	if separator <= strings.LastIndex(ref, "/") || strings.Contains(ref, "@") {
		return ""
	}
	return ref[separator+1:]
}

func imageRepository(ref string) (string, bool) {
	if componentImageRelease(ref) == "" {
		return "", false
	}
	return ref[:strings.LastIndex(ref, ":")], true
}

func validSHA256Digest(digest string) bool {
	value, ok := strings.CutPrefix(digest, "sha256:")
	decoded, err := hex.DecodeString(value)
	return ok && err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

// CatalogComponentReleaseChoice reads the release choice of a catalog
// module's entry component: its default image, its tested releases and its
// track. ok is false for a module without an entry component image.
func CatalogComponentReleaseChoice(catalog Catalog, moduleRef string) (ComponentReleaseChoice, bool) {
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
			image, hasImage := component["image"].(map[string]any)
			if entryRef == "" || component["id"] != entryRef || !hasImage {
				continue
			}
			releases, _ := component["releases"].([]any)
			choice := ComponentReleaseChoice{Track: ComponentReleaseTrack(moduleRef), Admitted: []ComponentReleaseImage{catalogReleaseImage(image)}}
			for _, rawRelease := range releases {
				release, _ := rawRelease.(map[string]any)
				image, _ := release["image"].(map[string]any)
				choice.Admitted = append(choice.Admitted, catalogReleaseImage(image))
			}
			return choice, true
		}
	}
	return ComponentReleaseChoice{}, false
}

// ComponentReleaseSetting is the workload setting that selects a release.
const ComponentReleaseSetting = componentReleaseSetting
