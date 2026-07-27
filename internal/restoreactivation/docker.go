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
cp -a "$src"/. "$dst"/`
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
	for _, volume := range authority.VolumeDetails {
		expected[volume.LiveName] = volume.LogicalName
	}
	stagingLogical := strings.TrimPrefix(
		authority.StagingVolume, authority.ComposeProject+"_",
	)
	if stagingLogical == authority.StagingVolume || stagingLogical == "" {
		return errors.New("restoreactivation: staging volume is outside the Compose project")
	}
	expected[authority.StagingVolume] = stagingLogical
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
			volume.Labels["com.docker.compose.project"] != authority.ComposeProject ||
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
		"--rm", "--network", "none", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
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
	composePath, err := runtime.verifiedComposePath(authority)
	if err != nil {
		return err
	}
	_, err = runtime.docker(
		ctx, "compose", "--project-name", authority.ComposeProject,
		"-f", composePath, "stop", "--timeout", "60",
	)
	return wrapDocker("stop verified Compose runtime", err)
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
	composePath, err := runtime.verifiedComposePath(authority)
	if err != nil {
		return err
	}
	_, err = runtime.docker(
		ctx, "compose", "--project-name", authority.ComposeProject,
		"-f", composePath, "up", "-d", "--wait", "--wait-timeout", "600",
	)
	return wrapDocker("start verified Compose runtime", err)
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
		"--rm", "--network", "none", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
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

func (runtime *dockerRuntime) docker(ctx context.Context, args ...string) ([]byte, error) {
	if runtime == nil || runtime.run == nil {
		return nil, errors.New("restoreactivation: Docker runtime is not initialized")
	}
	full := append([]string{"--host", localDockerSocket}, args...)
	return runtime.run(ctx, "docker", full, minimalCommandEnvironment())
}

func (runtime *dockerRuntime) verifyCompose(authority Authority) error {
	_, err := runtime.verifiedComposePath(authority)
	return err
}

func (runtime *dockerRuntime) verifiedComposePath(authority Authority) (string, error) {
	if authority.ComposePath == "" || !strings.HasPrefix(authority.ComposeDigest, "sha256:") {
		return "", errors.New("restoreactivation: verified Compose artifact identity is incomplete")
	}
	target := filepath.Join(runtime.workspace, filepath.FromSlash(authority.ComposePath))
	absolute, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(runtime.workspace, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("restoreactivation: Compose artifact escapes the workspace")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("restoreactivation: Compose artifact is not a plain file")
	}
	raw, err := os.ReadFile(absolute)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	if "sha256:"+hex.EncodeToString(sum[:]) != authority.ComposeDigest {
		return "", errors.New("restoreactivation: Compose artifact digest differs from the verified manifest")
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
