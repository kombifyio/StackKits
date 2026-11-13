package ownappfirewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Host locations and the trusted owner are variables only so the boundary
// tests can exercise the exact code against a temporary root; production
// never changes them.
var (
	// restoreRoot keeps the applied snapshot across a host reboot. Only the
	// boot-time restore unit reads it; the live watcher reads watchRoot.
	restoreRoot         = "/var/lib/stackkit-own-egress"
	unitRoot            = "/etc/systemd/system"
	bootIDPath          = "/proc/sys/kernel/random/boot_id"
	uptimePath          = "/proc/uptime"
	trustedOwner uint32 = 0
	trustRoot           = "/"
	runSystem    Runner = Run
)

// RestoreTimeout bounds one restore attempt. It leaves margin over the
// watcher's 20 second notify wait plus discovery, apply and verification.
const RestoreTimeout = 150 * time.Second

// RestorePermanentExit is the restore unit's exit status for a refusal that a
// retry cannot change: the rule definition or the signed record differ from
// the snapshot, or the host's network facts still differ once the boot has
// settled. The unit names it in RestartPreventExitStatus, so systemd stops
// retrying; the application stays stopped until a reviewed update or start.
// 78 is EX_CONFIG.
const RestorePermanentExit = 78

// HostSettleTime is how long after the kernel boot a host-facts mismatch is
// still retried: interfaces and routes that come up late must not stop the
// application for good.
const HostSettleTime = 5 * time.Minute

// MinimumSystemd is the oldest systemd that accepts Restart=on-failure on a
// Type=oneshot unit (systemd 244).
const MinimumSystemd = 244

// PermanentRefusal marks a restore refusal that no retry can change.
type PermanentRefusal struct{ Err error }

func (p PermanentRefusal) Error() string { return p.Err.Error() }
func (p PermanentRefusal) Unwrap() error { return p.Err }

// RestoreExitCode maps a RestoreMain result to the unit's exit status.
func RestoreExitCode(err error) int {
	var permanent PermanentRefusal
	switch {
	case err == nil:
		return 0
	case errors.As(err, &permanent):
		return RestorePermanentExit
	default:
		return 1
	}
}

var unitExecutable = regexp.MustCompile(`^/[A-Za-z0-9._/+-]{1,1000}$`)
var systemdVersion = regexp.MustCompile(`^systemd ([0-9]{1,5})\b`)

// requireSystemd refuses a host whose systemd would reject or ignore the
// restore unit's Restart=on-failure on a oneshot service.
func requireSystemd(ctx context.Context) error {
	raw, err := runSystem(ctx, "systemctl", []string{"--version"}, nil)
	match := systemdVersion.FindStringSubmatch(strings.TrimSpace(string(raw)))
	if err == nil && match != nil {
		if version, convErr := strconv.Atoi(match[1]); convErr == nil && version >= MinimumSystemd {
			return nil
		}
	}
	return errors.New("own firewall: systemd_too_old: the boot-time restore needs systemd " + strconv.Itoa(MinimumSystemd) + " or newer; application must stay stopped")
}

// kitExecutable is the path the restore unit and the watcher run. On Linux
// os.Executable reads /proc/self/exe and already strips the " (deleted)" the
// kernel appends once the running file was replaced or removed, so after an
// in-place upgrade (a new file renamed over the old path) it is the stable
// installed path, which now names the new kit; the unit runs that kit. A path
// whose file is gone fails admitExecutable, so the start is refused rather
// than bound to a missing binary.
func kitExecutable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, " (deleted)"), nil
}

// RestoreUnit is the persistent, enabled unit that brings the application
// back after a host reboot: it re-applies the exact reviewed ruleset and then
// hands the start to the watcher. It never starts the container itself.
func RestoreUnit(network string) string {
	return "stackkit-own-restore-" + identity(network)[:16] + ".service"
}
func restorePath(network string) string { return filepath.Join(restoreRoot, Table(network)+".json") }
func restoreUnitPath(network string) string {
	return filepath.Join(unitRoot, RestoreUnit(network))
}
func restoreWantsLink(network string) string {
	return filepath.Join(unitRoot, "multi-user.target.wants", RestoreUnit(network))
}

