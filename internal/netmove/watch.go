package netmove

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// WatchService and WatchTimer are the systemd units of the network watcher.
	WatchService = "kombify-stackkit-network-watch.service"
	WatchTimer   = "kombify-stackkit-network-watch.timer"

	systemdUnitDir = "/etc/systemd/system"
	systemdRuntime = "/run/systemd/system"
	watchMarker    = "# Managed by `stackkit network watch`.\n"

	// RetryBackoff is how long a failed automatic rebind waits before the
	// next attempt, so a broken apply does not loop every minute.
	RetryBackoff = 5 * time.Minute
	// staleLockAge bounds a lock left behind by a killed process.
	staleLockAge = 45 * time.Minute

	stateFile = "network-watch-state.json"
	lockFile  = "network-watch.lock"
)

// WatchConfig describes the stack the watcher re-binds.
type WatchConfig struct {
	Binary    string // absolute path of the stackkit executable
	Workspace string // absolute stack directory
	Spec      string // StackSpec path as the stack's commands take it
	// Home and Path carry the environment the stack was applied with. Apply
	// trust and the kit catalog live under the installing user's home, which
	// is not root's when the installer ran as a sudo-capable user.
	Home string
	Path string
}

func unitArg(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(value)
	return `"` + escaped + `"`
}

// RenderWatchService renders the one-shot service the timer starts.
func RenderWatchService(cfg WatchConfig) string {
	exec := unitArg(cfg.Binary) + " --chdir " + unitArg(cfg.Workspace)
	if strings.TrimSpace(cfg.Spec) != "" {
		exec += " --spec " + unitArg(cfg.Spec)
	}
	exec += " network rebind --yes --auto"
	environment := ""
	if strings.TrimSpace(cfg.Home) != "" {
		environment += "Environment=" + unitArg("HOME="+cfg.Home) + "\n"
	}
	if strings.TrimSpace(cfg.Path) != "" {
		environment += "Environment=" + unitArg("PATH="+cfg.Path) + "\n"
	}
	return watchMarker + "[Unit]\nDescription=StackKits network move watcher\nConditionPathIsDirectory=" +
		strings.ReplaceAll(cfg.Workspace, "%", "%%") + "\n\n[Service]\nType=oneshot\nWorkingDirectory=" +
		strings.ReplaceAll(cfg.Workspace, "%", "%%") + "\n" + environment + "ExecStart=" + exec + "\nTimeoutStartSec=30min\n"
}

// RenderWatchTimer renders the timer: shortly after boot, then every minute.
func RenderWatchTimer() string {
	return watchMarker + "[Unit]\nDescription=StackKits network move watcher schedule\n\n[Timer]\nOnBootSec=30s\nOnUnitActiveSec=60s\nAccuracySec=5s\nUnit=" +
		WatchService + "\n\n[Install]\nWantedBy=timers.target\n"
}

// WatchInstaller installs and removes the watcher units.
type WatchInstaller struct {
	UnitDir string
	Sys     Sys
}

// DefaultWatchInstaller targets /etc/systemd/system on the running host.
func DefaultWatchInstaller() WatchInstaller {
	return WatchInstaller{UnitDir: systemdUnitDir, Sys: DefaultSys()}
}

// WatchSupported reports whether systemd is the running init.
func (w WatchInstaller) WatchSupported() bool {
	return w.Sys.Exists != nil && w.Sys.Exists(systemdRuntime)
}

// WatchStatus is the watcher's installed and running state.
type WatchStatus struct {
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	Active    bool   `json:"active"`
	Unit      string `json:"unit"`
}

