package opentofu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

var (
	// ErrStage2RootExcluded is returned when Stage-1 Compose adoption encounters
	// a root carrying the RootExecutionNativeDocker marker (ADR-0054 §1).
	ErrStage2RootExcluded = errors.New("stage-2 native-docker OpenTofu root is excluded from automatic Compose adoption")
	// ErrLegacyPaaSRootExcluded is returned when Stage-1 adoption encounters a
	// retired Layer-2 PaaS root marker (Komodo, Coolify-core, Dokploy, Runtipi).
	ErrLegacyPaaSRootExcluded = errors.New("legacy Layer-2 PaaS root cannot be auto-adopted; follow write-fenced Kopia restore runbook")
	// ErrAdoptionComposeMismatch is returned when the existing Compose file does
	// not match the target Stage-1 Compose payload precondition.
	ErrAdoptionComposeMismatch = errors.New("existing Compose payload differs from target Stage-1 adoption payload")
	// ErrAdoptionRecreatedContainer is returned when post-apply evidence detects
	// a changed container ID or dropped volume after adoption.
	ErrAdoptionRecreatedContainer = errors.New("post-apply adoption verification detected container recreation or volume mismatch")
)

// RuntimeIdentitySnapshot captures container IDs and volume bindings of one
// Compose project before or after Stage-1 adoption.
type RuntimeIdentitySnapshot struct {
	ComposeProject string              `json:"composeProject"`
	Containers     map[string]string   `json:"containers"` // service -> full container ID
	StartedAt      map[string]string   `json:"startedAt"`
	Volumes        map[string][]string `json:"volumes"` // service -> sorted volume names/sources
}

// AdoptionPrecondition describes the inputs required to adopt an existing
// unmanaged or nativehost-managed Compose project into an OpenTofu Stage-1 root
// without recreating containers or touching Stage-2/PaaS roots.
type AdoptionPrecondition struct {
	WorkspaceRoot  string
	RootRelative   string
	ComposeProject string
	TargetCompose  []byte
	// RequireExactComposeBytes enforces byte-for-byte equality with the existing
	// ../compose.yaml when present; defaults to true via CheckAdoptionPrecondition.
	AllowMissingComposeFile bool
	Environment             []string
}

// AdoptionEvidence is the post-apply proof that no container was recreated and
// all volumes stayed bound identically.
type AdoptionEvidence struct {
	ComposeProject      string                  `json:"composeProject"`
	ComposeSHA256       string                  `json:"composeSha256"`
	CommandBoundaryFlag string                  `json:"commandBoundaryFlag"`
	Before              RuntimeIdentitySnapshot `json:"before"`
	After               RuntimeIdentitySnapshot `json:"after"`
	ContainerIDsEqual   bool                    `json:"containerIdsEqual"`
	VolumeIDsPreserved  bool                    `json:"volumeIdsPreserved"`
	RecreatedServices   []string                `json:"recreatedServices,omitempty"`
	VolumeDiffServices  []string                `json:"volumeDiffServices,omitempty"`
}

