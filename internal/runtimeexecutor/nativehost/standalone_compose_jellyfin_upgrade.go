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
	jellyfinMajorMigrationUnqualified  = "jellyfin_major_migration_unqualified"
	jellyfinExistingDataVersionUnknown = "jellyfin_existing_data_version_unknown"
)

// JellyfinApplySafetyError is the actionable refusal emitted before Apply can
// mutate identity custody, runtime files, or containers. It deliberately does
// not accept caller-supplied migration evidence: the release qualification
// hold is removed in source only after the stopped full-backup lane passes.
type JellyfinApplySafetyError struct {
	reason string
}

func (e *JellyfinApplySafetyError) Error() string {
	if e != nil && e.reason == jellyfinMajorMigrationUnqualified {
		return "Jellyfin Apply refused an unqualified major-version transition"
	}
	return "Jellyfin Apply cannot establish the existing data version"
}

// ActionableError projects the stable recovery contract used by CLI and MCP
// machine output without exposing Docker output or runtime file contents.
func (e *JellyfinApplySafetyError) ActionableError() actionableerror.Contract {
	reason := jellyfinExistingDataVersionUnknown
	message := "StackKits found existing Jellyfin data but cannot prove its governed runtime version."
	guidance := []string{
		"Restore the matching StackKits workspace and runtime custody for this Media installation, then retry.",
		"If the data belongs to a backup, use the full stopped restore flow with its matching runtime version.",
	}
	if e != nil && e.reason == jellyfinMajorMigrationUnqualified {
		reason = jellyfinMajorMigrationUnqualified
		message = "StackKits blocked a Jellyfin major-version transition before changing identity, runtime files, or containers."
		guidance = []string{
			"Keep the current Jellyfin runtime stopped and preserve its complete config and database data.",
			"Complete the governed username-collision, full-backup, and old-plugin checks before this release hold is removed.",
			"For a downgrade, restore the full stopped backup and its matching prior StackKits runtime instead of applying an older image to migrated data.",
		}
	}
	return actionableerror.New("stackkit_command_denied", reason, message, guidance, false)
}

func jellyfinSafetyError(reason string) error {
	return &JellyfinApplySafetyError{reason: reason}
}

// requireJellyfinApplySafety admits a fresh install or a same-major reapply.
// Existing data with missing custody and every major transition fail closed.
func (o *osStandaloneComposeWorkloadOperations) requireJellyfinApplySafety(
	ctx context.Context,
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
) error {
	if bundle.ModuleRef != jellyfinWorkloadModuleRef {
		return nil
	}
	target, ok := standaloneComposeComponent(bundle.Components, bundle.EntryComponent)
	if !ok || target.ID != "jellyfin" {
		return jellyfinSafetyError(jellyfinExistingDataVersionUnknown)
	}
	targetMajor, err := jellyfinImageMajor(target.ImageRef)
	if err != nil {
		return jellyfinSafetyError(jellyfinExistingDataVersionUnknown)
	}
	project := "stackkit-" + bundle.WorkloadRef + "-" + bundle.NodeRef
	directory := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "applications", project)
	currentImage, persisted, err := readPersistedJellyfinRuntime(directory, project)
	if err != nil {
		return jellyfinSafetyError(jellyfinExistingDataVersionUnknown)
	}
	if persisted {
		if err := o.verifyPersistedJellyfinRuntime(ctx, directory, project, currentImage); err != nil {
			return jellyfinSafetyError(jellyfinExistingDataVersionUnknown)
		}
		currentMajor, err := jellyfinImageMajor(currentImage)
		if err != nil {
			return jellyfinSafetyError(jellyfinExistingDataVersionUnknown)
		}
		if currentMajor != targetMajor {
			return jellyfinSafetyError(jellyfinMajorMigrationUnqualified)
		}
		return nil
	}

	present, err := o.discoverUncustodiedJellyfin(ctx, project)
	if err != nil || present {
		return jellyfinSafetyError(jellyfinExistingDataVersionUnknown)
	}
	return nil
}

func readPersistedJellyfinRuntime(directory, project string) (string, bool, error) {
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("Jellyfin runtime custody is unavailable")
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
		return "", false, errors.New("Jellyfin Compose custody is unavailable")
	}
	_, envInfo, err := tx.ReadStableBounded(standaloneComposeEnvFile, standaloneComposeOutputMax)
	if err != nil || envInfo == nil || !envInfo.Mode().IsRegular() || envInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("Jellyfin environment custody is unavailable")
	}
	var document standaloneComposeDocument
	decoder := yaml.NewDecoder(bytes.NewReader(compose))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil || document.Name != project || len(document.Services) != 1 {
		return "", false, errors.New("Jellyfin Compose custody differs from its project identity")
	}
	service, ok := document.Services["jellyfin"]
	if !ok || !jellyfinServiceMountsConfig(service.Volumes) {
		return "", false, errors.New("Jellyfin Compose custody lacks its config data")
	}
	if _, ok := document.Volumes["jellyfin-config"]; !ok {
		return "", false, errors.New("Jellyfin Compose custody lacks its config volume")
	}
	if _, err := jellyfinImageMajor(service.Image); err != nil {
		return "", false, err
	}
	return service.Image, true, nil
}

