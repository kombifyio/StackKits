package upgradelifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
)

// Generation targets whose runtime the executor-state snapshot captures.
// Terramate orchestrates the same per-module OpenTofu roots, so its custody
// is the OpenTofu root custody.
const (
	executorStateTargetCompose   = "compose"
	executorStateTargetOpenTofu  = "opentofu"
	executorStateTargetTerramate = "terramate"
)

var executorStateOpenTofuRootPattern = regexp.MustCompile(`^\.stackkit/runtime/(?:(` + opentofu.ApplicationsDir + `|` + opentofu.ModulesDir + `)/)?([a-z0-9][a-z0-9-]{0,62})/` + opentofu.RootDirName + `$`)

// ExecutorStateOpenTofuRootInput is one OpenTofu root captured when the
// generation target executes OpenTofu: its local-backend state and root
// configuration, plus the runtime Compose file its local_file resource writes
// one level up (Core and workload roots) and, for a workload root, the
// private .env its Compose project reads. Edge and federation contract roots
// (.stackkit/runtime/modules/<moduleRef>/opentofu) own no Compose file.
type ExecutorStateOpenTofuRootInput struct {
	ModuleRef   string
	Root        string
	State       ExecutorStateBlobInput
	Config      ExecutorStateBlobInput
	Compose     ExecutorStateBlobInput
	Environment ExecutorStateBlobInput
}

// ExecutorStateOpenTofuRoot is the signed identity of one captured root.
type ExecutorStateOpenTofuRoot struct {
	ModuleRef   string            `json:"moduleRef"`
	Root        string            `json:"root"`
	State       ExecutorStateBlob `json:"state"`
	Config      ExecutorStateBlob `json:"config"`
	Compose     ExecutorStateBlob `json:"compose,omitzero"`
	Environment ExecutorStateBlob `json:"environment,omitzero"`
}

func executorStateTargetExecutesOpenTofu(target string) bool {
	return target == executorStateTargetOpenTofu || target == executorStateTargetTerramate
}

// executorStateOpenTofuRootInputBlobs lists the files of one root in capture
// order: state, configuration, then the Compose file and .env when present.
func executorStateOpenTofuRootInputBlobs(root ExecutorStateOpenTofuRootInput) []ExecutorStateBlobInput {
	result := []ExecutorStateBlobInput{root.State, root.Config}
	if root.Compose.ID != "" {
		result = append(result, root.Compose)
	}
	if root.Environment.ID != "" {
		result = append(result, root.Environment)
	}
	return result
}

func executorStateOpenTofuRootBlobs(root ExecutorStateOpenTofuRoot) []ExecutorStateBlob {
	result := []ExecutorStateBlob{root.State, root.Config}
	if root.Compose != (ExecutorStateBlob{}) {
		result = append(result, root.Compose)
	}
	if root.Environment != (ExecutorStateBlob{}) {
		result = append(result, root.Environment)
	}
	return result
}

func executorStateOpenTofuBlobInputs(roots []ExecutorStateOpenTofuRootInput) []ExecutorStateBlobInput {
	result := make([]ExecutorStateBlobInput, 0, 4*len(roots))
	for _, root := range roots {
		result = append(result, executorStateOpenTofuRootInputBlobs(root)...)
	}
	return result
}

func executorStateOpenTofuBlobs(roots []ExecutorStateOpenTofuRoot) []ExecutorStateBlob {
	result := make([]ExecutorStateBlob, 0, 4*len(roots))
	for _, root := range roots {
		result = append(result, executorStateOpenTofuRootBlobs(root)...)
	}
	return result
}

// executorStateOpenTofuRootsFromPayloads rebinds the prepared blob identities
// of every root, in capture order, starting at offset.
func executorStateOpenTofuRootsFromPayloads(inputs []ExecutorStateOpenTofuRootInput, payloads []executorStatePayload, offset int) ([]ExecutorStateOpenTofuRoot, error) {
	roots := make([]ExecutorStateOpenTofuRoot, len(inputs))
	for index, input := range inputs {
		next := func() (ExecutorStateBlob, error) {
			if offset >= len(payloads) {
				return ExecutorStateBlob{}, errors.New("executor state: OpenTofu root blobs are incomplete")
			}
			offset++
			return payloads[offset-1].identity, nil
		}
		root := ExecutorStateOpenTofuRoot{ModuleRef: input.ModuleRef, Root: input.Root}
		var err error
		if root.State, err = next(); err != nil {
			return nil, err
		}
		if root.Config, err = next(); err != nil {
			return nil, err
		}
		if input.Compose.ID != "" {
			if root.Compose, err = next(); err != nil {
				return nil, err
			}
		}
		if input.Environment.ID != "" {
			if root.Environment, err = next(); err != nil {
				return nil, err
			}
		}
		roots[index] = root
	}
	if offset != len(payloads) {
		return nil, errors.New("executor state: OpenTofu root blobs are not closed")
	}
	return roots, nil
}

