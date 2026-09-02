package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/hostpreflight"
	"github.com/spf13/cobra"
)

// runNativeV2Prepare is the native-v2 prepare boundary. It deliberately uses
// the same read-only host preflight that Apply uses so Docker access, runtime
// facts, resources, storage, and published ports cannot drift into a second
// prerequisite implementation.
func runNativeV2Prepare(cmd *cobra.Command, ctx context.Context, workspace string) error {
	if !nativeV2PrepareLocalTarget(prepareHost) {
		err := fmt.Errorf(
			"native-v2 prepare only inspects the local host; target %q must be prepared through its owner-managed host channel",
			prepareHost,
		)
		return machineAwareCommandError(cmd, err,
			"Run stackkit prepare on the target host where the StackSpec and Docker daemon are local.",
			"For an externally managed host, supply its separately governed inventory and execution-channel binding.",
		)
	}

	policy, err := resolveHostPreflightPolicy("")
	if err != nil {
		return machineAwareCommandError(cmd, fmt.Errorf("native-v2 prepare: %w", err),
			"Set STACKKIT_PREFLIGHT to strict, warn, or skip, then retry.",
		)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	report := evaluateNativeV2HostPreflight(ctx, workspace, workspaceKitSlug(workspace), policy)
	if prepareJSON {
		status := "success"
		if !report.Admitted {
			status = "failed"
		}
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, report); err != nil {
			return err
		}
		if !report.Admitted {
			return nativeV2PrepareRefusal(report)
		}
		return nil
	}

	printHostPreflightReport(report)
	if !report.Admitted {
		return nativeV2PrepareRefusal(report)
	}
	switch report.Status {
	case hostpreflight.StatusSkipped:
		printWarning("Native-v2 host validation skipped; nothing about this host was verified")
	case hostpreflight.StatusWarning:
		printWarning("Native-v2 host check completed with warnings; review the findings before Apply")
	case hostpreflight.StatusUnknown:
		printWarning("Native-v2 host prerequisites remain unverified; review the findings before Apply")
	default:
		printSuccess("Native-v2 host prerequisites validated; no host changes were made")
	}
	return nil
}

func nativeV2PrepareLocalTarget(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func nativeV2PrepareRefusal(report hostpreflight.Report) error {
	blocked := report.BlockedChecks()
	if len(blocked) == 0 {
		return &exitCodeError{code: ExitCodeHostBlocked, err: fmt.Errorf(
			"native-v2 host validation did not admit this host under the %s policy; no host changes were made",
			report.Policy,
		)}
	}
	reasons := make([]string, 0, len(blocked))
	for _, check := range blocked {
		reasons = append(reasons, check.ID+": "+check.Summary)
	}
	return &exitCodeError{code: ExitCodeHostBlocked, err: fmt.Errorf(
		"native-v2 host validation failed (%s); no host changes were made",
		strings.Join(reasons, "; "),
	)}
}