// Enable writes both units and starts the timer. changed is false when the
// installed units already match and the timer is enabled.
func (w WatchInstaller) Enable(ctx context.Context, cfg WatchConfig) (changed bool, err error) {
	if !w.WatchSupported() {
		return false, errors.New("netmove: systemd is not the running init; the network watcher needs systemd")
	}
	service, timer := RenderWatchService(cfg), RenderWatchTimer()
	servicePath, timerPath := filepath.Join(w.UnitDir, WatchService), filepath.Join(w.UnitDir, WatchTimer)
	currentService, _ := os.ReadFile(servicePath) //nolint:gosec // fixed system path
	currentTimer, _ := os.ReadFile(timerPath)     //nolint:gosec // fixed system path
	if string(currentService) != service || string(currentTimer) != timer {
		if err := os.MkdirAll(w.UnitDir, 0o755); err != nil { //nolint:gosec // systemd unit directory
			return false, err
		}
		for path, content := range map[string]string{servicePath: service, timerPath: timer} {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // unit files are world-readable by convention
				return false, fmt.Errorf("netmove: write %s: %w (the network watcher needs root)", path, err)
			}
		}
		if out, err := w.Sys.Run(ctx, "systemctl", "daemon-reload"); err != nil {
			return false, fmt.Errorf("netmove: systemctl daemon-reload: %v: %s", err, strings.TrimSpace(string(out)))
		}
		changed = true
	}
	if !changed && w.Status(ctx).Enabled {
		return false, nil
	}
	if out, err := w.Sys.Run(ctx, "systemctl", "enable", "--now", WatchTimer); err != nil {
		return changed, fmt.Errorf("netmove: systemctl enable %s: %v: %s", WatchTimer, err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// Disable stops the timer and removes both units.
func (w WatchInstaller) Disable(ctx context.Context) (removed bool, err error) {
	servicePath, timerPath := filepath.Join(w.UnitDir, WatchService), filepath.Join(w.UnitDir, WatchTimer)
	_, timerErr := os.Stat(timerPath)
	_, serviceErr := os.Stat(servicePath)
	if timerErr != nil && serviceErr != nil {
		return false, nil
	}
	if w.WatchSupported() {
		_, _ = w.Sys.Run(ctx, "systemctl", "disable", "--now", WatchTimer)
	}
	for _, path := range []string{timerPath, servicePath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, err
		}
	}
	if w.WatchSupported() {
		_, _ = w.Sys.Run(ctx, "systemctl", "daemon-reload")
	}
	return true, nil
}

// Status reports the installed, enabled and active state of the timer.
func (w WatchInstaller) Status(ctx context.Context) WatchStatus {
	status := WatchStatus{Unit: WatchTimer}
	if _, err := os.Stat(filepath.Join(w.UnitDir, WatchTimer)); err == nil {
		status.Installed = true
	}
	if status.Installed && w.WatchSupported() && w.Sys.Run != nil {
		out, err := w.Sys.Run(ctx, "systemctl", "is-enabled", WatchTimer)
		status.Enabled = err == nil && strings.TrimSpace(string(out)) == "enabled"
		out, err = w.Sys.Run(ctx, "systemctl", "is-active", WatchTimer)
		status.Active = err == nil && strings.TrimSpace(string(out)) == "active"
	}
	return status
}

// ---- automatic-run state ----

// State is the watcher's persisted attempt record under the stack's .stackkit.
type State struct {
	LastAttempt time.Time `json:"lastAttempt"`
	LastFailed  bool      `json:"lastFailed"`
	LastError   string    `json:"lastError,omitempty"`
	LastAddress string    `json:"lastAddress,omitempty"`
	// ApplyPending is set once the custody was re-issued and cleared when the
	// regenerate-and-apply that follows succeeds, so a failed apply is retried
	// even though the recorded addresses already match.
	ApplyPending bool `json:"applyPending,omitempty"`
}

// ShouldBackOff reports whether the previous attempt failed too recently.
func (s State) ShouldBackOff(now time.Time) bool {
	return s.LastFailed && now.Sub(s.LastAttempt) < RetryBackoff
}

// LoadState reads the state; a missing or unreadable file is the zero state.
func LoadState(workspace string) State {
	var state State
	raw, err := os.ReadFile(filepath.Join(workspace, ".stackkit", stateFile)) //nolint:gosec // fixed path under the workspace
	if err == nil {
		_ = json.Unmarshal(raw, &state)
	}
	return state
}

// SaveState persists the state with owner-only permissions.
func SaveState(workspace string, state State) error {
	dir := filepath.Join(workspace, ".stackkit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, stateFile), raw)
}

// AcquireLock takes the watcher lock. ok is false when another run holds it.
// A lock older than staleLockAge belongs to a killed process and is replaced.
func AcquireLock(workspace string, now time.Time) (release func(), ok bool, err error) {
	dir := filepath.Join(workspace, ".stackkit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	path := filepath.Join(dir, lockFile)
	for attempt := 0; attempt < 2; attempt++ {
		file, openErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // fixed path under the workspace
		if openErr == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Close()
			return func() { _ = os.Remove(path) }, true, nil
		}
		if !errors.Is(openErr, os.ErrExist) {
			return nil, false, openErr
		}
		info, statErr := os.Stat(path)
		if statErr != nil || now.Sub(info.ModTime()) < staleLockAge {
			return nil, false, nil
		}
		_ = os.Remove(path)
	}
	return nil, false, nil
}

const optOutFile = "network-watch.disabled"

// WatchOptedOut reports whether the owner disabled the watcher for this stack,
// so a later Apply does not enable it again.
func WatchOptedOut(workspace string) bool {
	_, err := os.Stat(filepath.Join(workspace, ".stackkit", optOutFile))
	return err == nil
}

// SetWatchOptOut records or clears the owner's opt-out.
func SetWatchOptOut(workspace string, optedOut bool) error {
	path := filepath.Join(workspace, ".stackkit", optOutFile)
	if !optedOut {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("disabled by `stackkit network watch disable`\n"), 0o600)
}
