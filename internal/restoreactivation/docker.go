package restoreactivation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	localDockerSocket = "unix:///var/run/docker.sock"
	copyScript        = `src=$1
dst=$2
test -d "$src"
find "$dst" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} \;
find "$src" -mindepth 1 -maxdepth 1 -exec cp -a -- {} "$dst"/ \;`
	stagingValidationScript = `root=$1
shift
for path do
  test -d "$root/$path"
done`
)

type commandRunner func(
	context.Context,
	string,
	[]string,
	[]string,
) ([]byte, error)

type dockerRuntime struct {
	workspace string
	run       commandRunner
}

func NewDockerRuntime(workspace string) (Runtime, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("restoreactivation: resolve workspace: %w", err)
	}
	if runtime.GOOS == "windows" {
		return nil, errors.New("restoreactivation: live volume activation requires the Linux local Docker socket")
	}
	return &dockerRuntime{workspace: absolute, run: runCommand}, nil
}

func (runtime *dockerRuntime) Inspect(ctx context.Context, authority Authority) error {
	if err := runtime.verifyCompose(authority); err != nil {
		return err
	}
	expected := make(map[string]string, len(authority.VolumeDetails)+1)
	projects := make(map[string]string, len(authority.VolumeDetails)+1)
	for _, volume := range authority.VolumeDetails {
		expected[volume.LiveName] = volume.LogicalName
		project := volume.ComposeProject
		if project == "" {
			project = authority.ComposeProject
		}
		projects[volume.LiveName] = project
	}
	stagingLogical := strings.TrimPrefix(
		authority.StagingVolume, authority.ComposeProject+"_",
	)
	if stagingLogical == authority.StagingVolume || stagingLogical == "" {
		return errors.New("restoreactivation: staging volume is outside the Compose project")
	}
	expected[authority.StagingVolume] = stagingLogical
	projects[authority.StagingVolume] = authority.ComposeProject
	for name, logical := range expected {
		output, err := runtime.docker(ctx, "volume", "inspect", name)
		if err != nil {
			return fmt.Errorf("restoreactivation: inspect Docker volume %s: %w", name, err)
		}
		var inspected []struct {
			Name   string            `json:"Name"`
			Driver string            `json:"Driver"`
			Labels map[string]string `json:"Labels"`
		}
		if err := json.Unmarshal(output, &inspected); err != nil || len(inspected) != 1 {
			return fmt.Errorf("restoreactivation: Docker volume %s inspection is invalid", name)
		}
		volume := inspected[0]
		if volume.Name != name || volume.Driver != "local" ||
			volume.Labels["com.docker.compose.project"] != projects[name] ||
			volume.Labels["com.docker.compose.volume"] != logical {
			return fmt.Errorf("restoreactivation: Docker volume %s is not owned by the verified Compose project", name)
		}
	}
	return nil
}

func (runtime *dockerRuntime) ValidateStaging(ctx context.Context, authority Authority) error {
	paths := make([]string, 0, len(authority.VolumeDetails))
	for _, volume := range authority.VolumeDetails {
		relative, err := stagingVolumeRelativePath(authority, volume)
		if err != nil {
			return err
		}
		paths = append(paths, relative)
	}
	args := []string{
		"run", "--name", helperName(authority, "staging"),
		"--label", "io.stackkit.restore.operation=" + authority.OperationID,
		"--label", "io.stackkit.restore.plan=" + authority.PlanHash,
		"--rm", "--network", "none", "--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--mount", "type=volume,src=" + authority.StagingVolume + ",dst=/staging,readonly",
		"--entrypoint", "/bin/sh",
		authority.KopiaHelperImage,
		"-ceu", stagingValidationScript, "--", "/staging",
	}
	args = append(args, paths...)
	if _, err := runtime.docker(ctx, args...); err != nil {
		return fmt.Errorf("restoreactivation: validate exact staged volume set: %w", err)
	}
	return nil
}

func (runtime *dockerRuntime) Stop(ctx context.Context, authority Authority) error {
	// Killing the Docker CLI does not guarantee that its daemon-side helper
	// stopped copying. End only helpers bound to this exact operation before
	// any resumed copy or service restart can touch the same volumes.
	if err := runtime.stopCopyHelpers(ctx, authority); err != nil {
		return err
	}
	runtimes := authorityComposeRuntimes(authority)
	for index := len(runtimes) - 1; index >= 0; index-- {
		composePath, err := runtime.verifiedComposeRuntimePath(runtimes[index])
		if err != nil {
			return err
		}
		prefix, err := runtime.composeRuntimeArgs(runtimes[index], composePath)
		if err != nil {
			return err
		}
		if _, err = runtime.docker(
			ctx, append(prefix, "stop", "--timeout", "60")...,
		); err != nil {
			return wrapDocker("stop verified Compose runtime "+runtimes[index].Project, err)
		}
	}
	return nil
}

