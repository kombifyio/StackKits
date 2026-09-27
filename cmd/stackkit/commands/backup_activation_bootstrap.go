package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// restoreActivationBootstrapRuntime runs the native Apply side steps around
// restore activation's Compose start. The same Start method is used after the
// requested data is activated and after automatic rollback restores the prior
// data, so both outcomes leave Core identity and workload routing configured.
type restoreActivationBootstrapRuntime struct {
	restoreactivation.Runtime
	workspace  string
	executable string
	coreModule string
	workloads  map[string][]byte

	prepareCore      func(context.Context, string, string, string) (func(context.Context) error, error)
	completeWorkload func(context.Context, string, []byte) error
}

func newRestoreActivationBootstrapRuntime(
	runtime restoreactivation.Runtime,
	workspace string,
) (*restoreActivationBootstrapRuntime, error) {
	if runtime == nil {
		return nil, errors.New("restore activation bootstrap requires a runtime")
	}
	executable, err := runningStackKitExecutable()
	if err != nil {
		return nil, fmt.Errorf("resolve the running StackKit executable for restore activation: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	return &restoreActivationBootstrapRuntime{
		Runtime: runtime, workspace: workspace, executable: executable,
		prepareCore: func(ctx context.Context, workspace, moduleRef, executable string) (func(context.Context) error, error) {
			steps, err := nativehost.PrepareNativeComposeRoot(ctx, workspace, moduleRef, executable)
			if err != nil {
				return nil, err
			}
			return steps.CompleteWithRetainedIdentity, nil
		},
		completeWorkload: nativehost.CompleteRestoredWorkloadCompose,
	}, nil
}

// bind selects the exact Core module and credential-free workload bundles
// from the verified restore authority. Workload completion resolves existing
// owner custody to prove the persisted runtime files still match; it never
// materializes or rotates a credential during restore activation.
func (runtime *restoreActivationBootstrapRuntime) bind(
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	operationID string,
) error {
	moduleRef, err := restoreActivationCoreModule(plan)
	if err != nil {
		return err
	}
	custody, err := restoreactivation.DeriveStandaloneComposeRuntimeCustody(
		runtime.workspace, plan, manifest, operationID,
	)
	if err != nil {
		return fmt.Errorf("bind restore activation workload completion: %w", err)
	}
	workloads := make(map[string][]byte, len(custody))
	for _, retained := range custody {
		if retained.Project == "" || len(retained.Bundle.Data) == 0 {
			return errors.New("restore activation workload completion authority is incomplete")
		}
		if _, duplicate := workloads[retained.Project]; duplicate {
			return fmt.Errorf("restore activation workload completion repeats Compose project %q", retained.Project)
		}
		workloads[retained.Project] = append([]byte(nil), retained.Bundle.Data...)
	}
	runtime.coreModule = moduleRef
	runtime.workloads = workloads
	return nil
}

func (runtime *restoreActivationBootstrapRuntime) Start(
	ctx context.Context,
	authority restoreactivation.Authority,
) error {
	if runtime == nil || runtime.Runtime == nil || runtime.coreModule == "" || runtime.workloads == nil {
		return errors.New("restore activation bootstrap is not bound to verified runtime authority")
	}
	_, coreProject, ok := nativehost.NativeComposeProject(runtime.coreModule)
	if !ok {
		return errors.New("restore activation bootstrap Core module is unsupported")
	}
	runtimes := authority.ComposeRuntimes
	if len(runtimes) == 0 {
		runtimes = []restoreactivation.ComposeRuntime{{
			Project: authority.ComposeProject, Path: authority.ComposePath, Digest: authority.ComposeDigest,
		}}
	}
	expected := make(map[string]struct{}, len(runtime.workloads)+1)
	expected[coreProject] = struct{}{}
	for project := range runtime.workloads {
		expected[project] = struct{}{}
	}
	for _, composeRuntime := range runtimes {
		if _, exists := expected[composeRuntime.Project]; !exists {
			return fmt.Errorf("restore activation bootstrap has no completion authority for Compose project %q", composeRuntime.Project)
		}
		delete(expected, composeRuntime.Project)
	}
	if len(expected) != 0 {
		return errors.New("restore activation bootstrap authority omits a verified Compose project")
	}

	completeCore, err := runtime.prepareCore(ctx, runtime.workspace, runtime.coreModule, runtime.executable)
	if err != nil {
		return fmt.Errorf("prepare restore activation Core bootstrap: %w", err)
	}
	if err := runtime.Runtime.Start(ctx, authority); err != nil {
		return err
	}
	if err := completeCore(ctx); err != nil {
		return fmt.Errorf("complete restore activation Core bootstrap: %w", err)
	}
	projects := make([]string, 0, len(runtime.workloads))
	for project := range runtime.workloads {
		projects = append(projects, project)
	}
	sort.Strings(projects)
	for _, project := range projects {
		if err := runtime.completeWorkload(ctx, runtime.workspace, runtime.workloads[project]); err != nil {
			return fmt.Errorf("complete restore activation workload %q: %w", project, err)
		}
	}
	return nil
}

func restoreActivationCoreModule(plan generationartifact.VerifiedPlan) (string, error) {
	var document struct {
		Modules []struct {
			ID string `json:"id"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(plan.Canonical(), &document); err != nil {
		return "", fmt.Errorf("decode verified Plan for restore activation bootstrap: %w", err)
	}
	moduleRef := ""
	for _, module := range document.Modules {
		if _, supported := nativehost.LocalCoreRecoveryProfileForModule(module.ID); !supported {
			continue
		}
		if moduleRef != "" {
			return "", errors.New("restore activation bootstrap Core selection is ambiguous")
		}
		moduleRef = module.ID
	}
	if moduleRef == "" {
		return "", errors.New("restore activation bootstrap has no supported local Core module")
	}
	return moduleRef, nil
}
