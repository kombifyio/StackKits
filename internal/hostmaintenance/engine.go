package hostmaintenance

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/rollout"
)

// Paths and unit names the node-side contract depends on.
const (
	osReleasePath          = "/etc/os-release"
	crypttabPath           = "/etc/crypttab"
	systemdRuntimeDir      = "/run/systemd/system"
	bootIDPath             = "/proc/sys/kernel/random/boot_id"
	rebootRequiredPath     = "/var/run/reboot-required"
	rebootRequiredPkgsPath = "/var/run/reboot-required.pkgs"

	// UpdateUnitPrefix names the transient systemd unit apply runs apt in.
	UpdateUnitPrefix = "kombify-host-update-"
	// RebootUnitPrefix names the transient timer and service that reboot.
	RebootUnitPrefix = "kombify-host-reboot-"

	// MaintenanceLockPath serializes host maintenance operations on a node:
	// apply holds it from admission until its update unit ends, reboot while
	// it schedules, and the reboot guard while it requests the reboot.
	// It lives in /run, which only root can write: /run/lock is
	// world-writable, so any local user could pre-create and hold it.
	MaintenanceLockPath = "/run/kombify-host-maintenance.lock"
)

// Bounds inside the operation budgets.
const (
	// Refresh retries fit inside PlanTimeout: three attempts that each wait
	// up to 30s for the lock, 15s apart, leave a minute of the 3-minute plan
	// for downloading the index and simulating. refresh also stops retrying
	// when the remaining budget cannot hold another attempt.
	refreshLockTimeoutSeconds = 30
	refreshAttempts           = 3
	refreshRetryDelay         = 15 * time.Second
	refreshReserve            = time.Minute
	installLockTimeoutSeconds = 120
	applyLockWait             = 2 * time.Minute
	lockPollInterval          = 5 * time.Second
	heartbeatInterval         = 30 * time.Second
	shortProbeTimeout         = 15 * time.Second
	evidenceTextLimit         = 1024
	dpkgProblemLimit          = 20
)

// packageManagerLocks are the files apt and dpkg lock while they work.
var packageManagerLocks = []string{
	"/var/lib/dpkg/lock-frontend",
	"/var/lib/dpkg/lock",
	"/var/cache/apt/archives/lock",
	"/var/lib/apt/lists/lock",
}

// Engine runs host maintenance operations against a Host.
type Engine struct {
	Host Host
	// Progress receives rollout-style progress events. Nil discards them.
	Progress func(phase, status, message string, attrs map[string]string)
}

func (e Engine) progress(phase, status, message string, attrs map[string]string) {
	if e.Progress != nil {
		e.Progress(phase, status, message, attrs)
	}
}

func (e Engine) newResult(operation string) Result {
	return Result{SchemaVersion: SchemaVersion, Operation: operation, ObservedAt: e.Host.Now().UTC()}
}

// Plan refreshes the package index and reports what apply would install.
func (e Engine) Plan(ctx context.Context) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, PlanTimeout)
	defer cancel()
	result := e.newResult(OperationUpdatesPlan)
	if err := e.admitPackageHost(&result, false); err != nil {
		return result, err
	}
	problems, err := e.dpkgAudit(ctx)
	if err != nil {
		return result, fail(&result, FailureHostProbe, err.Error())
	}
	result.DpkgProblems = problems
	if err := e.refresh(ctx, &result); err != nil {
		return result, err
	}
	if _, err := e.simulate(ctx, &result); err != nil {
		return result, err
	}
	return result, nil
}

// Apply installs exactly the planned package set when the plan digest still
// matches a fresh simulation.
func (e Engine) Apply(ctx context.Context, planDigest string) (Result, error) {
	result := e.newResult(OperationUpdatesApply)
	result.StartedAt = result.ObservedAt
	if err := e.admitPackageHost(&result, false); err != nil {
		return result, err
	}
	// Holding the maintenance lock until the unit ends keeps a concurrent
	// retry from starting a second unit between its admission and ours.
	release, err := e.maintenanceLock(&result)
	if err != nil {
		return result, err
	}
	defer release()
	prepared, err := e.prepareApply(ctx, &result, planDigest)
	if err != nil || prepared == nil {
		return result, err
	}
	return result, e.runUpdateUnit(ctx, &result, planDigest, *prepared)
}