func (runtime *dockerRuntime) PrepareRollback(
	ctx context.Context,
	authority Authority,
	volume Volume,
) error {
	if err := requireAuthorityVolume(authority, volume); err != nil {
		return err
	}
	if _, err := runtime.docker(
		ctx, "volume", "create", "--driver", "local",
		"--label", "io.stackkit.restore.operation="+authority.OperationID,
		"--label", "io.stackkit.restore.source="+volume.LiveName,
		"--label", "io.stackkit.restore.role=rollback",
		volume.RollbackName,
	); err != nil {
		return wrapDocker("create rollback volume", err)
	}
	return runtime.copyVolume(
		ctx, authority, volume, volume.LiveName, "/", volume.RollbackName,
	)
}

func (runtime *dockerRuntime) ActivateVolume(
	ctx context.Context,
	authority Authority,
	volume Volume,
) error {
	if err := requireAuthorityVolume(authority, volume); err != nil {
		return err
	}
	relative, err := stagingVolumeRelativePath(authority, volume)
	if err != nil {
		return err
	}
	return runtime.copyVolume(
		ctx, authority, volume, authority.StagingVolume,
		"/"+relative, volume.LiveName,
	)
}

func stagingVolumeRelativePath(authority Authority, volume Volume) (string, error) {
	if path.Dir(authority.StagingPath) != "/restore-staging" {
		return "", errors.New("restoreactivation: staged result root is not governed")
	}
	stagingLeaf := path.Base(authority.StagingPath)
	if len(stagingLeaf) != 64 {
		return "", errors.New("restoreactivation: staged result identity is invalid")
	}
	liveRelative := strings.TrimPrefix(volume.StagingPath, authority.StagingPath+"/")
	if liveRelative == volume.StagingPath || liveRelative == "" ||
		strings.HasPrefix(liveRelative, "/") || strings.Contains(liveRelative, "..") {
		return "", errors.New("restoreactivation: staged volume path is outside the signed restore root")
	}
	return path.Join(stagingLeaf, liveRelative), nil
}

func (runtime *dockerRuntime) RestoreVolume(
	ctx context.Context,
	authority Authority,
	volume Volume,
) error {
	if err := requireAuthorityVolume(authority, volume); err != nil {
		return err
	}
	return runtime.copyVolume(
		ctx, authority, volume, volume.RollbackName, "/", volume.LiveName,
	)
}

func (runtime *dockerRuntime) Start(ctx context.Context, authority Authority) error {
	for _, composeRuntime := range authorityComposeRuntimes(authority) {
		composePath, err := runtime.verifiedComposeRuntimePath(composeRuntime)
		if err != nil {
			return err
		}
		prefix, err := runtime.composeRuntimeArgs(composeRuntime, composePath)
		if err != nil {
			return err
		}
		if _, err = runtime.docker(
			ctx, append(prefix, "up", "-d", "--wait", "--wait-timeout", "600")...,
		); err != nil {
			return wrapDocker("start verified Compose runtime "+composeRuntime.Project, err)
		}
	}
	return nil
}

func (runtime *dockerRuntime) CleanupRollback(
	ctx context.Context,
	authority Authority,
	volume Volume,
) error {
	if err := requireAuthorityVolume(authority, volume); err != nil {
		return err
	}
	_, err := runtime.docker(ctx, "volume", "rm", volume.RollbackName)
	return wrapDocker("remove rollback volume", err)
}

func (runtime *dockerRuntime) copyVolume(
	ctx context.Context,
	authority Authority,
	volume Volume,
	source, sourcePath, destination string,
) error {
	args := []string{
		"run", "--name", helperName(authority, volume.LiveName),
		"--label", "io.stackkit.restore.operation=" + authority.OperationID,
		"--label", "io.stackkit.restore.plan=" + authority.PlanHash,
		"--rm", "--network", "none", "--read-only",
		"--user", "0:0",
		"--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "FOWNER",
		"--cap-add", "DAC_OVERRIDE",
		"--security-opt", "no-new-privileges",
		"--mount", "type=volume,src=" + source + ",dst=/source,readonly",
		"--mount", "type=volume,src=" + destination + ",dst=/target",
		"--entrypoint", "/bin/sh",
		authority.KopiaHelperImage,
		"-ceu", copyScript, "--", "/source" + sourcePath, "/target",
	}
	if _, err := runtime.docker(ctx, args...); err != nil {
		return wrapDocker("copy governed volume", err)
	}
	return nil
}

