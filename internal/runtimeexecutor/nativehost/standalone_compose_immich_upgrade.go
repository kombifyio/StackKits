package nativehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"gopkg.in/yaml.v3"
)

const (
	immichMajorMigrationUnqualified  = "immich_major_migration_unqualified"
	immichExistingDataVersionUnknown = "immich_existing_data_version_unknown"

	immichServerImagePrefix  = "ghcr.io/immich-app/immich-server:"
	immichDatabaseVolume     = "immich-postgres-database"
	immichDatabaseService    = "immich-postgres"
	immichDatabaseDataTarget = "/var/lib/postgresql/data"
	immichProjectPrefix      = "stackkit-photos-"
)

// ImmichApplySafetyError is the actionable refusal emitted before Apply can
// mutate identity custody, runtime files, or containers of a Photos workload.
// Immich runs forward-only schema migrations on its first start after a major
// release and does not support a downgrade. The hold for a major transition is
// removed in source only after a stopped full-backup and restore drill of the
// existing database passes on a real host; it accepts no caller evidence.
type ImmichApplySafetyError struct {
	reason string
}

func (e *ImmichApplySafetyError) Error() string {
	if e != nil && e.reason == immichMajorMigrationUnqualified {
		return "Immich Apply refused an unqualified major-version transition"
	}
	return "Immich Apply cannot establish the existing data version"
}

// ActionableError projects the stable recovery contract used by CLI and MCP
// machine output without exposing Docker output or runtime file contents.
func (e *ImmichApplySafetyError) ActionableError() actionableerror.Contract {
	reason := immichExistingDataVersionUnknown
	message := "StackKits found existing Immich data but cannot prove its governed runtime version."
	guidance := []string{
		"Restore the matching StackKits workspace and runtime custody for this Photos installation, then retry.",
		"If the data belongs to a backup, use the full stopped restore flow with its matching runtime version.",
	}
	if e != nil && e.reason == immichMajorMigrationUnqualified {
		reason = immichMajorMigrationUnqualified
		message = "StackKits blocked an Immich major-version transition before changing identity, runtime files, or containers. The existing Photos runtime keeps its current version."
		guidance = []string{
			"Keep the current Immich runtime and preserve its complete library and PostgreSQL data; take a full stopped backup now.",
			"The new Immich major release migrates the database forward on first start and cannot be downgraded; this release hold is removed only after the governed backup, migration and restore drill passes.",
			"For a downgrade, restore the full stopped backup and its matching prior StackKits runtime instead of applying an older image to migrated data.",
		}
	}
	return actionableerror.New("stackkit_command_denied", reason, message, guidance, false)
}

func immichSafetyError(reason string) error {
	return &ImmichApplySafetyError{reason: reason}
}

func isImmichWorkloadModule(moduleRef string) bool {
	return moduleRef == immichWorkloadModuleRef || moduleRef == immichLiteWorkloadModuleRef
}

// requireImmichApplySafety admits a fresh install or a same-major reapply of
// either Photos variant (standard and lite share the project and its data).
// Existing data with missing custody and every major transition fail closed.
func (o *osStandaloneComposeWorkloadOperations) requireImmichApplySafety(
	ctx context.Context,
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
) error {
	if !isImmichWorkloadModule(bundle.ModuleRef) {
		return nil
	}
	target, ok := standaloneComposeComponent(bundle.Components, bundle.EntryComponent)
	if !ok || target.ID != immichWorkloadUnitRef {
		return immichSafetyError(immichExistingDataVersionUnknown)
	}
	targetMajor, err := immichImageMajor(target.ImageRef)
	if err != nil {
		return immichSafetyError(immichExistingDataVersionUnknown)
	}
	project := "stackkit-" + bundle.WorkloadRef + "-" + bundle.NodeRef
	if !validImmichProject(project) {
		return immichSafetyError(immichExistingDataVersionUnknown)
	}
	directory := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "applications", project)
	currentImage, persisted, err := readPersistedImmichRuntime(directory, project)
	if err != nil {
		return immichSafetyError(immichExistingDataVersionUnknown)
	}
	if persisted {
		if err := o.verifyPersistedImmichRuntime(ctx, directory, project, currentImage); err != nil {
			return immichSafetyError(immichExistingDataVersionUnknown)
		}
		currentMajor, err := immichImageMajor(currentImage)
		if err != nil {
			return immichSafetyError(immichExistingDataVersionUnknown)
		}
		if currentMajor != targetMajor {
			return immichSafetyError(immichMajorMigrationUnqualified)
		}
		return nil
	}

	present, err := o.discoverUncustodiedImmich(ctx, project)
	if err != nil || present {
		return immichSafetyError(immichExistingDataVersionUnknown)
	}
	return nil
}