// preparedApply is what the update unit needs from the admission phase.
type preparedApply struct {
	packages   []Package
	heldPins   []Package
	names      []string
	before     map[string]string
	autoMarked []string
}

// prepareApply admits the host and the plan. It returns nil without an error
// when there is nothing to install.
func (e Engine) prepareApply(ctx context.Context, result *Result, planDigest string) (*preparedApply, error) {
	ctx, cancel := context.WithTimeout(ctx, PlanTimeout)
	defer cancel()
	// A pending reboot would stop dpkg mid-run at shutdown.
	if unit, err := e.activeUnit(ctx, RebootUnitPrefix+"*", "timer,service"); err != nil {
		return nil, fail(result, FailureHostProbe, err.Error())
	} else if unit != "" {
		return nil, refuse(result, CodePackageManagerBusy,
			"a reboot is scheduled ("+unit+")",
			"Apply after the node has rebooted.")
	}
	if unit, err := e.activeUnit(ctx, UpdateUnitPrefix+"*", "service"); err != nil {
		return nil, fail(result, FailureHostProbe, err.Error())
	} else if unit != "" {
		return nil, refuse(result, CodePackageManagerBusy,
			"another host update is running in "+unit,
			"Wait for the running update to finish, then plan again.")
	}
	problems, err := e.dpkgAudit(ctx)
	if err != nil {
		return nil, fail(result, FailureHostProbe, err.Error())
	}
	if len(problems) > 0 {
		result.DpkgProblems = problems
		return nil, refuse(result, CodeDpkgBroken,
			"dpkg reports unfinished or broken packages",
			"Repair the package database (for example `dpkg --configure -a`), then plan again.")
	}
	sim, err := e.simulate(ctx, result)
	if err != nil {
		return nil, err
	}
	if result.PlanDigest != planDigest {
		return nil, refuse(result, CodePlanStale,
			"the pending package set changed since the plan was made",
			"Run `stackkit host updates plan` again, review it, and apply its plan_digest.")
	}
	if len(sim.Apply) == 0 {
		result.Outcome = OutcomeNoop
		e.recordRebootRequired(result)
		result.FinishedAt = e.Host.Now().UTC()
		return nil, nil
	}
	for _, pkg := range sim.Apply {
		if !validPackageToken(pkg) {
			return nil, fail(result, FailureSimulation,
				fmt.Sprintf("apt reported a package token outside the Debian alphabet: %q=%q", pkg.Name, pkg.To))
		}
	}
	heldPins, err := e.installedHeldPackages(ctx)
	if err != nil {
		return nil, fail(result, FailureHostProbe, err.Error())
	}
	if err := e.verifyInstallSet(ctx, result, sim.Apply, heldPins); err != nil {
		return nil, err
	}
	if err := e.waitForPackageLocks(ctx, result); err != nil {
		return nil, err
	}
	prepared := preparedApply{packages: sim.Apply, heldPins: heldPins, names: packageNames(sim.Apply)}
	if prepared.before, err = e.installedVersions(ctx, prepared.names); err != nil {
		return nil, fail(result, FailureHostProbe, err.Error())
	}
	if prepared.autoMarked, err = e.autoMarked(ctx, prepared.names); err != nil {
		return nil, fail(result, FailureHostProbe, err.Error())
	}
	return &prepared, nil
}

