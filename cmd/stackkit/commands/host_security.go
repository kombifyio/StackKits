package commands

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"github.com/spf13/cobra"
)

// ExitCodeHostSecurityDrift and ExitCodeHostSecurityUnknown are returned by
// `host security verify --fail-on-drift`, so a script can tell a drifted host
// from one whose state could not be established. They are opt-in: by default a
// verification that produced evidence exits 0 and the evidence carries the
// verdict.
const (
	ExitCodeHostSecurityDrift   = 5
	ExitCodeHostSecurityUnknown = 6
)

type hostSecurityFlags struct {
	json              bool
	kit               string
	siteKind          string
	mode              string
	freshness         time.Duration
	exceptions        string
	resolvedPlan      string
	localNode         string
	declaredPorts     []string
	managementSources []string
	noRecord          bool
	failOnDrift       bool
	apply             bool
	controls          []string
}

func newHostSecurityCommand() *cobra.Command {
	security := &cobra.Command{
		Use:   "security",
		Short: "Verify and repair the host security baseline with versioned evidence",
		Long: `Observe this node against the StackKits host security baseline, and restore what
drifted without cutting the management path.

'verify' never changes the host. It produces stackkit.host-security-evidence/v1
for the firewall, sshd, brute-force protection, automatic and pending security
updates, reboot state, kernel parameters, exposed listeners and certificate
expiry. Every control is compliant, drifted, unknown or an owner-approved
exception, and the evidence expires: a stale observation is unknown, and an
unknown control never counts as compliant.

It works on its own (Standard Mode, no account) and through the pinned CLI when
Techstack manages the deployment (--mode advanced).`,
	}
	security.AddCommand(newHostSecurityVerifyCommand(), newHostSecurityStatusCommand(), newHostSecurityRepairCommand())
	return security
}

func bindHostSecurityFlags(cmd *cobra.Command, flags *hostSecurityFlags) {
	cmd.Flags().BoolVar(&flags.json, "json", false, "Emit the result as machine-readable JSON")
	cmd.Flags().StringVar(&flags.kit, "kit", "", "Kit slug to record (default: the kit of the workspace StackSpec)")
	cmd.Flags().StringVar(&flags.siteKind, "site-kind", "", "Site kind of this host: home or cloud (default: inferred from the kit; modern-homelab needs it)")
	cmd.Flags().StringVar(&flags.mode, "mode", string(hostsecurity.ModeStandard), "Lifecycle mode to record: standard, or advanced when Techstack manages this deployment")
	cmd.Flags().DurationVar(&flags.freshness, "freshness", hostsecurity.DefaultFreshness, "How long the evidence may be relied on before it expires (1m to 24h)")
	cmd.Flags().StringVar(&flags.exceptions, "exceptions", hostsecurity.DefaultExceptionsPath, "Owner-controlled exceptions file (each exception needs an owner and an expiry)")
	cmd.Flags().StringVar(&flags.resolvedPlan, "resolved-plan", "", "Verified canonical ResolvedPlan whose declared listeners to judge exposure against")
	cmd.Flags().StringVar(&flags.localNode, "local-node", "", "Exact local Node in the supplied ResolvedPlan")
	cmd.Flags().StringArrayVar(&flags.declaredPorts, "declared-port", nil, "Declared service port as tcp/443 or udp/5353 (repeatable)")
	cmd.Flags().StringArrayVar(&flags.managementSources, "management-source", nil, "Network that must always reach ssh, as a CIDR (repeatable)")
}