// RestoreUnitFile renders the boot-time unit. A failed attempt is retried 30
// seconds after it ends, without limit, unless it exits with
// RestorePermanentExit; every attempt re-verifies everything, so a retry never
// starts the application without its rules. Restart= on a oneshot unit needs
// systemd 244 or newer (installRestore checks it).
func RestoreUnitFile(network, executable string) (string, error) {
	if !unitExecutable.MatchString(executable) || strings.Contains(executable, "//") || filepath.Clean(executable) != executable {
		return "", errors.New("own firewall: restore unit requires a plain absolute kit executable path")
	}
	return "[Unit]\n" +
		"Description=StackKits own application restore (" + Table(network) + ")\n" +
		"Wants=network-online.target docker.service\n" +
		"After=network-online.target docker.service\n" +
		"StartLimitIntervalSec=0\n\n" +
		"[Service]\n" +
		"Type=oneshot\n" +
		"ExecStart=" + executable + " --own-egress-restore " + restorePath(network) + "\n" +
		"Restart=on-failure\n" +
		"RestartSec=30s\n" +
		"RestartPreventExitStatus=" + strconv.Itoa(RestorePermanentExit) + "\n" +
		"TimeoutStartSec=180s\n" +
		"UMask=0077\n" +
		"NoNewPrivileges=yes\n" +
		"ProtectHome=read-only\n" +
		"PrivateTmp=yes\n" +
		"RestrictSUIDSGID=yes\n" +
		"LockPersonality=yes\n" +
		"RestrictRealtime=yes\n\n" +
		"[Install]\n" +
		"WantedBy=multi-user.target\n", nil
}
func currentBoot() (string, error) {
	raw, err := os.ReadFile(bootIDPath)
	boot := strings.TrimSpace(string(raw))
	if err != nil || len(boot) < 16 || len(boot) > 64 {
		return "", errors.New("own firewall: kernel boot identity unavailable")
	}
	return boot, nil
}

// admitTrusted refuses a path that anyone but the trusted owner could replace:
// the path and every parent up to the trust root must be no symlink, owned by
// root and writable by neither group nor others.
func admitTrusted(path string) error {
	refuse := errors.New("own firewall: " + path + " or a parent is not root-owned and protected; restore refused")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return refuse
	}
	if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path {
		return refuse
	}
	for p := path; ; p = filepath.Dir(p) {
		if p != trustRoot && !strings.HasPrefix(p, strings.TrimSuffix(trustRoot, "/")+"/") {
			return refuse
		}
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			return refuse
		}
		if owner, ok := fileOwner(info); !ok || owner != trustedOwner {
			return refuse
		}
		if p == trustRoot {
			return nil
		}
	}
}

// admitExecutable binds the root boot unit to a kit binary only root can change.
func admitExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("own firewall: kit executable is not a regular file; restore refused")
	}
	return admitTrusted(path)
}
func ensureTrustedDir(dir string, private bool) error {
	mode := os.FileMode(0o755)
	if private {
		mode = 0o700
	}
	if err := os.MkdirAll(dir, mode); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || (private && info.Mode().Perm()&0o077 != 0) {
		return errors.New("own firewall: " + dir + " is not a protected directory; restore refused")
	}
	return admitTrusted(dir)
}

