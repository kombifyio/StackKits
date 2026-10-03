package terramatehost

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

// Exit codes of `stackkit host security verify --fail-on-drift` and
// `repair --apply` that the host pre-step maps to a stack status.
const (
	hostStepExitDrifted = 5
	hostStepExitUnknown = 6
	hostStepExitBlocked = 3
)

// The host pre-step (owner decision O1, ADR-0045 addendum A4) is the Terramate
// stack that runs before every other stack of its host. It owns no OpenTofu
// root: Terramate runs the pinned StackKits CLI in its stack directory.
//
//   - verify is the drift signal: `host security verify --mode advanced
//     --fail-on-drift` records fresh host-security evidence and exits 5 when a
//     control drifted, so an unchanged host is converged and a drifted one is
//     drifted, exactly like `tofu plan -detailed-exitcode`.
//   - repair is the reconcile: `host security repair --apply --mode advanced`
//     restores drifted controls and still refuses a change that would cut the
//     SSH management channel (exit 3, reported as failed).
func verifyHostStep(ctx context.Context, workspace string, request ConvergeRequest, stack terramatestackgraph.Stack, result StackResult) StackResult {
	return runHostStep(ctx, workspace, request, stack, result,
		"host", "security", "verify", "--mode", "advanced", "--json", "--fail-on-drift")
}

func repairHostStep(ctx context.Context, workspace string, request ReconcileRequest, stack terramatestackgraph.Stack) StackResult {
	result := StackResult{
		StackID: stack.ID, Role: string(stack.Role), SiteRef: stack.SiteRef, NodeRef: stack.NodeRef,
		RuntimeRoot: stack.RuntimeRoot,
	}
	converge := ConvergeRequest{WorkspaceRoot: workspace, Layout: request.Layout, Tools: request.Tools, Timeout: request.Timeout}
	return runHostStep(ctx, workspace, converge, stack, result,
		"host", "security", "repair", "--apply", "--mode", "advanced", "--json")
}

func runHostStep(ctx context.Context, workspace string, request ConvergeRequest, stack terramatestackgraph.Stack, result StackResult, args ...string) StackResult {
	started := time.Now()
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	if request.Tools.StackKit == "" {
		result.Status, result.Detail = StackFailed, "the pinned StackKits CLI is required to run the host pre-step"
		return result
	}
	root := filepath.Join(workspace, filepath.FromSlash(stack.RuntimeRoot))
	command := append([]string{"-C", workspace}, args...)
	run, err := request.executor(workspace, root).RunStackCommand(ctx, StackTags, request.Tools.StackKit, command...)
	// RunStackCommand relays the command's own exit code in the result with a
	// nil error; an error means Terramate failed without one.
	switch {
	case run == nil || (err != nil && run.ExitCode == 0):
		result.Status, result.Detail = StackFailed, boundedDetail("terramate run failed: "+errorText(err))
	default:
		code := run.ExitCode
		result.PlanExitCode = &code
		switch code {
		case 0:
			result.Status = StackConverged
		case hostStepExitDrifted:
			result.Status, result.Detail = StackDrifted, "host security baseline is drifted"
		case hostStepExitUnknown:
			result.Status, result.Detail = StackFailed, "host security baseline state is unknown"
		case hostStepExitBlocked:
			result.Status, result.Detail = StackFailed, "a host security repair was refused; the refused change was not made"
		default:
			result.Status = StackFailed
			result.Detail = boundedDetail("host pre-step exited " + strconv.Itoa(code) + ": " + strings.TrimSpace(run.Stderr))
		}
	}
	return result
}

func errorText(err error) string {
	if err == nil {
		return errors.New("no result").Error()
	}
	return err.Error()
}

// RestoreHostStep brings the host pre-step of a coordinated rollback back to
// its baseline: it repairs every drifted control (never a change that would
// cut the SSH management channel) and then proves the host with the verify
// loop. The host-security baseline is policy, not a stored root, so the
// checkpoint's evidence blob records what was proven and the repair restores
// what it describes. The returned status is converged only when verify exits 0.
func RestoreHostStep(ctx context.Context, workspaceRoot string, tools Tools, timeout time.Duration, stack terramatestackgraph.Stack) (StackResult, error) {
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return StackResult{}, err
	}
	result := StackResult{
		StackID: stack.ID, Role: string(stack.Role), SiteRef: stack.SiteRef, NodeRef: stack.NodeRef,
		RuntimeRoot: stack.RuntimeRoot,
	}
	request := ConvergeRequest{WorkspaceRoot: workspace, Tools: tools, Timeout: timeout}
	repaired := repairHostStep(ctx, workspace, ReconcileRequest{WorkspaceRoot: workspace, Tools: tools, Timeout: timeout}, stack)
	if repaired.Status != StackConverged {
		return repaired, nil
	}
	return verifyHostStep(ctx, workspace, request, stack, result), nil
}
