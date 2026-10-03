package advancedrollback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/kombifyio/stackkits/internal/terramatehost"
	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

// Request is one coordinated rollback of the local host to one verified
// checkpoint.
type Request struct {
	WorkspaceRoot        string
	RollbackID           string
	TargetSnapshotID     string
	ChangeSetID          string
	LifecycleOperationID string
	// Current are the local stacks of the current generation, Target those
	// of the checkpoint, each in host run order.
	Current []Stack
	Target  []Stack
	// TargetRoots are the verified OpenTofu roots of the checkpoint.
	TargetRoots []TargetRoot
	Tools       terramatehost.Tools
	// Environment adds process environment for one stack's OpenTofu run,
	// such as the Compose interpolation environment of a Core payload.
	Environment func(Stack) ([]string, error)
	// Native runs the native Apply side steps around a restored or
	// recreated stack's forced apply, the steps the runtime executor runs
	// around its own `tofu apply`; nil runs none.
	Native  NativeSteps
	Timeout time.Duration
	// Event receives `stack` progress with the per-stack status.
	Event func(phase, status string, attributes map[string]string)
	Now   func() time.Time
}

// NativeSteps binds a stack to the native Apply side steps a Compose payload
// cannot express. Prepare runs after the checkpoint files are written and
// before the forced apply; the returned completion (nil for a stack without
// side steps) runs after the apply and before the convergence plan. A
// failure of either fails the stack, and a resumed rollback repeats both.
type NativeSteps interface {
	Prepare(ctx context.Context, stack Stack) (complete func(context.Context) error, err error)
}

func (request Request) now() time.Time {
	if request.Now != nil {
		return request.Now().UTC()
	}
	return time.Now().UTC()
}