func (runtime *dockerRuntime) stopCopyHelpers(ctx context.Context, authority Authority) error {
	allowedNames := map[string]bool{helperName(authority, "staging"): true}
	allowedVolumes := map[string]bool{authority.StagingVolume: true}
	for _, volume := range authority.VolumeDetails {
		allowedNames[helperName(authority, volume.LiveName)] = true
		allowedVolumes[volume.LiveName] = true
		allowedVolumes[volume.RollbackName] = true
	}
	queries := [][]string{{"--filter", "label=io.stackkit.restore.operation=" + authority.OperationID}}
	for name := range allowedNames {
		queries = append(queries, []string{"--filter", "name=^/" + name + "$"})
	}
	ids := map[string]bool{}
	for _, query := range queries {
		args := []string{"ps", "--all", "--no-trunc"}
		args = append(args, query...)
		args = append(args, "--format", "{{.ID}}")
		output, err := runtime.docker(ctx, args...)
		if err != nil {
			return wrapDocker("find interrupted restore helpers", err)
		}
		for _, id := range strings.Fields(string(output)) {
			ids[id] = true
		}
	}
	for id := range ids {
		decoded, err := hex.DecodeString(id)
		if err != nil || len(decoded) != 32 {
			return errors.New("restoreactivation: interrupted helper identity is invalid")
		}
		inspected, err := runtime.docker(ctx, "inspect", id)
		if err != nil {
			if runtime.containerGone(ctx, id) {
				continue
			}
			return wrapDocker("inspect interrupted restore helper", err)
		}
		var containers []struct {
			ID     string `json:"Id"`
			Name   string `json:"Name"`
			Config struct {
				Image  string            `json:"Image"`
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
			Mounts []struct {
				Type        string `json:"Type"`
				Name        string `json:"Name"`
				Destination string `json:"Destination"`
				RW          bool   `json:"RW"`
			} `json:"Mounts"`
		}
		if err := json.Unmarshal(inspected, &containers); err != nil || len(containers) != 1 {
			return errors.New("restoreactivation: interrupted helper inspection is invalid")
		}
		container := containers[0]
		operationLabel := container.Config.Labels["io.stackkit.restore.operation"]
		planLabel := container.Config.Labels["io.stackkit.restore.plan"]
		legacy := operationLabel == "" && planLabel == ""
		if container.ID != id || !allowedNames[strings.TrimPrefix(container.Name, "/")] || container.Config.Image != authority.KopiaHelperImage ||
			(!legacy && (operationLabel != authority.OperationID || planLabel != authority.PlanHash)) ||
			!validRestoreHelperMounts(container.Mounts, allowedVolumes) {
			return errors.New("restoreactivation: interrupted helper differs from the verified operation")
		}
		if _, err := runtime.docker(ctx, "rm", "--force", id); err != nil {
			if runtime.containerGone(ctx, id) {
				continue
			}
			return wrapDocker("stop interrupted restore helper", err)
		}
	}
	return nil
}

func (runtime *dockerRuntime) containerGone(ctx context.Context, id string) bool {
	output, err := runtime.docker(ctx, "ps", "--all", "--no-trunc", "--filter", "id="+id, "--format", "{{.ID}}")
	return err == nil && len(strings.Fields(string(output))) == 0
}

func validRestoreHelperMounts(mounts []struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}, allowed map[string]bool) bool {
	if len(mounts) < 1 || len(mounts) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, mount := range mounts {
		if mount.Type != "volume" || !allowed[mount.Name] || seen[mount.Destination] {
			return false
		}
		seen[mount.Destination] = true
		switch mount.Destination {
		case "/staging", "/source":
			if mount.RW {
				return false
			}
		case "/target":
			if !mount.RW {
				return false
			}
		default:
			return false
		}
	}
	return (len(mounts) == 1 && seen["/staging"]) || (len(mounts) == 2 && seen["/source"] && seen["/target"])
}

func (runtime *dockerRuntime) docker(ctx context.Context, args ...string) ([]byte, error) {
	if runtime == nil || runtime.run == nil {
		return nil, errors.New("restoreactivation: Docker runtime is not initialized")
	}
	full := append([]string{"--host", localDockerSocket}, args...)
	environment := append(
		minimalCommandEnvironment(),
		"STACKKIT_CUSTODY_DIR="+filepath.Join(runtime.workspace, ".stackkit", "custody"),
	)
	return runtime.run(ctx, "docker", full, environment)
}

