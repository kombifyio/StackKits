package backuplifecycle

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/kombifyio/stackkits/internal/backupplan"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// EmergencyContractInput lowers a generated v2 backup contract into the
// existing portable exporter. Policy supplies the CUE-selected volume set;
// the recovery plan supplies format, classes, media policy and target.
type EmergencyContractInput struct {
	Plan           backupplan.EmergencyExportPlan
	Policy         localbackuppolicy.Policy
	VolumeRoot     string
	DumpSources    []EmergencySource
	Sources        []EmergencySource
	Target         string
	Recipients     []age.Recipient
	LargeMediaMode string
}

// SourcesFromContract maps the generated local Kopia source policy onto
// explicit emergency-export sources. Missing include classes omit those
// volumes; secret volumes are skipped unless secrets are selected. Dump
// sources replace live volumes of the same class so a hook-produced dump is
// archived instead of a running database file.
func SourcesFromContract(plan backupplan.EmergencyExportPlan, policy localbackuppolicy.Policy, volumeRoot string, dumps []EmergencySource) ([]EmergencySource, error) {
	if !plan.Enabled {
		return nil, fmt.Errorf("emergency export is disabled by the generated recovery plan")
	}
	if plan.Format != "" && plan.Format != "tar.gz.age" {
		return nil, fmt.Errorf("emergency export supports tar.gz.age")
	}
	if volumeRoot == "" {
		volumeRoot = strings.TrimSpace(policy.Source.HostPath)
	}
	if volumeRoot == "" {
		return nil, fmt.Errorf("emergency export requires the generated backup volume root")
	}
	included := make(map[string]struct{}, len(plan.IncludeClasses))
	for _, class := range plan.IncludeClasses {
		if emergencyDataClass(class) {
			included[class] = struct{}{}
		}
	}
	if len(included) == 0 {
		return nil, fmt.Errorf("emergency export includeClasses has no supported data class")
	}
	dumpClasses := make(map[string]struct{}, len(dumps))
	for _, dump := range dumps {
		if !emergencyDataClass(dump.Class) || strings.TrimSpace(dump.Path) == "" {
			return nil, fmt.Errorf("emergency dump source requires a supported data class and path")
		}
		if _, ok := included[dump.Class]; !ok {
			continue
		}
		dumpClasses[dump.Class] = struct{}{}
	}
	var sources []EmergencySource
	for _, name := range policy.Source.ManagedVolumeNames {
		if excludedEmergencyVolume(name, policy.Source.ExcludePaths) {
			continue
		}
		class := classifyEmergencyVolume(name, nil)
		if _, ok := included[class]; !ok {
			continue
		}
		if _, replaced := dumpClasses[class]; replaced {
			continue
		}
		sources = append(sources, EmergencySource{
			Class: class,
			Path:  filepath.Join(volumeRoot, name, "_data"),
		})
	}
	for _, volume := range policy.Source.ApplicationVolumes {
		if excludedEmergencyVolume(volume.VolumeName, policy.Source.ExcludePaths) {
			continue
		}
		class := classifyEmergencyVolume(volume.VolumeName, volume.DataClasses)
		if _, ok := included[class]; !ok {
			continue
		}
		if _, replaced := dumpClasses[class]; replaced {
			continue
		}
		sources = append(sources, EmergencySource{
			Class: class,
			Path:  filepath.Join(volumeRoot, volume.VolumeName, "_data"),
		})
	}
	for _, dump := range dumps {
		if _, ok := included[dump.Class]; !ok {
			continue
		}
		sources = append(sources, dump)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("generated backup contract selected no emergency export sources")
	}
	return sources, nil
}

// ExportFromContract streams the generated v2 backup contract through the
// existing encrypted portable exporter. It does not activate applications or
// prove a live restore.
func ExportFromContract(ctx context.Context, input EmergencyContractInput) (EmergencyExportResult, error) {
	target := strings.TrimSpace(input.Target)
	if target == "" && input.Plan.Target != nil {
		target = strings.TrimSpace(input.Plan.Target.Path)
	}
	largeMediaMode := input.LargeMediaMode
	if largeMediaMode == "" {
		largeMediaMode = input.Plan.LargeMediaMode
	}
	sources := input.Sources
	if len(sources) == 0 {
		var err error
		sources, err = SourcesFromContract(input.Plan, input.Policy, input.VolumeRoot, input.DumpSources)
		if err != nil {
			return EmergencyExportResult{}, err
		}
	}
	return ExportEmergency(ctx, EmergencyExportInput{
		Target:         target,
		Sources:        sources,
		Recipients:     input.Recipients,
		LargeMediaMode: largeMediaMode,
	})
}

func classifyEmergencyVolume(name string, dataClasses []string) string {
	for _, class := range dataClasses {
		if class == "secret" {
			return "secrets"
		}
	}
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "postgres"), strings.Contains(lower, "mariadb"),
		strings.Contains(lower, "mongo"), strings.HasSuffix(lower, "-databases"):
		return "database"
	case strings.Contains(lower, "redis"):
		return "cache-generated"
	case strings.Contains(lower, "pocketid"), strings.Contains(lower, "tinyauth"),
		strings.Contains(lower, "step-ca"), strings.HasSuffix(lower, "-ssh"):
		return "secrets"
	case strings.Contains(lower, "photo"):
		return "photos"
	case strings.Contains(lower, "document") || strings.Contains(lower, "paperless"):
		return "documents"
	case len(dataClasses) > 0:
		return "documents"
	default:
		return "platform-state"
	}
}

func excludedEmergencyVolume(name string, excludePaths []string) bool {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "kopia-repository") || strings.Contains(lower, "kopia-config") ||
		strings.Contains(lower, "kopia-cache") || strings.Contains(lower, "kopia-restore-staging") {
		return true
	}
	for _, exclude := range excludePaths {
		if strings.Contains(exclude, name) {
			return true
		}
	}
	return false
}
