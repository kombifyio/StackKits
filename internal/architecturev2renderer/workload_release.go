package architecturev2renderer

import "strings"

// workloadReleaseImage is one exact admitted release of a workload's entry
// component. Values come only from the generated catalog projection
// (admittedWorkloadReleaseImages in workload_images_gen.go).
type workloadReleaseImage struct {
	Release, Ref, Digest string
}

// WorkloadRelease is one admitted release as lifecycle adapters see it.
type WorkloadRelease struct {
	Release string
	Ref     string
	Digest  string
}

// AdmittedWorkloadReleases returns the default release first, then every
// further release the catalog admits for the module's entry component. It is
// empty when the module admits no release choice.
func AdmittedWorkloadReleases(moduleRef string) []WorkloadRelease {
	images := admittedWorkloadReleaseImages[moduleRef]
	releases := make([]WorkloadRelease, len(images))
	for index, image := range images {
		releases[index] = WorkloadRelease(image)
	}
	return releases
}

// AdmittedWorkloadReleaseForImage names the admitted release whose exact pin is
// ref and digest; ok is false for any other image.
func AdmittedWorkloadReleaseForImage(moduleRef, ref, digest string) (string, bool) {
	for _, image := range admittedWorkloadReleaseImages[moduleRef] {
		if image.Ref == ref && image.Digest == digest {
			return image.Release, true
		}
	}
	return "", false
}

// admittedWorkloadRelease resolves a rendered entry image to its release. A
// module without further releases accepts only its single default pin.
func admittedWorkloadRelease(moduleRef, defaultRelease, defaultRef, defaultDigest, ref, digest string) (string, bool) {
	if len(admittedWorkloadReleaseImages[moduleRef]) == 0 {
		return defaultRelease, ref == defaultRef && digest == defaultDigest
	}
	return AdmittedWorkloadReleaseForImage(moduleRef, ref, digest)
}

// admittedWorkloadReleaseSchema is the release field of a renderer contract
// schema. It names every admitted release, so the renderer contract hash moves
// whenever the admitted set does.
func admittedWorkloadReleaseSchema(moduleRef, defaultRelease string) string {
	images := admittedWorkloadReleaseImages[moduleRef]
	if len(images) == 0 {
		return defaultRelease
	}
	names := make([]string, len(images))
	for index, image := range images {
		names[index] = image.Release
	}
	return strings.Join(names, ",")
}
