package terramatehost

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/terramate"
	"github.com/kombifyio/stackkits/internal/tofu"
)

var safePlanSummary = regexp.MustCompile(`(?m)^Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy\.?$`)

// Every Day 2 path uses the same portable root key as the initial apply.
// Only numeric plan summaries and exit status may leave the sensitive process.
func runOwnerEncryptedStack(ctx context.Context, workspace, root string, request ConvergeRequest, environment []string, args ...string) (*terramate.Result, error) {
	portable, err := filepath.Rel(workspace, root)
	if err != nil {
		return nil, err
	}
	var result *terramate.Result
	err = localevidence.WithOpenTofuStateKey(workspace, filepath.ToSlash(portable), func(key []byte) error {
		extra := make([]string, 0, len(environment)+1)
		for _, value := range environment {
			if !strings.HasPrefix(value, "TF_ENCRYPTION=") {
				extra = append(extra, value)
			}
		}
		extra = append(extra, "TF_ENCRYPTION="+tofu.StateEncryptionConfig(key, false))
		var runErr error
		result, runErr = request.executor(workspace, root, extra...).RunStackTofu(ctx, StackTags, args...)
		clear(extra)
		if result != nil {
			summary := safePlanSummary.FindAllString(result.Stdout, -1)
			noChanges := strings.Contains(result.Stdout, "No changes.")
			result.Stdout, result.Stderr = "", ""
			if len(summary) > 0 {
				result.Stdout = summary[len(summary)-1]
			} else if noChanges {
				result.Stdout = "No changes."
			}
			if runErr != nil {
				result.Stderr = fmt.Sprintf("OpenTofu stack command failed (exit %d)", result.ExitCode)
			}
		}
		if runErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("OpenTofu stack command failed; inspect the admitted root and owner custody")
		}
		return nil
	})
	return result, err
}
