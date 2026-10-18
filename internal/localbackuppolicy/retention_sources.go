package localbackuppolicy

import (
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
)

// SnapshotSource selects one already governed Docker volume. Per-volume Kopia
// sources let content expire independently in the same repository and custody.
// DataClasses on ApplicationVolume remain privacy labels, never retention keys.
type SnapshotSource struct {
	VolumeName   string   `json:"volumeName"`
	DataClass    string   `json:"dataClass"`
	Path         string   `json:"path"`
	ExcludePaths []string `json:"excludePaths,omitempty"`
}

func ValidBackupDataClass(value string) bool {
	switch value {
	case "config", "secret-material", "platform-state", "database", "user-content", "documents", "photos", "large-media", "telemetry-timeseries", "serverless-config":
		return true
	default:
		return false
	}
}

// SnapshotSources keeps historical whole-root policies unchanged. Enabling a
// class override requires an explicit semantic class for every application
// volume; a missing class cannot silently inherit the configuration ladder.
func (policy Policy) SnapshotSources() ([]SnapshotSource, error) {
	if len(policy.RetentionByDataClass) == 0 && len(policy.DataClasses) == 0 {
		return nil, nil
	}
	if policy.Retention == nil {
		return nil, fmt.Errorf("per-class retention requires the resolved default retention")
	}
	if err := policy.Retention.Validate(); err != nil {
		return nil, err
	}
	for class, retention := range policy.RetentionByDataClass {
		if !ValidBackupDataClass(class) {
			return nil, fmt.Errorf("retention class %q is not a backup data class", class)
		}
		if err := retention.Validate(); err != nil {
			return nil, fmt.Errorf("retention class %s: %w", class, err)
		}
	}
	all, err := policy.ClassifiedSnapshotSources()
	if err != nil {
		return nil, err
	}
	if len(policy.DataClasses) == 0 {
		return all, nil
	}
	selected := make(map[string]bool, len(policy.DataClasses))
	for _, class := range policy.DataClasses {
		if !ValidBackupDataClass(class) || selected[class] {
			return nil, fmt.Errorf("backup coverage classes must be unique semantic classes")
		}
		selected[class] = true
	}
	for class := range policy.RetentionByDataClass {
		if !selected[class] {
			return nil, fmt.Errorf("retention override selects a class outside backup coverage")
		}
	}
	for _, s := range policy.Source.MarketplaceServices {
		if !selected[s.BackupDataClass] {
			return nil, fmt.Errorf("selected Marketplace source class %s is outside backup coverage", s.BackupDataClass)
		}
	}
	if !selected["config"] {
		return nil, fmt.Errorf("Core recovery coverage requires the config class")
	}
	result := make([]SnapshotSource, 0, len(all))
	for _, source := range all {
		if selected[source.DataClass] {
			result = append(result, source)
		}
	}
	return result, nil
}

// ClassifiedSnapshotSources also admits lookup of signed historical class
// snapshots after the active retention ladder changes. It grants no writes.
func (policy Policy) ClassifiedSnapshotSources() ([]SnapshotSource, error) {
	source := policy.SourceProjection()
	if err := ValidateSourceProjection(source); err != nil {
		return nil, err
	}
	profile, err := coreProfile(source.CoreModuleRef)
	if err != nil {
		return nil, err
	}
	// The selected Core profile's custody, identity and control state form its
	// configuration source. Application classifications come only from CUE.
	classes := make(map[string]string, len(source.ManagedVolumeNames))
	for _, name := range profile.managedVolumeNames {
		classes[name] = "config"
	}
	for _, volume := range source.ApplicationVolumes {
		if !ValidBackupDataClass(volume.BackupDataClass) {
			return nil, fmt.Errorf("backup volume %s requires an explicit backupDataClass; privacy labels cannot supply it", volume.VolumeName)
		}
		classes[volume.VolumeName] = volume.BackupDataClass
	}
	for _, service := range source.MarketplaceServices {
		classes[service.VolumeName] = service.BackupDataClass
	}
	result := make([]SnapshotSource, 0, len(source.ManagedVolumeNames))
	for _, name := range source.ManagedVolumeNames {
		class, ok := classes[name]
		if !ok {
			return nil, fmt.Errorf("backup volume %s has no governed retention class", name)
		}
		member := SnapshotSource{VolumeName: name, DataClass: class, Path: path.Join(source.ContainerPath, name, "_data")}
		for _, excluded := range source.ExcludePaths {
			if strings.HasPrefix(excluded, member.Path+"/") {
				member.ExcludePaths = append(member.ExcludePaths, excluded)
			}
		}
		result = append(result, member)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].VolumeName < result[j].VolumeName })
	return result, nil
}

func (policy Policy) RetentionForDataClass(class string) (*Retention, error) {
	if !ValidBackupDataClass(class) {
		return nil, fmt.Errorf("unknown backup data class %q", class)
	}
	if retention, ok := policy.RetentionByDataClass[class]; ok {
		copy := retention
		return &copy, nil
	}
	if policy.Retention == nil {
		return nil, fmt.Errorf("resolved default retention is required")
	}
	copy := *policy.Retention
	return &copy, nil
}

// ValidateSnapshotSourceSelection checks a signed historical selection against
// physical volume custody. A retention reclassification does not rename data.
func (policy Policy) ValidateSnapshotSourceSelection(selected []SnapshotSource) error {
	all, err := policy.ClassifiedSnapshotSources()
	if err != nil {
		return err
	}
	known := make(map[string]SnapshotSource, len(all))
	for _, source := range all {
		known[source.VolumeName] = source
	}
	seen := make(map[string]bool, len(selected))
	previous := ""
	for _, source := range selected {
		wanted, ok := known[source.VolumeName]
		if !ok || seen[source.VolumeName] || source.VolumeName <= previous || !ValidBackupDataClass(source.DataClass) || source.Path != wanted.Path || !reflect.DeepEqual(source.ExcludePaths, wanted.ExcludePaths) {
			return fmt.Errorf("snapshot class selection differs from the governed physical volume custody")
		}
		seen[source.VolumeName] = true
		previous = source.VolumeName
	}
	if len(selected) == 0 {
		return fmt.Errorf("class snapshot selection is empty")
	}
	return nil
}

func CloneSnapshotSources(sources []SnapshotSource) []SnapshotSource {
	if sources == nil {
		return nil
	}
	result := append([]SnapshotSource(nil), sources...)
	for i := range result {
		result[i].ExcludePaths = append([]string(nil), result[i].ExcludePaths...)
	}
	return result
}
