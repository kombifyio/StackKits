package terramatehost

import (
	"context"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

// StackPlan is one read-only detailed-exitcode plan of a local stack outside
// a change set (per-stack drift detection, docs/ARCHITECTURE.md "Advanced
// drift per stack (Stage 1)").
type StackPlan struct {
	Result StackResult
	// Stdout is the plan output; empty when the plan did not run.
	Stdout string
}

// MissingCoreRoots returns the IDs of the layout's core stacks whose runtime
// root has no `main.tf`. Only the runtime executor creates core roots, so a
// host project with a missing core root must not be materialized.
func MissingCoreRoots(workspaceRoot string, layout Layout) ([]string, error) {
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, err
	}
	missing := make([]string, 0)
	for _, stack := range layout.Manifest.Stacks {
		if stack.Role == string(terramatestackgraph.RoleCore) && !hasOpenTofuRoot(workspace, stack.RuntimeRoot) {
			missing = append(missing, stack.ID)
		}
	}
	return missing, nil
}

// PlanLocalStack runs `terramate run --no-recursive --tags stackkit -- tofu
// plan -detailed-exitcode -input=false -no-color` in one stack root of the
// layout's host, with the same process environment as Converge. It never
// applies. The host project must already be materialized.
func PlanLocalStack(ctx context.Context, workspaceRoot string, layout Layout, tools Tools, timeout time.Duration, stack terramatestackgraph.Stack) (StackPlan, error) {
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return StackPlan{}, err
	}
	request := ConvergeRequest{WorkspaceRoot: workspace, Layout: layout, Tools: tools, Timeout: timeout}
	result := StackResult{
		StackID: stack.ID, Role: string(stack.Role), SiteRef: stack.SiteRef, NodeRef: stack.NodeRef,
		RuntimeRoot: stack.RuntimeRoot,
	}
	if stack.SiteRef != layout.Host.SiteRef || stack.NodeRef != layout.Host.NodeRef {
		result.Status = StackOtherHost
		return StackPlan{Result: result}, nil
	}
	result, stdout := runStackPlan(ctx, workspace, request, stack, result)
	return StackPlan{Result: result, Stdout: stdout}, nil
}
