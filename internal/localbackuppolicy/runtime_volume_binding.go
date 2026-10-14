package localbackuppolicy

import "fmt"

// WithRuntimeVolumeNames translates an already governed application selection
// to node-observed physical names. These runtime facts are not a second policy
// artifact; canonical snapshot paths and classes remain unchanged.
func (source Source) WithRuntimeVolumeNames(names map[string]string) (Source, error) {
	allowed := map[string]bool{}
	for _, volume := range source.ApplicationVolumes {
		allowed[volume.VolumeName] = true
	}
	bound := map[string]string{}
	for logical, physical := range names {
		if !allowed[logical] || !ValidComposeVolumeName(physical) {
			return Source{}, fmt.Errorf("physical binding is outside the selected application volumes")
		}
		bound[logical] = physical
	}
	source.physicalVolumes = bound
	return source, nil
}

func (source Source) PhysicalVolumeName(logical string) string {
	if physical := source.physicalVolumes[logical]; physical != "" {
		return physical
	}
	return logical
}
