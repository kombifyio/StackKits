package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/hostpreflight"
	"github.com/spf13/cobra"
)

// remediationPlan is what `stackkit host remediate` reports when it is asked
// what could be done rather than to do something.
type remediationPlan struct {
	SchemaVersion string             `json:"schemaVersion"`
	Findings      []remediationEntry `json:"findings"`
}

type remediationEntry struct {
	hostpreflight.Resolution
	Executable bool   `json:"executable"`
	Reason     string `json:"reason,omitempty"`
}

// remediationOutcome is what it reports after actually changing the host.
type remediationOutcome struct {
	SchemaVersion string               `json:"schemaVersion"`
	Record        hostpreflight.Record `json:"record"`
}

const remediationSchemaVersion = "stackkit.host-remediation/v1"

var (
	hostRemediateJSON   bool
	hostRemediateApply  string
	hostRemediateYes    bool
	hostRemediatePolicy string
	hostRemediateAuto   bool
)

func newHostRemediateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remediate",
		Short: "Show or carry out fixes for what host preflight found",
		Long: `List the fixes that answer this host's preflight findings, or carry one out.

Without --apply nothing is changed: the command measures the host and prints
what each fix would do, whether it can be undone, and whether it needs root.

With --apply <id> --yes the named fix runs, and the check that justified it is
measured again afterwards so the result states what actually changed rather
than that something was attempted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			policy, err := resolveHostPreflightPolicy(hostRemediatePolicy)
			if err != nil {
				return err
			}
			if policy == hostpreflight.PolicySkip {
				policy = hostpreflight.PolicyWarn
			}
			workspace := getWorkDir()
			kit := workspaceKitSlug(workspace)
			report := evaluateHostPreflight(cmd.Context(), workspace, kit, policy)
			if hostRemediateAuto {
				return runAutoRemediation(cmd, workspace, kit, policy, report)
			}
			if strings.TrimSpace(hostRemediateApply) == "" {
				return reportRemediationPlan(cmd, report)
			}
			return runHostRemediation(cmd, workspace, kit, policy, report)
		},
	}
	cmd.Flags().BoolVar(&hostRemediateJSON, "json", false, "Emit the remediation plan or record as machine-readable JSON")
	cmd.Flags().StringVar(&hostRemediateApply, "apply", "", "Carry out the named resolution (requires --yes)")
	cmd.Flags().BoolVar(&hostRemediateYes, "yes", false, "Confirm that the named resolution may change this host")
	cmd.Flags().StringVar(&hostRemediatePolicy, "policy", "", "Preflight policy used to find findings: strict or warn (default)")
	cmd.Flags().BoolVar(&hostRemediateAuto, "auto-reversible", false, "Carry out every undoable fix that may run unattended (requires --yes)")
	return cmd
}

// reportRemediationPlan prints what could be done and changes nothing.
func reportRemediationPlan(cmd *cobra.Command, report hostpreflight.Report) error {
	matched := hostpreflight.ResolutionsForReport(report)
	plan := remediationPlan{SchemaVersion: remediationSchemaVersion}
	for _, resolution := range matched {
		executable, reason := hostpreflight.Executable(resolution)
		plan.Findings = append(plan.Findings, remediationEntry{
			Resolution: resolution, Executable: executable, Reason: reason,
		})
	}
	if hostRemediateJSON {
		return writeCommandResultStatus(cmd, cmd.CommandPath(), "success", plan)
	}
	if humanOutputSuppressed() {
		return nil
	}
	if len(plan.Findings) == 0 {
		printSuccess("Host preflight found nothing that needs fixing")
		return nil
	}
	for _, entry := range plan.Findings {
		printInfo("%s — %s", entry.ID, entry.Title)
		printInfo("  %s", entry.Summary)
		for _, guidance := range entry.Guidance {
			printInfo("  %s", guidance)
		}
		switch {
		case entry.Executable:
			printInfo("  Run: stackkit host remediate --apply %s --yes", entry.ID)
		case entry.Reason != "":
			printInfo("  %s", entry.Reason)
		}
	}
	return nil
}

// runHostRemediation carries out one named resolution and re-measures.
func runHostRemediation(
	cmd *cobra.Command,
	workspace, kit string,
	policy hostpreflight.Policy,
	before hostpreflight.Report,
) error {
	resolution, exists := hostpreflight.ResolutionByID(hostRemediateApply)
	if !exists {
		return fmt.Errorf("unknown resolution %q; run 'stackkit host remediate' to list what applies here", hostRemediateApply)
	}
	if !hostRemediateYes {
		return fmt.Errorf(
			"%s changes this host; re-run with --yes to confirm (%s)",
			resolution.ID, resolution.Summary,
		)
	}
	if executable, reason := hostpreflight.Executable(resolution); !executable {
		return fmt.Errorf("%s cannot run here: %s", resolution.ID, reason)
	}

	record, applyErr := hostpreflight.ApplyResolution(cmd.Context(), resolution)
	record.Before = hostpreflight.FindCheck(before, resolution.AppliesTo)
	// Re-measure rather than assume. A fix that reports success while the
	// condition it targeted is unchanged is the same lie as a green checkmark
	// over a failed rollout.
	if applyErr == nil {
		after := evaluateHostPreflight(context.WithoutCancel(cmd.Context()), workspace, kit, policy)
		record.After = hostpreflight.FindCheck(after, resolution.AppliesTo)
	}

	if hostRemediateJSON {
		status := "success"
		if applyErr != nil {
			status = "failed"
		}
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, remediationOutcome{
			SchemaVersion: remediationSchemaVersion, Record: record,
		}); err != nil {
			return err
		}
		return applyErr
	}
	printRemediationRecord(record)
	return applyErr
}

// runAutoRemediation carries out every fix that is allowed to run without a
// person watching: undoable, no reboot, no credential.
//
// It is the installer's path, so it must not be able to end a rollout. A fix
// that fails is reported and the run continues -- the finding it targeted was
// already only a warning, and refusing to install because an optional
// improvement did not take is the failure mode this whole program exists to
// remove.
func runAutoRemediation(
	cmd *cobra.Command,
	workspace, kit string,
	policy hostpreflight.Policy,
	before hostpreflight.Report,
) error {
	if !hostRemediateYes {
		return fmt.Errorf("--auto-reversible changes this host; re-run with --yes to confirm")
	}
	records := make([]hostpreflight.Record, 0, 2)
	for _, resolution := range hostpreflight.ResolutionsForReport(before) {
		if !resolution.AutoInstallerEligible {
			continue
		}
		executable, reason := hostpreflight.Executable(resolution)
		if !executable {
			printVerbose("%s skipped: %s", resolution.ID, reason)
			continue
		}
		record, err := hostpreflight.ApplyResolution(cmd.Context(), resolution)
		record.Before = hostpreflight.FindCheck(before, resolution.AppliesTo)
		if err == nil {
			after := evaluateHostPreflight(context.WithoutCancel(cmd.Context()), workspace, kit, policy)
			record.After = hostpreflight.FindCheck(after, resolution.AppliesTo)
		}
		records = append(records, record)
		if err != nil {
			printWarning("%s did not complete: %s", record.ID, record.Reason)
			continue
		}
		printRemediationRecord(record)
	}
	if hostRemediateJSON {
		return writeCommandResultStatus(cmd, cmd.CommandPath(), "success", struct {
			SchemaVersion string                 `json:"schemaVersion"`
			Records       []hostpreflight.Record `json:"records"`
		}{SchemaVersion: remediationSchemaVersion, Records: records})
	}
	if len(records) == 0 && !humanOutputSuppressed() {
		printVerbose("No undoable host fix applied to this host")
	}
	return nil
}

func printRemediationRecord(record hostpreflight.Record) {
	if humanOutputSuppressed() {
		return
	}
	for _, step := range record.Steps {
		target := step.Target
		if len(step.Argv) > 1 {
			target = strings.Join(step.Argv, " ")
		}
		switch step.Status {
		case "failed":
			printError("%s: %s", target, step.Detail)
		case "skipped":
			printVerbose("%s: %s", target, step.Detail)
		default:
			printInfo("%s", target)
		}
	}
	if record.Status == "applied" && record.After != nil {
		printSuccess("%s applied; %s is now %s", record.ID, record.AppliesTo, record.After.Status)
		return
	}
	if record.Status == "applied" {
		printSuccess("%s applied", record.ID)
		return
	}
	printError("%s did not complete: %s", record.ID, record.Reason)
}
