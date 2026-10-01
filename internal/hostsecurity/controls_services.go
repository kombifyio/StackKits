package hostsecurity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/hostmaintenance"
)

const (
	unattendedStampPath   = "/var/lib/apt/periodic/unattended-upgrades-stamp"
	aptUpdateStampPath    = "/var/lib/apt/periodic/update-success-stamp"
	rebootRequiredPath    = "/var/run/reboot-required"
	rebootRequiredPkgs    = "/var/run/reboot-required.pkgs"
	apkUpgradeJobPath     = "/etc/periodic/daily/stackkit-security-upgrades"
	unattendedMaxAge      = 72 * time.Hour
	packageIndexMaxAge    = 48 * time.Hour
	unattendedTimerUnit   = "apt-daily-upgrade.timer"
	fail2banUnit          = "fail2ban"
	unattendedPackageName = "unattended-upgrades"
)

func (o *observation) bruteForce(ctx context.Context) Control {
	c := o.control(ControlBruteForce, "fail2ban is active with the sshd jail loaded",
		Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlBruteForce})
	if absent, detail := o.sshAbsent(); absent {
		return o.finish(c, StateCompliant, "no ssh server is installed", detail)
	} else if detail != "" {
		return o.unknown(c, detail)
	}
	if !o.engine.isRoot() {
		return o.unknown(c, rootHint("query the fail2ban sshd jail"))
	}
	if _, installed := o.engine.toolPath("fail2ban-client"); !installed {
		return o.finish(c, StateDrifted, "fail2ban is not installed", "brute-force protection for ssh is absent")
	}
	active, err := o.engine.run(ctx, "systemctl", "is-active", fail2banUnit)
	if errors.Is(err, errToolMissing) {
		return o.unknown(c, "systemd is not available, so the fail2ban service state cannot be read")
	}
	if err != nil {
		return o.unknown(c, "systemctl could not run: "+err.Error())
	}
	state := strings.TrimSpace(active.Stdout)
	if state != "active" {
		return o.finish(c, StateDrifted, "fail2ban service is "+valueOr(state, "not active"), "the brute-force protection service is not running")
	}
	jail, err := o.engine.run(ctx, "fail2ban-client", "status", "sshd")
	if err != nil {
		return o.unknown(c, "fail2ban-client could not run: "+err.Error())
	}
	if jail.ExitCode != 0 {
		return o.finish(c, StateDrifted, "fail2ban is active but the sshd jail is not loaded", "ssh is not covered by brute-force protection")
	}
	return o.finish(c, StateCompliant, "fail2ban active, sshd jail loaded", "")
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (o *observation) unattendedUpgrades(ctx context.Context) Control {
	c := o.control(ControlUnattended, "automatic security updates enabled and run within "+unattendedMaxAge.String(),
		Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlUnattended})
	if _, apt := o.engine.toolPath("apt-get"); !apt {
		return o.unattendedApk(c)
	}
	status, err := o.engine.run(ctx, "dpkg-query", "-W", "-f=${Status}", unattendedPackageName)
	if err != nil {
		return o.unknown(c, "dpkg-query could not run: "+err.Error())
	}
	if !strings.Contains(status.Stdout, "ok installed") {
		return o.finish(c, StateDrifted, "unattended-upgrades is not installed", "automatic security updates are absent")
	}
	config, err := o.engine.run(ctx, "apt-config", "dump")
	if err != nil || config.ExitCode != 0 {
		return o.unknown(c, "apt-config could not report the effective apt configuration")
	}
	if !aptPeriodicEnabled(config.Stdout, "APT::Periodic::Unattended-Upgrade") {
		return o.finish(c, StateDrifted, "APT::Periodic::Unattended-Upgrade is not enabled", "apt is not configured to run unattended upgrades")
	}
	timer, err := o.engine.run(ctx, "systemctl", "is-enabled", unattendedTimerUnit)
	if errors.Is(err, errToolMissing) {
		return o.unknown(c, "systemd is not available, so the apt-daily-upgrade timer cannot be read")
	}
	if err != nil {
		return o.unknown(c, "systemctl could not run: "+err.Error())
	}
	if state := strings.TrimSpace(timer.Stdout); state != "enabled" && state != "static" && state != "enabled-runtime" {
		return o.finish(c, StateDrifted, unattendedTimerUnit+" is "+valueOr(state, "not enabled"), "the timer that runs unattended upgrades is disabled")
	}
	now := o.now()
	info, statErr := o.engine.Host.Stat(unattendedStampPath)
	if statErr != nil {
		return o.finish(c, StateCompliant, "enabled; no run recorded yet", "the timer is active, so the first run is pending")
	}
	if age := now.Sub(info.ModTime()); age > unattendedMaxAge {
		return o.finish(c, StateDrifted, "last run "+info.ModTime().UTC().Format(time.RFC3339), fmt.Sprintf("unattended-upgrades has not run for %s", age.Round(time.Hour)))
	}
	return o.finish(c, StateCompliant, "enabled; last run "+info.ModTime().UTC().Format(time.RFC3339), "")
}

