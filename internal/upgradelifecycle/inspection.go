package upgradelifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	InspectionSchemaVersion  = "stackkit.upgrade-inspection/v1"
	defaultMaxExtractBytes   = int64(1 << 30)
	defaultMaxWorkspaceBytes = int64(512 << 20)
	defaultMaxFiles          = 20_000
)

type Runner interface {
	Run(context.Context, string, []string, string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, binary string, args []string, dir string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

type Inspector struct {
	Source            releaseindex.Source
	Attestations      releaseindex.AttestationVerifier
	Runner            Runner
	MaxBlobBytes      int64
	MaxExtractBytes   int64
	MaxWorkspaceBytes int64
	MaxFiles          int
	Timeout           time.Duration
}

type Inspection struct {
	SchemaVersion string              `json:"schemaVersion"`
	Target        Target              `json:"target"`
	Plan          PlanDiff            `json:"plan"`
	Artifacts     []ArtifactDiff      `json:"artifacts"`
	Execution     InspectionExecution `json:"execution"`
}

type Target struct {
	Kit           string                `json:"kit"`
	Version       string                `json:"version"`
	Channel       releaseindex.Channel  `json:"channel"`
	Platform      releaseindex.Platform `json:"platform"`
	Asset         string                `json:"asset"`
	ArchiveSHA256 string                `json:"archiveSha256"`
}

type PlanDiff struct {
	Changed             bool   `json:"changed"`
	CurrentPlanHash     string `json:"currentPlanHash"`
	TargetPlanHash      string `json:"targetPlanHash"`
	CurrentManifestHash string `json:"currentManifestHash"`
	TargetManifestHash  string `json:"targetManifestHash"`
}

type ArtifactDiff struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	Status          string `json:"status"`
	MetadataChanged bool   `json:"metadataChanged"`
	CurrentSHA256   string `json:"currentSha256,omitempty"`
	TargetSHA256    string `json:"targetSha256,omitempty"`
	CurrentKind     string `json:"currentKind,omitempty"`
	TargetKind      string `json:"targetKind,omitempty"`
	CurrentFormat   string `json:"currentFormat,omitempty"`
	TargetFormat    string `json:"targetFormat,omitempty"`
	CurrentMode     string `json:"currentMode,omitempty"`
	TargetMode      string `json:"targetMode,omitempty"`
}

type InspectionExecution struct {
	Mode            string `json:"mode"`
	TargetBinary    string `json:"targetBinary"`
	GenerateInvoked bool   `json:"generateInvoked"`
	ApplyInvoked    bool   `json:"applyInvoked"`
	SnapshotCreated bool   `json:"snapshotCreated"`
}

func (inspection Inspection) MarshalCanonical() ([]byte, error) {
	return resolvedplan.CanonicalJSON(inspection)
}

func (inspector Inspector) Inspect(ctx context.Context, resolution releaseindex.Resolution, workspace, specFile string, current generationartifact.PlanInspection) (Inspection, error) {
	if inspector.Runner == nil {
		inspector.Runner = ExecRunner{}
	}
	if inspector.Timeout <= 0 {
		inspector.Timeout = 5 * time.Minute
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return Inspection{}, fmt.Errorf("resolve workspace: %w", err)
	}
	specFile = strings.TrimSpace(specFile)
	if specFile == "" {
		specFile = "stack-spec.yaml"
	}
	if filepath.IsAbs(specFile) {
		relative, relErr := filepath.Rel(workspace, specFile)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return Inspection{}, fmt.Errorf("StackSpec must remain beneath the workspace")
		}
		specFile = relative
	}
	specFile = filepath.ToSlash(filepath.Clean(specFile))
	if _, err := safeRelative(specFile); err != nil {
		return Inspection{}, fmt.Errorf("invalid StackSpec path: %w", err)
	}
	if err := validatePlanInspection(current, "current"); err != nil {
		return Inspection{}, err
	}

	var result Inspection
	err = (releaseindex.Installer{
		Source: inspector.Source, Attestations: inspector.Attestations, MaxBlobBytes: inspector.MaxBlobBytes,
	}).InspectVerifiedArchive(ctx, resolution, func(verified releaseindex.VerifiedArchive) error {
		tempRoot, err := os.MkdirTemp("", "stackkit-upgrade-shadow-")
		if err != nil {
			return fmt.Errorf("create upgrade inspection root: %w", err)
		}
		defer os.RemoveAll(tempRoot)
		extractRoot := filepath.Join(tempRoot, "release")
		if err := os.Mkdir(extractRoot, 0o700); err != nil {
			return err
		}
		maxFiles := inspector.MaxFiles
		if maxFiles <= 0 {
			maxFiles = defaultMaxFiles
		}
		maxExtract := inspector.MaxExtractBytes
		if maxExtract <= 0 {
			maxExtract = defaultMaxExtractBytes
		}
		if err := extractArchive(verified.ArchivePath, resolution.Asset.Archive.Name, extractRoot, maxFiles, maxExtract); err != nil {
			return fmt.Errorf("extract verified target archive: %w", err)
		}
		binaryName := "stackkit"
		if resolution.Asset.Platform.OS == "windows" {
			binaryName += ".exe"
		}
		targetBinary := filepath.Join(extractRoot, binaryName)
		if err := requireTargetBinary(targetBinary); err != nil {
			return err
		}
		shadow := filepath.Join(tempRoot, "workspace")
		maxWorkspace := inspector.MaxWorkspaceBytes
		if maxWorkspace <= 0 {
			maxWorkspace = defaultMaxWorkspaceBytes
		}
		if err := copyWorkspace(workspace, shadow, maxFiles, maxWorkspace); err != nil {
			return fmt.Errorf("create bounded shadow workspace: %w", err)
		}

		runCtx, cancel := context.WithTimeout(ctx, inspector.Timeout)
		defer cancel()
		common := []string{"--chdir", shadow, "--spec", specFile, "--no-log"}
		if _, err := inspector.Runner.Run(runCtx, targetBinary, append(common, "generate"), shadow); err != nil {
			return fmt.Errorf("target release generate failed: %w", err)
		}
		rawInspection, err := inspector.Runner.Run(runCtx, targetBinary, append(common, "plan", "--json"), shadow)
		if err != nil {
			return fmt.Errorf("inspect target generation closure: %w", err)
		}
		var target generationartifact.PlanInspection
		if err := decodeExactJSON(rawInspection, &target); err != nil {
			return fmt.Errorf("decode target plan inspection: %w", err)
		}
		if err := validatePlanInspection(target, "target"); err != nil {
			return err
		}
		result = buildInspection(resolution, filepath.Base(targetBinary), current, target)
		return nil
	})
	if err != nil {
		return Inspection{}, err
	}
	return result, nil
}