func (flags hostSecurityFlags) options(workspace string) (hostsecurity.Options, error) {
	mode := hostsecurity.Mode(strings.ToLower(strings.TrimSpace(flags.mode)))
	if mode != hostsecurity.ModeStandard && mode != hostsecurity.ModeAdvanced {
		return hostsecurity.Options{}, fmt.Errorf("--mode must be standard or advanced, not %q", flags.mode)
	}
	kit := strings.TrimSpace(flags.kit)
	if kit == "" {
		kit = workspaceKitSlug(workspace)
	}
	site := hostsecurity.SiteUnknown
	switch strings.ToLower(strings.TrimSpace(flags.siteKind)) {
	case "":
		switch kit {
		case "basement-kit":
			site = hostsecurity.SiteHome
		case "cloud-kit":
			site = hostsecurity.SiteCloud
		}
	case "home":
		site = hostsecurity.SiteHome
	case "cloud":
		site = hostsecurity.SiteCloud
	default:
		return hostsecurity.Options{}, fmt.Errorf("--site-kind must be home or cloud, not %q", flags.siteKind)
	}
	options := hostsecurity.Options{
		Kit: kit, Mode: mode, SiteKind: site, Freshness: flags.freshness,
		ExceptionsPath: flags.exceptions, WorkspaceRoot: workspace,
	}
	for _, raw := range flags.managementSources {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return hostsecurity.Options{}, fmt.Errorf("--management-source %q is not a CIDR: %w", raw, err)
		}
		if prefix.Bits() == 0 {
			return hostsecurity.Options{}, fmt.Errorf("--management-source %q would admit every address", raw)
		}
		options.ManagementSources = append(options.ManagementSources, prefix.Masked())
	}
	if flags.resolvedPlan != "" {
		request, planKit, err := hostPreflightPlanRequest(workspace, flags.resolvedPlan, flags.localNode)
		if err != nil {
			return hostsecurity.Options{}, fmt.Errorf("read the declared listeners of the resolved plan: %w", err)
		}
		options.NodeRef, options.PlanHash = request.NodeRef, request.PlanHash
		options.DeclaredAuthority = "resolved plan " + request.PlanHash
		options.DeclaredListeners = []hostsecurity.Listener{}
		for _, listener := range request.RequiredListeners {
			options.DeclaredListeners = append(options.DeclaredListeners, hostsecurity.Listener{Transport: listener.Transport, Port: listener.Port})
		}
		if options.Kit == "" {
			options.Kit = planKit
		}
	}
	for _, raw := range flags.declaredPorts {
		transport, port, err := parseDeclaredPort(raw)
		if err != nil {
			return hostsecurity.Options{}, err
		}
		if options.DeclaredListeners == nil {
			options.DeclaredListeners = []hostsecurity.Listener{}
			options.DeclaredAuthority = "--declared-port"
		}
		options.DeclaredListeners = append(options.DeclaredListeners, hostsecurity.Listener{Transport: transport, Port: port})
	}
	return options, nil
}

func parseDeclaredPort(raw string) (string, int, error) {
	transport, port, found := strings.Cut(strings.ToLower(strings.TrimSpace(raw)), "/")
	number, err := strconv.Atoi(port)
	if !found || (transport != "tcp" && transport != "udp") || err != nil || number < 1 || number > 65535 {
		return "", 0, fmt.Errorf("--declared-port %q must look like tcp/443 or udp/5353", raw)
	}
	return transport, number, nil
}

func newHostSecurityVerifyCommand() *cobra.Command {
	flags := &hostSecurityFlags{}
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Observe the host security baseline and write versioned evidence",
		Long: `Observe every baseline control on this node and produce
stackkit.host-security-evidence/v1. Nothing on the host is changed.

Most controls need root to be observed; one that cannot be observed is reported
unknown with the reason, not as a failure. The evidence is written to
.stackkit/host-security-evidence.json in the workspace unless --no-record is
given, and it expires after --freshness.`,
		Example: `  # Verify this host and record the evidence
  sudo stackkit host security verify

  # What Techstack runs: machine-readable, advanced mode, exit non-zero on drift
  sudo stackkit host security verify --mode advanced --json --fail-on-drift

  # Judge exposure against the ports the verified plan declares
  sudo stackkit host security verify --resolved-plan plan.json --local-node node-1`,
		Args: machineAwareNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			workspace := getWorkDir()
			options, err := flags.options(workspace)
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			engine := hostsecurity.Engine{Host: hostsecurity.LocalHost{}}
			evidence := engine.Verify(cmd.Context(), options)
			if !flags.noRecord {
				if path, saveErr := engine.SaveEvidence(workspace, evidence); saveErr != nil {
					evidence.Notices = append(evidence.Notices, "evidence was not recorded locally: "+saveErr.Error())
				} else {
					printVerbose("evidence written to %s", path)
				}
			}
			return finishHostSecurityEvidence(cmd, flags, evidence)
		},
	}
	bindHostSecurityFlags(cmd, flags)
	cmd.Flags().BoolVar(&flags.noRecord, "no-record", false, "Do not write the evidence under .stackkit/")
	cmd.Flags().BoolVar(&flags.failOnDrift, "fail-on-drift", false, "Exit 5 when the host is drifted and 6 when its state is unknown")
	return cmd
}