// Prepare returns the unfinished journal of the rollback to the target
// checkpoint (resumed), or plans a new rollback and persists its journal
// before any runtime side effect. The plan is computed once: a resumed
// rollback executes the original plan, so a stack destroyed by the first
// invocation is never classified again.
func Prepare(request Request) (Journal, bool, error) {
	existing, found, err := LoadJournal(request.WorkspaceRoot, request.TargetSnapshotID)
	if err != nil {
		return Journal{}, false, err
	}
	if found && existing.InProgress() {
		return existing, true, nil
	}
	if strings.TrimSpace(request.RollbackID) == "" || strings.TrimSpace(request.LifecycleOperationID) == "" {
		return Journal{}, false, &Error{Code: ErrInvalid, Detail: "rollback and lifecycle operation IDs are required"}
	}
	steps, err := Plan(request)
	if err != nil {
		return Journal{}, false, err
	}
	now := request.now()
	journal := Journal{
		SchemaVersion: JournalSchemaVersion, RollbackID: request.RollbackID,
		TargetSnapshotID: request.TargetSnapshotID, ChangeSetID: request.ChangeSetID,
		LifecycleOperationID: request.LifecycleOperationID, Status: journalRunning,
		Steps: steps, Results: map[string]StepResult{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := SaveJournal(request.WorkspaceRoot, journal); err != nil {
		return Journal{}, false, err
	}
	return journal, false, nil
}

// Plan decides one action per local stack. Stacks of the current graph run
// first in reverse run order, so every dependent is destroyed or restored
// before the stacks it runs after; stacks only the checkpoint has are
// recreated afterwards in run order, because they run after the restored
// cores.
func Plan(request Request) ([]Step, error) {
	targetRoots := make(map[string]RootFiles, len(request.TargetRoots))
	for _, root := range request.TargetRoots {
		targetRoots[root.RuntimeRoot] = root.Files
	}
	inTarget := make(map[string]bool, len(request.Target))
	for _, stack := range request.Target {
		inTarget[stack.RuntimeRoot] = true
	}
	inCurrent := make(map[string]bool, len(request.Current))
	steps := make([]Step, 0, len(request.Current)+len(request.Target))
	for index := len(request.Current) - 1; index >= 0; index-- {
		stack := request.Current[index]
		inCurrent[stack.RuntimeRoot] = true
		step := Step{StackID: stack.ID, Role: stack.Role, RuntimeRoot: stack.RuntimeRoot, Action: ActionUnchanged}
		files, captured := targetRoots[stack.RuntimeRoot]
		switch {
		case stack.Role == hostRole:
			// The host pre-step owns no root and is never destroyed: it is
			// reconciled to its baseline, whatever the checkpoint's graph
			// held (a checkpoint older than the host stack has none).
			step.Action = ActionRestored
		case !inTarget[stack.RuntimeRoot]:
			// Added after the checkpoint: tear it down if anything of it
			// exists on disk.
			exists, err := directoryExists(request.WorkspaceRoot, stack.RuntimeRoot)
			if err != nil {
				return nil, err
			}
			if exists {
				step.Action = ActionDestroyed
			}
		case !captured:
			// The checkpoint's graph has the stack but captured no root
			// (a root the executor had not materialized yet). The stack is
			// left alone: destroying it would stop a runtime the checkpoint
			// ran natively.
		default:
			applied, err := hasAppliedRoot(request.WorkspaceRoot, stack.RuntimeRoot)
			if err != nil {
				return nil, err
			}
			if !applied {
				step.Action = ActionRecreated
				break
			}
			equal, err := rootEquals(request.WorkspaceRoot, stack.RuntimeRoot, files)
			if err != nil {
				return nil, err
			}
			if !equal {
				step.Action = ActionRestored
			}
		}
		if err := validateStep(step); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	for _, stack := range request.Target {
		if inCurrent[stack.RuntimeRoot] {
			continue
		}
		if _, captured := targetRoots[stack.RuntimeRoot]; !captured {
			continue
		}
		step := Step{StackID: stack.ID, Role: stack.Role, RuntimeRoot: stack.RuntimeRoot, Action: ActionRecreated}
		if err := validateStep(step); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// Execute runs the journal's plan, persisting each step's outcome. Steps a
// previous invocation already converged are skipped. The first failing step
// stops the rollback; the journal stays in progress so a second invocation
// resumes at that step.
func Execute(ctx context.Context, request Request, journal Journal, resumed bool) (Report, error) {
	targetRoots := make(map[string]RootFiles, len(request.TargetRoots))
	for _, root := range request.TargetRoots {
		targetRoots[root.RuntimeRoot] = root.Files
	}
	emit := request.Event
	if emit == nil {
		emit = func(string, string, map[string]string) {}
	}
	var failure error
	for _, step := range journal.Steps {
		previous := journal.Results[step.RuntimeRoot]
		if previous.Status == StackConverged || previous.Status == StackSkipped {
			continue
		}
		if failure != nil {
			break
		}
		result := runStep(ctx, request, &journal, step, targetRoots)
		journal.Results[step.RuntimeRoot] = result
		journal.UpdatedAt = request.now()
		if err := SaveJournal(request.WorkspaceRoot, journal); err != nil {
			return report(journal, resumed), err
		}
		attributes := map[string]string{
			"rollbackId": journal.RollbackID, "stackId": step.StackID, "role": step.Role,
			"action": step.Action, "runtimeRoot": step.RuntimeRoot,
			"durationMs": strconv.FormatInt(result.DurationMS, 10),
		}
		if result.PlanExitCode != nil {
			attributes["planExitCode"] = strconv.Itoa(*result.PlanExitCode)
		}
		emit("stack", result.Status, attributes)
		if result.Status == StackFailed {
			failure = &Error{Code: ErrNotConverged, Stacks: []string{step.StackID}, Detail: result.Detail}
		}
	}
	if failure != nil {
		return report(journal, resumed), failure
	}
	journal.Status = journalConverged
	journal.UpdatedAt = request.now()
	if err := SaveJournal(request.WorkspaceRoot, journal); err != nil {
		return report(journal, resumed), err
	}
	return report(journal, resumed), nil
}

func report(journal Journal, resumed bool) Report {
	result := Report{
		SchemaVersion: ResultSchemaVersion, RollbackID: journal.RollbackID,
		TargetSnapshotID: journal.TargetSnapshotID, ChangeSetID: journal.ChangeSetID,
		LifecycleOperationID: journal.LifecycleOperationID, Resumed: resumed,
		Order: make([]string, 0, len(journal.Steps)), Stacks: make([]StackReport, 0, len(journal.Steps)),
		SealStatus: SealNotAttempted, Status: StatusConverged,
	}
	for _, step := range journal.Steps {
		outcome, ran := journal.Results[step.RuntimeRoot]
		if !ran {
			outcome.Status = StackPending
		}
		if outcome.Status != StackConverged && outcome.Status != StackSkipped {
			result.Status = StatusFailed
		}
		result.Order = append(result.Order, step.StackID)
		result.Stacks = append(result.Stacks, StackReport{
			StackID: step.StackID, Role: step.Role, RuntimeRoot: step.RuntimeRoot, Action: step.Action,
			Status: outcome.Status, PlanExitCode: outcome.PlanExitCode, DurationMS: outcome.DurationMS,
			Detail: outcome.Detail,
		})
	}
	return result
}

func runStep(ctx context.Context, request Request, journal *Journal, step Step, targetRoots map[string]RootFiles) StepResult {
	started := time.Now()
	// A retried step keeps only the fact that its checkpoint files are
	// already written, so the forced apply does not clobber state a
	// previous attempt advanced.
	result := StepResult{FilesRestored: journal.Results[step.RuntimeRoot].FilesRestored}
	finish := func(status, detail string) StepResult {
		result.Status = status
		result.Detail = detail
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	stack := Stack{ID: step.StackID, Role: step.Role, RuntimeRoot: step.RuntimeRoot}
	if step.Role == hostRole {
		return runHostStep(ctx, request, step, result, finish)
	}
	var environment []string
	if request.Environment != nil && step.Action != ActionUnchanged {
		var err error
		if environment, err = request.Environment(stack); err != nil {
			return finish(StackFailed, "resolve the stack environment: "+err.Error())
		}
	}
	run := func(args ...string) (terramatehost.StackTofuResult, error) {
		return terramatehost.RunStackTofu(ctx, terramatehost.StackTofuRequest{
			WorkspaceRoot: request.WorkspaceRoot, Tools: request.Tools, RuntimeRoot: step.RuntimeRoot,
			Env: environment, Timeout: request.Timeout,
		}, args...)
	}
	switch step.Action {
	case ActionUnchanged:
		return finish(StackSkipped, "")
	case ActionDestroyed:
		applied, err := hasAppliedRoot(request.WorkspaceRoot, step.RuntimeRoot)
		if err != nil {
			return finish(StackFailed, err.Error())
		}
		if applied {
			if detail := ensureInitialized(request.WorkspaceRoot, step.RuntimeRoot, run); detail != "" {
				return finish(StackFailed, detail)
			}
			// A native root forgets its data volumes (`destroy = false`);
			// the flag keeps that successful destroy from exiting 1.
			destroyed, err := run("destroy", "-auto-approve", "-input=false", "-no-color", "-suppress-forget-errors")
			if err != nil || destroyed.ExitCode != 0 {
				return finish(StackFailed, commandDetail("tofu destroy", destroyed, err))
			}
		}
		if err := removeRoot(request.WorkspaceRoot, step.RuntimeRoot); err != nil {
			return finish(StackFailed, err.Error())
		}
		return finish(StackConverged, "")
	case ActionRestored, ActionRecreated:
		files, captured := targetRoots[step.RuntimeRoot]
		if !captured {
			return finish(StackFailed, "the target checkpoint has no captured root for this stack")
		}
		targetNative, err := architecturev2renderer.NativeDockerRootConfig(files.Config)
		if err != nil {
			return finish(StackFailed, "read the checkpoint root: "+err.Error())
		}
		currentNative := currentExecutionNative(request.WorkspaceRoot, step.RuntimeRoot)
		if !result.FilesRestored {
			// A native Docker-provider root on either side is left with a
			// forget-safe destroy of the current root before the checkpoint
			// files are written: the restored state does not track the
			// containers a newer revision created, so they would block the
			// restored ones by name, and a wrapper and a native root never
			// own the same containers at once. Data volumes stay.
			if step.Action == ActionRestored && (targetNative || currentNative) {
				if detail := ensureInitialized(request.WorkspaceRoot, step.RuntimeRoot, run); detail != "" {
					return finish(StackFailed, detail)
				}
				left, err := run("destroy", "-auto-approve", "-input=false", "-no-color", "-suppress-forget-errors")
				if err != nil || left.ExitCode != 0 {
					return finish(StackFailed, commandDetail("tofu destroy of the current root", left, err))
				}
			}
			if err := restoreRoot(request.WorkspaceRoot, step.RuntimeRoot, files); err != nil {
				return finish(StackFailed, err.Error())
			}
			result.FilesRestored = true
			journal.Results[step.RuntimeRoot] = result
			if err := SaveJournal(request.WorkspaceRoot, *journal); err != nil {
				return finish(StackFailed, err.Error())
			}
		}
		if err := opentofu.AlignRestoredWorkloadRoot(request.WorkspaceRoot, step.RuntimeRoot); err != nil {
			return finish(StackFailed, "align the restored workload root: "+err.Error())
		}
		applyArgs, err := terramatehost.ForcedApplyArgs(files.Config)
		if err != nil {
			return finish(StackFailed, err.Error())
		}
		if targetNative || currentNative {
			// The restored lock can name other providers than the ones the
			// root was initialized with.
			initialized, err := run("init", "-input=false", "-lockfile=readonly", "-no-color")
			if err != nil || initialized.ExitCode != 0 {
				return finish(StackFailed, commandDetail("tofu init", initialized, err))
			}
		} else if detail := ensureInitialized(request.WorkspaceRoot, step.RuntimeRoot, run); detail != "" {
			return finish(StackFailed, detail)
		}
		var complete func(context.Context) error
		if request.Native != nil {
			if complete, err = request.Native.Prepare(ctx, stack); err != nil {
				return finish(StackFailed, "prepare the native side steps: "+err.Error())
			}
		}
		// Restored state and payload plan as a no-op even when newer
		// containers still run, so a wrapper trigger is replaced
		// explicitly: its create-time provisioner runs `up` against the
		// restored payload. A native root converges with a plain apply.
		applied, err := run(applyArgs...)
		if err != nil || applied.ExitCode != 0 {
			return finish(StackFailed, commandDetail("tofu "+strings.Join(applyArgs, " "), applied, err))
		}
		if complete != nil {
			if err := complete(ctx); err != nil {
				return finish(StackFailed, "complete the native side steps: "+err.Error())
			}
		}
		planned, err := run("plan", "-detailed-exitcode", "-input=false", "-no-color")
		if err != nil {
			return finish(StackFailed, commandDetail("tofu plan", planned, err))
		}
		code := planned.ExitCode
		result.PlanExitCode = &code
		if code != 0 {
			return finish(StackFailed, fmt.Sprintf("tofu plan -detailed-exitcode exited %d after the forced apply", code))
		}
		return finish(StackConverged, "")
	default:
		return finish(StackFailed, "unknown action "+step.Action)
	}
}

// currentExecutionNative reports whether the workload root's executor marker
// records a native Docker-provider execution.
func currentExecutionNative(workspaceRoot, runtimeRoot string) bool {
	absolute, err := confinedFile(workspaceRoot, runtimeRoot, false)
	if err != nil {
		return false
	}
	marker, err := opentofu.ReadRootMarker(absolute)
	return err == nil && marker.Execution == opentofu.RootExecutionNativeDocker
}

func commandDetail(command string, result terramatehost.StackTofuResult, err error) string {
	detail := command
	if err != nil {
		detail += ": " + err.Error()
	} else {
		detail += fmt.Sprintf(" exited %d", result.ExitCode)
		if result.Detail != "" {
			detail += ": " + result.Detail
		}
	}
	return detail
}

func ensureInitialized(
	workspaceRoot, runtimeRoot string,
	run func(...string) (terramatehost.StackTofuResult, error),
) string {
	root, err := confinedFile(workspaceRoot, path.Join(runtimeRoot, ".terraform"), false)
	if err != nil {
		return err.Error()
	}
	if info, statErr := os.Lstat(root); statErr == nil && info.IsDir() {
		return ""
	}
	initialized, err := run("init", "-input=false", "-lockfile=readonly", "-no-color")
	if err != nil || initialized.ExitCode != 0 {
		return commandDetail("tofu init", initialized, err)
	}
	return ""
}

func rootFilePaths(runtimeRoot string) (state, config, lock, compose, environment string) {
	parent := path.Dir(runtimeRoot)
	return path.Join(runtimeRoot, opentofu.StateFile),
		path.Join(runtimeRoot, opentofu.ConfigFile),
		path.Join(runtimeRoot, opentofu.LockFile),
		path.Join(parent, opentofu.ComposeFile),
		path.Join(parent, opentofu.EnvFile)
}

func hasAppliedRoot(workspaceRoot, runtimeRoot string) (bool, error) {
	state, config, _, _, _ := rootFilePaths(runtimeRoot)
	for _, relative := range []string{config, state} {
		absolute, err := confinedFile(workspaceRoot, relative, false)
		if err != nil {
			return false, err
		}
		info, err := os.Lstat(absolute)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil || !info.Mode().IsRegular() {
			return false, fmt.Errorf("%s must be a regular file", relative)
		}
	}
	return true, nil
}

func directoryExists(workspaceRoot, runtimeRoot string) (bool, error) {
	absolute, err := confinedFile(workspaceRoot, runtimeRoot, false)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%s must be a plain directory", runtimeRoot)
	}
	return true, nil
}

func rootEquals(workspaceRoot, runtimeRoot string, files RootFiles) (bool, error) {
	state, config, lock, compose, environment := rootFilePaths(runtimeRoot)
	expected := []struct {
		path    string
		data    []byte
		present bool
	}{
		{state, files.State, true}, {config, files.Config, true}, {lock, files.Lock, files.HasLock},
		{compose, files.Compose, files.HasCompose}, {environment, files.Environment, files.HasEnvironment},
	}
	for _, file := range expected {
		if !file.present {
			continue
		}
		absolute, err := confinedFile(workspaceRoot, file.path, false)
		if err != nil {
			return false, err
		}
		current, err := os.ReadFile(absolute)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read %s: %w", file.path, err)
		}
		if !bytes.Equal(current, file.data) {
			return false, nil
		}
	}
	return true, nil
}

// restoreRoot writes the checkpoint payload first and the state last, the
// order executor-state recovery uses. A recreated root must still hold the
// executor's root marker: the runtime executor, not the rollback, creates
// roots.
func restoreRoot(workspaceRoot, runtimeRoot string, files RootFiles) error {
	marker, err := confinedFile(workspaceRoot, path.Join(runtimeRoot, opentofu.MarkerFile), false)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(marker); statErr != nil || !info.Mode().IsRegular() {
		return errors.New("the runtime root has no executor root marker; the runtime executor must materialize the root before it can be restored")
	}
	state, config, lock, compose, environment := rootFilePaths(runtimeRoot)
	type rootWrite struct {
		path string
		data []byte
		mode os.FileMode
	}
	writes := make([]rootWrite, 0, 5)
	if files.HasEnvironment {
		writes = append(writes, rootWrite{environment, files.Environment, 0o600})
	}
	if files.HasCompose {
		writes = append(writes, rootWrite{compose, files.Compose, 0o600})
	}
	if files.HasLock {
		writes = append(writes, rootWrite{lock, files.Lock, 0o640})
	}
	writes = append(writes, rootWrite{config, files.Config, 0o640}, rootWrite{state, files.State, 0o600})
	for _, write := range writes {
		absolute, err := confinedFile(workspaceRoot, write.path, false)
		if err != nil {
			return err
		}
		if err := writeAtomic(absolute, write.data, write.mode); err != nil {
			return err
		}
	}
	return nil
}

// removeRoot deletes a destroyed stack's runtime root and, for a workload or
// module stack, its project directory (Compose file and .env) too. Data
// volumes are Docker objects and stay.
func removeRoot(workspaceRoot, runtimeRoot string) error {
	target := runtimeRoot
	parent := path.Dir(runtimeRoot)
	switch path.Dir(parent) {
	case path.Join(".stackkit", "runtime", opentofu.ApplicationsDir),
		path.Join(".stackkit", "runtime", opentofu.ModulesDir):
		target = parent
	}
	absolute, err := confinedFile(workspaceRoot, target, false)
	if err != nil {
		return err
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a plain directory", target)
	}
	if err := os.RemoveAll(absolute); err != nil {
		return fmt.Errorf("remove %s: %w", filepath.ToSlash(target), err)
	}
	return nil
}

// hostRole is the role of the host pre-step stack (owner decision O1).
const hostRole = "host"

// runHostStep reconciles the host pre-step through the pinned StackKits CLI:
// repair, then the verify loop as the convergence proof, in place of the
// forced apply and detailed-exitcode plan of an OpenTofu root.
func runHostStep(
	ctx context.Context, request Request, step Step, result StepResult,
	finish func(status, detail string) StepResult,
) StepResult {
	restored, err := terramatehost.RestoreHostStep(ctx, request.WorkspaceRoot, request.Tools, request.Timeout, terramatestackgraph.Stack{
		ID: step.StackID, Role: terramatestackgraph.RoleHost, RuntimeRoot: step.RuntimeRoot,
	})
	if err != nil {
		return finish(StackFailed, err.Error())
	}
	result.PlanExitCode = restored.PlanExitCode
	if restored.Status != terramatehost.StackConverged {
		return finish(StackFailed, restored.Detail)
	}
	return finish(StackConverged, "")
}