func validatePlanInspection(inspection generationartifact.PlanInspection, label string) error {
	if inspection.APIVersion != generationartifact.PlanInspectionAPIVersion ||
		inspection.Kind != generationartifact.PlanInspectionKind ||
		inspection.VerifiedPhase != generationartifact.ExecutionPhaseGeneration ||
		inspection.ExecutorInvoked ||
		inspection.InfrastructureDiff != generationartifact.InfrastructureDiffNotAvailable ||
		inspection.Renderer != inspection.Binding.Renderer ||
		inspection.Manifest.Hash == "" {
		return fmt.Errorf("%s plan inspection is invalid or overclaims execution", label)
	}
	if inspection.Readiness.Generation.Status != "ready" || len(inspection.Readiness.Generation.Blockers) != 0 ||
		inspection.Readiness.Apply.Status != "ready" || len(inspection.Readiness.Apply.Blockers) != 0 {
		return fmt.Errorf("%s plan inspection is not generation/apply ready", label)
	}
	if inspection.OutputRoot != "." {
		if _, err := safeRelative(inspection.OutputRoot); err != nil {
			return fmt.Errorf("%s plan inspection outputRoot: %w", label, err)
		}
	}
	manifest := generationartifact.ArtifactManifest{
		APIVersion: generationartifact.ArtifactManifestAPIVersion,
		Kind:       generationartifact.ArtifactManifestKind,
		Binding:    inspection.Binding,
		Artifacts:  inspection.Manifest.Artifacts,
	}
	hash, err := manifest.Hash()
	if err != nil || hash != inspection.Manifest.Hash {
		return fmt.Errorf("%s plan inspection has an invalid manifest projection", label)
	}
	return nil
}