func newHostSecurityStatusCommand() *cobra.Command {
	flags := &hostSecurityFlags{}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Read the stored evidence as it stands now, without observing the host",
		Long: `Read the evidence 'verify' last recorded in this workspace and judge it at the
current time. Evidence past its freshness budget is unknown: an old observation
proves nothing about the host now.`,
		Args: machineAwareNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			engine := hostsecurity.Engine{Host: hostsecurity.LocalHost{}}
			stored, err := engine.LoadEvidence(getWorkDir())
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					err = errors.New("no host security evidence is recorded in this workspace; run 'stackkit host security verify'")
				}
				return machineAwareCommandError(cmd, err)
			}
			return finishHostSecurityEvidence(cmd, flags, stored.AtTime(engine.Host.Now()))
		},
	}
	cmd.Flags().BoolVar(&flags.json, "json", false, "Emit the evidence as machine-readable JSON")
	cmd.Flags().BoolVar(&flags.failOnDrift, "fail-on-drift", false, "Exit 5 when the host is drifted and 6 when its state is unknown")
	return cmd
}

func finishHostSecurityEvidence(cmd *cobra.Command, flags *hostSecurityFlags, evidence hostsecurity.Evidence) error {
	if flags.json {
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), "success", evidence); err != nil {
			return err
		}
	} else if !humanOutputSuppressed() {
		printHostSecurityEvidence(evidence)
	}
	if !flags.failOnDrift {
		return nil
	}
	switch evidence.Overall {
	case hostsecurity.StateDrifted:
		return &exitCodeError{code: ExitCodeHostSecurityDrift, err: errors.New("host security baseline is drifted")}
	case hostsecurity.StateUnknown:
		return &exitCodeError{code: ExitCodeHostSecurityUnknown, err: errors.New("host security baseline state is unknown")}
	}
	return nil
}

func printHostSecurityEvidence(evidence hostsecurity.Evidence) {
	for _, control := range evidence.Controls {
		line := fmt.Sprintf("%s: %s", control.ID, control.Observed)
		switch control.State {
		case hostsecurity.StateCompliant:
			printVerbose("%s", line)
		case hostsecurity.StateException:
			printWarning("%s (exception by %s until %s)", line, control.Exception.Owner, control.Exception.ExpiresAt.Format(time.RFC3339))
		case hostsecurity.StateDrifted:
			printError("%s: %s", line, control.Reason)
			if control.Remediation.Action != "" {
				printInfo("  %s", control.Remediation.Action)
			}
		default:
			printWarning("%s: unknown: %s", control.ID, control.Reason)
		}
	}
	for _, notice := range evidence.Notices {
		printInfo("%s", notice)
	}
	summary := fmt.Sprintf("Host security baseline %s is %s (observed %s, expires %s)",
		evidence.BaselineVersion, evidence.Overall, evidence.ObservedAt.Format(time.RFC3339), evidence.ExpiresAt.Format(time.RFC3339))
	switch evidence.Overall {
	case hostsecurity.StateCompliant:
		printSuccess("%s", summary)
	case hostsecurity.StateDrifted:
		printError("%s", summary)
	default:
		printWarning("%s", summary)
	}
}

