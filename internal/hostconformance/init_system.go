package hostconformance

import (
	"context"
	"errors"
	"runtime"
	"strings"
)

const (
	// InitSystemNameSystemd is emitted only when PID 1 and the systemd
	// manager agree that the local manager is active.
	InitSystemNameSystemd = "systemd"
	// InitSystemNameOther identifies a known non-systemd PID 1. It is not a
	// scheduler capability claim.
	InitSystemNameOther = "other"
	// InitSystemNameUnknown means the local init system could not be safely
	// identified or its manager was not active.
	InitSystemNameUnknown = "unknown"

	InitSystemPID1Systemd = "systemd"
	InitSystemPID1Other   = "other"
	InitSystemPID1Unknown = "unknown"

	InitSystemStateInitializing = "initializing"
	InitSystemStateStarting     = "starting"
	InitSystemStateRunning      = "running"
	InitSystemStateDegraded     = "degraded"
	InitSystemStateMaintenance  = "maintenance"
	InitSystemStateStopping     = "stopping"
	InitSystemStateOffline      = "offline"
	InitSystemStateUnknown      = "unknown"
	InitSystemStateUnavailable  = "unavailable"
)

// InitSystemFacts is the bounded, read-only observation needed by consumers
// that may later offer a native systemd timer. Name is systemd only when the
// observed PID 1 is systemd and its manager reports an active state. Other
// init systems and an unavailable manager remain explicit non-capabilities.
type InitSystemFacts struct {
	Name         string `json:"name"`
	PID1         string `json:"pid1"`
	ManagerState string `json:"managerState"`
}

// Observed reports whether the fact was populated. Empty values are retained
// as a compatibility path for older injected host observations; a new local
// probe always returns a populated fact, including an unknown result.
func (f InitSystemFacts) Observed() bool {
	return f.Name != "" || f.PID1 != "" || f.ManagerState != ""
}

// SystemdActive is the only state that a future systemd scheduler may accept.
// Callers must still bind any resulting mutation to their plan and owner
// custody; this fact alone never authorizes a timer.
func (f InitSystemFacts) SystemdActive() bool {
	return f.Name == InitSystemNameSystemd && f.PID1 == InitSystemPID1Systemd &&
		allowedValue(f.ManagerState, InitSystemStateRunning, InitSystemStateDegraded)
}

// ObserveInitSystem is the shared local observation boundary for
// host-conformance and host-preflight. It reads PID 1 and, only for systemd,
// asks the local manager for its bounded active state. It never installs,
// enables, starts, or otherwise mutates an init system.
func ObserveInitSystem(ctx context.Context, source LocalSource) InitSystemFacts {
	return observeInitSystemForOS(runtime.GOOS, ctx, source)
}

func observeInitSystemForOS(goos string, ctx context.Context, source LocalSource) InitSystemFacts {
	if source == nil {
		source = osLocalSource{}
	}
	if goos != "linux" {
		return InitSystemFacts{
			Name: InitSystemNameUnknown, PID1: InitSystemPID1Unknown,
			ManagerState: InitSystemStateUnavailable,
		}
	}

	pid1Output, err := source.ReadFile("/proc/1/comm")
	if err != nil {
		return InitSystemFacts{
			Name: InitSystemNameUnknown, PID1: InitSystemPID1Unknown,
			ManagerState: InitSystemStateUnknown,
		}
	}
	if !strings.EqualFold(strings.TrimSpace(string(pid1Output)), InitSystemPID1Systemd) {
		return InitSystemFacts{
			Name: InitSystemNameOther, PID1: InitSystemPID1Other,
			ManagerState: InitSystemStateUnavailable,
		}
	}

	if _, err := source.LookPath("systemctl"); err != nil {
		return InitSystemFacts{
			Name: InitSystemNameUnknown, PID1: InitSystemPID1Systemd,
			ManagerState: InitSystemStateUnavailable,
		}
	}
	output, runErr := source.Run(ctx, "systemctl", "is-system-running")
	state := normalizeSystemdState(output, runErr)
	name := InitSystemNameUnknown
	if allowedValue(state, InitSystemStateRunning, InitSystemStateDegraded) {
		name = InitSystemNameSystemd
	}
	return InitSystemFacts{Name: name, PID1: InitSystemPID1Systemd, ManagerState: state}
}

func normalizeSystemdState(output []byte, runErr error) string {
	value := strings.TrimSpace(string(output))
	if value == "" {
		return InitSystemStateUnavailable
	}
	fields := strings.Fields(value)
	if len(fields) != 1 {
		return InitSystemStateUnknown
	}
	state := strings.ToLower(fields[0])
	if allowedValue(state,
		InitSystemStateInitializing,
		InitSystemStateStarting,
		InitSystemStateRunning,
		InitSystemStateDegraded,
		InitSystemStateMaintenance,
		InitSystemStateStopping,
		InitSystemStateOffline,
		InitSystemStateUnknown,
	) {
		// systemctl exits non-zero for degraded/starting and several other
		// reported states. The bounded state itself remains useful evidence.
		return state
	}
	if runErr != nil {
		return InitSystemStateUnavailable
	}
	return InitSystemStateUnknown
}

func validateInitSystemFacts(facts InitSystemFacts) error {
	if !facts.Observed() {
		return nil
	}
	if !allowedValue(facts.Name, InitSystemNameSystemd, InitSystemNameOther, InitSystemNameUnknown) ||
		!allowedValue(facts.PID1, InitSystemPID1Systemd, InitSystemPID1Other, InitSystemPID1Unknown) ||
		!allowedValue(facts.ManagerState,
			InitSystemStateInitializing,
			InitSystemStateStarting,
			InitSystemStateRunning,
			InitSystemStateDegraded,
			InitSystemStateMaintenance,
			InitSystemStateStopping,
			InitSystemStateOffline,
			InitSystemStateUnknown,
			InitSystemStateUnavailable,
		) {
		return errors.New("host init-system facts are invalid")
	}
	switch facts.Name {
	case InitSystemNameSystemd:
		if !facts.SystemdActive() {
			return errors.New("systemd init-system fact is not backed by an active PID 1 manager")
		}
	case InitSystemNameOther:
		if facts.PID1 != InitSystemPID1Other || facts.ManagerState != InitSystemStateUnavailable {
			return errors.New("non-systemd init-system fact is inconsistent")
		}
	case InitSystemNameUnknown:
		// Unknown is deliberately valid for every bounded PID1/manager state;
		// consumers must not treat it as a scheduler capability.
	}
	return nil
}
