package terramatehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// StackTofuRequest runs one OpenTofu command in one local stack root through
// `terramate run --no-recursive --tags stackkit`, with the same process
// environment as the change-set convergence plan (docs/ARCHITECTURE.md
// "Coordinated rollback across stacks (Stage 1)").
type StackTofuRequest struct {
	WorkspaceRoot string
	Tools         Tools
	// RuntimeRoot is the workspace-relative stack runtime root
	// (.stackkit/runtime/.../opentofu).
	RuntimeRoot string
	// Env is added to the process environment, for example the Compose
	// interpolation environment a wrapper root's local-exec needs.
	Env     []string
	Timeout time.Duration
}

// StackTofuResult is the relayed OpenTofu exit code and bounded stderr.
type StackTofuResult struct {
	ExitCode   int
	Detail     string
	DurationMS int64
}

// RunStackTofu runs `terramate run --no-recursive --tags stackkit -- tofu
// <args>` in request.RuntimeRoot. A non-zero OpenTofu exit code is returned in
// the result, not as an error; an error means Terramate could not run it.
func RunStackTofu(ctx context.Context, request StackTofuRequest, args ...string) (StackTofuResult, error) {
	if request.Tools.Terramate == "" || request.Tools.Tofu == "" {
		return StackTofuResult{}, &Error{Code: ErrToolMissing, Detail: "Terramate and OpenTofu binaries are required"}
	}
	workspace, err := filepath.Abs(request.WorkspaceRoot)
	if err != nil {
		return StackTofuResult{}, fmt.Errorf("resolve workspace: %w", err)
	}
	clean := path.Clean(request.RuntimeRoot)
	if clean != request.RuntimeRoot || !strings.HasPrefix(clean, ".stackkit/runtime/") {
		return StackTofuResult{}, fmt.Errorf("stack runtime root %q is outside the runtime tree", request.RuntimeRoot)
	}
	root := filepath.Join(workspace, filepath.FromSlash(clean))
	extra := append([]string{}, request.Env...)
	if info, statErr := os.Lstat(filepath.Join(root, openTofuCLIConfigFile)); statErr == nil && info.Mode().IsRegular() {
		extra = append(extra, "TF_CLI_CONFIG_FILE="+filepath.Join(root, openTofuCLIConfigFile))
	}
	convergence := ConvergeRequest{Tools: request.Tools, Timeout: request.Timeout}
	started := time.Now()
	run, err := convergence.executor(workspace, root, extra...).RunStackTofu(ctx, StackTags, args...)
	result := StackTofuResult{DurationMS: time.Since(started).Milliseconds()}
	if run != nil {
		result.ExitCode = run.ExitCode
		result.Detail = boundedDetail(run.Stderr)
	}
	if err != nil || run == nil {
		detail := "terramate run failed"
		if run != nil {
			detail = boundedDetail(run.Stderr)
		}
		if err != nil {
			detail = strings.TrimSpace(err.Error() + ": " + detail)
		}
		result.Detail = boundedDetail(detail)
		if err == nil {
			err = errors.New(result.Detail)
		}
		return result, err
	}
	return result, nil
}

// ReplaceTriggerAddress returns the `terraform_data` resource whose
// replacement forces a wrapper root to run `docker compose up` against its
// current payload. A root with a dedicated `<prefix>_up` resource (the
// split up/lifecycle layout) names that one, so the trigger never runs the
// destroy-time `down`; an older root with a single `terraform_data` resource
// names that resource.
func ReplaceTriggerAddress(mainTF []byte) (string, error) {
	file, diagnostics := hclsyntax.ParseConfig(mainTF, OpenTofuConfigFile, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return "", fmt.Errorf("parse the OpenTofu root: %s", diagnostics.Error())
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return "", errors.New("OpenTofu root has no native HCL body")
	}
	names := make([]string, 0, 2)
	up := make([]string, 0, 1)
	for _, block := range body.Blocks {
		if block.Type != "resource" || len(block.Labels) != 2 || block.Labels[0] != "terraform_data" {
			continue
		}
		names = append(names, block.Labels[1])
		if strings.HasSuffix(block.Labels[1], "_up") {
			up = append(up, block.Labels[1])
		}
	}
	sort.Strings(names)
	switch {
	case len(up) == 1:
		return "terraform_data." + up[0], nil
	case len(up) == 0 && len(names) == 1:
		return "terraform_data." + names[0], nil
	default:
		return "", fmt.Errorf("OpenTofu root has no unambiguous terraform_data trigger (resources %v)", names)
	}
}