// validateExecutorStateOpenTofuRoots checks the closed root layout written by
// internal/runtimeexecutor/opentofu and binds every root configuration, and the Core
// root in particular, to a governed generation artifact.
func validateExecutorStateOpenTofuRoots(
	roots []ExecutorStateOpenTofuRoot,
	artifacts []ExecutorStateBlob,
	profile CurrentStateCoreProfile,
) error {
	if len(roots) == 0 {
		return errors.New("executor state: an OpenTofu generation target requires captured OpenTofu roots")
	}
	artifactDigests := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		artifactDigests[artifact.SHA256] = struct{}{}
	}
	seenRoots := make(map[string]struct{}, len(roots))
	seenModules := make(map[string]struct{}, len(roots))
	var core *ExecutorStateOpenTofuRoot
	for index := range roots {
		root := roots[index]
		match := executorStateOpenTofuRootPattern.FindStringSubmatch(root.Root)
		if match == nil || !executorStateIDPattern.MatchString(root.ModuleRef) {
			return errors.New("executor state: OpenTofu root identity is not the governed runtime layout")
		}
		if _, duplicate := seenRoots[root.Root]; duplicate {
			return errors.New("executor state: duplicate OpenTofu root")
		}
		if _, duplicate := seenModules[root.ModuleRef]; duplicate {
			return errors.New("executor state: duplicate OpenTofu root module")
		}
		seenRoots[root.Root] = struct{}{}
		seenModules[root.ModuleRef] = struct{}{}
		runtimeDir := path.Dir(root.Root)
		if root.State.Path != path.Join(root.Root, opentofu.StateFile) || root.State.Mode != "0600" ||
			root.Config.Path != path.Join(root.Root, opentofu.ConfigFile) || root.Config.Mode != "0640" {
			return errors.New("executor state: OpenTofu root files are not the governed state and configuration")
		}
		composeFile := root.Compose.Path == path.Join(runtimeDir, opentofu.ComposeFile) && root.Compose.Mode == "0600"
		environmentFile := root.Environment.Path == path.Join(runtimeDir, opentofu.EnvFile) && root.Environment.Mode == "0600" &&
			root.Environment.ID == executorStateStandaloneEnvironmentID(match[2])
		switch match[1] {
		case "":
			// Core root: its configuration is the governed generation artifact.
			if !composeFile || root.Environment != (ExecutorStateBlob{}) {
				return errors.New("executor state: Core OpenTofu root files are not the governed state, configuration, and Compose payload")
			}
			if _, governed := artifactDigests[root.Config.SHA256]; !governed {
				return errors.New("executor state: OpenTofu root configuration differs from every governed generation artifact")
			}
		case opentofu.ApplicationsDir:
			// Workload root: the executor renders its configuration from the
			// Compose project it applies; the private .env stays beside it.
			if !composeFile || !environmentFile {
				return errors.New("executor state: workload OpenTofu root files are not the governed state, configuration, Compose payload, and .env")
			}
		default:
			// Edge or federation contract root: state and configuration only.
			if root.Compose != (ExecutorStateBlob{}) || root.Environment != (ExecutorStateBlob{}) || match[2] != root.ModuleRef {
				return errors.New("executor state: contract OpenTofu root carries runtime files")
			}
		}
		if root.ModuleRef == profile.ModuleRef && match[1] == "" {
			core = &roots[index]
		}
	}
	if core == nil {
		return errors.New("executor state: the selected Core module has no captured OpenTofu root")
	}
	coreConfigRef := path.Join(path.Dir(profile.ComposeOutputRef), opentofu.ConfigFile)
	sourceMatches := 0
	for _, artifact := range artifacts {
		if artifact.Path == coreConfigRef &&
			(profile.ComposeArtifactID == "" || artifact.ID == profile.ComposeArtifactID) {
			sourceMatches++
			if artifact.SHA256 != core.Config.SHA256 {
				return errors.New("executor state: Core OpenTofu root differs from its governed generation artifact")
			}
		}
	}
	if sourceMatches != 1 {
		return errors.New("executor state: Core OpenTofu root requires exactly one governed source artifact")
	}
	return nil
}

// CollectOpenTofuRootStates reads every materialized OpenTofu root below
// .stackkit/runtime as capture input: Core roots (<runtime>/opentofu),
// workload roots (applications/<project>/opentofu) and edge and federation
// contract roots (modules/<moduleRef>/opentofu), the runtime roots of every
// stack in the Terramate stack graph. A root is identified by the marker the
// runtime executor writes; roots are returned in path order.
func CollectOpenTofuRootStates(workspaceRoot string) ([]ExecutorStateOpenTofuRootInput, error) {
	roots := make([]ExecutorStateOpenTofuRootInput, 0)
	for _, parent := range []string{"", opentofu.ApplicationsDir, opentofu.ModulesDir} {
		parentRelative := path.Join(".stackkit", "runtime", parent)
		entries, err := os.ReadDir(filepath.Join(workspaceRoot, filepath.FromSlash(parentRelative)))
		if parent != "" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("executor state: list runtime directories: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || parent == "" && (entry.Name() == opentofu.ApplicationsDir || entry.Name() == opentofu.ModulesDir) {
				continue
			}
			root, found, err := collectOpenTofuRootState(workspaceRoot, parent, entry.Name())
			if err != nil {
				return nil, err
			}
			if found {
				roots = append(roots, root)
			}
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Root < roots[j].Root })
	if len(roots) == 0 {
		return nil, errors.New("executor state: no applied OpenTofu root exists below .stackkit/runtime")
	}
	return roots, nil
}