// CheckAdoptionPrecondition enforces root-marker exclusion (refusing
// RootExecutionNativeDocker and retired Layer-2 PaaS roots fail-closed) AND
// verifies Compose payload parity BEFORE any OpenTofu write or apply.
func CheckAdoptionPrecondition(pre AdoptionPrecondition) (string, error) {
	if strings.TrimSpace(pre.WorkspaceRoot) == "" || strings.TrimSpace(pre.RootRelative) == "" || strings.TrimSpace(pre.ComposeProject) == "" {
		return "", errors.New("adoption precondition requires workspace, rootRelative, and composeProject")
	}
	if len(pre.TargetCompose) == 0 {
		return "", errors.New("adoption precondition requires non-empty target Compose payload")
	}
	absWorkspace, err := filepath.Abs(pre.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	rootDir := filepath.Join(absWorkspace, filepath.FromSlash(pre.RootRelative))

	// 1. Root-marker exclusion keyed on RootExecutionNativeDocker (NOT module
	//    identity) and retired Layer-2 PaaS executions.
	if marker, err := ReadRootMarker(rootDir); err == nil {
		if marker.Execution == RootExecutionNativeDocker {
			return "", fmt.Errorf("%w (%s): unset %s and run the volume-preserving native-to-wrapper switch", ErrStage2RootExcluded, pre.RootRelative, NativeModulesEnv)
		}
		if IsPlatformProviderExecution(marker.Execution) {
			return "", fmt.Errorf("%w (%s, execution=%s): see docs/runbooks/paas-to-opentofu-restore-cutover.md", ErrLegacyPaaSRootExcluded, pre.RootRelative, marker.Execution)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read existing root marker before adoption: %w", err)
	} else {
		// Mirror workload.go:376 fallback check when main.tf exists without marker.
		if config, readErr := os.ReadFile(filepath.Join(rootDir, ConfigFile)); readErr == nil {
			native, parseErr := architecturev2renderer.NativeDockerRootConfig(config)
			if parseErr != nil {
				return "", fmt.Errorf("inspect existing main.tf before adoption: %w", parseErr)
			}
			if native {
				return "", fmt.Errorf("%w (%s): unset %s and run the volume-preserving native-to-wrapper switch", ErrStage2RootExcluded, pre.RootRelative, NativeModulesEnv)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return "", readErr
		}
	}

	// 2. Verify existing ../compose.yaml matches target payload so the first
	//    Stage-1 adoption pass cannot silently alter container definitions.
	composePath := filepath.Join(filepath.Dir(rootDir), ComposeFile)
	existingBytes, err := os.ReadFile(composePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && pre.AllowMissingComposeFile {
			sum := sha256.Sum256(pre.TargetCompose)
			return hex.EncodeToString(sum[:]), nil
		}
		return "", fmt.Errorf("read existing compose.yaml before adoption: %w", err)
	}
	if !bytes.Equal(bytes.TrimSpace(existingBytes), bytes.TrimSpace(pre.TargetCompose)) {
		return "", fmt.Errorf("%w: %s differs from rendered Stage-1 payload", ErrAdoptionComposeMismatch, composePath)
	}
	sum := sha256.Sum256(pre.TargetCompose)
	return hex.EncodeToString(sum[:]), nil
}

// SnapshotProjectRuntime collects secret-free container IDs and volume names
// for every service in composeProject.
func SnapshotProjectRuntime(ctx context.Context, inspector ContainerInspector, composeProject string, environment []string) (RuntimeIdentitySnapshot, error) {
	if inspector == nil {
		inspector = DockerCLIContainerInspector{Binary: "docker"}
	}
	containers, err := inspector.InspectProjectContainers(ctx, LiveDriftRequest{
		ComposeProject: composeProject,
		Environment:    environment,
	})
	if err != nil {
		return RuntimeIdentitySnapshot{}, err
	}
	snap := RuntimeIdentitySnapshot{
		StartedAt:      map[string]string{},
		ComposeProject: composeProject,
		Containers:     make(map[string]string, len(containers)),
		Volumes:        make(map[string][]string, len(containers)),
	}
	for _, c := range containers {
		if c.State != "running" {
			return RuntimeIdentitySnapshot{}, fmt.Errorf("adoption requires running containers; %s is %s", c.Name, c.State)
		}
		key := c.Name
		if key == "" {
			key = c.Service
		}
		snap.Containers[key] = c.ContainerID
		snap.StartedAt[key] = c.StartedAt
		vols := append([]string(nil), c.VolumeNames...)
		slices.Sort(vols)
		snap.Volumes[key] = vols
	}
	return snap, nil
}

// RenderAdoptionComposePayloadOpenTofu emits a Stage-1 OpenTofu root whose
// initial `up` command passes `--no-recreate` at the command boundary
// (ADR-0054 §1), preventing container recreation even if Compose would
// otherwise attempt to replace a running container. Destroy semantics remain
// identical to RenderComposePayloadOpenTofu (never executed on adoption error).
func RenderAdoptionComposePayloadOpenTofu(spec architecturev2renderer.ComposePayloadSpec) ([]byte, error) {
	spec.AdoptNoRecreate = true
	return architecturev2renderer.RenderComposePayloadOpenTofu(spec)
}

// VerifyNoRecreateEvidence compares pre- and post-adoption runtime snapshots
// to prove that every pre-existing container kept its exact container ID and
// every volume binding stayed intact.
func VerifyNoRecreateEvidence(composeSHA string, before, after RuntimeIdentitySnapshot) (AdoptionEvidence, error) {
	ev := AdoptionEvidence{
		ComposeProject:      before.ComposeProject,
		ComposeSHA256:       composeSHA,
		CommandBoundaryFlag: "--no-recreate",
		Before:              before,
		After:               after,
		ContainerIDsEqual:   true,
		VolumeIDsPreserved:  true,
	}
	if len(before.Containers) == 0 {
		ev.ContainerIDsEqual = false
		return ev, fmt.Errorf("%w: pre-adoption snapshot found zero running containers for %s", ErrAdoptionRecreatedContainer, before.ComposeProject)
	}
	for service, beforeID := range before.Containers {
		afterID, ok := after.Containers[service]
		if !ok || afterID != beforeID || before.StartedAt[service] != after.StartedAt[service] {
			ev.ContainerIDsEqual = false
			ev.RecreatedServices = append(ev.RecreatedServices, service)
		}
		if !slices.Equal(before.Volumes[service], after.Volumes[service]) {
			ev.VolumeIDsPreserved = false
			ev.VolumeDiffServices = append(ev.VolumeDiffServices, service)
		}
	}
	slices.Sort(ev.RecreatedServices)
	slices.Sort(ev.VolumeDiffServices)
	if !ev.ContainerIDsEqual || !ev.VolumeIDsPreserved {
		return ev, fmt.Errorf("%w: recreated=%v volumeDiff=%v", ErrAdoptionRecreatedContainer, ev.RecreatedServices, ev.VolumeDiffServices)
	}
	return ev, nil
}
