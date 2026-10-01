package hostsecurity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"
)

// StepStatus is what became of one control in a repair.
type StepStatus string

const (
	StepPlanned StepStatus = "planned"
	StepApplied StepStatus = "applied"
	StepNoop    StepStatus = "noop"
	StepBlocked StepStatus = "blocked"
	StepManual  StepStatus = "manual"
	StepFailed  StepStatus = "failed"
)

// Outcome summarizes a whole repair.
type Outcome string

const (
	OutcomeNothingToDo Outcome = "nothing_to_do"
	OutcomePlanned     Outcome = "planned"
	OutcomeApplied     Outcome = "applied"
	OutcomeManual      Outcome = "manual_required"
	OutcomeBlocked     Outcome = "blocked"
	OutcomeFailed      Outcome = "failed"
)

// RepairOptions select what to repair. Without Apply nothing is changed.
type RepairOptions struct {
	Options
	// Controls restricts the repair to the named controls. Empty repairs every
	// drifted control that can be restored automatically.
	Controls []string
	// Apply carries the plan out. It is the only switch that changes the host.
	Apply bool
}

// ControlSnapshot is one control's state at a moment.
type ControlSnapshot struct {
	State    State  `json:"state"`
	Observed string `json:"observed"`
}

// RepairStep reports one control: what would be, or was, done to it.
type RepairStep struct {
	Control string     `json:"control"`
	Status  StepStatus `json:"status"`
	Summary string     `json:"summary,omitempty"`
	Changes []string   `json:"changes,omitempty"`
	Reason  string     `json:"reason,omitempty"`
	// ReasonCode is a stable code for a refusal, for example no_authorized_key.
	ReasonCode string           `json:"reason_code,omitempty"`
	Before     *ControlSnapshot `json:"before,omitempty"`
	After      *ControlSnapshot `json:"after,omitempty"`
}

