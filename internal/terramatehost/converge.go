package terramatehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/terramate"
	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
	"github.com/kombifyio/stackkits/internal/tofu"
)

const (
	// ResultSchemaVersion identifies the per-stack change-set result
	// (schemas/stackkit-change-set-result-v1.schema.json).
	ResultSchemaVersion = "stackkit.change-set-result/v1"
	// StackTags selects every StackKits stack of a host project.
	StackTags = "stackkit"
	// openTofuCLIConfigFile is the offline CLI configuration the OpenTofu
	// runtime executor writes into every root it installs.
	openTofuCLIConfigFile = "stackkit.tofurc"
	maxDetailBytes        = 2048
)

// Per-stack statuses of a change-set result.
const (
	// StackConverged: `tofu plan -detailed-exitcode` exited 0 after apply.
	StackConverged = "converged"
	// StackDrifted: the plan exited 2, so the applied root still differs from
	// its configuration. The change set fails and rolls back.
	StackDrifted = "drifted"
	// StackFailed: the plan could not run (exit 1 or a Terramate error).
	StackFailed = "failed"
	// StackPendingRoot: the runtime root has no `main.tf` yet. Tolerated only
	// for artifact-only roles (edge, federation), whose roots the executor
	// does not materialize yet; a core or workload root must exist.
	StackPendingRoot = "pending_root"
	// StackOtherHost: the stack belongs to another host project. Its change
	// runs through that host's execution channel (Techstack dispatch).
	StackOtherHost = "other_host"
)

// Overall statuses of a change-set result.
const (
	ResultConverged    = "converged"
	ResultNotConverged = "not_converged"
)

// ErrorCode classifies a failed Terramate orchestration.
type ErrorCode string

const (
	// ErrToolMissing: the packaged Terramate or OpenTofu binary is absent.
	ErrToolMissing ErrorCode = "terramate_tool_missing"
	// ErrHostDiverged: the host layout differs from the one the change set
	// was approved with.
	ErrHostDiverged ErrorCode = "terramate_host_diverged"
	// ErrRunOrder: `terramate list --run-order` disagrees with the graph.
	ErrRunOrder ErrorCode = "terramate_run_order_mismatch"
	// ErrNotConverged: at least one affected stack failed its post-apply
	// convergence plan, lacks a required root, or drifted.
	ErrNotConverged ErrorCode = "advanced_change_set_not_converged"
)

// Error is the structured orchestration failure. Stacks names the affected
// stack IDs that caused it.
type Error struct {
	Code   ErrorCode
	Stacks []string
	Detail string
	Err    error
}

