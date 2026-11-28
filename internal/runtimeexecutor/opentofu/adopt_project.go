package opentofu

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

type adoptedProject struct {
	Project, Directory, File, Digest string
	State                            ExpectedStateRoot
}
type adoptionRegistry struct {
	Projects  []adoptedProject
	Signature localevidence.OwnerExecutorStateSignature
}

const adoptionRegistryFile = ".stackkit/runtime/adopted-projects.json"

func (r Runtime) adoptedProjects() ([]adoptedProject, error) {
	raw, exists, err := readBoundedRegularFile(filepath.Join(r.WorkspaceRoot, adoptionRegistryFile), maxArtifactBytes)
	if err != nil || !exists {
		return nil, err
	}
	var registry adoptionRegistry
	if err = json.Unmarshal(raw, &registry); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(registry.Projects)
	if err != nil {
		return nil, err
	}
	if err = localevidence.VerifyOwnerExecutorState(r.WorkspaceRoot, payload, registry.Signature); err != nil {
		return nil, err
	}
	return registry.Projects, nil
}
func (r Runtime) AdoptedStateRoots() ([]ExpectedStateRoot, error) {
	projects, err := r.adoptedProjects()
	if err != nil {
		return nil, err
	}
	var roots []ExpectedStateRoot
	for _, p := range projects {
		roots = append(roots, p.State)
	}
	return roots, nil
}

func resolveAdoptedProject(ctx context.Context, directory string) (adoptedProject, []byte, error) {
	var p adoptedProject
	directory, err := filepath.Abs(directory)
	if err != nil {
		return p, nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return p, nil, err
	}
	p.Directory = directory
	if _, err := os.Lstat(filepath.Join(directory, RootDirName, ConfigFile)); !errors.Is(err, os.ErrNotExist) {
		return p, nil, errors.New("refusing automatic Compose adoption of an existing OpenTofu project")
	}
	var payload []byte
	var exists bool
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
		p.File = filepath.Join(directory, name)
		payload, exists, err = readBoundedRegularFile(p.File, maxArtifactBytes)
		if exists || err != nil {
			break
		}
	}
	if err != nil {
		return p, nil, err
	}
	if !exists {
		return p, nil, errors.New("no supported Compose file found")
	}
	ids, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "label=com.docker.compose.project.working_dir="+directory, "--format", "{{.ID}}").Output()
	if err != nil {
		return p, nil, fmt.Errorf("list Compose containers: %w", err)
	}
	if len(strings.Fields(string(ids))) == 0 {
		return p, nil, errors.New("no existing Compose project matches the source directory")
	}
	args := append([]string{"inspect", "--format", `{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.project.config_files"}}`}, strings.Fields(string(ids))...)
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return p, nil, fmt.Errorf("inspect running Compose project: %w", err)
	}
	for _, row := range strings.FieldsFunc(strings.TrimSpace(string(out)), func(ch rune) bool { return ch == '\n' }) {
		project, file, ok := strings.Cut(row, "|")
		if !ok || file != p.File || project == "" || p.Project != "" && project != p.Project {
			return p, nil, errors.New("Compose runtime labels do not identify a single project and source file")
		}
		p.Project = project
	}
	if p.Project == "" {
		return p, nil, errors.New("no existing Compose project matches the source directory")
	}
	hash := sha256.Sum256([]byte(p.Project))
	name := "adopted-" + fmt.Sprintf("%x", hash[:12])
	relative, err := WorkloadRootRelativePath(name)
	p.State = ExpectedStateRoot{relative, "compose-adoption", name}
	p.Digest = fmt.Sprintf("%x", sha256.Sum256(payload))
	return p, payload, err
}