func readPersistedImmichRuntime(directory, project string) (string, bool, error) {
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("Immich runtime custody is unavailable")
	}
	if standaloneComposeRuntimeCustodyAbsent(directory) {
		return "", false, nil
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	tx, err := root.BeginTransaction()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Close() }()
	compose, composeInfo, err := tx.ReadStableBounded("compose.yaml", standaloneComposeOutputMax)
	if err != nil || composeInfo == nil || !composeInfo.Mode().IsRegular() || composeInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("Immich Compose custody is unavailable")
	}
	_, envInfo, err := tx.ReadStableBounded(standaloneComposeEnvFile, standaloneComposeOutputMax)
	if err != nil || envInfo == nil || !envInfo.Mode().IsRegular() || envInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("Immich environment custody is unavailable")
	}
	var document standaloneComposeDocument
	decoder := yaml.NewDecoder(bytes.NewReader(compose))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil || document.Name != project {
		return "", false, errors.New("Immich Compose custody differs from its project identity")
	}
	server, ok := document.Services[immichWorkloadUnitRef]
	if !ok {
		return "", false, errors.New("Immich Compose custody lacks its server")
	}
	database, ok := document.Services[immichDatabaseService]
	if !ok || !composeServiceMountsVolume(database.Volumes, immichDatabaseVolume, immichDatabaseDataTarget) {
		return "", false, errors.New("Immich Compose custody lacks its database data")
	}
	if _, ok := document.Volumes[immichDatabaseVolume]; !ok {
		return "", false, errors.New("Immich Compose custody lacks its database volume")
	}
	if _, err := immichImageMajor(server.Image); err != nil {
		return "", false, err
	}
	return server.Image, true, nil
}