// runUpdateUnit runs apt in its transient unit, waits a bounded time and
// records what changed.
func (e Engine) runUpdateUnit(ctx context.Context, result *Result, planDigest string, prepared preparedApply) error {
	unit := UpdateUnitPrefix + strings.TrimPrefix(planDigest, "sha256:")[:12] + "-" + strconv.FormatInt(e.Host.Now().Unix(), 10)
	result.Unit = unit
	attrs := map[string]string{"unit": unit, "packages": strconv.Itoa(len(prepared.packages)), "planDigest": planDigest}
	e.progress("host.updates.install", "started", "installing the planned package set", attrs)

	wait, cancelWait := context.WithTimeout(ctx, ApplyWaitTimeout)
	defer cancelWait()
	stopHeartbeat := e.heartbeat(wait, "host.updates.install", "waiting for "+unit, attrs)
	output, runErr := e.Host.Run(wait, Command{Name: "systemd-run", Args: updateUnitArgs(unit, prepared.packages, prepared.heldPins, prepared.autoMarked)})
	stopHeartbeat()

	evidence, cancelEvidence := context.WithTimeout(context.WithoutCancel(ctx), shortProbeTimeout)
	defer cancelEvidence()
	if runErr != nil && wait.Err() != nil {
		result.Outcome = OutcomeRunning
		result.Failure = &Problem{
			Code:    FailureWaitExpired,
			Message: "stopped waiting after " + ApplyWaitTimeout.String() + "; " + unit + " keeps running",
			Guidance: []string{
				"Poll `systemctl is-active " + unit + "`; when it is inactive, run `stackkit host updates plan` to see what remains.",
			},
		}
		e.progress("host.updates.install", "running", "stopped waiting; "+unit+" keeps running", attrs)
		return ErrStillRunning
	}
	if runErr != nil {
		e.progress("host.updates.install", "failed", runErr.Error(), attrs)
		return fail(result, FailureInstall, "could not start the update unit: "+boundedText(runErr.Error()+" "+output.Stderr))
	}

	after, afterErr := e.installedVersions(evidence, prepared.names)
	if afterErr == nil {
		result.Changes = versionChanges(prepared.names, prepared.before, after)
	}
	e.recordRebootRequired(result)
	result.FinishedAt = e.Host.Now().UTC()
	if output.ExitCode != 0 {
		journal := e.unitJournal(evidence, unit)
		if isLockContention(journal) && !anyChanged(result.Changes) {
			e.progress("host.updates.install", "failed", "package manager busy", attrs)
			return refuse(result, CodePackageManagerBusy,
				"apt could not get the package manager lock within its timeout",
				"Wait for the other package operation to finish, then apply again.")
		}
		e.progress("host.updates.install", "failed", "update unit exited "+strconv.Itoa(output.ExitCode), attrs)
		return fail(result, FailureInstall,
			fmt.Sprintf("%s exited %d: %s", unit, output.ExitCode, boundedText(journal)),
			"Inspect `journalctl -u "+unit+"`, repair the cause, then plan again.")
	}
	result.Outcome = OutcomeApplied
	if len(prepared.autoMarked) > 0 {
		// Verify rather than trust the restore inside the unit.
		if stillAuto, err := e.autoMarked(evidence, prepared.autoMarked); err != nil {
			result.AutoMarksNotRestored = prepared.autoMarked
		} else {
			result.AutoMarksNotRestored = missing(prepared.autoMarked, stillAuto)
		}
		if len(result.AutoMarksNotRestored) > 0 {
			e.progress("host.updates.install", "running",
				"automatic-install marks not restored: "+strings.Join(result.AutoMarksNotRestored, " "), attrs)
		}
	}
	if afterErr != nil {
		return fail(result, FailureHostProbe, "packages installed, but the installed versions could not be read: "+afterErr.Error())
	}
	e.progress("host.updates.install", "succeeded", "planned package set installed", attrs)
	return nil
}

// admitPackageHost refuses hosts outside the v1 scope: Debian-family systems
// with apt and systemd. forReboot distinguishes an appliance without apt
// (Home Assistant OS), which cannot be rebooted unattended by this contract.
func (e Engine) admitPackageHost(result *Result, forReboot bool) error {
	raw, err := e.Host.ReadFile(osReleasePath)
	if err != nil {
		return refuse(result, CodeUnsupportedOS,
			"this host has no readable /etc/os-release; host maintenance supports Debian and Ubuntu nodes")
	}
	info, like := parseOSRelease(string(raw))
	result.OS = &info
	manager := e.packageManager()
	result.PackageManager = manager
	switch manager {
	case "apt":
	case "":
		if forReboot {
			return refuse(result, CodeUnattendedRebootUnsupported,
				"this host has no apt; an appliance operating system manages its own updates and reboots",
				"Use the appliance's own update and restart controls.")
		}
		return refuse(result, CodeUnsupportedPackageManager,
			"this host has no supported package manager; host maintenance supports apt only")
	default:
		return refuse(result, CodeUnsupportedPackageManager,
			manager+" is not supported; host maintenance supports apt only")
	}
	if !debianFamily(info.ID, like) {
		return refuse(result, CodeUnsupportedOS,
			fmt.Sprintf("%s is not a Debian-family system", info.ID))
	}
	if !e.Host.Exists(systemdRuntimeDir) {
		return refuse(result, CodeUnsupportedOS,
			"systemd is not the running init; host maintenance runs apt and reboots through systemd")
	}
	return nil
}

