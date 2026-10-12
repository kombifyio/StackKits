package backuplifecycle

import (
	"fmt"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"sort"
)

// BackupCoverage describes the selected source policy, not proof that its data
// is recoverable. Only the owner-bound configuration supplies these facts.
type BackupCoverage struct {
	Scope                string                                `json:"scope"`
	RecoveryMode         string                                `json:"recoveryMode"`
	PolicyArtifactDigest string                                `json:"policyArtifactDigest"`
	Target               localbackuppolicy.Target              `json:"target"`
	ManagedVolumeNames   []string                              `json:"managedVolumeNames"`
	ApplicationVolumes   []localbackuppolicy.ApplicationVolume `json:"applicationVolumes,omitempty"`
	ExcludePaths         []string                              `json:"excludePaths"`
	OffHostRecovery      string                                `json:"offHostRecovery"`
	ApplicationRecovery  string                                `json:"applicationRecovery"`
	SnapshotSources      []localbackuppolicy.SnapshotSource    `json:"snapshotSources,omitempty"`
	DataClasses          []string                              `json:"dataClasses,omitempty"`
	ExcludedDataClasses  []string                              `json:"excludedDataClasses,omitempty"`
}

func sourceCoverage(configuration Configuration) (*BackupCoverage, error) {
	policy := clonePolicy(configuration.Policy)
	result := &BackupCoverage{
		Scope: "declared-source-policy", RecoveryMode: "physical-whole-set",
		PolicyArtifactDigest: configuration.PolicyArtifactDigest, Target: policy.Target,
		ManagedVolumeNames: policy.Source.ManagedVolumeNames, ApplicationVolumes: policy.Source.ApplicationVolumes,
		ExcludePaths: policy.Source.ExcludePaths,
		// No native producer currently attests independent off-host custody or
		// functional data/client recovery. Repository and HTTP health cannot do so.
		OffHostRecovery: "unverified", ApplicationRecovery: "unverified",
	}
	sources, err := policy.SnapshotSources()
	if err != nil {
		return nil, fmt.Errorf("backup source coverage cannot be projected: %w", err)
	}
	if len(sources) > 0 {
		result.RecoveryMode = "physical-source-set"
		result.SnapshotSources = sources
		result.DataClasses = append([]string(nil), policy.DataClasses...)
		selected := make(map[string]bool, len(sources))
		result.ManagedVolumeNames = nil
		for _, source := range sources {
			selected[source.VolumeName] = true
			result.ManagedVolumeNames = append(result.ManagedVolumeNames, source.VolumeName)
		}
		result.ApplicationVolumes = nil
		excluded := map[string]bool{}
		for _, volume := range policy.Source.ApplicationVolumes {
			if selected[volume.VolumeName] {
				result.ApplicationVolumes = append(result.ApplicationVolumes, volume)
			} else {
				excluded[volume.BackupDataClass] = true
			}
		}
		for class := range excluded {
			result.ExcludedDataClasses = append(result.ExcludedDataClasses, class)
		}
		sort.Strings(result.ExcludedDataClasses)
	}
	return result, nil
}