// standaloneComposeRuntimeCustodyAbsent reports a project directory that holds
// neither persisted Compose file nor its environment. Under the opentofu and
// terramate generation targets the wrapper root <project>/opentofu is prepared
// before the native project is first persisted, so the directory alone does
// not prove an earlier install. Existing data without custody is still found
// by the container and volume discovery that follows. Any other state,
// including an unreadable entry, is left to the strict custody read.
func standaloneComposeRuntimeCustodyAbsent(directory string) bool {
	for _, name := range []string{"compose.yaml", standaloneComposeEnvFile} {
		if _, err := os.Lstat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func composeServiceMountsVolume(volumes []any, source, target string) bool {
	for _, raw := range volumes {
		switch mount := raw.(type) {
		case string:
			parts := strings.Split(mount, ":")
			if len(parts) >= 2 && parts[0] == source && parts[1] == target {
				return true
			}
		case map[string]any:
			if mount["type"] == "volume" && mount["source"] == source && mount["target"] == target {
				return true
			}
		}
	}
	return false
}

func (o *osStandaloneComposeWorkloadOperations) verifyPersistedImmichRuntime(
	ctx context.Context,
	directory, project, image string,
) error {
	probe := standaloneComposeProject{name: project, directory: directory}
	raw, err := o.runner.Run(ctx, standaloneComposeArgs(probe, "ps"), directory)
	if err != nil {
		return err
	}
	statuses, err := parseStandaloneComposeStatusesAllowingEmpty(raw)
	if err != nil {
		return err
	}
	status, ok := statuses[immichWorkloadUnitRef]
	if !ok {
		// Persisted runtime authority still proves this is existing data; an
		// absent stopped server must never turn it into a fresh install.
		return nil
	}
	if status.Image != image {
		return errors.New("Immich server container differs from persisted runtime custody")
	}
	labels := standaloneComposeCSVMap(status.Labels)
	if labels["com.docker.compose.project"] != project || labels["com.docker.compose.service"] != immichWorkloadUnitRef {
		return errors.New("Immich server container labels differ from persisted runtime custody")
	}
	return nil
}

func (o *osStandaloneComposeWorkloadOperations) discoverUncustodiedImmich(ctx context.Context, project string) (bool, error) {
	raw, err := o.runner.Run(ctx, immichContainerDiscoveryArgs(project), o.workspaceRoot)
	if err != nil {
		return false, err
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := validateImmichContainerDiscovery(raw, project); err != nil {
			return false, err
		}
		return true, nil
	}
	raw, err = o.runner.Run(ctx, immichVolumeDiscoveryArgs(project), o.workspaceRoot)
	if err != nil {
		return false, err
	}
	return validateImmichVolumeDiscovery(raw, project)
}

func immichContainerDiscoveryArgs(project string) []string {
	return []string{
		"container", "ls", "--all",
		"--filter", "label=com.docker.compose.project=" + project,
		"--filter", "label=com.docker.compose.service=" + immichWorkloadUnitRef,
		"--format", "{{json .}}",
	}
}

func immichVolumeDiscoveryArgs(project string) []string {
	return []string{
		"volume", "ls", "--filter", "name=^" + project + "_" + immichDatabaseVolume + "$", "--format", "{{.Name}}",
	}
}

func validImmichDiscoveryQuery(args []string) bool {
	if len(args) == 9 && args[0] == "container" && args[1] == "ls" && args[2] == "--all" &&
		args[3] == "--filter" && strings.HasPrefix(args[4], "label=com.docker.compose.project=") &&
		args[5] == "--filter" && args[6] == "label=com.docker.compose.service="+immichWorkloadUnitRef &&
		args[7] == "--format" && args[8] == "{{json .}}" {
		return validImmichProject(strings.TrimPrefix(args[4], "label=com.docker.compose.project="))
	}
	if len(args) == 6 && args[0] == "volume" && args[1] == "ls" && args[2] == "--filter" &&
		strings.HasPrefix(args[3], "name=^") && strings.HasSuffix(args[3], "_"+immichDatabaseVolume+"$") &&
		args[4] == "--format" && args[5] == "{{.Name}}" {
		return validImmichProject(strings.TrimSuffix(strings.TrimPrefix(args[3], "name=^"), "_"+immichDatabaseVolume+"$"))
	}
	return false
}

func validImmichProject(project string) bool {
	if !strings.HasPrefix(project, immichProjectPrefix) || len(project) > 128 {
		return false
	}
	for _, value := range project {
		if (value < 'a' || value > 'z') && (value < '0' || value > '9') && value != '-' {
			return false
		}
	}
	return true
}

func validateImmichContainerDiscovery(raw []byte, project string) error {
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 1 {
		return errors.New("Immich discovery returned multiple server containers")
	}
	var result struct {
		ID     string `json:"ID"`
		Image  string `json:"Image"`
		Labels string `json:"Labels"`
	}
	if err := json.Unmarshal(lines[0], &result); err != nil || strings.TrimSpace(result.ID) == "" {
		return errors.New("Immich container discovery is malformed")
	}
	if _, err := immichImageMajor(result.Image); err != nil {
		return errors.New("Immich container image is not governed")
	}
	labels := standaloneComposeCSVMap(result.Labels)
	if labels["com.docker.compose.project"] != project || labels["com.docker.compose.service"] != immichWorkloadUnitRef {
		return errors.New("Immich container discovery labels differ")
	}
	return nil
}

func validateImmichVolumeDiscovery(raw []byte, project string) (bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return false, nil
	}
	if strings.ContainsAny(trimmed, "\r\n") || trimmed != project+"_"+immichDatabaseVolume {
		return false, errors.New("Immich database volume discovery is malformed")
	}
	return true, nil
}

// immichImageMajor reads the major release of a governed Immich server image
// reference, ghcr.io/immich-app/immich-server:vMAJOR.MINOR.PATCH[@digest].
func immichImageMajor(image string) (int, error) {
	if !strings.HasPrefix(image, immichServerImagePrefix) {
		return 0, errors.New("Immich image repository is not governed")
	}
	version := strings.TrimPrefix(image, immichServerImagePrefix)
	if tag, _, found := strings.Cut(version, "@"); found {
		version = tag
	}
	numeric, ok := strings.CutPrefix(version, "v")
	if !ok {
		return 0, fmt.Errorf("Immich image tag %q has no governed release version", version)
	}
	parts := strings.Split(numeric, ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("Immich image tag %q has no governed release version", version)
	}
	for _, part := range parts {
		if part == "" {
			return 0, fmt.Errorf("Immich image tag %q is not numeric", version)
		}
		for _, value := range part {
			if value < '0' || value > '9' {
				return 0, fmt.Errorf("Immich image tag %q is not numeric", version)
			}
		}
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major <= 0 {
		return 0, fmt.Errorf("Immich image tag %q has no governed major version", version)
	}
	return major, nil
}