func (o *observation) unattendedApk(c Control) Control {
	if _, apk := o.engine.toolPath("apk"); !apk {
		return o.unknown(c, "neither apt nor apk is available, so automatic security updates cannot be observed")
	}
	info, err := o.engine.Host.Stat(apkUpgradeJobPath)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		return o.finish(c, StateDrifted, "no periodic apk upgrade job", "automatic security updates are absent")
	}
	return o.finish(c, StateCompliant, "periodic apk upgrade job installed (last run is not recorded on apk hosts)", "")
}

// aptPeriodicEnabled reads `apt-config dump` for a periodic key that is set to
// a non-zero value.
func aptPeriodicEnabled(dump, key string) bool {
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		rest, found := strings.CutPrefix(line, key)
		if !found {
			continue
		}
		value := strings.Trim(strings.TrimSpace(rest), `";`)
		return value != "" && value != "0"
	}
	return false
}

func (o *observation) pendingSecurity(ctx context.Context) Control {
	c := o.control(ControlPendingSecurity, "no pending security updates",
		Remediation{Capability: RemediationManual, Action: "stackkit host updates plan, then stackkit host updates apply --plan-digest <digest> --yes"})
	if _, apt := o.engine.toolPath("apt-get"); !apt {
		return o.unknown(c, "apt is not available, so pending security updates cannot be counted")
	}
	output, err := o.engine.run(ctx, "apt-get", "-s", "-q", "-o", "Debug::NoLocking=true", "upgrade")
	if err != nil {
		return o.unknown(c, "apt-get could not simulate the upgrade: "+err.Error())
	}
	if output.ExitCode != 0 {
		return o.unknown(c, "apt-get -s upgrade failed: "+boundedText(output.Stderr))
	}
	pending, security := hostmaintenance.SimulationCounts(output.Stdout)
	observed := fmt.Sprintf("%d security updates pending (%d updates in total)", security, pending)
	if security > 0 {
		return o.finish(c, StateDrifted, observed, "security updates are available and not installed")
	}
	// A count of zero from an old package index proves nothing.
	info, statErr := o.engine.Host.Stat(aptUpdateStampPath)
	if statErr != nil {
		return o.finish(c, StateUnknown, observed, "the package index has no recorded refresh, so a zero count cannot be trusted")
	}
	if age := o.now().Sub(info.ModTime()); age > packageIndexMaxAge {
		return o.finish(c, StateUnknown, observed, fmt.Sprintf("the package index is %s old, so a zero count cannot be trusted", age.Round(time.Hour)))
	}
	return o.finish(c, StateCompliant, observed, "")
}

func (o *observation) rebootRequired() Control {
	c := o.control(ControlRebootRequired, "no reboot pending",
		Remediation{Capability: RemediationManual, Action: "stackkit host reboot --yes"})
	for _, path := range []string{rebootRequiredPath, "/run/reboot-required"} {
		if _, err := o.engine.Host.Stat(path); err == nil {
			observed := "reboot required"
			if raw, readErr := o.engine.Host.ReadFile(rebootRequiredPkgs); readErr == nil {
				if packages := strings.Fields(string(raw)); len(packages) > 0 {
					if len(packages) > 5 {
						packages = append(packages[:5], "...")
					}
					observed += " by " + strings.Join(packages, ", ")
				}
			}
			return o.finish(c, StateDrifted, observed, "updates are installed but not active until the node reboots")
		}
	}
	return o.finish(c, StateCompliant, "no reboot pending", "")
}
