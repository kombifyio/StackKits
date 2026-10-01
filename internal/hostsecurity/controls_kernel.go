package hostsecurity

import (
	"fmt"
	"strconv"
	"strings"
)

// ManagedSysctl is one kernel parameter the baseline manages. The baseline
// accepts a stricter value than it sets: Minimum is the lowest acceptable.
type ManagedSysctl struct {
	Key     string
	Minimum int
}

// managedSysctls mirrors the keys the architecture-v2 security-baseline
// host policy writes to /etc/sysctl.d/99-stackkit-foundation-hardening.conf.
var managedSysctls = []ManagedSysctl{
	{"net.ipv4.tcp_syncookies", 1},
	{"kernel.kptr_restrict", 1},
	{"kernel.dmesg_restrict", 1},
	{"fs.protected_hardlinks", 1},
	{"fs.protected_symlinks", 1},
	{"fs.protected_fifos", 2},
	{"fs.protected_regular", 2},
}

// ManagedSysctls lists the kernel parameters the baseline manages.
func ManagedSysctls() []ManagedSysctl { return append([]ManagedSysctl(nil), managedSysctls...) }

func sysctlPath(key string) string {
	return "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
}

func (o *observation) sysctl() Control {
	c := o.control(ControlSysctl, fmt.Sprintf("%d managed kernel parameters at or above the baseline value", len(managedSysctls)),
		Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlSysctl})
	var drifted, unreadable []string
	for _, key := range managedSysctls {
		raw, err := o.engine.Host.ReadFile(sysctlPath(key.Key))
		if err != nil {
			unreadable = append(unreadable, key.Key)
			continue
		}
		value, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if parseErr != nil {
			unreadable = append(unreadable, key.Key)
			continue
		}
		if value < key.Minimum {
			drifted = append(drifted, fmt.Sprintf("%s=%d (want >= %d)", key.Key, value, key.Minimum))
		}
	}
	switch {
	case len(drifted) > 0:
		return o.finish(c, StateDrifted, strings.Join(drifted, ", "), "kernel hardening parameters are below the baseline")
	case len(unreadable) > 0:
		return o.unknown(c, "kernel parameters are not readable here: "+strings.Join(unreadable, ", "))
	}
	return o.finish(c, StateCompliant, "all managed kernel parameters at or above the baseline", "")
}
