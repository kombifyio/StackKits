package opentofu

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
)

const (
	// UnitRef is the render unit, and runtime target unit, this executor owns.
	UnitRef = "opentofu"
	// TerramateUnitRef is the Core unit under the terramate target: the same
	// OpenTofu root plus its Terramate stack file.
	TerramateUnitRef = "terramate"
	// ArtifactKind and ArtifactFormat identify the executable root artifact.
	ArtifactKind   = "opentofu"
	ArtifactFormat = "hcl"
	ArtifactMode   = "0640"

	// RootDirName is the per-module root directory below the module's native
	// runtime directory: .stackkit/runtime/<runtimeDir>/opentofu.
	RootDirName = "opentofu"
	// ConfigFile, StateFile, and ComposeFile are the root files the
	// executor-state checkpoint captures.
	ConfigFile  = "main.tf"
	StateFile   = "terraform.tfstate"
	ComposeFile = "compose.yaml"
	// StackFile is the Terramate stack file placed beside main.tf.
	StackFile = "stack.tm.hcl"
	// EnvFile is the private interpolation file of a workload project; the
	// workload root references it and never embeds it.
	EnvFile = ".env"
	// PlanFile is the saved plan between plan and apply; it is removed after
	// apply because it embeds the full configuration.
	PlanFile = "tfplan"
	// CLIConfigFile is the generated offline CLI configuration.
	CLIConfigFile = "stackkit.tofurc"
	// MarkerFile binds a root directory to the module that owns it.
	MarkerFile = "stackkit-root.json"
	// ObservationFile holds the last apply observation; its digest is the
	// RuntimeOutcome observation digest.
	ObservationFile = "stackkit-apply-observation.json"

	// RootMarkerSchemaVersion versions the root marker document.
	RootMarkerSchemaVersion  = "stackkit.opentofu-root/v1"
	observationSchemaVersion = "stackkit.opentofu-apply-observation/v1"
	maxArtifactBytes         = 1 << 20
)

// Root kinds. Core roots carry no kind in their marker (P1.1 layout).
const (
	// RootKindWorkload is the executor-materialized root of one standalone
	// workload project: .stackkit/runtime/applications/<project>/opentofu.
	RootKindWorkload = "workload"
	// RootKindModule is the executor-materialized contract root of one edge
	// or federation owner: .stackkit/runtime/modules/<moduleRef>/opentofu.
	RootKindModule = "module"

	// ApplicationsDir and ModulesDir are the runtime subtrees of the
	// executor-materialized roots (terramatestackgraph runtime roots).
	ApplicationsDir = "applications"
	ModulesDir      = "modules"
)

var runtimeDirPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// RootRelativePath returns the slash-separated workspace-relative root of the
// module whose native runtime directory is runtimeDir.
func RootRelativePath(runtimeDir string) (string, error) {
	if !runtimeDirPattern.MatchString(runtimeDir) || runtimeDir == ApplicationsDir || runtimeDir == ModulesDir {
		return "", fmt.Errorf("OpenTofu runtime directory %q is not portable", runtimeDir)
	}
	return path.Join(".stackkit", "runtime", runtimeDir, RootDirName), nil
}

// WorkloadRootRelativePath is the root of the standalone workload project
// stackkit-<workloadRef>-<nodeRef>, the stack graph's runtimeRoot for it.
func WorkloadRootRelativePath(project string) (string, error) {
	if !runtimeDirPattern.MatchString(project) {
		return "", fmt.Errorf("OpenTofu workload project %q is not portable", project)
	}
	return path.Join(".stackkit", "runtime", ApplicationsDir, project, RootDirName), nil
}

// ModuleRootRelativePath is the contract root of one edge or federation
// owner module, the stack graph's runtimeRoot for it.
func ModuleRootRelativePath(moduleRef string) (string, error) {
	if !runtimeDirPattern.MatchString(moduleRef) {
		return "", fmt.Errorf("OpenTofu owner module %q is not portable", moduleRef)
	}
	return path.Join(".stackkit", "runtime", ModulesDir, moduleRef, RootDirName), nil
}

// RootMarker is the secret-free identity of one materialized root.
type RootMarker struct {
	SchemaVersion  string `json:"schemaVersion"`
	ModuleRef      string `json:"moduleRef"`
	InstanceRef    string `json:"instanceRef"`
	RuntimeDir     string `json:"runtimeDir"`
	ComposeProject string `json:"composeProject"`
	// Kind is empty for a Core root, else RootKindWorkload or RootKindModule.
	Kind string `json:"kind,omitempty"`
}

// ReadRootMarker reads and validates the marker of one root directory.
func ReadRootMarker(rootDir string) (RootMarker, error) {
	raw, err := os.ReadFile(filepath.Join(rootDir, MarkerFile))
	if err != nil {
		return RootMarker{}, fmt.Errorf("read OpenTofu root marker: %w", err)
	}
	var marker RootMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return RootMarker{}, fmt.Errorf("decode OpenTofu root marker: %w", err)
	}
	parent := filepath.Base(filepath.Dir(filepath.Dir(rootDir)))
	layout := marker.Kind == "" && parent == "runtime" ||
		marker.Kind == RootKindWorkload && parent == ApplicationsDir ||
		marker.Kind == RootKindModule && parent == ModulesDir
	if marker.SchemaVersion != RootMarkerSchemaVersion || marker.ModuleRef == "" || marker.InstanceRef == "" || !layout ||
		!runtimeDirPattern.MatchString(marker.RuntimeDir) || filepath.Base(filepath.Dir(rootDir)) != marker.RuntimeDir {
		return RootMarker{}, errors.New("OpenTofu root marker does not identify this root")
	}
	return marker, nil
}
