package hostmaintenance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The Techstack control plane as it is shipped: the packaged systemd unit
// (kombify-Techstack packaging/techstack.service, `/usr/bin/techstack serve`),
// the container image (`/app/techstack serve` in ghcr.io/kombifyio/techstack)
// and the self-hosted Compose service `techstack`. Every enrolled worker runs
// the same binary as `techstack agent`, which is not a control plane.
const (
	ControlPlaneUnit         = "techstack.service"
	ControlPlaneExecutable   = "techstack"
	ControlPlaneCommand      = "serve"
	ControlPlaneImage        = "ghcr.io/kombifyio/techstack"
	ControlPlaneService      = "techstack"
	controlPlaneComposeLabel = "com.docker.compose.service"
)

// Compose names a service container <project>-<service>-<n> (v2) or
// <project>_<service>_<n> (v1); the self-hosted file names it kombify-techstack.
var controlPlaneContainerName = regexp.MustCompile(`(^|[-_])techstack([-_][0-9]+)?$`)

var dockerSockets = []string{"/var/run/docker.sock", "/run/docker.sock"}

// dpkgLocks are the locks the reboot guard holds while it requests the
// reboot, so no apt or dpkg run can start between its check and the shutdown.
var dpkgLocks = []string{"/var/lib/dpkg/lock-frontend", "/var/lib/dpkg/lock"}

const (
	rebootProbeTimeout = time.Minute
	guardPollInterval  = 10 * time.Second
	// guardHoldAfterReboot keeps the guard (and its locks) alive until the
	// shutdown stops it.
	guardHoldAfterReboot = 10 * time.Minute
)

// ErrRebootAbandoned is returned by the guard when the node stayed busy past
// its wait; the reboot did not happen.
var ErrRebootAbandoned = errors.New("reboot abandoned")

// Reboot schedules a guarded reboot after delay and returns without waiting.
//
// The timer does not run a bare `systemctl reboot`: it runs this binary's
// reboot guard, which checks again at fire time, waits for any package
// operation to finish, and requests the reboot while holding the dpkg locks.
func (e Engine) Reboot(ctx context.Context, delay time.Duration) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, rebootProbeTimeout)
	defer cancel()
	result := e.newResult(OperationReboot)
	if err := e.admitPackageHost(&result, true); err != nil {
		return result, err
	}
	release, err := e.maintenanceLock(&result)
	if err != nil {
		return result, err
	}
	defer release()
	entries, err := e.crypttabPassphraseEntries()
	if err != nil {
		return result, fail(&result, FailureHostProbe, err.Error())
	}
	if len(entries) > 0 {
		return result, refuse(&result, CodeUnattendedRebootUnsupported,
			"encrypted volumes in /etc/crypttab need a passphrase at boot: "+strings.Join(entries, ", "),
			"Reboot this node from its console, where the passphrase can be entered.")
	}
	if err := e.rebootBlocker(ctx, &result); err != nil {
		return result, err
	}
	if path, pid, err := e.heldPackageLock(); err != nil {
		return result, fail(&result, FailureHostProbe, err.Error())
	} else if path != "" {
		return result, refuse(&result, CodePackageManagerBusy,
			lockHolderMessage(path, pid),
			"Wait for the package operation to finish, then reboot.")
	}
	rawBootID, err := e.Host.ReadFile(bootIDPath)
	if err != nil {
		return result, fail(&result, FailureHostProbe, "read boot id: "+err.Error())
	}
	self, err := e.Host.Executable()
	if err != nil || !filepath.IsAbs(self) {
		return result, fail(&result, FailureHostProbe, fmt.Sprintf("locate the stackkit binary for the reboot guard: %v", err))
	}
	now := e.Host.Now().UTC()
	seconds := int(delay / time.Second)
	unit := RebootUnitPrefix + strconv.FormatInt(now.Unix(), 10)
	attrs := map[string]string{"unit": unit, "delaySeconds": strconv.Itoa(seconds)}
	e.progress("host.reboot.schedule", "started", "scheduling reboot", attrs)
	output, err := e.Host.Run(ctx, Command{Name: "systemd-run", Args: []string{
		"--unit=" + unit,
		"--description=kombify host reboot",
		"--on-active=" + strconv.Itoa(seconds) + "s",
		"--timer-property=AccuracySec=1s",
		self, "host", "reboot-guard", "--max-wait", RebootGuardMaxWait.String(),
	}})
	if err != nil || output.ExitCode != 0 {
		detail := output.Stderr
		if err != nil {
			detail = err.Error() + " " + detail
		}
		e.progress("host.reboot.schedule", "failed", boundedText(detail), attrs)
		return result, fail(&result, FailureSchedule, "systemd-run did not schedule the reboot: "+boundedText(detail))
	}
	result.Scheduled = boolPtr(true)
	result.BootIDBefore = strings.TrimSpace(string(rawBootID))
	result.ScheduledAt = now.Add(time.Duration(seconds) * time.Second)
	result.DelaySeconds = seconds
	result.Unit = unit
	e.progress("host.reboot.schedule", "succeeded", "reboot scheduled", attrs)
	return result, nil
}