// installRestore writes the snapshot for the next boot, unmarked, and enables
// the restore unit. It runs before the watcher starts the container; the
// snapshot is marked for this boot only once the start has completed.
func (g *Guard) installRestore(ctx context.Context, executable string) error {
	if err := admitExecutable(executable); err != nil {
		return err
	}
	if err := requireSystemd(ctx); err != nil {
		return err
	}
	unit, err := RestoreUnitFile(g.Policy.Network, executable)
	if err != nil {
		return err
	}
	if err = ensureTrustedDir(restoreRoot, true); err != nil {
		return err
	}
	if err = ensureTrustedDir(unitRoot, false); err != nil {
		return err
	}
	if err = g.writeSnapshot(restoreRoot, restorePath(g.Policy.Network), ""); err != nil {
		return err
	}
	if current, readErr := os.ReadFile(restoreUnitPath(g.Policy.Network)); readErr != nil || string(current) != unit {
		if err = writeAtomic(unitRoot, restoreUnitPath(g.Policy.Network), []byte(unit), 0o644); err != nil {
			return err
		}
		if _, err = runSystem(ctx, "systemctl", []string{"daemon-reload"}, nil); err != nil {
			return err
		}
	}
	if _, err = runSystem(ctx, "systemctl", []string{"enable", RestoreUnit(g.Policy.Network)}, nil); err != nil {
		return errors.New("own firewall: boot-time restore unit not enabled; application must stay stopped")
	}
	return nil
}

// stampRestore marks the snapshot as restored in the running boot.
func (g *Guard) stampRestore() error {
	boot, err := currentBoot()
	if err != nil {
		return err
	}
	return g.writeSnapshot(restoreRoot, restorePath(g.Policy.Network), boot)
}

