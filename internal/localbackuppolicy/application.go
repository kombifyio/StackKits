package localbackuppolicy

import (
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
)

// ApplicationVolume is the canonical source-policy record for one persistent
// volume selected from a Standalone-Compose application workload. Identity and
// placement stay in the artifact so consumers cannot widen the source to a
// similarly named volume on another node.
type ApplicationVolume struct {
	WorkloadRef    string   `json:"workloadRef"`
	SiteRef        string   `json:"siteRef"`
	NodeRef        string   `json:"nodeRef"`
	ComposeProject string   `json:"composeProject"`
	ComponentRef   string   `json:"componentRef"`
	VolumeRef      string   `json:"volumeRef"`
	LogicalName    string   `json:"logicalName"`
	VolumeName     string   `json:"volumeName"`
	Target         string   `json:"target"`
	Class          string   `json:"class"`
	Backup         bool     `json:"backup"`
	DataClasses    []string `json:"dataClasses"`
	DataBindingRef string   `json:"dataBindingRef"`
}

var (
	applicationRefPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	applicationVolumeNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
)

// StandaloneComposeProjectName is the shared project-name derivation used by
// the runtime renderer, backup projection, and restore authority.
func StandaloneComposeProjectName(workloadRef, nodeRef string) string {
	return "stackkit-" + workloadRef + "-" + nodeRef
}

// StandaloneComposeLogicalVolumeName is the shared unqualified Compose volume
// name derivation for one component volume.
func StandaloneComposeLogicalVolumeName(componentRef, volumeRef string) string {
	return componentRef + "-" + volumeRef
}

// StandaloneComposeVolumeName is the shared Docker Compose-qualified volume
// name derivation for one Standalone-Compose application volume.
func StandaloneComposeVolumeName(workloadRef, nodeRef, componentRef, volumeRef string) string {
	return StandaloneComposeProjectName(workloadRef, nodeRef) + "_" +
		StandaloneComposeLogicalVolumeName(componentRef, volumeRef)
}

func cloneApplicationVolumes(applicationVolumes []ApplicationVolume) []ApplicationVolume {
	if len(applicationVolumes) == 0 {
		return nil
	}
	cloned := make([]ApplicationVolume, len(applicationVolumes))
	for index, application := range applicationVolumes {
		cloned[index] = application
		cloned[index].DataClasses = append([]string(nil), application.DataClasses...)
	}
	return cloned
}

func canonicalApplicationVolumes(applicationVolumes []ApplicationVolume) ([]ApplicationVolume, error) {
	applications := cloneApplicationVolumes(applicationVolumes)
	if len(applications) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(applications))
	for index := range applications {
		application := &applications[index]
		if err := validateApplicationVolume(*application); err != nil {
			return nil, fmt.Errorf("application volume %d: %w", index, err)
		}
		if _, exists := seen[application.VolumeName]; exists {
			return nil, fmt.Errorf("application volume %q is selected more than once", application.VolumeName)
		}
		seen[application.VolumeName] = struct{}{}
		slices.Sort(application.DataClasses)
		for classIndex := 1; classIndex < len(application.DataClasses); classIndex++ {
			if application.DataClasses[classIndex] == application.DataClasses[classIndex-1] {
				return nil, fmt.Errorf("application volume %q repeats data class %q", application.VolumeName, application.DataClasses[classIndex])
			}
		}
	}
	slices.SortFunc(applications, func(left, right ApplicationVolume) int {
		if left.VolumeName < right.VolumeName {
			return -1
		}
		if left.VolumeName > right.VolumeName {
			return 1
		}
		return 0
	})
	return applications, nil
}

func applicationVolumeNames(applicationVolumes []ApplicationVolume) []string {
	names := make([]string, len(applicationVolumes))
	for index, application := range applicationVolumes {
		names[index] = application.VolumeName
	}
	return names
}

// ApplicationVolumesForTarget narrows the compiler aggregate to one exact
// node-local policy target. It preserves the canonical order and deep-copy
// semantics used by the policy codec.
func ApplicationVolumesForTarget(applicationVolumes []ApplicationVolume, siteRef, nodeRef string) ([]ApplicationVolume, error) {
	applications, err := canonicalApplicationVolumes(applicationVolumes)
	if err != nil {
		return nil, err
	}
	selected := make([]ApplicationVolume, 0, len(applications))
	for _, application := range applications {
		if application.SiteRef == siteRef && application.NodeRef == nodeRef {
			selected = append(selected, application)
		}
	}
	return selected, nil
}