func (e *Error) Error() string {
	message := string(e.Code) + ": " + e.Detail
	if len(e.Stacks) > 0 {
		message += " (stacks " + strings.Join(e.Stacks, ", ") + ")"
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *Error) Unwrap() error { return e.Err }

// Reason returns the structured code of an orchestration error.
func Reason(err error) (ErrorCode, bool) {
	var typed *Error
	if !errors.As(err, &typed) {
		return "", false
	}
	return typed.Code, true
}

// Tools are the packaged binaries a host project runs with.
type Tools struct {
	Terramate string
	Tofu      string
}

// PackagedTools resolves the release-packaged Terramate and OpenTofu binaries
// (STACKKIT_TERRAMATE_BINARY and STACKKIT_TOFU_BINARY override them). It
// never falls back to PATH.
func PackagedTools() (Tools, error) {
	terramateBinary, terramateOK := terramate.PackagedBinaryPath()
	tofuBinary, tofuOK := tofu.PackagedBinaryPath()
	if !terramateOK || !tofuOK {
		return Tools{}, &Error{Code: ErrToolMissing, Detail: "Advanced change sets require the Terramate and OpenTofu binaries packaged with the StackKit release (or STACKKIT_TERRAMATE_BINARY and STACKKIT_TOFU_BINARY)"}
	}
	return Tools{Terramate: terramateBinary, Tofu: tofuBinary}, nil
}

// StackResult is the outcome of one affected stack.
type StackResult struct {
	StackID      string `json:"stackId"`
	Role         string `json:"role"`
	SiteRef      string `json:"siteRef"`
	NodeRef      string `json:"nodeRef"`
	RuntimeRoot  string `json:"runtimeRoot"`
	Status       string `json:"status"`
	PlanExitCode *int   `json:"planExitCode,omitempty"`
	DurationMS   int64  `json:"durationMs"`
	Detail       string `json:"detail,omitempty"`
}

// Report is the `stackkit.change-set-result/v1` document.
type Report struct {
	SchemaVersion      string        `json:"schemaVersion"`
	ChangeSetID        string        `json:"changeSetId"`
	StackID            string        `json:"stackId"`
	PlanHash           string        `json:"planHash"`
	SiteRef            string        `json:"siteRef"`
	NodeRef            string        `json:"nodeRef"`
	ProjectRoot        string        `json:"projectRoot"`
	HostManifestSHA256 string        `json:"hostManifestSha256"`
	Materialized       []string      `json:"materialized"`
	RunOrder           []string      `json:"runOrder"`
	AffectedStacks     []string      `json:"affectedStacks"`
	Status             string        `json:"status"`
	Stacks             []StackResult `json:"stacks"`
}

// EventFunc receives orchestration progress: phase is one of
// materialize-host, run-order or converge; status is started, succeeded,
// failed or, for converge, the per-stack status.
type EventFunc func(phase, status string, attributes map[string]string)

// ConvergeRequest is one post-apply Terramate orchestration of a change set
// on the local host.
type ConvergeRequest struct {
	WorkspaceRoot string
	ChangeSetID   string
	Layout        Layout
	// ExpectedManifestSHA256 is the host manifest digest recorded by the
	// change set. A different layout fails before any write.
	ExpectedManifestSHA256 string
	// AffectedStacks are the change set's stack IDs in graph run order.
	AffectedStacks []string
	Tools          Tools
	Timeout        time.Duration
	Event          EventFunc
}

// Converge materializes the host project, proves that Terramate orders the
// host stacks exactly as the graph does, and runs
// `terramate run --no-recursive --tags stackkit -- tofu plan -detailed-exitcode -input=false`
// in every affected local stack root, in run order. It only reads OpenTofu
// state: the apply already happened through the runtime executor. The report
// is returned on every path after validation, so a failed change set still
// records what each stack showed.
func Converge(ctx context.Context, request ConvergeRequest) (Report, error) {
	layout := request.Layout
	emit := request.Event
	if emit == nil {
		emit = func(string, string, map[string]string) {}
	}
	report := Report{
		SchemaVersion: ResultSchemaVersion, ChangeSetID: request.ChangeSetID,
		StackID: layout.Manifest.StackID, PlanHash: layout.Manifest.PlanHash,
		SiteRef: layout.Host.SiteRef, NodeRef: layout.Host.NodeRef, ProjectRoot: layout.Host.ProjectRoot,
		HostManifestSHA256: layout.ManifestSHA256, Materialized: make([]string, 0), RunOrder: make([]string, 0),
		AffectedStacks: append([]string{}, request.AffectedStacks...), Status: ResultNotConverged,
		Stacks: make([]StackResult, 0, len(request.AffectedStacks)),
	}
	if request.Tools.Terramate == "" || request.Tools.Tofu == "" {
		return report, &Error{Code: ErrToolMissing, Detail: "Terramate and OpenTofu binaries are required"}
	}
	if request.ExpectedManifestSHA256 != "" && request.ExpectedManifestSHA256 != layout.ManifestSHA256 {
		return report, &Error{Code: ErrHostDiverged, Detail: fmt.Sprintf("host manifest %s differs from the approved %s", layout.ManifestSHA256, request.ExpectedManifestSHA256)}
	}
	workspace, err := filepath.Abs(request.WorkspaceRoot)
	if err != nil {
		return report, fmt.Errorf("resolve workspace: %w", err)
	}

	// Core roots are generated and installed by the runtime executor, which
	// also writes the root marker the executor-state checkpoint requires. A
	// core root created here would be marker-less and break later
	// checkpoints, so a missing core root fails before any write.
	missing := make([]string, 0)
	for _, stack := range layout.Manifest.Stacks {
		if stack.Role == string(terramatestackgraph.RoleCore) && !hasOpenTofuRoot(workspace, stack.RuntimeRoot) {
			missing = append(missing, stack.ID)
			report.Stacks = append(report.Stacks, StackResult{
				StackID: stack.ID, Role: stack.Role, SiteRef: layout.Host.SiteRef, NodeRef: layout.Host.NodeRef,
				RuntimeRoot: stack.RuntimeRoot, Status: StackPendingRoot,
				Detail: "core OpenTofu root is not installed; the host project was not materialized",
			})
		}
	}
	if len(missing) > 0 {
		emit("materialize-host", "failed", map[string]string{"missingCoreRoots": strings.Join(missing, ",")})
		return report, &Error{Code: ErrNotConverged, Stacks: missing, Detail: "core OpenTofu roots are not installed after apply"}
	}

	emit("materialize-host", "started", map[string]string{"hostManifestSha256": layout.ManifestSHA256})
	materialized, err := Materialize(workspace, layout)
	report.Materialized = append(report.Materialized, materialized.Written...)
	if err != nil {
		emit("materialize-host", "failed", map[string]string{"error": err.Error()})
		return report, err
	}
	emit("materialize-host", "succeeded", map[string]string{"written": strconv.Itoa(len(materialized.Written))})

	emit("run-order", "started", nil)
	runOrder, err := listRunOrder(ctx, workspace, request, layout)
	report.RunOrder = runOrder
	if err != nil {
		emit("run-order", "failed", map[string]string{"error": err.Error()})
		return report, err
	}
	emit("run-order", "succeeded", map[string]string{"stacks": strconv.Itoa(len(runOrder))})

	failed := make([]string, 0)
	drifted := make([]string, 0)
	for _, id := range request.AffectedStacks {
		stack, found := layout.Stack(id)
		if !found {
			return report, &Error{Code: ErrNotConverged, Stacks: []string{id}, Detail: "affected stack is not in the stack graph"}
		}
		result := StackResult{
			StackID: stack.ID, Role: string(stack.Role), SiteRef: stack.SiteRef, NodeRef: stack.NodeRef,
			RuntimeRoot: stack.RuntimeRoot,
		}
		switch {
		case stack.SiteRef != layout.Host.SiteRef || stack.NodeRef != layout.Host.NodeRef:
			result.Status = StackOtherHost
		default:
			result = planStack(ctx, workspace, request, stack, result)
		}
		switch result.Status {
		case StackDrifted:
			drifted = append(drifted, stack.ID)
		case StackFailed:
			failed = append(failed, stack.ID)
		case StackPendingRoot:
			if stack.Role == terramatestackgraph.RoleCore || stack.Role == terramatestackgraph.RoleWorkload {
				failed = append(failed, stack.ID)
			}
		}
		attributes := map[string]string{
			"stackId": stack.ID, "role": string(stack.Role), "runtimeRoot": stack.RuntimeRoot,
			"durationMs": strconv.FormatInt(result.DurationMS, 10),
		}
		if result.PlanExitCode != nil {
			attributes["planExitCode"] = strconv.Itoa(*result.PlanExitCode)
		}
		emit("converge", result.Status, attributes)
		report.Stacks = append(report.Stacks, result)
	}
	switch {
	case len(failed) > 0:
		return report, &Error{Code: ErrNotConverged, Stacks: failed, Detail: "affected stacks could not prove convergence after apply"}
	case len(drifted) > 0:
		return report, &Error{Code: ErrNotConverged, Stacks: drifted, Detail: "affected stacks still plan changes after apply (tofu plan exit 2)"}
	}
	report.Status = ResultConverged
	return report, nil
}

func (request ConvergeRequest) executor(workspace, directory string, extraEnv ...string) *terramate.Executor {
	environment := []string{
		// The runtime tree is not a Git repository. Without a ceiling at its
		// parent, a surrounding work tree becomes the Terramate root and the
		// host project is invisible.
		"GIT_CEILING_DIRECTORIES=" + filepath.Join(workspace, ".stackkit"),
		"PATH=" + filepath.Dir(request.Tools.Tofu) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"CHECKPOINT_DISABLE=1",
	}
	options := []terramate.ExecutorOption{
		terramate.WithWorkDir(directory), terramate.WithBinary(request.Tools.Terramate),
		terramate.WithTofuBinary(request.Tools.Tofu), terramate.WithChangeDetection(false),
		terramate.WithoutInheritedEnv(append([]string{"GIT_CEILING_DIRECTORIES"}, tofu.OfflineInheritedEnv...)...),
		terramate.WithEnv(append(environment, extraEnv...)...),
	}
	if request.Timeout > 0 {
		options = append(options, terramate.WithTimeout(request.Timeout))
	}
	return terramate.NewExecutor(options...)
}

func listRunOrder(ctx context.Context, workspace string, request ConvergeRequest, layout Layout) ([]string, error) {
	projectRoot := filepath.Join(workspace, filepath.FromSlash(layout.Host.ProjectRoot))
	paths, err := request.executor(workspace, projectRoot).ListRunOrder(ctx, StackTags)
	if err != nil {
		return nil, &Error{Code: ErrRunOrder, Detail: "terramate list --run-order failed", Err: err}
	}
	byRoot := make(map[string]string, len(layout.Manifest.Stacks))
	for _, stack := range layout.Manifest.Stacks {
		byRoot[strings.TrimPrefix(stack.RuntimeRoot, layout.Host.ProjectRoot+"/")] = stack.ID
	}
	ids := make([]string, 0, len(paths))
	for _, listed := range paths {
		id, known := byRoot[strings.TrimPrefix(path.Clean(filepath.ToSlash(listed)), "/")]
		if !known {
			return ids, &Error{Code: ErrRunOrder, Detail: fmt.Sprintf("terramate lists %q, which is not a stack of this host", listed)}
		}
		ids = append(ids, id)
	}
	if strings.Join(ids, ",") != strings.Join(layout.Host.RunOrder, ",") {
		return ids, &Error{Code: ErrRunOrder, Detail: fmt.Sprintf("terramate run order %v differs from the graph run order %v", ids, layout.Host.RunOrder)}
	}
	position := make(map[string]int, len(ids))
	for index, id := range ids {
		position[id] = index
	}
	last := -1
	for _, id := range request.AffectedStacks {
		index, local := position[id]
		if !local {
			continue
		}
		if index < last {
			return ids, &Error{Code: ErrRunOrder, Stacks: []string{id}, Detail: "affected stacks are not in the host run order"}
		}
		last = index
	}
	return ids, nil
}

func planStack(ctx context.Context, workspace string, request ConvergeRequest, stack terramatestackgraph.Stack, result StackResult) StackResult {
	result, _ = runStackPlan(ctx, workspace, request, stack, result)
	if result.Status == StackDrifted {
		result.Detail = "tofu plan reports changes after apply"
	}
	return result
}

// runStackPlan runs the detailed-exitcode plan of one local stack and also
// returns the plan's standard output (empty when the plan did not run).
func runStackPlan(ctx context.Context, workspace string, request ConvergeRequest, stack terramatestackgraph.Stack, result StackResult) (StackResult, string) {
	root := filepath.Join(workspace, filepath.FromSlash(stack.RuntimeRoot))
	if !hasOpenTofuRoot(workspace, stack.RuntimeRoot) {
		result.Status = StackPendingRoot
		result.Detail = "runtime root has no " + OpenTofuConfigFile + "; the executor has not materialized it"
		return result, ""
	}
	extra := make([]string, 0, 1)
	if info, err := os.Lstat(filepath.Join(root, openTofuCLIConfigFile)); err == nil && info.Mode().IsRegular() {
		extra = append(extra, "TF_CLI_CONFIG_FILE="+filepath.Join(root, openTofuCLIConfigFile))
	}
	started := time.Now()
	run, err := request.executor(workspace, root, extra...).RunStackTofu(ctx, StackTags, "plan", "-detailed-exitcode", "-input=false", "-no-color")
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil || run == nil {
		result.Status = StackFailed
		detail := "terramate run failed"
		if run != nil {
			detail = boundedDetail(run.Stderr)
		}
		if err != nil {
			detail = strings.TrimSpace(err.Error() + ": " + detail)
		}
		result.Detail = boundedDetail(detail)
		return result, ""
	}
	code := run.ExitCode
	result.PlanExitCode = &code
	switch code {
	case 0:
		result.Status = StackConverged
	case 2:
		result.Status = StackDrifted
	default:
		result.Status = StackFailed
		result.Detail = boundedDetail(run.Stderr)
	}
	return result, run.Stdout
}

func hasOpenTofuRoot(workspace, runtimeRoot string) bool {
	info, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(runtimeRoot), OpenTofuConfigFile))
	return err == nil && info.Mode().IsRegular()
}

func boundedDetail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxDetailBytes {
		value = value[len(value)-maxDetailBytes:]
	}
	return value
}