func (e Engine) packageManager() string {
	for _, candidate := range []struct{ binary, name string }{
		{"apt-get", "apt"}, {"dnf", "dnf"}, {"yum", "yum"}, {"zypper", "zypper"}, {"apk", "apk"}, {"pacman", "pacman"},
	} {
		if _, err := e.Host.LookPath(candidate.binary); err == nil {
			return candidate.name
		}
	}
	return ""
}

func parseOSRelease(raw string) (OSInfo, string) {
	values := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		values[key] = strings.Trim(value, `"'`)
	}
	return OSInfo{ID: values["ID"], VersionID: values["VERSION_ID"], PrettyName: values["PRETTY_NAME"]}, values["ID_LIKE"]
}

func debianFamily(id, like string) bool {
	for _, value := range append([]string{id}, strings.Fields(like)...) {
		if value == "debian" || value == "ubuntu" {
			return true
		}
	}
	return false
}

// refresh runs apt-get update with a lock timeout and bounded retries while
// another package operation holds the index lock.
func (e Engine) refresh(ctx context.Context, result *Result) error {
	command := Command{Name: "apt-get", Args: []string{
		"-q", "-o", "DPkg::Lock::Timeout=" + strconv.Itoa(refreshLockTimeoutSeconds), "update",
	}}
	for attempt := 1; attempt <= refreshAttempts; attempt++ {
		attrs := map[string]string{"attempt": strconv.Itoa(attempt)}
		e.progress("host.updates.refresh", "started", "refreshing the package index", attrs)
		output, err := e.Host.Run(ctx, command)
		if err != nil {
			e.progress("host.updates.refresh", "failed", err.Error(), attrs)
			return fail(result, FailureRefresh, "apt-get update did not complete: "+err.Error())
		}
		if output.ExitCode == 0 {
			e.progress("host.updates.refresh", "succeeded", "package index refreshed", attrs)
			return nil
		}
		if !isLockContention(output.Stderr + output.Stdout) {
			e.progress("host.updates.refresh", "failed", boundedText(output.Stderr), attrs)
			return fail(result, FailureRefresh, "apt-get update failed: "+boundedText(output.Stderr))
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < refreshRetryDelay+refreshLockTimeoutSeconds*time.Second+refreshReserve {
			break
		}
		if attempt < refreshAttempts {
			e.progress("host.updates.refresh", "running", "package index locked; retrying", attrs)
			if err := e.Host.Sleep(ctx, refreshRetryDelay); err != nil {
				break
			}
		}
	}
	return refuse(result, CodePackageManagerBusy,
		"another package operation held the package index lock through every retry",
		"Wait for unattended-upgrades or the other package operation to finish, then plan again.")
}

// simulate runs `apt-get -s upgrade` and fills the plan fields of result.
// Simulation takes no lock, so it works while unattended-upgrades runs.
func (e Engine) simulate(ctx context.Context, result *Result) (simulation, error) {
	e.progress("host.updates.simulate", "started", "simulating the upgrade", nil)
	output, err := e.Host.Run(ctx, Command{Name: "apt-get", Args: []string{
		"-s", "-q", "-o", "Debug::NoLocking=true", "upgrade",
	}})
	if err != nil || output.ExitCode != 0 {
		detail := output.Stderr
		if err != nil {
			detail = err.Error()
		}
		e.progress("host.updates.simulate", "failed", boundedText(detail), nil)
		return simulation{}, fail(result, FailureSimulation, "apt-get -s upgrade failed: "+boundedText(detail))
	}
	sim := parseSimulation(output.Stdout)
	result.Packages = sim.Apply
	result.Held = sim.Held
	result.KeptBack = sim.KeptBack
	result.HoldScope = HoldScope
	result.PendingCount = intPtr(len(sim.Apply))
	result.SecurityCount = intPtr(securityCount(sim.Apply))
	result.RebootLikely = boolPtr(rebootLikely(sim.Apply))
	result.PlanDigest = PlanDigest(sim.Apply)
	e.progress("host.updates.simulate", "succeeded", "upgrade simulated", map[string]string{
		"pending": strconv.Itoa(len(sim.Apply)), "held": strconv.Itoa(len(sim.Held)), "planDigest": result.PlanDigest,
	})
	return sim, nil
}

// installArgs is the apt-get argv apply runs. Explicit name=version pairs bind
// the run to the approved digest, never name a held package, and leave every
// persistent apt state (holds, pins) untouched.
//
// Every installed held package is pinned to its installed version, so an
// index refresh between the simulation and the install cannot pull it along
// as a dependency; apt fails instead.
func installArgs(packages, heldPins []Package, simulate bool) []string {
	args := []string{"-q"}
	if simulate {
		args = append(args, "-s", "-o", "Debug::NoLocking=true")
	} else {
		args = append(args, "-y",
			"-o", "DPkg::Lock::Timeout="+strconv.Itoa(installLockTimeoutSeconds),
			"-o", "Dpkg::Options::=--force-confdef",
			"-o", "Dpkg::Options::=--force-confold")
	}
	args = append(args, "--no-install-recommends", "--no-remove", "--only-upgrade", "install")
	for _, pkg := range packages {
		args = append(args, pkg.Name+"="+pkg.To)
	}
	for _, pin := range heldPins {
		args = append(args, pin.Name+"="+pin.To)
	}
	return args
}

func (e Engine) verifyInstallSet(ctx context.Context, result *Result, packages, heldPins []Package) error {
	output, err := e.Host.Run(ctx, Command{Name: "apt-get", Args: installArgs(packages, heldPins, true)})
	if err != nil || output.ExitCode != 0 {
		detail := output.Stderr
		if err != nil {
			detail = err.Error()
		}
		return fail(result, FailureInstallSetRejected, "apt rejects the planned package set: "+boundedText(detail),
			"Run `stackkit host updates plan` again; if it persists, the held container runtime may block a dependency.")
	}
	if violations := installSetViolations(output.Stdout, packages); len(violations) > 0 {
		return fail(result, FailureInstallSetRejected,
			"installing the planned set would change more than planned: "+boundedText(strings.Join(violations, "; ")),
			"Review the node's package state; apply never removes packages or installs unplanned ones.")
	}
	return nil
}

// updateUnitArgs runs apt in a transient unit. --wait makes systemd-run exit
// with apt's status, and killing systemd-run (a CLI or agent timeout) stops
// only the wait: dpkg keeps running in the unit. Output goes to the journal,
// never to a pipe that a dying client could break.
func updateUnitArgs(unit string, packages, heldPins []Package, autoMarked []string) []string {
	script := "apt-get"
	for _, arg := range installArgs(packages, heldPins, false) {
		script += " " + shellQuote(arg)
	}
	script += "\nstatus=$?\n"
	if len(autoMarked) > 0 {
		// Restore automatic-install marks so installing by name never turns a
		// dependency into a manually installed package.
		script += "apt-mark auto"
		for _, name := range autoMarked {
			script += " " + shellQuote(name)
		}
		script += " >/dev/null 2>&1 || echo 'kombify-host-update: restoring automatic-install marks failed' >&2\n"
	}
	script += "exit $status\n"
	return []string{
		"--unit=" + unit,
		"--description=kombify host update",
		"--collect", "--wait", "--quiet",
		"--property=StandardOutput=journal",
		"--property=StandardError=journal",
		"--setenv=DEBIAN_FRONTEND=noninteractive",
		"--setenv=NEEDRESTART_MODE=l",
		"--setenv=APT_LISTCHANGES_FRONTEND=none",
		"--setenv=LC_ALL=C",
		"/bin/sh", "-c", script,
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func (e Engine) dpkgAudit(ctx context.Context) ([]string, error) {
	output, err := e.Host.Run(ctx, Command{Name: "dpkg", Args: []string{"--audit"}})
	if err != nil {
		return nil, fmt.Errorf("dpkg --audit: %w", err)
	}
	var problems []string
	for _, line := range strings.Split(output.Stdout, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			problems = append(problems, boundedText(trimmed))
		}
		if len(problems) == dpkgProblemLimit {
			break
		}
	}
	if len(problems) == 0 && output.ExitCode != 0 {
		return nil, fmt.Errorf("dpkg --audit exited %d: %s", output.ExitCode, boundedText(output.Stderr))
	}
	return problems, nil
}

// activeUnit names a loaded unit matching pattern that is active or changing
// state; a waiting timer is active.
func (e Engine) activeUnit(ctx context.Context, pattern, types string) (string, error) {
	output, err := e.Host.Run(ctx, Command{Name: "systemctl", Args: []string{
		"list-units", "--type=" + types, "--plain", "--no-legend", "--no-pager",
		"--state=active,activating,deactivating,reloading", pattern,
	}})
	if err != nil {
		return "", fmt.Errorf("list %s units: %w", pattern, err)
	}
	if output.ExitCode != 0 {
		return "", fmt.Errorf("list %s units exited %d: %s", pattern, output.ExitCode, boundedText(output.Stderr))
	}
	for _, line := range strings.Split(output.Stdout, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			return fields[0], nil
		}
	}
	return "", nil
}

// maintenanceLock takes the node's host maintenance lock or refuses as busy.
func (e Engine) maintenanceLock(result *Result) (func(), error) {
	release, acquired, err := e.Host.TryLock(MaintenanceLockPath, 0o600)
	if err != nil {
		return nil, fail(result, FailureHostProbe, "lock "+MaintenanceLockPath+": "+err.Error())
	}
	if !acquired {
		return nil, refuse(result, CodePackageManagerBusy,
			"another host maintenance operation is running on this node",
			"Wait for it to finish, then retry.")
	}
	return release, nil
}

// installedHeldPackages lists installed held packages at their installed
// version, the pins apply adds to its install command.
func (e Engine) installedHeldPackages(ctx context.Context) ([]Package, error) {
	output, err := e.Host.Run(ctx, Command{Name: "dpkg-query", Args: []string{
		"-W", "-f=${binary:Package}\t${Version}\t${db:Status-Abbrev}\n",
	}})
	if err != nil {
		return nil, fmt.Errorf("dpkg-query: %w", err)
	}
	if output.ExitCode != 0 {
		return nil, fmt.Errorf("dpkg-query exited %d: %s", output.ExitCode, boundedText(output.Stderr))
	}
	var pins []Package
	for _, line := range strings.Split(output.Stdout, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 3 || len(fields[2]) < 2 || fields[2][1] != 'i' || !IsHeld(fields[0]) {
			continue
		}
		pin := Package{Name: fields[0], From: fields[1], To: fields[1]}
		if !validPackageToken(pin) {
			return nil, fmt.Errorf("dpkg reported a package token outside the Debian alphabet: %q=%q", pin.Name, pin.To)
		}
		pins = append(pins, pin)
	}
	return pins, nil
}

func (e Engine) heldPackageLock() (string, int, error) {
	for _, path := range packageManagerLocks {
		held, pid, err := e.Host.LockHolder(path)
		if err != nil {
			return "", 0, fmt.Errorf("probe %s: %w", path, err)
		}
		if held {
			return path, pid, nil
		}
	}
	return "", 0, nil
}

// waitForPackageLocks gives a running package operation (usually
// unattended-upgrades) a bounded time to finish before refusing as busy.
func (e Engine) waitForPackageLocks(ctx context.Context, result *Result) error {
	deadline := e.Host.Now().Add(applyLockWait)
	for {
		path, pid, err := e.heldPackageLock()
		if err != nil {
			return fail(result, FailureHostProbe, err.Error())
		}
		if path == "" {
			return nil
		}
		if !e.Host.Now().Before(deadline) {
			return refuse(result, CodePackageManagerBusy, lockHolderMessage(path, pid),
				"Wait for unattended-upgrades or the other package operation to finish, then apply again.")
		}
		e.progress("host.updates.install", "running", lockHolderMessage(path, pid)+"; waiting", nil)
		if err := e.Host.Sleep(ctx, lockPollInterval); err != nil {
			return refuse(result, CodePackageManagerBusy, lockHolderMessage(path, pid),
				"Wait for unattended-upgrades or the other package operation to finish, then apply again.")
		}
	}
}

func lockHolderMessage(path string, pid int) string {
	if pid > 0 {
		return fmt.Sprintf("%s is held by process %d", path, pid)
	}
	return path + " is held by another process"
}

// installedVersions reads dpkg's installed versions. dpkg-query exits 1 when
// a name is not installed and still prints the others.
func (e Engine) installedVersions(ctx context.Context, names []string) (map[string]string, error) {
	output, err := e.Host.Run(ctx, Command{Name: "dpkg-query", Args: append([]string{
		"-W", "-f=${binary:Package}\t${Version}\n",
	}, names...)})
	if err != nil {
		return nil, fmt.Errorf("dpkg-query: %w", err)
	}
	if output.ExitCode > 1 {
		return nil, fmt.Errorf("dpkg-query exited %d: %s", output.ExitCode, boundedText(output.Stderr))
	}
	versions := map[string]string{}
	for _, line := range strings.Split(output.Stdout, "\n") {
		name, version, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		versions[name] = version
		// Multi-Arch: same packages print name:arch for the native one.
		if base := packageBaseName(name); base != name {
			if _, exists := versions[base]; !exists {
				versions[base] = version
			}
		}
	}
	return versions, nil
}

func (e Engine) autoMarked(ctx context.Context, names []string) ([]string, error) {
	output, err := e.Host.Run(ctx, Command{Name: "apt-mark", Args: append([]string{"showauto"}, names...)})
	if err != nil {
		return nil, fmt.Errorf("apt-mark showauto: %w", err)
	}
	if output.ExitCode != 0 {
		return nil, fmt.Errorf("apt-mark showauto exited %d: %s", output.ExitCode, boundedText(output.Stderr))
	}
	auto := map[string]bool{}
	for _, line := range strings.Split(output.Stdout, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			auto[name] = true
			auto[packageBaseName(name)] = true
		}
	}
	var marked []string
	for _, name := range names {
		if auto[name] {
			marked = append(marked, name)
		}
	}
	return marked, nil
}

