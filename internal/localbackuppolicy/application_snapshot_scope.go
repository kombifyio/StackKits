package localbackuppolicy

import (
	"errors"
	"path"
	"slices"
	"sort"
	"strings"
)

// ApplicationSnapshotSources is an explicit complete application closure,
// independent of scheduled retention coverage. Core/other workloads never enter it.
func (policy Policy) ApplicationSnapshotSources(workloads []string) ([]SnapshotSource, error) {
	if len(workloads) == 0 {
		return policy.SnapshotSources()
	}
	if _, err := ArtifactBytes(policy); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, ref := range workloads {
		if ref == "" || seen[ref] {
			return nil, errors.New("invalid application snapshot scope")
		}
		seen[ref] = true
	}
	var result []SnapshotSource
	for _, v := range policy.Source.ApplicationVolumes {
		if seen[v.WorkloadRef] {
			if !ValidBackupDataClass(v.BackupDataClass) {
				return nil, errors.New("application snapshot requires declared semantic data class")
			}
			source := SnapshotSource{VolumeName: v.VolumeName, DataClass: v.BackupDataClass, Path: path.Join(policy.Source.ContainerPath, v.VolumeName, "_data")}
			for _, excluded := range policy.Source.ExcludePaths {
				if strings.HasPrefix(excluded, source.Path+"/") {
					source.ExcludePaths = append(source.ExcludePaths, excluded)
				}
			}
			result = append(result, source)
		}
	}
	for _, ref := range workloads {
		if !slices.ContainsFunc(policy.Source.ApplicationVolumes, func(v ApplicationVolume) bool { return v.WorkloadRef == ref }) {
			return nil, errors.New("application snapshot scope is outside the node policy")
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].VolumeName < result[j].VolumeName })
	return result, nil
}
