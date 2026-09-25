package commands

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/hostmaintenance"
	"github.com/spf13/cobra"
)

// ExitCodeHostUpdateRunning is returned when `host updates apply` stopped
// waiting while its update unit keeps running. Refusals exit with
// ExitCodeHostBlocked (3): nothing on the host changed.
const ExitCodeHostUpdateRunning = 4

var planDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type hostMaintenanceDeps struct {
	host func() hostmaintenance.Host
}

func defaultHostMaintenanceDeps() hostMaintenanceDeps {
	return hostMaintenanceDeps{host: func() hostmaintenance.Host { return hostmaintenance.LocalHost{} }}
}

func (deps hostMaintenanceDeps) engine() hostmaintenance.Engine {
	return hostmaintenance.Engine{
		Host: deps.host(),
		Progress: func(phase, status, message string, attrs map[string]string) {
			rolloutEvent(phase, status, message, attrs)
		},
	}
}

func newHostUpdatesCommand(deps hostMaintenanceDeps) *cobra.Command {
	updates := &cobra.Command{
		Use:   "updates",
		Short: "Plan and apply operating-system package updates on this node",
		Long: `Plan and apply operating-system package updates on this Debian or Ubuntu node.

The container runtime (docker.io, docker-ce*, containerd*, runc) is held: it
is listed in the plan but never installed by apply, because upgrading it
restarts every workload. Apply never removes packages, never runs autoremove,
and never reboots; use 'stackkit host reboot' for that.`,
	}
	updates.AddCommand(newHostUpdatesPlanCommand(deps), newHostUpdatesApplyCommand(deps))
	return updates
}

func newHostUpdatesPlanCommand(deps hostMaintenanceDeps) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Refresh the package index and show the pending updates",
		Long: `Refresh the package index and show what 'stackkit host updates apply' would install.

The result lists every pending package with its current and new version, the
held container-runtime packages, packages apt keeps back, whether a reboot is
likely (kernel, C library or systemd), and dpkg problems. plan_digest binds
the exact package set; apply refuses when it no longer matches.`,
		Example: `  # Show the pending updates of this node
  sudo stackkit host updates plan

  # Machine-readable plan with its plan_digest
  sudo stackkit host updates plan --json`,
		Args: machineAwareNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := deps.engine().Plan(cmd.Context())
			return finishHostMaintenance(cmd, jsonOutput, result, err, printHostUpdatesPlan)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit the stackkit.host-maintenance/v1 result as machine-readable JSON")
	return cmd
}

func newHostUpdatesApplyCommand(deps hostMaintenanceDeps) *cobra.Command {
	var (
		jsonOutput bool
		yes        bool
		planDigest string
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Install exactly the reviewed update plan",
		Long: `Install exactly the package set a reviewed 'stackkit host updates plan' reported.

The pending set is simulated again first; when its digest differs from
--plan-digest nothing is installed (plan_stale). apt runs non-interactively in
a transient systemd unit (kombify-host-update-*), keeping existing
configuration files, so a CLI or agent timeout stops only the wait and never
dpkg. The result records every package's version before and after and
whether the node now requires a reboot.`,
		Example: `  # Apply the plan you reviewed
  sudo stackkit host updates apply --plan-digest sha256:<digest> --yes`,
		Args: machineAwareNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !planDigestPattern.MatchString(planDigest) {
				return machineAwareCommandError(cmd, errors.New("--plan-digest must be the sha256:<64 hex> plan_digest of 'stackkit host updates plan'"))
			}
			if !yes {
				return machineAwareCommandError(cmd, errors.New("host updates apply changes installed packages; re-run with --yes to confirm"))
			}
			result, err := deps.engine().Apply(cmd.Context(), planDigest)
			return finishHostMaintenance(cmd, jsonOutput, result, err, printHostUpdatesApply)
		},
	}
	cmd.Flags().StringVar(&planDigest, "plan-digest", "", "plan_digest of the reviewed plan (sha256:<hex>)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm that the planned packages may be installed")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit the stackkit.host-maintenance/v1 result as machine-readable JSON")
	_ = cmd.MarkFlagRequired("plan-digest")
	return cmd
}