func validateApplicationVolume(application ApplicationVolume) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "workloadRef", value: application.WorkloadRef},
		{name: "siteRef", value: application.SiteRef},
		{name: "nodeRef", value: application.NodeRef},
		{name: "componentRef", value: application.ComponentRef},
		{name: "volumeRef", value: application.VolumeRef},
		{name: "dataBindingRef", value: application.DataBindingRef},
	} {
		if !applicationRefPattern.MatchString(field.value) {
			return fmt.Errorf("%s must be a non-empty portable contract ID", field.name)
		}
	}
	if !applicationVolumeNameRegex.MatchString(application.ComposeProject) ||
		!applicationVolumeNameRegex.MatchString(application.LogicalName) ||
		!applicationVolumeNameRegex.MatchString(application.VolumeName) {
		return fmt.Errorf("Compose volume identities must use the portable Docker name subset")
	}
	if application.ComposeProject != StandaloneComposeProjectName(application.WorkloadRef, application.NodeRef) {
		return fmt.Errorf("composeProject does not match the Standalone-Compose workload and node")
	}
	if application.LogicalName != StandaloneComposeLogicalVolumeName(application.ComponentRef, application.VolumeRef) {
		return fmt.Errorf("logicalName does not match componentRef and volumeRef")
	}
	if application.VolumeName != StandaloneComposeVolumeName(application.WorkloadRef, application.NodeRef, application.ComponentRef, application.VolumeRef) {
		return fmt.Errorf("volumeName does not match the qualified Compose volume identity")
	}
	if application.Class != "persistent" || !application.Backup {
		return fmt.Errorf("must describe a persistent backup-enabled volume")
	}
	if application.Target == "" || application.Target[0] != '/' || path.Clean(application.Target) != application.Target {
		return fmt.Errorf("target must be a canonical absolute path")
	}
	if len(application.DataClasses) == 0 {
		return fmt.Errorf("dataClasses must not be empty")
	}
	for _, class := range application.DataClasses {
		switch class {
		case "public", "internal", "personal", "sensitive", "secret":
		default:
			return fmt.Errorf("dataClasses contains an unsupported class")
		}
	}
	if _, isCoreVolume := slices.BinarySearch(managedVolumeNames, application.VolumeName); isCoreVolume {
		return fmt.Errorf("volumeName collides with a governed Core volume")
	}
	return nil
}

func validateSource(source Source, target Target) error {
	applications, err := canonicalApplicationVolumes(source.ApplicationVolumes)
	if err != nil {
		return err
	}
	runtimes, err := canonicalApplicationRuntimes(source.ApplicationRuntimes)
	if err != nil {
		return err
	}
	for index, application := range applications {
		if application.SiteRef != target.SiteRef || application.NodeRef != target.NodeRef {
			return fmt.Errorf("application volume %d is bound to %s/%s instead of %s/%s", index, application.SiteRef, application.NodeRef, target.SiteRef, target.NodeRef)
		}
	}
	for index, runtime := range runtimes {
		if runtime.SiteRef != target.SiteRef || runtime.NodeRef != target.NodeRef {
			return fmt.Errorf("application runtime %d is bound to %s/%s instead of %s/%s", index, runtime.SiteRef, runtime.NodeRef, target.SiteRef, target.NodeRef)
		}
	}
	if err := validateSourceProjection(source); err != nil {
		return err
	}
	if !reflect.DeepEqual(source.ApplicationVolumes, applications) {
		return fmt.Errorf("applicationVolumes are not in canonical order or encoding")
	}
	if !reflect.DeepEqual(source.ApplicationRuntimes, runtimes) {
		return fmt.Errorf("applicationRuntimes are not in canonical order or encoding")
	}
	return nil
}

// ValidateSourceProjection verifies the aggregate compiler projection before
// a renderer narrows it to one node-local policy instance.
func ValidateSourceProjection(source Source) error {
	return validateSourceProjection(source)
}

func validateSourceProjection(source Source) error {
	applications, err := canonicalApplicationVolumes(source.ApplicationVolumes)
	if err != nil {
		return err
	}
	runtimes, err := canonicalApplicationRuntimes(source.ApplicationRuntimes)
	if err != nil {
		return err
	}
	var expected Source
	if source.CoreModuleRef == "" {
		expected, err = GovernedSourceWithApplicationVolumesAndRuntimes(applications, runtimes)
	} else {
		expected, err = GovernedSourceWithApplicationVolumesAndRuntimesForCoreModule(source.CoreModuleRef, applications, runtimes)
	}
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(source, expected) {
		return fmt.Errorf("source topology does not match the governed Core and application projection")
	}
	if !reflect.DeepEqual(source.ApplicationVolumes, applications) {
		return fmt.Errorf("applicationVolumes are not in canonical order or encoding")
	}
	if !reflect.DeepEqual(source.ApplicationRuntimes, runtimes) {
		return fmt.Errorf("applicationRuntimes are not in canonical order or encoding")
	}
	return nil
}

func validateApplicationProjection(applications []ApplicationVolume, runtimes []ApplicationRuntime) error {
	if len(applications) == 0 && len(runtimes) != 0 {
		return fmt.Errorf("application runtime graph has no selected backup volume")
	}
	runtimeByKey := make(map[string]ApplicationRuntime, len(runtimes))
	for _, runtime := range runtimes {
		runtimeByKey[applicationRuntimeKey(runtime)] = runtime
	}
	for _, application := range applications {
		key := application.WorkloadRef + "\x00" + application.SiteRef + "\x00" + application.NodeRef + "\x00" + application.ComposeProject
		runtime, exists := runtimeByKey[key]
		if !exists {
			return fmt.Errorf("application volume %q has no selected runtime graph", application.VolumeName)
		}
		foundComponent := false
		for _, component := range runtime.Components {
			if component.ComponentRef == application.ComponentRef {
				foundComponent = true
				break
			}
		}
		if !foundComponent {
			return fmt.Errorf("application volume %q references component %q outside its runtime graph", application.VolumeName, application.ComponentRef)
		}
	}
	for _, runtime := range runtimes {
		selected := false
		for _, application := range applications {
			if application.WorkloadRef == runtime.WorkloadRef && application.SiteRef == runtime.SiteRef &&
				application.NodeRef == runtime.NodeRef && application.ComposeProject == runtime.ComposeProject {
				selected = true
				break
			}
		}
		if !selected {
			return fmt.Errorf("application runtime %q has no selected backup volume", runtime.ComposeProject)
		}
	}
	return nil
}
