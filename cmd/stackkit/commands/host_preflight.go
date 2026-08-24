package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/hostpreflight"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/spf13/cobra"
)

// ExitCodeHostBlocked is returned when host admission refused to mutate the
// target. It is distinct from a general failure so an installer or orchestrator
// can tell "this device cannot run the kit" from "the rollout broke".
const ExitCodeHostBlocked = 3

// exitCodeError carries a process exit code alongside the failure.
type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string { return e.err.Error() }

func (e *exitCodeError) Unwrap() error { return e.err }

// ExitCode reports the process exit code an Execute error should produce.
// An error without an explicit code is an ordinary failure.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded *exitCodeError
	if errors.As(err, &coded) {
		return coded.code
	}
	return 1
}

// applyPublishedPorts are the host ports every StackKit edge router binds. A
// rollout cannot share them, which is why the installer refuses to install over
// a workspace that already runs one.
var applyPublishedPorts = []int{80, 443}

// resolveHostPreflightPolicy reads the requested admission policy, preferring an
// explicit flag over the environment.
func resolveHostPreflightPolicy(requested string) (hostpreflight.Policy, error) {
	value := strings.TrimSpace(requested)
	if value == "" {
		value = strings.TrimSpace(os.Getenv("STACKKIT_PREFLIGHT"))
	}
	if value == "" {
		return hostpreflight.PolicyWarn, nil
	}
	if !hostpreflight.ValidPolicy(value) {
		return "", fmt.Errorf("unsupported preflight policy %q (want strict, warn, or skip)", value)
	}
	return hostpreflight.Policy(value), nil
}

// evaluateHostPreflight measures this host against the floor the named kit
// declares in the embedded authority. An unknown kit still yields the runtime
// checks; only the declared resource floors are then unavailable.
func evaluateHostPreflight(ctx context.Context, workspace, kitSlug string, policy hostpreflight.Policy) hostpreflight.Report {
	if policy == hostpreflight.PolicySkip {
		return hostpreflight.Evaluate(hostpreflight.Facts{}, hostpreflight.Requirements{}, kitSlug, policy)
	}
	requirements := hostpreflight.Requirements{}
	if strings.TrimSpace(kitSlug) != "" {
		if definition, err := architecturev2.EmbeddedKitDefinition(kitSlug); err == nil {
			requirements = hostpreflight.RequirementsFromDefinition(definition)
		}
	}
	facts := hostpreflight.Observe(ctx, hostpreflight.ObserveRequest{
		WorkspacePath: workspace,
		RequiredPorts: applyPublishedPorts,
	})
	return hostpreflight.Evaluate(facts, requirements, kitSlug, policy)
}

// hostPreflightRefusal turns a refused report into the operator-facing error,
// naming every blocking condition and what to do about it.
func hostPreflightRefusal(report hostpreflight.Report) error {
	blocked := report.BlockedChecks()
	if len(blocked) == 0 {
		// Strict policy refused on warnings or unverified facts.
		return &exitCodeError{code: ExitCodeHostBlocked, err: fmt.Errorf(
			"host preflight refused this host under the strict policy: %s", report.Status,
		)}
	}
	reasons := make([]string, 0, len(blocked))
	for _, check := range blocked {
		reasons = append(reasons, check.ID+": "+check.Summary)
	}
	return &exitCodeError{code: ExitCodeHostBlocked, err: fmt.Errorf(
		"host preflight refused to mutate this host (%s); nothing was applied", strings.Join(reasons, "; "),
	)}
}

func printHostPreflightReport(report hostpreflight.Report) {
	if humanOutputSuppressed() {
		return
	}
	for _, check := range report.Checks {
		switch check.Status {
		case hostpreflight.StatusBlocked:
			printError("%s: %s", check.ID, check.Summary)
		case hostpreflight.StatusWarning:
			printWarning("%s: %s", check.ID, check.Summary)
		case hostpreflight.StatusUnknown:
			printVerbose("%s: %s", check.ID, check.Summary)
		case hostpreflight.StatusSkipped:
			printVerbose("%s: %s", check.ID, check.Summary)
		default:
			printVerbose("%s: %s", check.ID, check.Summary)
		}
		if check.Status == hostpreflight.StatusBlocked || check.Status == hostpreflight.StatusWarning {
			for _, guidance := range check.Remediation {
				if strings.EqualFold(strings.TrimRight(guidance, "."), strings.TrimRight(check.Summary, ".")) {
					continue
				}
				printInfo("  %s", guidance)
			}
		}
	}
}

var hostPreflightJSON bool
var hostPreflightPolicyFlag string

func newHostPreflightCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Check whether this host can run the selected StackKit",
		Long: `Measure this host and compare it against the floor the kit declares.

Preflight performs read-only probes only: it never installs, starts, or
configures anything. Apply runs the same admission before it mutates the host.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			policy, err := resolveHostPreflightPolicy(hostPreflightPolicyFlag)
			if err != nil {
				return err
			}
			workspace := getWorkDir()
			report := evaluateHostPreflight(cmd.Context(), workspace, workspaceKitSlug(workspace), policy)
			if hostPreflightJSON {
				status := "success"
				if !report.Admitted {
					status = "failed"
				}
				if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, report); err != nil {
					return err
				}
				if !report.Admitted {
					return hostPreflightRefusal(report)
				}
				return nil
			}
			printHostPreflightReport(report)
			if !report.Admitted {
				return hostPreflightRefusal(report)
			}
			switch report.Status {
			case hostpreflight.StatusSkipped:
				printWarning("Host admission skipped; nothing about this host was verified")
			case hostpreflight.StatusWarning, hostpreflight.StatusUnknown:
				printSuccess("Host admitted with findings")
			default:
				printSuccess("Host admitted")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&hostPreflightJSON, "json", false, "Emit the versioned preflight report as machine-readable JSON")
	cmd.Flags().StringVar(&hostPreflightPolicyFlag, "policy", "", "Admission policy: strict, warn (default), or skip")
	return cmd
}

// workspaceKitSlug reports which kit this workspace selected. An unreadable or
// absent spec yields an empty slug: the runtime checks still run, only the
// kit-declared resource floors are unavailable.
func workspaceKitSlug(workspace string) string {
	path, _, _, err := config.NewLoader(workspace).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	document, err := stackspecmigration.Read(raw)
	if err != nil || document.V2 == nil {
		return ""
	}
	return string(document.V2.KitProfile)
}