// rebootBlocker refuses a reboot of the control plane host or during a host
// update. It fails closed: a probe that cannot answer is a failure.
func (e Engine) rebootBlocker(ctx context.Context, result *Result) error {
	controlPlane, err := e.controlPlane(ctx)
	if err != nil {
		return fail(result, FailureHostProbe, "cannot rule out a Techstack control plane on this node: "+err.Error(),
			"Run the command as root with a responsive Docker daemon, or reboot the node from its console.")
	}
	if controlPlane != "" {
		return refuse(result, CodeControlPlaneHost,
			"this node runs the Techstack control plane ("+controlPlane+"); rebooting it would cut off the orchestrator that waits for it",
			"Reboot the control plane node from its console or through its own maintenance procedure.")
	}
	if unit, err := e.activeUnit(ctx, UpdateUnitPrefix+"*", "service"); err != nil {
		return fail(result, FailureHostProbe, err.Error())
	} else if unit != "" {
		return refuse(result, CodePackageManagerBusy,
			"a host update is running in "+unit,
			"Wait for the update to finish, then reboot.")
	}
	return nil
}

// controlPlane names the Techstack control plane when it runs here: the
// systemd unit in any running or restarting state, a `techstack serve`
// process (which also covers containers, whose processes are visible in
// /proc), or a container of the Techstack image or Compose service. A
// `techstack agent` worker is not a control plane.
func (e Engine) controlPlane(ctx context.Context) (string, error) {
	output, err := e.Host.Run(ctx, Command{Name: "systemctl", Args: []string{
		"show", "--property=ActiveState", "--property=SubState", ControlPlaneUnit,
	}})
	if err != nil {
		return "", fmt.Errorf("probe %s: %w", ControlPlaneUnit, err)
	}
	if output.ExitCode != 0 {
		return "", fmt.Errorf("probe %s exited %d: %s", ControlPlaneUnit, output.ExitCode, boundedText(output.Stderr))
	}
	states := map[string]string{}
	for _, line := range strings.Split(output.Stdout, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			states[key] = value
		}
	}
	switch {
	case states["ActiveState"] == "":
		return "", fmt.Errorf("probe %s reported no ActiveState", ControlPlaneUnit)
	case states["ActiveState"] == "active", states["ActiveState"] == "activating",
		states["ActiveState"] == "reloading", states["ActiveState"] == "deactivating",
		states["SubState"] == "auto-restart":
		return ControlPlaneUnit + " " + states["ActiveState"] + "/" + states["SubState"], nil
	}

	processes, err := e.Host.Processes()
	if err != nil {
		return "", fmt.Errorf("list processes: %w", err)
	}
	for _, process := range processes {
		// `serve` anywhere after the binary counts, so global flags before the
		// subcommand cannot hide a control plane; workers run `techstack agent`.
		if filepath.Base(process.Exe) == ControlPlaneExecutable && len(process.Args) > 1 && slices.Contains(process.Args[1:], ControlPlaneCommand) {
			return fmt.Sprintf("process %d: %s", process.PID, strings.Join(process.Args, " ")), nil
		}
	}
	return e.controlPlaneContainer(ctx)
}

// controlPlaneContainer asks Docker when its daemon socket exists. A daemon
// that exists but does not answer cannot rule the container out.
func (e Engine) controlPlaneContainer(ctx context.Context) (string, error) {
	socket := false
	for _, path := range dockerSockets {
		socket = socket || e.Host.Exists(path)
	}
	if !socket {
		return "", nil
	}
	if _, err := e.Host.LookPath("docker"); err != nil {
		return "", errors.New("a Docker socket exists but the docker CLI is missing")
	}
	probe, cancel := context.WithTimeout(ctx, shortProbeTimeout)
	defer cancel()
	output, err := e.Host.Run(probe, Command{Name: "docker", Args: []string{
		"ps", "--no-trunc", "--format",
		`{{.Image}}` + "\t" + `{{.Names}}` + "\t" + `{{.Label "` + controlPlaneComposeLabel + `"}}`,
	}})
	if err != nil {
		return "", fmt.Errorf("docker ps: %w", err)
	}
	if output.ExitCode != 0 {
		return "", fmt.Errorf("docker ps exited %d: %s", output.ExitCode, boundedText(output.Stderr))
	}
	for _, line := range strings.Split(output.Stdout, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 2 {
			continue
		}
		image, name := fields[0], fields[1]
		service := ""
		if len(fields) > 2 {
			service = fields[2]
		}
		if imageRepository(image) == ControlPlaneImage || service == ControlPlaneService || controlPlaneContainerName.MatchString(name) {
			return "container " + name, nil
		}
	}
	return "", nil
}