func newHostRebootCommand(deps hostMaintenanceDeps) *cobra.Command {
	var (
		jsonOutput bool
		yes        bool
		delay      time.Duration
	)
	cmd := &cobra.Command{
		Use:   "reboot",
		Short: "Schedule a reboot of this node",
		Long: `Schedule a reboot of this node after a short delay and return immediately.

The returned boot_id_before identifies the current boot, so a caller can tell
the node came back by reading a different boot ID. The command refuses a node
that runs the Techstack control plane, a node where apt or dpkg holds its lock
or a host update runs, and a node that cannot boot unattended (volumes in
/etc/crypttab that need a passphrase, or no apt).

When the delay ends a guard checks again, waits up to 15 minutes for any
package operation to finish, and requests the reboot while holding the dpkg
locks. A node that stays busy is not rebooted; its boot ID stays the same.`,
		Example: `  # Reboot this node in 30 seconds
  sudo stackkit host reboot --yes

  # Reboot in two minutes and report the boot ID to compare against
  sudo stackkit host reboot --yes --delay 2m --json`,
		Args: machineAwareNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if delay < hostmaintenance.MinRebootDelay || delay > hostmaintenance.MaxRebootDelay {
				return machineAwareCommandError(cmd, fmt.Errorf("--delay must be between %s and %s", hostmaintenance.MinRebootDelay, hostmaintenance.MaxRebootDelay))
			}
			if !yes {
				return machineAwareCommandError(cmd, errors.New("host reboot restarts this node; re-run with --yes to confirm"))
			}
			result, err := deps.engine().Reboot(cmd.Context(), delay)
			return finishHostMaintenance(cmd, jsonOutput, result, err, printHostReboot)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm that this node may reboot")
	cmd.Flags().DurationVar(&delay, "delay", 30*time.Second, "Time until the reboot starts (1s to 5m)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit the stackkit.host-maintenance/v1 result as machine-readable JSON")
	return cmd
}

// newHostRebootGuardCommand is what the reboot timer runs. It is hidden: an
// owner schedules reboots with 'stackkit host reboot'.
func newHostRebootGuardCommand(deps hostMaintenanceDeps) *cobra.Command {
	var maxWait time.Duration
	cmd := &cobra.Command{
		Use:    "reboot-guard",
		Short:  "Re-check this node and request the scheduled reboot",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if maxWait <= 0 || maxWait > hostmaintenance.RebootGuardMaxWait {
				return fmt.Errorf("--max-wait must be between 1s and %s", hostmaintenance.RebootGuardMaxWait)
			}
			return deps.engine().RebootGuard(cmd.Context(), maxWait)
		},
	}
	cmd.Flags().DurationVar(&maxWait, "max-wait", hostmaintenance.RebootGuardMaxWait, "Longest wait for package operations before the reboot is abandoned")
	return cmd
}

// finishHostMaintenance writes the result and maps the outcome to the command
// status and exit code: refused exits 3 (nothing changed), still running
// exits 4, any other failure exits 1.
func finishHostMaintenance(
	cmd *cobra.Command,
	jsonOutput bool,
	result hostmaintenance.Result,
	runErr error,
	printHuman func(hostmaintenance.Result),
) error {
	status := "success"
	var refusal *hostmaintenance.RefusalError
	switch {
	case runErr == nil:
	case errors.As(runErr, &refusal):
		status = "denied"
		runErr = &exitCodeError{code: ExitCodeHostBlocked, err: runErr}
	case errors.Is(runErr, hostmaintenance.ErrStillRunning):
		status = "failed"
		runErr = &exitCodeError{code: ExitCodeHostUpdateRunning, err: runErr}
	default:
		status = "failed"
	}
	if jsonOutput {
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, result); err != nil {
			return errors.Join(runErr, err)
		}
		return runErr
	}
	if !humanOutputSuppressed() {
		printHuman(result)
		printHostMaintenanceProblem(result)
	}
	return runErr
}

func printHostMaintenanceProblem(result hostmaintenance.Result) {
	problem := result.Refusal
	if problem == nil {
		problem = result.Failure
	}
	if problem == nil {
		return
	}
	// The error itself is printed by the command runner; add what to do.
	for _, guidance := range problem.Guidance {
		printInfo("%s", guidance)
	}
}

func printHostUpdatesPlan(result hostmaintenance.Result) {
	if result.PendingCount == nil {
		return
	}
	if *result.PendingCount == 0 {
		printSuccess("No pending package updates")
	} else {
		printInfo("%d pending package updates (%d security)", *result.PendingCount, *result.SecurityCount)
		for _, pkg := range result.Packages {
			printInfo("  %s %s -> %s", pkg.Name, pkg.From, pkg.To)
		}
	}
	for _, pkg := range result.Held {
		printInfo("  held: %s %s -> %s (container runtime; not applied)", pkg.Name, pkg.From, pkg.To)
	}
	if len(result.Held) > 0 {
		printInfo("%s", result.HoldScope)
	}
	if len(result.KeptBack) > 0 {
		printInfo("Kept back by apt: %s", strings.Join(result.KeptBack, ", "))
	}
	for _, problem := range result.DpkgProblems {
		printWarning("dpkg: %s", problem)
	}
	if result.RebootLikely != nil && *result.RebootLikely {
		printInfo("A reboot is likely after these updates")
	}
	if *result.PendingCount > 0 {
		printInfo("Apply: stackkit host updates apply --plan-digest %s --yes", result.PlanDigest)
	}
}

func printHostUpdatesApply(result hostmaintenance.Result) {
	switch result.Outcome {
	case hostmaintenance.OutcomeNoop:
		printSuccess("No pending package updates; nothing installed")
	case hostmaintenance.OutcomeApplied:
		for _, change := range result.Changes {
			printInfo("  %s %s -> %s", change.Name, change.Before, change.After)
		}
		printSuccess("Installed %d package updates in %s", len(result.Changes), result.Unit)
		if len(result.AutoMarksNotRestored) > 0 {
			printWarning("Automatic-install marks not restored: %s", strings.Join(result.AutoMarksNotRestored, ", "))
		}
	case hostmaintenance.OutcomeRunning:
		printWarning("%s is still running", result.Unit)
	}
	if result.RebootRequired != nil && *result.RebootRequired {
		printInfo("This node requires a reboot: stackkit host reboot --yes")
	}
}

func printHostReboot(result hostmaintenance.Result) {
	if result.Scheduled == nil || !*result.Scheduled {
		return
	}
	printSuccess("Reboot scheduled for %s (%s)", result.ScheduledAt.Format(time.RFC3339), result.Unit)
	printInfo("Current boot ID: %s", result.BootIDBefore)
}