func (runtime *dockerRuntime) verifyCompose(authority Authority) error {
	for _, composeRuntime := range authorityComposeRuntimes(authority) {
		composePath, err := runtime.verifiedComposeRuntimePath(composeRuntime)
		if err != nil {
			return err
		}
		if _, err := runtime.composeRuntimeArgs(composeRuntime, composePath); err != nil {
			return err
		}
	}
	return nil
}

func authorityComposeRuntimes(authority Authority) []ComposeRuntime {
	if len(authority.ComposeRuntimes) != 0 {
		return append([]ComposeRuntime(nil), authority.ComposeRuntimes...)
	}
	return []ComposeRuntime{{
		Project: authority.ComposeProject, Path: authority.ComposePath,
		Digest: authority.ComposeDigest,
	}}
}

func (runtime *dockerRuntime) verifiedComposeRuntimePath(composeRuntime ComposeRuntime) (string, error) {
	if composeRuntime.Project == "" || composeRuntime.Path == "" || !strings.HasPrefix(composeRuntime.Digest, "sha256:") {
		return "", errors.New("restoreactivation: verified Compose artifact identity is incomplete")
	}
	target := filepath.Join(runtime.workspace, filepath.FromSlash(composeRuntime.Path))
	absolute, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(runtime.workspace, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("restoreactivation: Compose artifact escapes the workspace")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 1<<20 {
		return "", errors.New("restoreactivation: Compose artifact is not a plain file")
	}
	raw, err := os.ReadFile(absolute)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	if "sha256:"+hex.EncodeToString(sum[:]) != composeRuntime.Digest {
		return "", errors.New("restoreactivation: Compose artifact digest differs from the verified manifest")
	}
	return absolute, nil
}

func (runtime *dockerRuntime) composeRuntimeArgs(composeRuntime ComposeRuntime, composePath string) ([]string, error) {
	args := []string{"compose", "--project-name", composeRuntime.Project}
	if composeRuntime.EnvironmentPath != "" || composeRuntime.EnvironmentDigest != "" {
		environmentPath, err := runtime.verifiedRuntimeFile(
			composeRuntime.EnvironmentPath, composeRuntime.EnvironmentDigest,
			"Compose environment",
		)
		if err != nil {
			return nil, err
		}
		args = append(args, "--env-file", environmentPath)
	}
	return append(args, "-f", composePath), nil
}

func (runtime *dockerRuntime) verifiedRuntimeFile(relativePath, digest, identity string) (string, error) {
	if relativePath == "" || !strings.HasPrefix(digest, "sha256:") {
		return "", fmt.Errorf("restoreactivation: verified %s identity is incomplete", identity)
	}
	target := filepath.Join(runtime.workspace, filepath.FromSlash(relativePath))
	absolute, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(runtime.workspace, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("restoreactivation: %s escapes the workspace", identity)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 1<<20 {
		return "", fmt.Errorf("restoreactivation: %s is not a plain file", identity)
	}
	raw, err := os.ReadFile(absolute)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	if "sha256:"+hex.EncodeToString(sum[:]) != digest {
		return "", fmt.Errorf("restoreactivation: %s digest differs from the verified custody", identity)
	}
	return absolute, nil
}

func requireAuthorityVolume(authority Authority, volume Volume) error {
	for _, expected := range authority.VolumeDetails {
		if expected == volume {
			return nil
		}
	}
	return errors.New("restoreactivation: foreign volume is outside the activation authority")
}

func helperName(authority Authority, purpose string) string {
	sum := sha256.Sum256([]byte(authority.OperationID + "\x00" + purpose))
	return "stackkit-restore-" + hex.EncodeToString(sum[:8])
}

func minimalCommandEnvironment() []string {
	environment := make([]string, 0, 2)
	if pathValue := os.Getenv("PATH"); pathValue != "" {
		environment = append(environment, "PATH="+pathValue)
	}
	if locale := os.Getenv("LANG"); locale != "" {
		environment = append(environment, "LANG="+locale)
	}
	return environment
}

func runCommand(
	ctx context.Context,
	name string,
	args []string,
	environment []string,
) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...) // #nosec G204 -- executable and every control argument are fixed or verified authority values.
	command.Env = append([]string(nil), environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if len(detail) > 4096 {
			detail = detail[:4096]
		}
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
	}
	return output, err
}

func wrapDocker(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("restoreactivation: %s: %w", operation, err)
}