func (e Engine) recordRebootRequired(result *Result) {
	required := e.Host.Exists(rebootRequiredPath)
	result.RebootRequired = boolPtr(required)
	if !required {
		return
	}
	if raw, err := e.Host.ReadFile(rebootRequiredPkgsPath); err == nil {
		seen := map[string]bool{}
		for _, line := range strings.Split(string(raw), "\n") {
			if name := strings.TrimSpace(line); name != "" && !seen[name] {
				seen[name] = true
				result.RebootRequiredPackages = append(result.RebootRequiredPackages, name)
			}
		}
	}
}

func (e Engine) unitJournal(ctx context.Context, unit string) string {
	output, err := e.Host.Run(ctx, Command{Name: "journalctl", Args: []string{
		"--unit=" + unit, "--output=cat", "--lines=40", "--no-pager",
	}})
	if err != nil {
		return ""
	}
	return output.Stdout
}

func (e Engine) heartbeat(ctx context.Context, phase, message string, attrs map[string]string) func() {
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.progress(phase, "running", message, attrs)
			}
		}
	}()
	// Stopping waits for the goroutine so no heartbeat interleaves with the
	// events that follow.
	return func() { close(done); <-finished }
}

func packageNames(packages []Package) []string {
	names := make([]string, 0, len(packages))
	for _, pkg := range packages {
		names = append(names, pkg.Name)
	}
	return names
}

func versionChanges(names []string, before, after map[string]string) []Change {
	changes := make([]Change, 0, len(names))
	for _, name := range names {
		changes = append(changes, Change{Name: name, Before: before[name], After: after[name]})
	}
	return changes
}

// missing returns the names of want that are not in have.
func missing(want, have []string) []string {
	present := map[string]bool{}
	for _, name := range have {
		present[name] = true
	}
	var absent []string
	for _, name := range want {
		if !present[name] {
			absent = append(absent, name)
		}
	}
	return absent
}

func anyChanged(changes []Change) bool {
	for _, change := range changes {
		if change.Before != change.After {
			return true
		}
	}
	return false
}

// boundedText redacts and truncates host command output before it is kept.
func boundedText(input string) string {
	trimmed := strings.TrimSpace(rollout.Redact(input))
	runes := []rune(trimmed)
	if len(runes) <= evidenceTextLimit {
		return trimmed
	}
	return string(runes[:evidenceTextLimit]) + "..."
}