// disableRestore keys on the unit itself, not on the snapshot: a loaded or
// present unit is disabled and stopped (ending a running retry) and its failed
// state reset; remove also deletes the unit file and the snapshot. It fails
// unless the boot link is gone.
func disableRestore(ctx context.Context, network string, remove bool) error {
	_, statErr := os.Lstat(restoreUnitPath(network))
	present := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	state, err := runSystem(ctx, "systemctl", []string{"show", "--property=LoadState", "--value", RestoreUnit(network)}, nil)
	if err != nil {
		return err
	}
	loaded := strings.TrimSpace(string(state)) != "not-found"
	if present && !loaded {
		if _, err = runSystem(ctx, "systemctl", []string{"daemon-reload"}, nil); err != nil {
			return err
		}
		loaded = true
	}
	if loaded {
		if _, err = runSystem(ctx, "systemctl", []string{"disable", "--now", RestoreUnit(network)}, nil); err != nil {
			return err
		}
		// A unit that never failed has nothing to reset; that answer is not an error.
		_, _ = runSystem(ctx, "systemctl", []string{"reset-failed", RestoreUnit(network)}, nil)
	}
	if remove {
		if err = os.Remove(restoreUnitPath(network)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if present {
			if _, err = runSystem(ctx, "systemctl", []string{"daemon-reload"}, nil); err != nil {
				return err
			}
		}
		if err = os.Remove(restorePath(network)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err = removeDanglingWantsLink(network); err != nil {
		return err
	}
	if _, err = os.Lstat(restoreWantsLink(network)); !errors.Is(err, os.ErrNotExist) {
		return errors.New("own firewall: boot-time restore is still enabled; stop not verified")
	}
	return nil
}

// removeDanglingWantsLink deletes the unit's multi-user.target.wants link when
// it is a symlink whose target no longer exists (the unit file was removed
// without disabling it). systemd reports such a unit not-found and its
// disable cannot clear the link, which would otherwise block stop and remove
// forever. A link to an existing file is left for the verification to refuse.
func removeDanglingWantsLink(network string) error {
	link := restoreWantsLink(network)
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	if _, err = os.Stat(link); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err = os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// restoreDue loads the snapshot and reports whether the running boot still
// needs a restore: an unmarked snapshot (no completed start yet) always does.
func restoreDue(path string) (snapshot, bool, error) {
	if err := admitTrusted(path); err != nil {
		return snapshot{}, false, err
	}
	saved, err := loadSnapshot(path, restorePath)
	if err != nil {
		return snapshot{}, false, err
	}
	boot, err := currentBoot()
	if err != nil {
		return snapshot{}, false, err
	}
	return saved, saved.bootID != boot, nil
}

// admitReviewed requires the signed operation record's receipt to name this
// network, the current rule compiler, and the snapshot's and the freshly
// observed host's static ruleset.
// A changed rule definition or an edited snapshot is permanent at once; host
// facts that differ are permanent only once the boot has settled.
func admitReviewed(reviewed Receipt, network string, saved, observed Policy, settled bool) error {
	if reviewed.Network != network || saved.Network != network || observed.Network != network || reviewed.RulesetDigest == "" ||
		reviewed.PolicyDigest != DefinitionDigest() || reviewed.RulesetDigest != saved.Digest() {
		return PermanentRefusal{errors.New("own firewall: rule_definition_changed: the rule definition or snapshot differs from the reviewed ruleset; application stays stopped until a reviewed update")}
	}
	if reviewed.RulesetDigest != observed.Digest() {
		err := errors.New("own firewall: host_facts_changed: host network facts differ from the reviewed ruleset; application stays stopped until a reviewed update")
		if settled {
			return PermanentRefusal{err}
		}
		return err
	}
	return nil
}

// admitApplied requires the receipt of this boot's apply to equal the
// reviewed one exactly.
func admitApplied(reviewed, applied Receipt) error {
	if !reflect.DeepEqual(reviewed, applied) {
		return PermanentRefusal{errors.New("own firewall: rule_definition_changed: applied ruleset differs from the reviewed receipt; application stays stopped")}
	}
	return nil
}

// hostSettled reports whether the kernel has been up for HostSettleTime.
// An unreadable uptime counts as not settled, which only keeps retrying.
func hostSettled() bool {
	raw, err := os.ReadFile(uptimePath)
	if err != nil {
		return false
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return false
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	return err == nil && time.Duration(seconds*float64(time.Second)) >= HostSettleTime
}

// RestoreMain is the restore unit's only action. Once a start completed in the
// running boot it does nothing: the reviewed lifecycle owns that boot, and a
// flushed or changed table is never silently reinstalled. Otherwise it loads
// the signed reviewed receipt, observes the host again and requires the exact
// reviewed static ruleset, observes a fresh egress answer, applies the same
// rules and requires the reviewed receipt, checks the bridge, and only then
// delegates the start to the watcher. Any failure leaves the application
// stopped. systemd retries, except after a PermanentRefusal (RestoreExitCode
// maps it to RestorePermanentExit). An ingress-only policy observes no host
// facts and needs no egress echo, so it also restores on an offline host.
func RestoreMain(path string, reviewed func(workspace, network string) (Receipt, error)) (err error) {
	saved, due, err := restoreDue(path)
	if err != nil || !due {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), RestoreTimeout)
	defer cancel()
	network := saved.Policy.Network
	receipt, err := reviewed(saved.workspace, network)
	if err != nil {
		return err
	}
	observed, err := New(ctx, network, !saved.Policy.IngressOnly)
	if err != nil {
		return err
	}
	if err = admitReviewed(receipt, network, saved.Policy, observed.Policy, hostSettled()); err != nil {
		return err
	}
	g := &Guard{Policy: saved.Policy, run: Run}
	if err = g.ObserveEgress(ctx, saved.workspace); err != nil {
		return err
	}
	applied, err := g.Apply(ctx)
	if err != nil {
		return err
	}
	if err = admitApplied(receipt, applied); err != nil {
		return err
	}
	if err = g.VerifyBridge(); err != nil {
		return err
	}
	if err = g.startWatcher(ctx, saved.container, true); err != nil {
		stop, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer done()
		return errors.Join(err, stopLiveWatcher(stop, network, false))
	}
	return nil
}

// writeAtomic replaces path in root with a synced file and syncs the directory
// entry; the temporary file is removed on every error.
func writeAtomic(root, path string, raw []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(root, ".watch-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		if err = file.Chmod(mode); err == nil {
			err = file.Sync()
		}
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