func buildInspection(resolution releaseindex.Resolution, binaryName string, current, target generationartifact.PlanInspection) Inspection {
	type pair struct {
		current *generationartifact.RenderedArtifact
		target  *generationartifact.RenderedArtifact
	}
	entries := map[string]pair{}
	for index := range current.Manifest.Artifacts {
		artifact := &current.Manifest.Artifacts[index]
		key := artifact.ID + "\x00" + artifact.Path
		value := entries[key]
		value.current = artifact
		entries[key] = value
	}
	for index := range target.Manifest.Artifacts {
		artifact := &target.Manifest.Artifacts[index]
		key := artifact.ID + "\x00" + artifact.Path
		value := entries[key]
		value.target = artifact
		entries[key] = value
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	diffs := make([]ArtifactDiff, 0, len(keys))
	for _, key := range keys {
		value := entries[key]
		diff := ArtifactDiff{}
		switch {
		case value.current == nil:
			diff.ID, diff.Path, diff.Status, diff.TargetSHA256 = value.target.ID, value.target.Path, "added", value.target.SHA256
			diff.TargetKind, diff.TargetFormat, diff.TargetMode = value.target.Kind, value.target.Format, value.target.Mode
		case value.target == nil:
			diff.ID, diff.Path, diff.Status, diff.CurrentSHA256 = value.current.ID, value.current.Path, "removed", value.current.SHA256
			diff.CurrentKind, diff.CurrentFormat, diff.CurrentMode = value.current.Kind, value.current.Format, value.current.Mode
		default:
			diff.ID, diff.Path = value.target.ID, value.target.Path
			diff.CurrentSHA256, diff.TargetSHA256 = value.current.SHA256, value.target.SHA256
			diff.CurrentKind, diff.TargetKind = value.current.Kind, value.target.Kind
			diff.CurrentFormat, diff.TargetFormat = value.current.Format, value.target.Format
			diff.CurrentMode, diff.TargetMode = value.current.Mode, value.target.Mode
			diff.MetadataChanged = diff.CurrentKind != diff.TargetKind ||
				diff.CurrentFormat != diff.TargetFormat || diff.CurrentMode != diff.TargetMode
			diff.Status = "unchanged"
			if diff.CurrentSHA256 != diff.TargetSHA256 || diff.MetadataChanged {
				diff.Status = "changed"
			}
		}
		diffs = append(diffs, diff)
	}
	return Inspection{
		SchemaVersion: InspectionSchemaVersion,
		Target: Target{
			Kit: resolution.Asset.Kit, Version: resolution.Asset.Version, Channel: resolution.Asset.Channel,
			Platform: resolution.Asset.Platform, Asset: resolution.Asset.Archive.Name,
			ArchiveSHA256: resolution.Asset.Archive.SHA256,
		},
		Plan: PlanDiff{
			Changed:         current.Binding.PlanHash != target.Binding.PlanHash || current.Manifest.Hash != target.Manifest.Hash,
			CurrentPlanHash: current.Binding.PlanHash, TargetPlanHash: target.Binding.PlanHash,
			CurrentManifestHash: current.Manifest.Hash, TargetManifestHash: target.Manifest.Hash,
		},
		Artifacts: diffs,
		Execution: InspectionExecution{
			Mode: "verified-target-shadow-generate/v1", TargetBinary: binaryName,
			GenerateInvoked: true, ApplyInvoked: false, SnapshotCreated: false,
		},
	}
}

func decodeExactJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func requireTargetBinary(binary string) error {
	info, err := os.Lstat(binary)
	if err != nil {
		return fmt.Errorf("exact target binary is missing: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("exact target binary must be a regular non-symlink file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("exact target binary is not executable")
	}
	return nil
}

func safeRelative(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) || path.IsAbs(name) {
		return "", fmt.Errorf("path must be a portable relative path")
	}
	clean := path.Clean(filepath.ToSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != filepath.ToSlash(name) ||
		(len(clean) > 1 && clean[1] == ':') {
		return "", fmt.Errorf("path escapes its root")
	}
	return clean, nil
}