// imageRepository strips the tag and digest from an image reference.
func imageRepository(image string) string {
	if index := strings.IndexByte(image, '@'); index >= 0 {
		image = image[:index]
	}
	if index := strings.LastIndexByte(image, ':'); index > strings.LastIndexByte(image, '/') {
		image = image[:index]
	}
	return image
}

// crypttabPassphraseEntries lists crypttab volumes that need a passphrase at
// boot: the key field is empty, none or -, in any mode. Only noauto volumes
// and random-key volumes (/dev/urandom, /dev/random) are skipped. A missing
// crypttab has no volumes; any other read error cannot rule them out.
func (e Engine) crypttabPassphraseEntries() ([]string, error) {
	raw, err := e.Host.ReadFile(crypttabPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", crypttabPath, err)
	}
	var entries []string
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		key, options := "", ""
		if len(fields) > 2 {
			key = fields[2]
		}
		if len(fields) > 3 {
			options = fields[3]
		}
		if key == "/dev/urandom" || key == "/dev/random" || hasCrypttabOption(options, "noauto") {
			continue
		}
		if key == "" || key == "none" || key == "-" {
			entries = append(entries, fields[0])
		}
	}
	return entries, nil
}

func hasCrypttabOption(options, want string) bool {
	for _, option := range strings.Split(options, ",") {
		if strings.TrimSpace(option) == want {
			return true
		}
	}
	return false
}

// RebootGuard runs when the reboot timer fires. It re-checks the node, waits
// up to maxWait for package operations and host updates to finish, then
// requests the reboot while holding the maintenance and dpkg locks, so no
// apt or dpkg run can start before the shutdown stops this process. When the
// node stays busy it abandons the reboot rather than killing dpkg.
//
// The wait is bounded by a poll count, not the wall clock, so a clock step
// (NTP) can neither end it early nor stretch it.
func (e Engine) RebootGuard(ctx context.Context, maxWait time.Duration) error {
	polls := int((maxWait + guardPollInterval - 1) / guardPollInterval)
	for poll := 0; ; poll++ {
		reason, release, err := e.guardAdmit(ctx)
		if err != nil {
			return err
		}
		if release != nil {
			return e.requestReboot(ctx, release)
		}
		if poll >= polls {
			return fmt.Errorf("%w: %s after %s", ErrRebootAbandoned, reason, maxWait)
		}
		e.progress("host.reboot.guard", "running", reason+"; waiting", nil)
		if err := e.Host.Sleep(ctx, guardPollInterval); err != nil {
			return fmt.Errorf("%w: %s", ErrRebootAbandoned, reason)
		}
	}
}

// guardAdmit returns the locks to hold for the reboot, or why it must wait.
// A control plane that appeared since scheduling ends the guard.
func (e Engine) guardAdmit(ctx context.Context) (string, func(), error) {
	var probe Result
	if err := e.rebootBlocker(ctx, &probe); err != nil {
		var refusal *RefusalError
		if errors.As(err, &refusal) && refusal.Problem.Code == CodePackageManagerBusy {
			return refusal.Problem.Message, nil, nil
		}
		return "", nil, fmt.Errorf("%w: %v", ErrRebootAbandoned, err)
	}
	var releases []func()
	releaseAll := func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}
	for index, path := range append([]string{MaintenanceLockPath}, dpkgLocks...) {
		// dpkg creates its lock files 0640 on first use; create them the
		// same way rather than abandon a reboot on a node that never ran it.
		mode := os.FileMode(0o640)
		if index == 0 {
			mode = 0o600
		}
		release, acquired, err := e.Host.TryLock(path, mode)
		if err != nil {
			releaseAll()
			return "", nil, fmt.Errorf("%w: lock %s: %v", ErrRebootAbandoned, path, err)
		}
		if !acquired {
			releaseAll()
			return path + " is held by another process", nil, nil
		}
		releases = append(releases, release)
	}
	return "", releaseAll, nil
}

func (e Engine) requestReboot(ctx context.Context, release func()) error {
	systemctl, err := e.Host.LookPath("systemctl")
	if err != nil {
		release()
		return fmt.Errorf("%w: systemctl not found", ErrRebootAbandoned)
	}
	output, err := e.Host.Run(ctx, Command{Name: systemctl, Args: []string{"reboot"}})
	if err != nil || output.ExitCode != 0 {
		release()
		return fmt.Errorf("%w: systemctl reboot: %v %s", ErrRebootAbandoned, err, boundedText(output.Stderr))
	}
	e.progress("host.reboot.guard", "succeeded", "reboot requested", nil)
	// Keep the locks until the shutdown stops this unit.
	_ = e.Host.Sleep(ctx, guardHoldAfterReboot)
	release()
	return nil
}
