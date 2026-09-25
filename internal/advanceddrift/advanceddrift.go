// Package advanceddrift detects drift per Terramate stack of the local host
// (ADR-0045 section 2 and section 3, docs/ARCHITECTURE.md "Advanced drift per
// stack (Stage 1)").
//
// Stack-level drift is what `tofu plan -detailed-exitcode` sees in each
// OpenTofu root. Container state that changes outside OpenTofu stays
// invisible to that plan; the native container drift detection of
// `stackkit drift detect` keeps covering it (ADR-0045 section 5), and the
// drift report combines both.
package advanceddrift

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/terramatehost"
	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

// EventPhase is the rollout event phase emitted once per stack, so Techstack
// can stream one drift subject per stack.
const EventPhase = "advanced.drift.stack"

// Per-stack statuses.
const (
	StackConverged   = terramatehost.StackConverged
	StackDrifted     = terramatehost.StackDrifted
	StackFailed      = terramatehost.StackFailed
	StackPendingRoot = terramatehost.StackPendingRoot
)

// Overall statuses of a drift report that carries stacks.
const (
	StatusClean   = "clean"
	StatusDrifted = "drifted"
	StatusUnknown = "unknown"
)

// Summary is the resource count of a plan's `Plan: X to add, Y to change,
// Z to destroy.` line.
type Summary struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
}

// Stack is one `stacks[]` entry of `stackkit.drift-report/v1`.
type Stack struct {
	StackID      string   `json:"stackId"`
	Role         string   `json:"role"`
	ModuleRef    string   `json:"moduleRef"`
	SiteRef      string   `json:"siteRef"`
	NodeRef      string   `json:"nodeRef"`
	RuntimeRoot  string   `json:"runtimeRoot"`
	Status       string   `json:"status"`
	PlanExitCode *int     `json:"planExitCode,omitempty"`
	Summary      *Summary `json:"summary,omitempty"`
	DurationMS   int64    `json:"durationMs"`
	Detail       string   `json:"detail,omitempty"`
}

// Request is one per-stack drift detection over the local host project.
type Request struct {
	WorkspaceRoot string
	Layout        terramatehost.Layout
	Tools         terramatehost.Tools
	// ToolsErr reports that the packaged binaries are unavailable; every
	// stack is then failed without any process run.
	ToolsErr error
	Timeout  time.Duration
	// Event receives every finished stack entry in run order.
	Event func(Stack)
}

// Detect plans every stack of the local host in run order and never applies.
// A missing core root leaves the host project unmaterialized and reports
// every local stack as pending_root. An error is returned only for an
// invalid request; stack-level failures are entries with status failed.
func Detect(ctx context.Context, request Request) ([]Stack, error) {
	layout := request.Layout
	emit := request.Event
	if emit == nil {
		emit = func(Stack) {}
	}
	stacks := make([]Stack, 0, len(layout.Host.RunOrder))
	local := make([]terramatestackgraph.Stack, 0, len(layout.Host.RunOrder))
	for _, id := range layout.Host.RunOrder {
		stack, found := layout.Stack(id)
		if !found {
			return nil, fmt.Errorf("host run order names unknown stack %s", id)
		}
		local = append(local, stack)
	}
	finish := func(entry Stack) {
		stacks = append(stacks, entry)
		emit(entry)
	}
	skipAll := func(status, detail string) []Stack {
		for _, stack := range local {
			entry := newEntry(stack)
			entry.Status, entry.Detail = status, detail
			finish(entry)
		}
		return stacks
	}

	if request.ToolsErr != nil || request.Tools.Terramate == "" || request.Tools.Tofu == "" {
		detail := "packaged Terramate and OpenTofu binaries are unavailable"
		if request.ToolsErr != nil {
			detail = request.ToolsErr.Error()
		}
		return skipAll(StackFailed, detail), nil
	}
	missing, err := terramatehost.MissingCoreRoots(request.WorkspaceRoot, layout)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		return skipAll(StackPendingRoot, "core OpenTofu roots are not installed ("+strings.Join(missing, ", ")+"); the host project was not materialized"), nil
	}
	if _, err := terramatehost.Materialize(request.WorkspaceRoot, layout); err != nil {
		return skipAll(StackFailed, "materialize host project: "+err.Error()), nil
	}
	for _, stack := range local {
		plan, err := terramatehost.PlanLocalStack(ctx, request.WorkspaceRoot, layout, request.Tools, request.Timeout, stack)
		entry := newEntry(stack)
		if err != nil {
			entry.Status, entry.Detail = StackFailed, err.Error()
			finish(entry)
			continue
		}
		entry.Status = plan.Result.Status
		entry.PlanExitCode = plan.Result.PlanExitCode
		entry.DurationMS = plan.Result.DurationMS
		entry.Detail = plan.Result.Detail
		entry.Summary = ParseSummary(plan.Stdout)
		if entry.Status == StackDrifted {
			entry.Detail = "tofu plan reports changes against the recorded OpenTofu state"
		}
		finish(entry)
	}
	return stacks, nil
}

