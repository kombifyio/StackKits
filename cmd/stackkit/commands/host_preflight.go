package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/hostpreflight"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
// declares in the embedded authority when that floor belongs to the compatible
// v2alpha1 surface. Native v2alpha2 module demand stays out of this general
// report; an unknown kit still yields the runtime checks.
func evaluateHostPreflight(ctx context.Context, workspace, kitSlug string, policy hostpreflight.Policy) hostpreflight.Report {
	if policy == hostpreflight.PolicySkip {
		return hostpreflight.Evaluate(hostpreflight.Facts{}, hostpreflight.Requirements{}, kitSlug, policy)
	}
	requirements := hostpreflight.Requirements{}
	nativeModuleProfiles := hostPreflightUsesNativeModuleProfiles(workspace)
	if !nativeModuleProfiles && strings.TrimSpace(kitSlug) != "" {
		if definition, err := architecturev2.EmbeddedKitDefinition(kitSlug); err == nil {
			requirements = hostpreflight.RequirementsFromDefinitionForTier(definition, hostPreflightComputeTier(workspace))
		}
	}
	facts := hostpreflight.Observe(ctx, hostpreflight.ObserveRequest{
		WorkspacePath: workspace,
		RequiredPorts: applyPublishedPorts,
	})
	return hostpreflight.Evaluate(facts, requirements, kitSlug, policy)
}

// evaluateNativeV2HostPreflight adds the bounded native-v2 limitation to the
// general read-only report. Apply keeps using evaluateHostPreflight so an
// already admitted canonical ResolvedPlan is not blocked by a prepare-only
// observation that cannot measure module-local demand.
func evaluateNativeV2HostPreflight(ctx context.Context, workspace, kitSlug string, policy hostpreflight.Policy) hostpreflight.Report {
	report := evaluateHostPreflight(ctx, workspace, kitSlug, policy)
	if hostPreflightUsesNativeModuleProfiles(workspace) {
		return markNativeModuleCapacityUnverified(report)
	}
	return report
}

// hostPreflightUsesNativeModuleProfiles keeps the v2alpha2 path out of the
// v2alpha1-only kit-wide compute-tier projection. Its selected module demand is
// owned by the canonical module admission/ResolvedPlan path, so this general
// host preflight reports that capacity as unverified rather than guessing a
// global low, standard, or high tier.
func hostPreflightUsesNativeModuleProfiles(workspace string) bool {
	loaded, err := config.NewLoader(workspace).ReadStackSpecDocument(specFile)
	if err != nil {
		return false
	}
	document, err := stackspecmigration.Read(loaded.Document.Raw)
	return err == nil && document.Version == stackspecmigration.SourceVersionV2Alpha2
}

func markNativeModuleCapacityUnverified(report hostpreflight.Report) hostpreflight.Report {
	if report.Policy == hostpreflight.PolicySkip {
		return report
	}
	report.Checks = append(report.Checks, hostpreflight.Check{
		ID:      "module-capacity",
		Status:  hostpreflight.StatusUnknown,
		Summary: "Native v2alpha2 module-local capacity is not measured by this host preflight",
		Remediation: []string{
			"Resolve and generate with an attested local Inventory before Apply; selected module profiles own their capacity demand.",
		},
	})
	sort.SliceStable(report.Checks, func(i, j int) bool { return report.Checks[i].ID < report.Checks[j].ID })
	if report.Status != hostpreflight.StatusBlocked {
		report.Status = hostpreflight.StatusUnknown
		report.Admitted = report.Policy != hostpreflight.PolicyStrict
	}
	return report
}

func hostPreflightComputeTier(workspace string) string {
	// This kit-wide floor is retained for the explicit v2alpha1 compatibility
	// adapter. Native v2alpha2 admission uses module-local profiles instead.
	loader := config.NewLoader(workspace)
	loaded, err := loader.ReadStackSpecDocument(specFile)
	if err != nil {
		return "standard"
	}
	var view struct {
		APIVersion string `yaml:"apiVersion"`
		Install    struct {
			ComputeTier string `yaml:"computeTier"`
		} `yaml:"install"`
	}
	if err := yaml.Unmarshal(loaded.Document.Raw, &view); err != nil {
		return "standard"
	}
	if view.APIVersion == stackspecmigration.APIVersionV2Alpha2 {
		return ""
	}
	tier := strings.TrimSpace(view.Install.ComputeTier)
	if tier == "" {
		return "standard"
	}
	return tier
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
			if check.ID == "module-capacity" {
				printWarning("%s: %s", check.ID, check.Summary)
			} else {
				printVerbose("%s: %s", check.ID, check.Summary)
			}
		case hostpreflight.StatusSkipped:
			printVerbose("%s: %s", check.ID, check.Summary)
		default:
			printVerbose("%s: %s", check.ID, check.Summary)
		}
		if check.Status == hostpreflight.StatusBlocked || check.Status == hostpreflight.StatusWarning || check.ID == "module-capacity" {
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
			report := evaluateNativeV2HostPreflight(cmd.Context(), workspace, workspaceKitSlug(workspace), policy)
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
