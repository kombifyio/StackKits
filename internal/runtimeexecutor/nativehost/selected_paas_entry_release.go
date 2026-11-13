package nativehost

import (
	"errors"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

// entryImageAdmitted reports an entry image a module-specific executor may
// start: the catalog default, a tested release or an owner-accepted in-track
// release of the same image (architecturev2renderer workload_release.go).
func entryImageAdmitted(moduleRef, ref, digest string) bool {
	_, _, ok := architecturev2renderer.WorkloadEntryImageRelease(moduleRef, ref, digest)
	return ok
}

// effectiveEntryRelease replaces a module parser's catalog-default release and
// component pins with the bundle's effective ones, and requires the effective
// entry image to be the authorized runtime target image.
func effectiveEntryRelease(content []byte, release string, components []architecturev2renderer.SelectedPaaSWorkloadComponentDescriptor, target runtimeexecutor.RuntimeTarget) (string, []architecturev2renderer.SelectedPaaSWorkloadComponentDescriptor, error) {
	effectiveRelease, effective, err := architecturev2renderer.EffectiveWorkloadRelease(content, release, components)
	if err != nil {
		return "", nil, err
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(content)
	if err != nil {
		return "", nil, err
	}
	for _, component := range bundle.Components {
		if component.ID == bundle.EntryComponent && component.ImageRef == target.ImageRef && component.ImageDigest == target.ImageDigest {
			return effectiveRelease, effective, nil
		}
	}
	return "", nil, errors.New("the entry image differs from the authorized runtime target")
}