func (r Runtime) AdoptCompose(ctx context.Context, directory string) (AdoptionEvidence, error) {
	p, payload, err := resolveAdoptedProject(ctx, directory)
	if err != nil {
		return AdoptionEvidence{}, err
	}
	projects, err := r.adoptedProjects()
	if err != nil {
		return AdoptionEvidence{}, err
	}
	for _, known := range projects {
		if known.Project == p.Project {
			if known != p {
				return AdoptionEvidence{}, errors.New("already adopted project source changed")
			}
			return r.convergeAdoption(ctx, p)
		}
	}
	before, err := SnapshotProjectRuntime(ctx, nil, p.Project, nil)
	if err != nil {
		return AdoptionEvidence{}, err
	}
	if len(before.Containers) == 0 {
		return AdoptionEvidence{}, errors.New("refusing to adopt an empty project")
	}
	binary, providers, err := r.packagedTools()
	if err != nil {
		return AdoptionEvidence{}, err
	}
	relative := p.State.Root
	if _, err := os.Lstat(filepath.Dir(filepath.Join(r.WorkspaceRoot, relative))); !errors.Is(err, os.ErrNotExist) {
		return AdoptionEvidence{}, errors.New("refusing to take over an existing runtime directory")
	}
	if err = r.requireStateCustody(ctx, relative, binary, providers.Directory()); err != nil {
		return AdoptionEvidence{}, err
	}
	digest, err := CheckAdoptionPrecondition(AdoptionPrecondition{WorkspaceRoot: r.WorkspaceRoot, RootRelative: relative, ComposeProject: p.Project, TargetCompose: payload, AllowMissingComposeFile: true})
	if err != nil {
		return AdoptionEvidence{}, err
	}
	config, err := RenderAdoptionComposePayloadOpenTofu(architecturev2renderer.ComposePayloadSpec{ResourcePrefix: "adopted", ProjectName: p.Project, SourceProjectDirectory: p.Directory, Compose: payload, AdoptNoRecreate: true})
	if err != nil {
		return AdoptionEvidence{}, err
	}
	lock, err := providers.LockForConfiguration(config)
	if err != nil {
		return AdoptionEvidence{}, err
	}
	root, err := ensureRootDir(r.WorkspaceRoot, relative)
	if err != nil {
		return AdoptionEvidence{}, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return AdoptionEvidence{}, err
	}
	if len(entries) != 0 {
		return AdoptionEvidence{}, errors.New("refusing to overwrite an unregistered OpenTofu root")
	}
	marker := RootMarker{SchemaVersion: RootMarkerSchemaVersion, Kind: RootKindWorkload, RuntimeDir: p.State.InstanceRef, ModuleRef: p.State.ModuleRef, InstanceRef: p.State.InstanceRef, ComposeProject: p.Project}
	if err = writeRoot(root, marker, providers.Directory(), lock, rootFile{ConfigFile, config, 0600}); err != nil {
		return AdoptionEvidence{}, err
	}
	if _, err = r.runRoot(ctx, relative, root, binary, nil); err != nil {
		return AdoptionEvidence{}, err
	}
	after, err := SnapshotProjectRuntime(ctx, nil, p.Project, nil)
	if err != nil {
		return AdoptionEvidence{}, err
	}
	evidence, err := VerifyNoRecreateEvidence(digest, before, after)
	if err != nil {
		return evidence, err
	}
	projects = append(projects, p)
	data, err := json.Marshal(projects)
	if err != nil {
		return evidence, err
	}
	sig, err := localevidence.SignOwnerExecutorState(r.WorkspaceRoot, data)
	if err != nil {
		return evidence, err
	}
	data, err = json.Marshal(adoptionRegistry{projects, sig})
	if err != nil {
		return evidence, err
	}
	return evidence, writeFileAtomic(filepath.Join(r.WorkspaceRoot, ".stackkit/runtime"), "adopted-projects.json", data, 0600)
}

func (r Runtime) convergeAdoption(ctx context.Context, p adoptedProject) (AdoptionEvidence, error) {
	current, _, err := resolveAdoptedProject(ctx, p.Directory)
	if err != nil {
		return AdoptionEvidence{}, err
	}
	if current != p {
		return AdoptionEvidence{}, errors.New("adopted Compose source differs from owner receipt")
	}
	before, err := SnapshotProjectRuntime(ctx, nil, p.Project, nil)
	if err != nil {
		return AdoptionEvidence{}, err
	}
	if len(before.Containers) == 0 {
		return AdoptionEvidence{}, errors.New("adopted project is missing")
	}
	command := exec.CommandContext(ctx, "docker", "compose", "--project-name", p.Project, "--project-directory", p.Directory, "-f", p.File, "up", "-d", "--no-recreate", "--wait")
	if err = command.Run(); err != nil {
		return AdoptionEvidence{}, err
	}
	after, err := SnapshotProjectRuntime(ctx, nil, p.Project, nil)
	if err != nil {
		return AdoptionEvidence{}, err
	}
	return VerifyNoRecreateEvidence(p.Digest, before, after)
}
func (r Runtime) ConvergeAdoptions(ctx context.Context) ([]AdoptionEvidence, error) {
	projects, err := r.adoptedProjects()
	if err != nil {
		return nil, err
	}
	var result []AdoptionEvidence
	for _, p := range projects {
		ev, err := r.convergeAdoption(ctx, p)
		if err != nil {
			return nil, err
		}
		result = append(result, ev)
	}
	return result, nil
}