func collectOpenTofuRootState(workspaceRoot, parent, name string) (ExecutorStateOpenTofuRootInput, bool, error) {
	runtimeDir := path.Join(".stackkit", "runtime", parent, name)
	relative := path.Join(runtimeDir, opentofu.RootDirName)
	absolute := filepath.Join(workspaceRoot, filepath.FromSlash(relative))
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return ExecutorStateOpenTofuRootInput{}, false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ExecutorStateOpenTofuRootInput{}, false, fmt.Errorf("executor state: OpenTofu root %s is not a plain directory", relative)
	}
	marker, err := opentofu.ReadRootMarker(absolute)
	if err != nil {
		return ExecutorStateOpenTofuRootInput{}, false, fmt.Errorf("executor state: %s: %w", relative, err)
	}
	read := func(portable, id, mode string) (ExecutorStateBlobInput, error) {
		data, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(portable)))
		if err != nil {
			return ExecutorStateBlobInput{}, fmt.Errorf("executor state: read %s: %w", portable, err)
		}
		return ExecutorStateBlobInput{ID: id, Path: portable, Mode: mode, Data: data}, nil
	}
	idSuffix := name
	if parent != "" {
		idSuffix = parent + "-" + name
	}
	root := ExecutorStateOpenTofuRootInput{ModuleRef: marker.ModuleRef, Root: relative}
	if root.State, err = read(path.Join(relative, opentofu.StateFile), "opentofu-state-"+idSuffix, "0600"); err != nil {
		return ExecutorStateOpenTofuRootInput{}, false, err
	}
	if root.Config, err = read(path.Join(relative, opentofu.ConfigFile), "opentofu-config-"+idSuffix, "0640"); err != nil {
		return ExecutorStateOpenTofuRootInput{}, false, err
	}
	if parent != opentofu.ModulesDir {
		if root.Compose, err = read(path.Join(runtimeDir, opentofu.ComposeFile), "opentofu-compose-"+idSuffix, "0600"); err != nil {
			return ExecutorStateOpenTofuRootInput{}, false, err
		}
	}
	if parent == opentofu.ApplicationsDir {
		if root.Environment, err = read(path.Join(runtimeDir, opentofu.EnvFile), executorStateStandaloneEnvironmentID(name), "0600"); err != nil {
			return ExecutorStateOpenTofuRootInput{}, false, err
		}
	}
	return root, true, nil
}

// restoreExecutorStateOpenTofuRoots writes every captured root file back:
// state and configuration into the root, the Compose payload to the runtime
// Compose file. All are installed owner-only; the next OpenTofu run restores
// the configuration mode it renders.
func restoreExecutorStateOpenTofuRoots(
	transaction *confinedfs.Transaction,
	view confinedfs.View,
	roots []ExecutorStateOpenTofuRoot,
) ([]string, error) {
	restored := make([]string, 0, 4*len(roots))
	for _, root := range roots {
		if err := transaction.MkdirAll(root.Root, 0o700); err != nil {
			return nil, fmt.Errorf("executor state: recreate OpenTofu root %s: %w", root.Root, err)
		}
		blobs := make([]ExecutorStateBlob, 0, 4)
		for _, blob := range []ExecutorStateBlob{root.Environment, root.Compose} {
			if blob != (ExecutorStateBlob{}) {
				blobs = append(blobs, blob)
			}
		}
		for _, blob := range append(blobs, root.Config, root.State) {
			data, err := readExecutorStateRecoveryBlob(transaction, blob)
			if err != nil {
				return nil, err
			}
			if _, err := view.WriteAtomic0600(blob.Path, data); err != nil {
				return nil, fmt.Errorf("executor state: atomically restore %s: %w", blob.Path, err)
			}
			restored = append(restored, blob.Path)
		}
	}
	return restored, nil
}

// GenerationTargetForPlan returns the generation target a verified plan
// resolved from its StackSpec.
func GenerationTargetForPlan(plan generationartifact.VerifiedPlan) (string, error) {
	var document struct {
		Generation struct {
			Target string `json:"target"`
		} `json:"generation"`
	}
	if err := json.Unmarshal(plan.Canonical(), &document); err != nil {
		return "", fmt.Errorf("read the plan generation target: %w", err)
	}
	target := strings.TrimSpace(document.Generation.Target)
	switch target {
	case executorStateTargetCompose, executorStateTargetOpenTofu, executorStateTargetTerramate:
		return target, nil
	default:
		return "", fmt.Errorf("plan generation target %q is not supported", target)
	}
}
