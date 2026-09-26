package terramatehost

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

// ReconcileRequest forces the drifted local stacks of an Advanced drift
// reconcile to re-converge (docs/ARCHITECTURE.md "Advanced drift per stack
// (Stage 1)"). Runtime drift such as a stopped container or an edited
// payload leaves the desired state unchanged, so a plain `tofu apply` of the
// wrapper root is a no-op for the containers; replacing the wrapper trigger
// runs its create-time `docker compose up` against the root's payload.
type ReconcileRequest struct {
	WorkspaceRoot string
	Layout        Layout
	// Stacks are the stack IDs to force. Stacks of other hosts and stacks
	// without an OpenTofu root are left alone; every other stack of the
	// layout is never touched.
	Stacks []string
	Tools  Tools
	// Environment adds the process environment of one stack's OpenTofu run,
	// such as the Compose interpolation environment of a Core payload.
	Environment func(terramatestackgraph.Stack) ([]string, error)
	Timeout     time.Duration
	// Event receives one `reconcile` event per forced stack.
	Event EventFunc
}

// ForceConverge runs, in host run order, `terramate run --no-recursive --tags
// stackkit -- tofu apply -auto-approve -input=false -no-color
// -replace=<wrapper trigger>` in the root of every requested local stack.
// The trigger is the root's `terraform_data` `_up` resource
// (ReplaceTriggerAddress), so the replacement never runs the destroy-time
// `docker compose down`. The apply also applies any other planned change of
// the root, such as a rewritten payload file. The host project is
// materialized first; the first failed stack stops the run.
func ForceConverge(ctx context.Context, request ReconcileRequest) ([]StackResult, error) {
	emit := request.Event
	if emit == nil {
		emit = func(string, string, map[string]string) {}
	}
	results := make([]StackResult, 0, len(request.Stacks))
	if len(request.Stacks) == 0 {
		return results, nil
	}
	if request.Tools.Terramate == "" || request.Tools.Tofu == "" {
		return results, &Error{Code: ErrToolMissing, Detail: "Terramate and OpenTofu binaries are required"}
	}
	workspace, err := filepath.Abs(request.WorkspaceRoot)
	if err != nil {
		return results, err
	}
	layout := request.Layout
	if _, err := Materialize(workspace, layout); err != nil {
		return results, err
	}
	for _, id := range layout.Host.RunOrder {
		if !slices.Contains(request.Stacks, id) {
			continue
		}
		stack, found := layout.Stack(id)
		if !found || stack.SiteRef != layout.Host.SiteRef || stack.NodeRef != layout.Host.NodeRef ||
			!hasOpenTofuRoot(workspace, stack.RuntimeRoot) {
			continue
		}
		result := forceStack(ctx, workspace, request, stack)
		results = append(results, result)
		attributes := map[string]string{
			"stackId": stack.ID, "role": string(stack.Role), "runtimeRoot": stack.RuntimeRoot,
			"durationMs": strconv.FormatInt(result.DurationMS, 10),
		}
		emit("reconcile", result.Status, attributes)
		if result.Status != StackConverged {
			return results, &Error{Code: ErrNotConverged, Stacks: []string{stack.ID}, Detail: "a drifted stack could not be forced to re-converge: " + result.Detail}
		}
	}
	return results, nil
}

func forceStack(ctx context.Context, workspace string, request ReconcileRequest, stack terramatestackgraph.Stack) StackResult {
	started := time.Now()
	result := StackResult{
		StackID: stack.ID, Role: string(stack.Role), SiteRef: stack.SiteRef, NodeRef: stack.NodeRef,
		RuntimeRoot: stack.RuntimeRoot,
	}
	finish := func(status, detail string) StackResult {
		result.Status, result.Detail = status, boundedDetail(detail)
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	root := filepath.Join(workspace, filepath.FromSlash(stack.RuntimeRoot))
	config, err := os.ReadFile(filepath.Join(root, OpenTofuConfigFile))
	if err != nil {
		return finish(StackFailed, "read the stack root: "+err.Error())
	}
	address, err := ReplaceTriggerAddress(config)
	if err != nil {
		return finish(StackFailed, err.Error())
	}
	var environment []string
	if request.Environment != nil {
		if environment, err = request.Environment(stack); err != nil {
			return finish(StackFailed, "resolve the stack environment: "+err.Error())
		}
	}
	run := func(args ...string) (StackTofuResult, error) {
		return RunStackTofu(ctx, StackTofuRequest{
			WorkspaceRoot: workspace, Tools: request.Tools, RuntimeRoot: stack.RuntimeRoot,
			Env: environment, Timeout: request.Timeout,
		}, args...)
	}
	if info, statErr := os.Stat(filepath.Join(root, ".terraform")); statErr != nil || !info.IsDir() {
		initialized, initErr := run("init", "-input=false", "-no-color")
		if initErr != nil || initialized.ExitCode != 0 {
			return finish(StackFailed, stackCommandDetail("tofu init", initialized, initErr))
		}
	}
	applied, err := run("apply", "-auto-approve", "-input=false", "-no-color", "-replace="+address)
	if err != nil || applied.ExitCode != 0 {
		return finish(StackFailed, stackCommandDetail("tofu apply -replace="+address, applied, err))
	}
	return finish(StackConverged, "")
}

func stackCommandDetail(command string, result StackTofuResult, err error) string {
	switch {
	case err != nil:
		return command + ": " + err.Error()
	case result.Detail != "":
		return command + " exited " + strconv.Itoa(result.ExitCode) + ": " + result.Detail
	default:
		return command + " exited " + strconv.Itoa(result.ExitCode)
	}
}
