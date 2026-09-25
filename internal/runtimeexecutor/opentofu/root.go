package opentofu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/tofu"
)

// tofuRun is the recorded outcome of one init, plan, and apply of a root.
type tofuRun struct {
	Init        stepRecord  `json:"init"`
	Plan        planRecord  `json:"plan"`
	Apply       *stepRecord `json:"apply,omitempty"`
	StateSHA256 string      `json:"stateSha256"`
}

type rootFile struct {
	name string
	data []byte
	mode os.FileMode
}

// packagedTools resolves the packaged tofu binary and provider mirror and
// fails closed before anything is written when either is missing.
func (r Runtime) packagedTools() (string, string, error) {
	binary := r.Binary
	if binary == "" {
		packaged, ok := tofu.PackagedBinaryPath()
		if !ok {
			return "", "", errors.New("the packaged OpenTofu binary is missing: reinstall the StackKit release archive, which ships tofu beside stackkit, or set STACKKIT_TOFU_BINARY")
		}
		binary = packaged
	}
	providers := r.ProvidersDir
	if providers == "" {
		providers, _ = tofu.PackagedProvidersDir()
	}
	if err := tofu.RequireLocalProviderMirror(providers); err != nil {
		return "", "", err
	}
	absolute, err := filepath.Abs(providers)
	if err != nil {
		return "", "", fmt.Errorf("resolve the OpenTofu provider mirror: %w", err)
	}
	return binary, absolute, nil
}

// ensureRootDir creates the workspace-relative root as a private plain
// directory below .stackkit/runtime.
func ensureRootDir(workspace, relative string) (string, error) {
	current := workspace
	for index, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, part)
		mode := os.FileMode(0o750)
		if index >= 2 {
			mode = 0o700
		}
		if err := os.Mkdir(current, mode); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create OpenTofu root %s: %w", relative, err)
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("OpenTofu root path %s is not a plain directory", current)
		}
	}
	if err := os.Chmod(current, 0o700); err != nil {
		return "", fmt.Errorf("restrict OpenTofu root: %w", err)
	}
	return current, nil
}

// writeRoot writes the root marker, the offline CLI configuration, and the
// root files.
func writeRoot(root string, marker RootMarker, providers string, files ...rootFile) error {
	encoded, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	all := append([]rootFile{
		{MarkerFile, append(encoded, '\n'), 0o600},
		{CLIConfigFile, tofu.OfflineCLIConfig(providers), 0o600},
	}, files...)
	for _, file := range all {
		if err := writeFileAtomic(root, file.name, file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

// runRoot runs `tofu init`, `plan -detailed-exitcode -out=tfplan`, and, only
// when the plan has changes, `apply tfplan` offline in root. It never
// refreshes only, replaces, or destroys; the saved plan is removed and the
// local-backend state restricted to the owner.
func (r Runtime) runRoot(ctx context.Context, root, binary string, environment []string) (tofuRun, error) {
	environment = append(append([]string(nil), environment...),
		"TF_CLI_CONFIG_FILE="+filepath.Join(root, CLIConfigFile),
		"TF_CLI_ARGS=-no-color",
	)
	options := []tofu.ExecutorOption{
		tofu.WithWorkDir(root), tofu.WithBinary(binary),
		tofu.WithoutInheritedEnv(tofu.OfflineInheritedEnv...), tofu.WithEnv(environment...),
	}
	if r.Timeout > 0 {
		options = append(options, tofu.WithTimeout(r.Timeout))
	}
	runner := tofu.NewExecutor(options...)
	var run tofuRun
	initResult, err := runner.Init(ctx)
	if err := requireTofuStep("init", initResult, err); err != nil {
		return tofuRun{}, err
	}
	run.Init = stepRecord{ExitCode: initResult.ExitCode}
	planResult, err := runner.Plan(ctx, PlanFile, false)
	if err := requireTofuStep("plan", planResult, err); err != nil {
		return tofuRun{}, err
	}
	changes := tofu.ParsePlanOutput(planResult.Stdout)
	run.Plan = planRecord{ExitCode: planResult.ExitCode, Add: changes.Add, Change: changes.Change, Destroy: changes.Destroy}
	if planResult.ExitCode == 2 {
		applyResult, err := runner.Apply(ctx, PlanFile)
		if err := requireTofuStep("apply", applyResult, err); err != nil {
			return tofuRun{}, err
		}
		run.Apply = &stepRecord{ExitCode: applyResult.ExitCode}
	}
	if err := os.Remove(filepath.Join(root, PlanFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return tofuRun{}, fmt.Errorf("remove the applied OpenTofu plan file: %w", err)
	}
	if run.StateSHA256, err = protectState(root); err != nil {
		return tofuRun{}, err
	}
	return run, nil
}

func writeFileAtomic(directory, name string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(directory, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("stage %s: %w", name, err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	_, writeErr := temporary.Write(data)
	chmodErr := temporary.Chmod(mode)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	target := filepath.Join(directory, name)
	if info, err := os.Lstat(target); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("OpenTofu root file %s is not a regular file", name)
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return fmt.Errorf("install %s: %w", name, err)
	}
	return nil
}

// protectState restricts the local-backend state to the owner and returns
// its digest. A root without state after a successful run is a failure.
func protectState(root string) (string, error) {
	statePath := filepath.Join(root, StateFile)
	info, err := os.Lstat(statePath)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("OpenTofu apply left no local-backend state in the root")
	}
	if err := os.Chmod(statePath, 0o600); err != nil {
		return "", fmt.Errorf("restrict OpenTofu state: %w", err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		return "", fmt.Errorf("read OpenTofu state: %w", err)
	}
	return digestBytes(data), nil
}