func newEntry(stack terramatestackgraph.Stack) Stack {
	return Stack{
		StackID: stack.ID, Role: string(stack.Role), ModuleRef: stack.ModuleRef,
		SiteRef: stack.SiteRef, NodeRef: stack.NodeRef, RuntimeRoot: stack.RuntimeRoot,
	}
}

var planSummaryLine = regexp.MustCompile(`Plan: (\d+) to add, (\d+) to change, (\d+) to destroy`)

// ParseSummary reads the last `Plan: X to add, Y to change, Z to destroy`
// line of a plan output. `No changes.` yields a zero summary; any other
// output yields nil.
func ParseSummary(stdout string) *Summary {
	matches := planSummaryLine.FindAllStringSubmatch(stdout, -1)
	if len(matches) == 0 {
		if strings.Contains(stdout, "No changes.") {
			return &Summary{}
		}
		return nil
	}
	last := matches[len(matches)-1]
	add, _ := strconv.Atoi(last[1])
	change, _ := strconv.Atoi(last[2])
	destroy, _ := strconv.Atoi(last[3])
	return &Summary{Add: add, Change: change, Destroy: destroy}
}

// Required reports whether a pending_root stack prevents a clean report.
// Core and workload roots must exist; edge and federation roots are not
// materialized by the executor yet.
func Required(role string) bool {
	return role == string(terramatestackgraph.RoleCore) || role == string(terramatestackgraph.RoleWorkload)
}

// OverallStatus combines native drift and the stack entries: drifted when the
// native report or any stack drifted, otherwise unknown when any stack failed
// or a required root is pending, otherwise clean.
func OverallStatus(nativeDrift bool, stacks []Stack) string {
	unknown := false
	for _, stack := range stacks {
		switch stack.Status {
		case StackDrifted:
			return StatusDrifted
		case StackFailed:
			unknown = true
		case StackPendingRoot:
			if Required(stack.Role) {
				unknown = true
			}
		}
	}
	switch {
	case nativeDrift:
		return StatusDrifted
	case unknown:
		return StatusUnknown
	}
	return StatusClean
}

// EventAttributes are the rollout event attributes of one stack entry.
func EventAttributes(stack Stack) map[string]string {
	attributes := map[string]string{
		"stackId": stack.StackID, "role": stack.Role, "moduleRef": stack.ModuleRef,
		"siteRef": stack.SiteRef, "nodeRef": stack.NodeRef, "runtimeRoot": stack.RuntimeRoot,
		"durationMs": strconv.FormatInt(stack.DurationMS, 10),
	}
	if stack.PlanExitCode != nil {
		attributes["planExitCode"] = strconv.Itoa(*stack.PlanExitCode)
	}
	if stack.Summary != nil {
		attributes["add"] = strconv.Itoa(stack.Summary.Add)
		attributes["change"] = strconv.Itoa(stack.Summary.Change)
		attributes["destroy"] = strconv.Itoa(stack.Summary.Destroy)
	}
	return attributes
}