func newHostSecurityRepairCommand() *cobra.Command {
	flags := &hostSecurityFlags{}
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Restore drifted baseline controls without cutting the management path",
		Long: `Plan, or with --apply carry out, the restoration of drifted controls.

Without --apply nothing is changed: the command measures the host and prints
what each repair would do. With --apply only the named (--control) or drifted
controls are touched, and every change is checked first:

  - the firewall always keeps admitting the ssh sessions in use and every
    --management-source on every sshd port, and a policy that would not is
    refused before it is loaded;
  - password logins are disabled only when an account holds an authorized key;
    without one they stay on, the step is refused with reason no_authorized_key
    and the rest of the baseline is still repaired. The previous sshd and
    firewall configuration is restored if the new one does not validate or does
    not take effect;
  - fail2ban never bans the management path.

A control that cannot be observed, or that the owner approved an exception for,
is not touched. A Cloud site's firewall and root login are restored by 'stackkit
apply'. Each step records the control before and after, and a repeated repair
of a compliant control changes nothing.`,
		Example: `  # See what would be repaired
  sudo stackkit host security repair --site-kind home

  # Restore only the firewall, keeping a jump host reachable
  sudo stackkit host security repair --apply --control firewall.default_inbound --management-source 198.51.100.0/24`,
		Args: machineAwareNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			workspace := getWorkDir()
			options, err := flags.options(workspace)
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			engine := hostsecurity.Engine{Host: hostsecurity.LocalHost{}}
			report := engine.Repair(cmd.Context(), hostsecurity.RepairOptions{Options: options, Controls: flags.controls, Apply: flags.apply})
			if flags.apply && report.After != nil && !flags.noRecord {
				if _, saveErr := engine.SaveEvidence(workspace, *report.After); saveErr != nil {
					report.After.Notices = append(report.After.Notices, "evidence was not recorded locally: "+saveErr.Error())
				}
			}
			return finishHostSecurityRepair(cmd, flags, report)
		},
	}
	bindHostSecurityFlags(cmd, flags)
	cmd.Flags().BoolVar(&flags.apply, "apply", false, "Carry out the repair; without it the command only plans")
	cmd.Flags().StringArrayVar(&flags.controls, "control", nil, "Restrict the repair to this control ID (repeatable)")
	cmd.Flags().BoolVar(&flags.noRecord, "no-record", false, "Do not write the post-repair evidence under .stackkit/")
	return cmd
}

func finishHostSecurityRepair(cmd *cobra.Command, flags *hostSecurityFlags, report hostsecurity.RepairReport) error {
	status := "success"
	var runErr error
	switch report.Outcome {
	case hostsecurity.OutcomeBlocked:
		status = "denied"
		runErr = &exitCodeError{code: ExitCodeHostBlocked, err: errors.New("host security repair was refused for at least one control; the refused change was not made")}
	case hostsecurity.OutcomeFailed:
		status = "failed"
		runErr = errors.New("host security repair failed for at least one control")
	}
	if flags.json {
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, report); err != nil {
			return errors.Join(runErr, err)
		}
		return runErr
	}
	if !humanOutputSuppressed() {
		printHostSecurityRepair(report)
	}
	return runErr
}

func printHostSecurityRepair(report hostsecurity.RepairReport) {
	if len(report.Management.SessionPeers) > 0 || len(report.Management.ManagementSources) > 0 {
		printInfo("Management path kept open: ssh ports %v, sessions %v, sources %v",
			report.Management.SSHPorts, report.Management.SessionPeers, report.Management.ManagementSources)
	}
	for _, step := range report.Steps {
		switch step.Status {
		case hostsecurity.StepApplied:
			printSuccess("%s repaired", step.Control)
		case hostsecurity.StepPlanned:
			printInfo("%s would be repaired:", step.Control)
			for _, change := range step.Changes {
				printInfo("    %s", change)
			}
		case hostsecurity.StepNoop:
			printInfo("%s: %s", step.Control, step.Summary)
		case hostsecurity.StepManual:
			printWarning("%s needs a manual step: %s", step.Control, step.Summary)
		case hostsecurity.StepBlocked:
			printError("%s refused: %s", step.Control, step.Reason)
		case hostsecurity.StepFailed:
			printError("%s failed: %s", step.Control, step.Reason)
		}
	}
	switch report.Outcome {
	case hostsecurity.OutcomeNothingToDo:
		printSuccess("Nothing to repair")
	case hostsecurity.OutcomePlanned:
		printInfo("Nothing was changed. Carry this out with: stackkit host security repair --apply")
	}
}