// RepairReport is the stackkit.host-security-repair/v1 document.
type RepairReport struct {
	SchemaVersion string          `json:"schema_version"`
	Outcome       Outcome         `json:"outcome"`
	Applied       bool            `json:"applied"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    time.Time       `json:"finished_at"`
	Management    ManagementPath  `json:"management_path"`
	Steps         []RepairStep    `json:"steps"`
	Before        Evidence        `json:"before"`
	After         *Evidence       `json:"after,omitempty"`
	Firewall      *FirewallPolicy `json:"firewall_policy,omitempty"`
}

// blockedError marks a refusal made before the change that would have been
// unsafe. Nothing the step was about to do has happened.
type blockedError struct {
	reason string
	code   string
}

func (e *blockedError) Error() string { return e.reason }

func blocked(format string, args ...any) error {
	return &blockedError{reason: fmt.Sprintf(format, args...)}
}

// repairer carries one repair run.
type repairer struct {
	engine   Engine
	obs      *observation
	apply    bool
	options  RepairOptions
	before   Evidence
	path     ManagementPath
	changes  []string
	firewall *FirewallPolicy
	// selected are the controls this repair was asked to restore.
	selected map[string]bool
}

func snapshot(evidence Evidence, id string) *ControlSnapshot {
	for _, control := range evidence.Controls {
		if control.ID == id {
			return &ControlSnapshot{State: control.State, Observed: control.Observed}
		}
	}
	return nil
}

func (r *repairer) control(id string) (Control, bool) {
	for _, control := range r.before.Controls {
		if control.ID == id {
			return control, true
		}
	}
	return Control{}, false
}

// write records a file change and performs it when applying.
func (r *repairer) write(path string, data []byte, mode os.FileMode) error {
	r.changes = append(r.changes, fmt.Sprintf("write %s (%04o)", path, mode))
	if !r.apply {
		return nil
	}
	return r.engine.Host.WriteFile(path, data, mode)
}

// exec records a mutating command and runs it when applying. In a plan it
// reports success, because the plan only describes the change.
func (r *repairer) exec(ctx context.Context, name string, args ...string) (Output, error) {
	r.changes = append(r.changes, "run "+name+" "+strings.Join(args, " "))
	if !r.apply {
		return Output{}, nil
	}
	output, err := r.engine.run(ctx, name, args...)
	if err == nil && output.ExitCode != 0 {
		return output, fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), boundedText(output.Stderr+" "+output.Stdout))
	}
	return output, err
}

// Repair restores drifted controls. Without opts.Apply it only plans. It never
// changes a control it cannot observe, never changes one the owner approved an
// exception for, and never cuts the management path: a change that would is
// refused before it is made.
func (e Engine) Repair(ctx context.Context, opts RepairOptions) RepairReport {
	report := RepairReport{SchemaVersion: RepairSchemaVersion, Applied: opts.Apply, StartedAt: e.Host.Now().UTC()}
	before := e.Verify(ctx, opts.Options)
	report.Before = before
	if opts.SiteKind == "" {
		opts.SiteKind = SiteUnknown
		opts.Options.SiteKind = SiteUnknown
	}
	obs := &observation{engine: e, options: opts.Options}
	settings, _ := obs.sshdEffective(ctx)
	path := obs.detectManagementPath(ctx, settings)
	report.Management = path

	r := &repairer{engine: e, obs: obs, apply: opts.Apply, options: opts, before: before, path: path}
	report.Steps = r.run(ctx)
	report.Firewall = r.firewall
	report.Outcome = outcomeOf(report.Steps)

	if opts.Apply {
		after := e.Verify(ctx, opts.Options)
		report.After = &after
		for i := range report.Steps {
			step := &report.Steps[i]
			step.After = snapshot(after, step.Control)
			if step.Status == StepApplied && step.After != nil && step.After.State != StateCompliant && step.After.State != StateException {
				step.Status = StepFailed
				step.Reason = fmt.Sprintf("the change was made but the control is still %s: %s", step.After.State, step.After.Observed)
			}
		}
		report.Outcome = outcomeOf(report.Steps)
	}
	report.FinishedAt = e.Host.Now().UTC()
	return report
}

func outcomeOf(steps []RepairStep) Outcome {
	var failed, blockedCount, done, planned, manual int
	for _, step := range steps {
		switch step.Status {
		case StepFailed:
			failed++
		case StepBlocked:
			blockedCount++
		case StepApplied:
			done++
		case StepPlanned:
			planned++
		case StepManual:
			manual++
		}
	}
	switch {
	case failed > 0:
		return OutcomeFailed
	case blockedCount > 0:
		return OutcomeBlocked
	case done > 0:
		return OutcomeApplied
	case planned > 0:
		return OutcomePlanned
	case manual > 0:
		return OutcomeManual
	}
	return OutcomeNothingToDo
}

// repairOrder is the order controls are restored in: packages and kernel
// settings first, the firewall before ssh so a failed ssh change leaves the
// path the firewall already admits.
var repairOrder = []string{
	ControlSysctl, ControlUnattended, ControlBruteForce, ControlFirewall, ControlSSHPort,
	ControlSSHPassword, ControlSSHRootLogin,
}

//nolint:gocyclo // One linear selection over every control and the ways it can be unrepairable.
func (r *repairer) run(ctx context.Context) []RepairStep {
	named := map[string]bool{}
	for _, id := range r.options.Controls {
		named[id] = true
	}
	var steps []RepairStep
	for id := range named {
		if _, known := r.control(id); !known {
			steps = append(steps, RepairStep{Control: id, Status: StepBlocked, Reason: "names no control of this baseline"})
		}
	}
	repairable := map[string]bool{}
	firewall, _ := r.control(ControlFirewall)
	firewallPlanned := firewall.State == StateDrifted && firewall.Remediation.Capability == RemediationAutomatic &&
		(len(named) == 0 || named[ControlFirewall] || named[ControlSSHPort])
	for _, control := range r.before.Controls {
		if len(named) > 0 && !named[control.ID] {
			continue
		}
		before := &ControlSnapshot{State: control.State, Observed: control.Observed}
		switch {
		case control.ID == ControlSSHPort && control.State == StateUnknown && firewallPlanned:
			// Until a firewall policy is recorded the ssh port has nothing to
			// be compared with; the firewall repair records it.
			repairable[ControlFirewall] = true
			steps = append(steps, RepairStep{Control: control.ID, Status: StepNoop, Before: before,
				Summary: "established by the firewall repair, which records the ports it keeps open"})
		case control.State == StateDrifted && control.Remediation.Capability == RemediationAutomatic && slices.Contains(repairOrder, control.ID):
			repairable[control.ID] = true
		case control.State == StateDrifted:
			steps = append(steps, RepairStep{Control: control.ID, Status: StepManual, Before: before,
				Summary: control.Remediation.Action, Reason: "this control is not restored by host security repair"})
		case len(named) > 0 && control.State == StateUnknown:
			steps = append(steps, RepairStep{Control: control.ID, Status: StepBlocked, Before: before,
				Reason: "an unobserved control cannot be repaired: " + control.Reason})
		case len(named) > 0:
			steps = append(steps, RepairStep{Control: control.ID, Status: StepNoop, Before: before,
				Summary: "already " + string(control.State)})
		}
	}
	// ssh.port drifts only when the firewall does not admit sshd's port; the
	// firewall repair re-renders from the current ports, so it is that repair.
	if repairable[ControlSSHPort] {
		repairable[ControlFirewall] = true
		steps = append(steps, RepairStep{Control: ControlSSHPort, Status: StepNoop, Summary: "restored by the firewall repair",
			Before: snapshot(r.before, ControlSSHPort)})
		delete(repairable, ControlSSHPort)
	}
	sshSelected := repairable[ControlSSHPassword] || repairable[ControlSSHRootLogin]
	sshDone := false
	for _, id := range repairOrder {
		if !repairable[id] {
			continue
		}
		if id == ControlSSHPassword || id == ControlSSHRootLogin {
			// One drop-in carries both ssh controls, so it is written once.
			if sshDone || !sshSelected {
				continue
			}
			sshDone = true
		}
		steps = append(steps, r.step(ctx, id, repairable)...)
	}
	return steps
}

func (r *repairer) step(ctx context.Context, id string, repairable map[string]bool) []RepairStep {
	r.changes = nil
	var err error
	var passwordBlock *blockedError
	ids := []string{id}
	switch id {
	case ControlSysctl:
		err = r.repairSysctl(ctx)
	case ControlUnattended:
		err = r.repairUnattended(ctx)
	case ControlBruteForce:
		err = r.repairBruteForce(ctx)
	case ControlFirewall:
		err = r.repairFirewall(ctx)
	default:
		ids = nil
		for _, candidate := range []string{ControlSSHPassword, ControlSSHRootLogin} {
			if repairable[candidate] {
				ids = append(ids, candidate)
			}
		}
		r.selected = repairable
		passwordBlock, err = r.repairSSH(ctx)
	}
	steps := make([]RepairStep, 0, len(ids))
	for _, control := range ids {
		step := RepairStep{Control: control, Before: snapshot(r.before, control)}
		var block *blockedError
		switch {
		case control == ControlSSHPassword && passwordBlock != nil:
			step.Status, step.Reason, step.ReasonCode = StepBlocked, passwordBlock.reason, passwordBlock.code
		case errors.As(err, &block):
			step.Status, step.Reason, step.ReasonCode = StepBlocked, block.reason, block.code
		case err != nil:
			step.Status, step.Reason = StepFailed, err.Error()
			step.Changes = r.changes
		case r.apply:
			step.Status, step.Changes = StepApplied, r.changes
		default:
			step.Status, step.Changes = StepPlanned, r.changes
		}
		steps = append(steps, step)
	}
	return steps
}

// --- package installation ----------------------------------------------------

func (r *repairer) ensurePackages(ctx context.Context, packages ...string) error {
	var missing []string
	for _, name := range packages {
		if !r.packageInstalled(ctx, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if _, apt := r.engine.toolPath("apt-get"); !apt {
		return blocked("%s is missing and this host has no apt to install it; install it with the host's package manager", strings.Join(missing, ", "))
	}
	if _, err := r.exec(ctx, "apt-get", "-q", "-o", "DPkg::Lock::Timeout=120", "update"); err != nil {
		return fmt.Errorf("refresh the package index: %w", err)
	}
	args := append([]string{"-q", "-y", "-o", "DPkg::Lock::Timeout=120", "install", "--no-install-recommends"}, missing...)
	if _, err := r.exec(ctx, "apt-get", args...); err != nil {
		return fmt.Errorf("install %s: %w", strings.Join(missing, ", "), err)
	}
	return nil
}

func (r *repairer) packageInstalled(ctx context.Context, name string) bool {
	output, err := r.engine.run(ctx, "dpkg-query", "-W", "-f=${Status}", name)
	if err == nil {
		return strings.Contains(output.Stdout, "ok installed")
	}
	// Without dpkg, fall back to the command the package provides.
	command := map[string]string{"nftables": "nft", "fail2ban": "fail2ban-client", "unattended-upgrades": "unattended-upgrade"}[name]
	_, found := r.engine.toolPath(command)
	return found
}

func (r *repairer) systemdRunning() bool {
	_, err := r.engine.Host.Stat("/run/systemd/system")
	return err == nil
}

// --- kernel parameters -------------------------------------------------------

const sysctlDropIn = "/etc/sysctl.d/99-stackkit-foundation-hardening.conf"

func (r *repairer) repairSysctl(ctx context.Context) error {
	var lines []string
	for _, key := range managedSysctls {
		lines = append(lines, fmt.Sprintf("%s=%d", key.Key, key.Minimum))
	}
	if err := r.write(sysctlDropIn, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	_, err := r.exec(ctx, "sysctl", "-p", sysctlDropIn)
	return err
}

// --- automatic updates -------------------------------------------------------

const unattendedDropIn = "/etc/apt/apt.conf.d/99-stackkits-host-security"

func (r *repairer) repairUnattended(ctx context.Context) error {
	if _, apt := r.engine.toolPath("apt-get"); !apt {
		return blocked("automatic security updates are restored on apt hosts only; this host has no apt")
	}
	if err := r.ensurePackages(ctx, unattendedPackageName); err != nil {
		return err
	}
	content := strings.Join([]string{
		"// Managed by StackKits host security. Do not edit.",
		`APT::Periodic::Update-Package-Lists "1";`,
		`APT::Periodic::Unattended-Upgrade "1";`,
		`APT::Periodic::AutocleanInterval "7";`,
		`Unattended-Upgrade::Automatic-Reboot "false";`, "",
	}, "\n")
	if err := r.write(unattendedDropIn, []byte(content), 0o644); err != nil {
		return err
	}
	if r.systemdRunning() {
		_, err := r.exec(ctx, "systemctl", "enable", "--now", "apt-daily.timer", unattendedTimerUnit)
		return err
	}
	return nil
}

// --- brute-force protection --------------------------------------------------

const fail2banDropIn = "/etc/fail2ban/jail.d/00-stackkits-sshd.conf"

func (r *repairer) repairBruteForce(ctx context.Context) error {
	if !r.path.SSHInstalled {
		return blocked("there is no sshd on this host, so there is no ssh login for fail2ban to protect")
	}
	if err := r.ensurePackages(ctx, "fail2ban"); err != nil {
		return err
	}
	// The management path is never banned: its addresses are ignored by the jail.
	ignore := []string{"127.0.0.1/8", "::1"}
	for _, peer := range r.path.allPeers {
		ignore = append(ignore, peer.String())
	}
	ignore = append(ignore, r.path.ManagementSources...)
	backend := "auto"
	if r.systemdRunning() {
		backend = "systemd"
	}
	ports := r.path.SSHPorts
	if len(ports) == 0 {
		ports = []int{22}
	}
	content := strings.Join([]string{
		"# Managed by StackKits host security. Do not edit.",
		"[DEFAULT]",
		"ignoreip = " + strings.Join(dedupe(ignore), " "),
		"",
		"[sshd]",
		"enabled = true",
		"port = " + joinInts(ports),
		"backend = " + backend,
		"maxretry = 5",
		"findtime = 10m",
		"bantime = 1h", "",
	}, "\n")
	if err := r.write(fail2banDropIn, []byte(content), 0o644); err != nil {
		return err
	}
	if r.systemdRunning() {
		if _, err := r.exec(ctx, "systemctl", "enable", "--now", fail2banUnit); err != nil {
			return err
		}
		if _, err := r.exec(ctx, "systemctl", "restart", fail2banUnit); err != nil {
			return err
		}
	} else if _, err := r.exec(ctx, "fail2ban-client", "-x", "start"); err != nil {
		return err
	}
	if !r.apply {
		return nil
	}
	for attempt := 0; attempt < 30; attempt++ {
		if output, err := r.engine.run(ctx, "fail2ban-client", "status", "sshd"); err == nil && output.ExitCode == 0 {
			return nil
		}
		if err := r.engine.Host.Sleep(ctx, time.Second); err != nil {
			return err
		}
	}
	return errors.New("the fail2ban sshd jail did not become ready within 30s")
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

// --- firewall ----------------------------------------------------------------

const (
	firewallUnitName = "stackkit-host-firewall.service"
	firewallUnitPath = "/etc/systemd/system/" + firewallUnitName
)

// firewallPolicyFor builds the home policy from the observed management path.
func (r *repairer) firewallPolicyFor() FirewallPolicy {
	policy := FirewallPolicy{
		SSHPorts: r.path.SSHPorts, Peers: r.path.peers, ManagementSources: r.path.sources,
	}
	if r.options.DeclaredListeners != nil {
		policy.DeclaredOnly = true
		for _, listener := range r.options.DeclaredListeners {
			switch listener.Transport {
			case "tcp":
				policy.DeclaredTCP = append(policy.DeclaredTCP, listener.Port)
			case "udp":
				policy.DeclaredUDP = append(policy.DeclaredUDP, listener.Port)
			}
		}
	}
	return policy.normalized()
}

// admitsManagementPath is the guard every firewall change passes before it is
// made: the policy must still admit every ssh session in use and every
// configured management network on every sshd port.
func admitsManagementPath(policy FirewallPolicy, path ManagementPath) error {
	probe := func(source netip.Addr, port int) bool {
		return policy.Admits(Packet{Source: source, Interface: "eth0", Protocol: "tcp", Port: port, SourcePort: 50000})
	}
	for _, port := range policy.SSHPorts {
		for _, peer := range path.allPeers {
			if !probe(peer, port) {
				return fmt.Errorf("the policy would cut the ssh session from %s on port %d", peer, port)
			}
		}
		for _, source := range path.sources {
			if !probe(source.Addr(), port) {
				return fmt.Errorf("the policy would cut the management network %s on port %d", source, port)
			}
		}
	}
	return nil
}

func (r *repairer) repairFirewall(ctx context.Context) error {
	if r.options.SiteKind != SiteHome {
		return blocked("the firewall is restored automatically for home sites only; a Cloud site is restored by the Cloud host-security owner")
	}
	if r.path.SSHInstalled && len(r.path.SSHPorts) == 0 {
		return blocked("the sshd ports could not be read, so a firewall that keeps the ssh path open cannot be built")
	}
	policy := r.firewallPolicyFor()
	if err := policy.Validate(); err != nil {
		return blocked("%s", err)
	}
	if err := admitsManagementPath(policy, r.path); err != nil {
		return blocked("%s", err)
	}
	r.firewall = &policy
	if err := r.ensurePackages(ctx, "nftables"); err != nil {
		return err
	}
	staging := HomeFirewallRuleset + ".new"
	prior, priorExisted := r.priorHomeTable(ctx)
	if err := r.write(staging, []byte(policy.RenderRuleset()), 0o600); err != nil {
		return err
	}
	if r.apply {
		defer func() { _ = r.engine.Host.Remove(staging) }()
		if output, err := r.engine.run(ctx, "nft", "-c", "-f", staging); err != nil || output.ExitCode != 0 {
			return fmt.Errorf("nft rejected the ruleset: %s", boundedText(output.Stderr))
		}
	}
	if _, err := r.exec(ctx, "nft", "-f", staging); err != nil {
		return err
	}
	if !r.apply {
		return nil
	}
	live, err := r.engine.run(ctx, "nft", "list", "table", "inet", HomeFirewallTable)
	if err != nil || live.ExitCode != 0 || normalizeRuleset(live.Stdout) != normalizeRuleset(policy.Render()) {
		r.rollbackFirewall(ctx, prior, priorExisted)
		return errors.New("the live firewall table does not match the policy that was just installed; the previous firewall was restored")
	}
	record, marshalErr := json.MarshalIndent(recordedPolicy{SchemaVersion: recordedPolicySchema, Policy: policy}, "", "  ")
	if marshalErr != nil {
		return marshalErr
	}
	if err := r.write(HomeFirewallRuleset, []byte(policy.RenderRuleset()), 0o600); err != nil {
		return err
	}
	if err := r.write(HomeFirewallPolicyPath, append(record, '\n'), 0o600); err != nil {
		return err
	}
	return r.persistFirewall(ctx)
}

// priorHomeTable captures the table this repair is about to replace, so a
// failed install can put it back.
func (r *repairer) priorHomeTable(ctx context.Context) (string, bool) {
	output, err := r.engine.run(ctx, "nft", "list", "table", "inet", HomeFirewallTable)
	if err != nil || output.ExitCode != 0 {
		return "", false
	}
	return output.Stdout, true
}

func (r *repairer) rollbackFirewall(ctx context.Context, prior string, existed bool) {
	if !existed {
		_, _ = r.engine.run(ctx, "nft", "delete", "table", "inet", HomeFirewallTable)
		return
	}
	restore := "table inet " + HomeFirewallTable + "\ndelete table inet " + HomeFirewallTable + "\n" + prior
	staging := HomeFirewallRuleset + ".restore"
	if err := r.engine.Host.WriteFile(staging, []byte(restore), 0o600); err == nil {
		_, _ = r.engine.run(ctx, "nft", "-f", staging)
		_ = r.engine.Host.Remove(staging)
	}
}

// persistFirewall makes the table survive a reboot with a oneshot unit that
// loads the recorded ruleset. The management path it admits was built from the
// sessions in use at repair time.
func (r *repairer) persistFirewall(ctx context.Context) error {
	if !r.systemdRunning() {
		r.changes = append(r.changes, "no systemd: the ruleset is not reloaded at boot; verification will report drift after a reboot")
		return nil
	}
	nft, _ := r.engine.toolPath("nft")
	unit := strings.Join([]string{
		"# Managed by StackKits host security. Do not edit.",
		"[Unit]",
		"Description=StackKits host security inbound firewall",
		"Wants=network-pre.target",
		"Before=network-pre.target",
		"After=nftables.service",
		"",
		"[Service]",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"ExecStart=" + nft + " -f " + HomeFirewallRuleset,
		"ExecStop=" + nft + " delete table inet " + HomeFirewallTable,
		"",
		"[Install]",
		"WantedBy=multi-user.target", "",
	}, "\n")
	if err := r.write(firewallUnitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if _, err := r.exec(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err := r.exec(ctx, "systemctl", "enable", firewallUnitName)
	return err
}

// --- ssh ---------------------------------------------------------------------

const sshdDropIn = "/etc/ssh/sshd_config.d/00-stackkits-host-security.conf"

// loginKeyGuard refuses to disable password logins when that could leave the
// owner without a way in. The refusal carries the no_authorized_key code.
func (r *repairer) loginKeyGuard() *blockedError {
	blockedNow, detail := loginKeyProblem(r.path.KeyAccounts, r.path.SessionUser)
	if !blockedNow {
		return nil
	}
	return &blockedError{code: ReasonNoAuthorizedKey, reason: ReasonNoAuthorizedKey + ": " + detail + "; " + keyGuidance(r.path.SessionUser)}
}

// repairSSH writes the managed sshd drop-in. A password-login refusal (no
// authorized key) is returned separately and leaves password logins as they
// are, while the rest of the drop-in (root login) is still enforced.
//
//nolint:gocyclo // One linear sequence of guarded writes, each with its own rollback.
func (r *repairer) repairSSH(ctx context.Context) (*blockedError, error) {
	settings, failure := r.obs.sshdEffective(ctx)
	if settings == nil {
		return nil, blocked("the effective sshd configuration is not readable: %s", failure)
	}
	password, _ := r.control(ControlSSHPassword)
	root, _ := r.control(ControlSSHRootLogin)
	lines := []string{"# Managed by StackKits host security. Do not edit."}
	header := len(lines)
	var passwordBlock *blockedError
	if password.State != StateException && (password.State != StateDrifted || r.selected[ControlSSHPassword]) {
		if password.State == StateDrifted {
			passwordBlock = r.loginKeyGuard()
		}
		if passwordBlock == nil {
			lines = append(lines,
				"PasswordAuthentication no", "KbdInteractiveAuthentication no", "ChallengeResponseAuthentication no",
				"PermitEmptyPasswords no", "PubkeyAuthentication yes")
		}
	}
	if root.State == StateDrifted && r.selected[ControlSSHRootLogin] {
		lines = append(lines, "PermitRootLogin prohibit-password")
	} else if kept := r.previousRootLoginLine(); kept != "" && root.State != StateException {
		lines = append(lines, kept)
	}
	if len(lines) == header {
		// Nothing safe to write: the refusal is the whole result.
		return passwordBlock, nil
	}
	content := strings.Join(lines, "\n") + "\n"

	previous, hadPrevious := r.readPrevious(sshdDropIn)
	if !r.apply {
		return passwordBlock, r.write(sshdDropIn, []byte(content), 0o600)
	}
	if err := r.engine.Host.MkdirAll("/run/sshd", 0o755); err != nil {
		return passwordBlock, fmt.Errorf("create the sshd privilege separation directory: %w", err)
	}
	restore := func() {
		if hadPrevious {
			_ = r.engine.Host.WriteFile(sshdDropIn, previous, 0o600)
		} else {
			_ = r.engine.Host.Remove(sshdDropIn)
		}
	}
	if err := r.write(sshdDropIn, []byte(content), 0o600); err != nil {
		return passwordBlock, err
	}
	if output, err := r.engine.run(ctx, sshdBinary, "-t"); err != nil || output.ExitCode != 0 {
		restore()
		return passwordBlock, fmt.Errorf("sshd rejected the new configuration and the previous one was restored: %s", boundedText(output.Stderr))
	}
	if _, err := r.exec(ctx, "systemctl", "reload", "ssh"); err != nil {
		// Ubuntu socket-activates ssh: an inactive ssh.service cannot be
		// reloaded, and every new connection reads the drop-in anyway.
		if active, _ := r.engine.run(ctx, "systemctl", "is-active", "ssh"); strings.TrimSpace(active.Stdout) == "active" {
			restore()
			_, _ = r.engine.run(ctx, "systemctl", "reload", "ssh")
			return passwordBlock, fmt.Errorf("reload sshd: %w", err)
		}
	}
	effective, failure := (&observation{engine: r.engine, options: r.options.Options}).sshdEffective(ctx)
	if effective == nil {
		restore()
		return passwordBlock, fmt.Errorf("the new sshd configuration could not be read back and the previous one was restored: %s", failure)
	}
	if password.State == StateDrifted && passwordBlock == nil && effective.first("passwordauthentication") != "no" {
		restore()
		_, _ = r.engine.run(ctx, "systemctl", "reload", "ssh")
		return nil, errors.New("another sshd setting still allows password logins; the drop-in was removed. Edit /etc/ssh/sshd_config so it no longer sets PasswordAuthentication yes")
	}
	return passwordBlock, nil
}

func (r *repairer) readPrevious(path string) ([]byte, bool) {
	raw, err := r.engine.Host.ReadFile(path)
	return raw, err == nil
}

func (r *repairer) previousRootLoginLine() string {
	raw, err := r.engine.Host.ReadFile(sshdDropIn)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "PermitRootLogin ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