func jellyfinServiceMountsConfig(volumes []any) bool {
	for _, raw := range volumes {
		switch mount := raw.(type) {
		case string:
			parts := strings.Split(mount, ":")
			if len(parts) >= 2 && parts[0] == "jellyfin-config" && parts[1] == "/config" {
				return true
			}
		case map[string]any:
			if mount["type"] == "volume" && mount["source"] == "jellyfin-config" && mount["target"] == "/config" {
				return true
			}
		}
	}
	return false
}

func (o *osStandaloneComposeWorkloadOperations) verifyPersistedJellyfinRuntime(
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
	if len(statuses) == 0 {
		// Persisted runtime authority still proves this is existing data; an
		// absent stopped container must never turn it into a fresh install.
		return nil
	}
	status, ok := statuses["jellyfin"]
	if !ok || len(statuses) != 1 || status.Image != image {
		return errors.New("Jellyfin container differs from persisted runtime custody")
	}
	labels := standaloneComposeCSVMap(status.Labels)
	if labels["com.docker.compose.project"] != project || labels["com.docker.compose.service"] != "jellyfin" {
		return errors.New("Jellyfin container labels differ from persisted runtime custody")
	}
	return nil
}

func (o *osStandaloneComposeWorkloadOperations) discoverUncustodiedJellyfin(ctx context.Context, project string) (bool, error) {
	containerArgs := jellyfinContainerDiscoveryArgs(project)
	raw, err := o.runner.Run(ctx, containerArgs, o.workspaceRoot)
	if err != nil {
		return false, err
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := validateJellyfinContainerDiscovery(raw, project); err != nil {
			return false, err
		}
		return true, nil
	}
	raw, err = o.runner.Run(ctx, jellyfinVolumeDiscoveryArgs(project), o.workspaceRoot)
	if err != nil {
		return false, err
	}
	return validateJellyfinVolumeDiscovery(raw, project)
}

func jellyfinContainerDiscoveryArgs(project string) []string {
	return []string{
		"container", "ls", "--all",
		"--filter", "label=com.docker.compose.project=" + project,
		"--filter", "label=com.docker.compose.service=jellyfin",
		"--format", "{{json .}}",
	}
}

func jellyfinVolumeDiscoveryArgs(project string) []string {
	return []string{
		"volume", "ls", "--filter", "name=^" + project + "_jellyfin-config$", "--format", "{{.Name}}",
	}
}

func validJellyfinDiscoveryQuery(args []string) bool {
	if len(args) == 9 && args[0] == "container" && args[1] == "ls" && args[2] == "--all" &&
		args[3] == "--filter" && strings.HasPrefix(args[4], "label=com.docker.compose.project=") &&
		args[5] == "--filter" && args[6] == "label=com.docker.compose.service=jellyfin" &&
		args[7] == "--format" && args[8] == "{{json .}}" {
		project := strings.TrimPrefix(args[4], "label=com.docker.compose.project=")
		return validJellyfinProject(project)
	}
	if len(args) == 6 && args[0] == "volume" && args[1] == "ls" && args[2] == "--filter" &&
		strings.HasPrefix(args[3], "name=^") && strings.HasSuffix(args[3], "_jellyfin-config$") &&
		args[4] == "--format" && args[5] == "{{.Name}}" {
		project := strings.TrimSuffix(strings.TrimPrefix(args[3], "name=^"), "_jellyfin-config$")
		return validJellyfinProject(project)
	}
	return false
}

func validJellyfinProject(project string) bool {
	if !strings.HasPrefix(project, "stackkit-media-") || len(project) > 128 {
		return false
	}
	for _, value := range project {
		if (value < 'a' || value > 'z') && (value < '0' || value > '9') && value != '-' {
			return false
		}
	}
	return true
}

func validateJellyfinContainerDiscovery(raw []byte, project string) error {
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 1 {
		return errors.New("Jellyfin discovery returned multiple containers")
	}
	var result struct {
		ID     string `json:"ID"`
		Image  string `json:"Image"`
		Labels string `json:"Labels"`
	}
	if err := json.Unmarshal(lines[0], &result); err != nil || strings.TrimSpace(result.ID) == "" {
		return errors.New("Jellyfin container discovery is malformed")
	}
	if _, err := jellyfinImageMajor(result.Image); err != nil {
		return errors.New("Jellyfin container image is not governed")
	}
	labels := standaloneComposeCSVMap(result.Labels)
	if labels["com.docker.compose.project"] != project || labels["com.docker.compose.service"] != "jellyfin" {
		return errors.New("Jellyfin container discovery labels differ")
	}
	return nil
}

func validateJellyfinVolumeDiscovery(raw []byte, project string) (bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return false, nil
	}
	if strings.ContainsAny(trimmed, "\r\n") || trimmed != project+"_jellyfin-config" {
		return false, errors.New("Jellyfin config volume discovery is malformed")
	}
	return true, nil
}

func jellyfinImageMajor(image string) (int, error) {
	const prefix = "docker.io/jellyfin/jellyfin:"
	if !strings.HasPrefix(image, prefix) {
		return 0, errors.New("Jellyfin image repository is not governed")
	}
	version := strings.TrimPrefix(image, prefix)
	if tag, _, found := strings.Cut(version, "@"); found {
		version = tag
	}
	majorText, _, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil || major <= 0 || version == majorText {
		return 0, fmt.Errorf("Jellyfin image tag %q has no governed major version", version)
	}
	for _, value := range version {
		if (value < '0' || value > '9') && value != '.' {
			return 0, fmt.Errorf("Jellyfin image tag %q is not numeric", version)
		}
	}
	return major, nil
}
