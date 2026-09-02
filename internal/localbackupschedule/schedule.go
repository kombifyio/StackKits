// Package localbackupschedule lowers the CUE-governed UTC backup cadence to a
// bounded pair of local systemd units. It owns no plan or owner authority and
// never invokes the backup runtime directly.
package localbackupschedule

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/hostconformance"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

const (
	defaultUnitDirectory = "/etc/systemd/system"
	serviceUnitSuffix    = ".service"
	timerUnitSuffix      = ".timer"
	backupUnitPrefix     = "stackkit-local-backup-"
	serviceTimeoutSec    = 14 * 60
	unitOwnershipVersion = "stackkit-local-backup-schedule/v1"
)

var (
	ErrSystemdUnavailable = errors.New("local backup scheduling requires an active local systemd manager")
	ErrUnitNotInstalled   = errors.New("local backup schedule units are not installed")
)

// Runner is the narrow command boundary for systemctl. Implementations must
// execute argv directly without a shell; the production runner applies its
// own short timeout and the package never sends backup-runtime commands here.
type Runner interface {
	Run(context.Context, []string) ([]byte, error)
}

// InitObserver is the shared hostconformance observation boundary. Production
// callers should leave it nil so the package observes the local host through
// hostconformance.ObserveInitSystem; tests may inject a deterministic fact.
type InitObserver func(context.Context) hostconformance.InitSystemFacts

// ProcessUID resolves the effective UID that will be written to systemd's
// User=. Production uses the numeric effective UID and never a caller supplied
// username.
type ProcessUID func() (string, error)

// Options configures the local command and filesystem boundaries. Unit names
// remain package-owned; UnitDirectory is intended for an injected test root
// and defaults to the system unit directory in production.
type Options struct {
	Runner        Runner
	ObserveInit   InitObserver
	CurrentUID    ProcessUID
	UnitDirectory string
}

// Scheduler renders and controls one workspace-bound local backup timer.
// Owner approval and signed plan state are deliberately outside this package.
type Scheduler struct {
	runner        Runner
	observeInit   InitObserver
	currentUID    ProcessUID
	unitDirectory string
}

// RenderRequest contains already resolved, owner-independent inputs. The
// schedule is the shared localbackuppolicy value; this package does not add
// defaults or parse arbitrary cron expressions.
type RenderRequest struct {
	WorkspacePath string
	SpecPath      string
	Schedule      localbackuppolicy.Schedule
	CLI           interface {
		Path() string
		Verify() error
	}
}

// UnitNames identifies the fixed service/timer pair derived from a workspace.
// The workspace digest prevents two local stacks from silently sharing a
// timer while keeping the unit identity independent of mutable plan fields.
type UnitNames struct {
	Service string `json:"service"`
	Timer   string `json:"timer"`
}

// RenderedUnits is the complete canonical unit pair and its confined paths.
// Unit bytes contain only paths, a process user, and the resolved UTC cadence;
// no credentials or owner material are included.
type RenderedUnits struct {
	Names         UnitNames
	ServicePath   string
	TimerPath     string
	ServiceBytes  []byte
	TimerBytes    []byte
	WorkspacePath string
	SpecPath      string
	// ProcessUID is the numeric effective UID used by User=.
	ProcessUID string
}

// UnitStatus is a read-only systemd status snapshot for one workspace timer.
// State values are normalized closed vocabulary; command diagnostics are not
// copied into the result.
type UnitStatus struct {
	Names        UnitNames `json:"names"`
	EnabledState string    `json:"enabledState"`
	ActiveState  string    `json:"activeState"`
	Enabled      bool      `json:"enabled"`
	Active       bool      `json:"active"`
}

// Digest returns the secret-free digest of both rendered unit files. The
// separator keeps the pair boundary unambiguous and lets an authority bind
// the exact service/timer bytes as one artifact.
func (units RenderedUnits) Digest() string {
	hasher := sha256.New()
	_, _ = hasher.Write(units.ServiceBytes)
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(units.TimerBytes)
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

// New creates a scheduler with the shared host fact and direct command
// boundaries. It performs no observation, file write, or systemd mutation.
func New(options Options) *Scheduler {
	runner := options.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	observeInit := options.ObserveInit
	if observeInit == nil {
		observeInit = func(ctx context.Context) hostconformance.InitSystemFacts {
			return hostconformance.ObserveInitSystem(ctx, nil)
		}
	}
	currentUID := options.CurrentUID
	if currentUID == nil {
		currentUID = currentProcessUID
	}
	unitDirectory := options.UnitDirectory
	if unitDirectory == "" {
		unitDirectory = defaultUnitDirectory
	}
	return &Scheduler{
		runner: runner, observeInit: observeInit, currentUID: currentUID,
		unitDirectory: unitDirectory,
	}
}

// UnitNamesForWorkspace derives the package-owned unit identity without
// requiring the workspace to exist. Disable and status use this for cleanup
// and diagnostics after a workspace has been moved or removed.
func UnitNamesForWorkspace(workspace string) (UnitNames, error) {
	canonical, err := absolutePath(workspace, "workspace")
	if err != nil {
		return UnitNames{}, err
	}
	digest := sha256.Sum256([]byte(filepath.ToSlash(canonical)))
	suffix := hex.EncodeToString(digest[:])
	prefix := backupUnitPrefix + suffix
	return UnitNames{Service: prefix + serviceUnitSuffix, Timer: prefix + timerUnitSuffix}, nil
}

// Render produces the exact service and timer files without requiring an
// active systemd manager. Installation, enabling, and status each perform
// the live prerequisite observation separately before their side effect or
// readback.
func (s *Scheduler) Render(request RenderRequest) (RenderedUnits, error) {
	if s == nil {
		return RenderedUnits{}, errors.New("local backup scheduler is not configured")
	}
	workspace, err := existingDirectory(request.WorkspacePath, "workspace")
	if err != nil {
		return RenderedUnits{}, err
	}
	specPath, err := existingFile(request.SpecPath, "StackSpec")
	if err != nil {
		return RenderedUnits{}, err
	}
	if request.CLI == nil {
		return RenderedUnits{}, errors.New("local backup schedule requires a bound StackKit CLI")
	}
	if err := request.CLI.Verify(); err != nil {
		return RenderedUnits{}, fmt.Errorf("verify bound StackKit CLI: %w", err)
	}
	cliPath := request.CLI.Path()
	if _, err := absolutePath(cliPath, "StackKit CLI"); err != nil {
		return RenderedUnits{}, err
	}
	if err := request.Schedule.Validate(); err != nil {
		return RenderedUnits{}, err
	}
	processUID, err := s.currentUID()
	if err != nil {
		return RenderedUnits{}, fmt.Errorf("resolve system service UID: %w", err)
	}
	if err := validateProcessUID(processUID); err != nil {
		return RenderedUnits{}, err
	}
	names, err := UnitNamesForWorkspace(workspace)
	if err != nil {
		return RenderedUnits{}, err
	}
	unitDirectory, err := absolutePath(s.unitDirectory, "systemd unit directory")
	if err != nil {
		return RenderedUnits{}, err
	}
	serviceBytes, err := renderService(names, workspace, specPath, cliPath, processUID)
	if err != nil {
		return RenderedUnits{}, err
	}
	timerBytes, err := renderTimer(names, workspace, request.Schedule)
	if err != nil {
		return RenderedUnits{}, err
	}
	return RenderedUnits{
		Names:         names,
		ServicePath:   filepath.Join(unitDirectory, names.Service),
		TimerPath:     filepath.Join(unitDirectory, names.Timer),
		ServiceBytes:  serviceBytes,
		TimerBytes:    timerBytes,
		WorkspacePath: workspace,
		SpecPath:      specPath,
		ProcessUID:    processUID,
	}, nil
}

func renderService(names UnitNames, workspace, specPath, cliPath, processUID string) ([]byte, error) {
	marker, err := unitOwnershipMarker(workspace, names.Service)
	if err != nil {
		return nil, err
	}
	workspaceArg, err := systemdPathArg(workspace, "workspace")
	if err != nil {
		return nil, err
	}
	workspaceValue, err := systemdPathValue(workspace, "workspace")
	if err != nil {
		return nil, err
	}
	cliArg, err := systemdPathArg(cliPath, "StackKit CLI")
	if err != nil {
		return nil, err
	}
	specArg, err := systemdPathArg(specPath, "StackSpec")
	if err != nil {
		return nil, err
	}
	// Environment expansion applies only to ExecStart, while specifier
	// expansion also applies to WorkingDirectory and ConditionPathIsDirectory.
	execArg := func(value string) string { return strings.ReplaceAll(value, "$", "$$") }
	service := fmt.Sprintf("%s[Unit]\nDescription=StackKits local backup\nConditionPathIsDirectory=%s\nStartLimitIntervalSec=1h\nStartLimitBurst=3\n\n[Service]\nType=oneshot\nUser=%s\nWorkingDirectory=%s\nExecStart=%s --chdir %s --spec %s backup run --scheduled --json\nTimeoutStartSec=%ds\nRestart=on-failure\nRestartSec=60s\n\n[Install]\nWantedBy=multi-user.target\n", marker, workspaceValue, processUID, workspaceValue, execArg(cliArg), execArg(workspaceArg), execArg(specArg), serviceTimeoutSec)
	return []byte(service), nil
}

func renderTimer(names UnitNames, workspace string, schedule localbackuppolicy.Schedule) ([]byte, error) {
	marker, err := unitOwnershipMarker(workspace, names.Timer)
	if err != nil {
		return nil, err
	}
	calendar, err := calendarExpression(schedule)
	if err != nil {
		return nil, err
	}
	timer := fmt.Sprintf("%s[Unit]\nDescription=StackKits local backup schedule\n\n[Timer]\nOnCalendar=%s\nPersistent=true\nRandomizedDelaySec=%ds\nUnit=%s\n\n[Install]\nWantedBy=timers.target\n", marker, calendar, schedule.JitterSeconds, names.Service)
	return []byte(timer), nil
}

func calendarExpression(schedule localbackuppolicy.Schedule) (string, error) {
	if err := schedule.Validate(); err != nil {
		return "", err
	}
	minute := fmt.Sprintf("%02d", schedule.MinuteUTC)
	switch schedule.Cadence {
	case "hourly":
		return fmt.Sprintf("*-*-* *:%s:00 UTC", minute), nil
	case "daily":
		return fmt.Sprintf("*-*-* %02d:%s:00 UTC", *schedule.HourUTC, minute), nil
	case "weekly":
		weekday, ok := map[string]string{
			"sunday": "Sun", "monday": "Mon", "tuesday": "Tue", "wednesday": "Wed",
			"thursday": "Thu", "friday": "Fri", "saturday": "Sat",
		}[schedule.WeekdayUTC]
		if !ok {
			return "", errors.New("weekly backup schedule has no canonical UTC weekday")
		}
		return fmt.Sprintf("%s *-*-* %02d:%s:00 UTC", weekday, *schedule.HourUTC, minute), nil
	default:
		return "", errors.New("backup schedule cadence is unsupported")
	}
}

// LatestSlot returns the most recent governed UTC slot at or before now. It
// deliberately does not apply timer jitter: jitter affects when systemd
// starts the service, while the scheduled operation remains bound to the
// deterministic cadence slot.
func LatestSlot(schedule localbackuppolicy.Schedule, now time.Time) (time.Time, error) {
	if err := schedule.Validate(); err != nil {
		return time.Time{}, err
	}
	if now.IsZero() {
		return time.Time{}, errors.New("scheduled backup slot requires a current UTC time")
	}
	now = now.UTC()
	var candidate time.Time
	switch schedule.Cadence {
	case "hourly":
		candidate = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), schedule.MinuteUTC, 0, 0, time.UTC)
		if candidate.After(now) {
			candidate = candidate.Add(-time.Hour)
		}
	case "daily":
		candidate = time.Date(now.Year(), now.Month(), now.Day(), *schedule.HourUTC, schedule.MinuteUTC, 0, 0, time.UTC)
		if candidate.After(now) {
			candidate = candidate.AddDate(0, 0, -1)
		}
	case "weekly":
		target := map[string]time.Weekday{
			"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
			"wednesday": time.Wednesday, "thursday": time.Thursday,
			"friday": time.Friday, "saturday": time.Saturday,
		}[schedule.WeekdayUTC]
		daysBack := (int(now.Weekday()) - int(target) + 7) % 7
		date := now.AddDate(0, 0, -daysBack)
		candidate = time.Date(date.Year(), date.Month(), date.Day(), *schedule.HourUTC, schedule.MinuteUTC, 0, 0, time.UTC)
		if candidate.After(now) {
			candidate = candidate.AddDate(0, 0, -7)
		}
	default:
		return time.Time{}, errors.New("backup schedule cadence is unsupported")
	}
	return candidate, nil
}

func (s *Scheduler) requireSystemd(ctx context.Context) error {
	if s == nil || s.observeInit == nil {
		return ErrSystemdUnavailable
	}
	facts := s.observeInit(ctx)
	if !facts.SystemdActive() {
		return fmt.Errorf("%w: name=%s pid1=%s managerState=%s", ErrSystemdUnavailable, facts.Name, facts.PID1, facts.ManagerState)
	}
	return nil
}

func (s *Scheduler) run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("systemd command is empty")
	}
	return s.runner.Run(ctx, args)
}

func (s *Scheduler) unitPaths(workspace string) (UnitNames, string, string, error) {
	names, err := UnitNamesForWorkspace(workspace)
	if err != nil {
		return UnitNames{}, "", "", err
	}
	directory, err := absolutePath(s.unitDirectory, "systemd unit directory")
	if err != nil {
		return UnitNames{}, "", "", err
	}
	return names, filepath.Join(directory, names.Service), filepath.Join(directory, names.Timer), nil
}

// Install writes the exact unit pair and reloads the observed local systemd
// manager. It does not enable or start the timer; callers must perform that
// owner-approved action explicitly through Enable.
func (s *Scheduler) Install(ctx context.Context, request RenderRequest) (RenderedUnits, error) {
	if err := s.requireSystemd(ctx); err != nil {
		return RenderedUnits{}, err
	}
	rendered, err := s.Render(request)
	if err != nil {
		return RenderedUnits{}, err
	}
	if err := ensureUnitDirectory(s.unitDirectory); err != nil {
		return RenderedUnits{}, err
	}
	if err := verifyInstallTargets(rendered); err != nil {
		return RenderedUnits{}, err
	}
	previousService, serviceExisted, err := snapshotUnit(rendered.ServicePath)
	if err != nil {
		return RenderedUnits{}, err
	}
	previousTimer, timerExisted, err := snapshotUnit(rendered.TimerPath)
	if err != nil {
		return RenderedUnits{}, err
	}
	changedService, err := writeUnitIfChanged(rendered.ServicePath, rendered.ServiceBytes, unitOwnershipMarkerFor(rendered, rendered.Names.Service))
	if err != nil {
		return RenderedUnits{}, err
	}
	changedTimer, err := writeUnitIfChanged(rendered.TimerPath, rendered.TimerBytes, unitOwnershipMarkerFor(rendered, rendered.Names.Timer))
	if err != nil {
		rollbackErr := rollbackUnit(rendered.ServicePath, previousService, serviceExisted, rendered.ServiceBytes)
		if rollbackErr != nil {
			rollbackErr = fmt.Errorf("restore service unit after failed timer install: %w", rollbackErr)
		}
		return RenderedUnits{}, errors.Join(err, rollbackErr)
	}
	if changedService || changedTimer {
		if _, err := s.run(ctx, "systemctl", "daemon-reload"); err != nil {
			rollbackErr := rollbackInstalledUnits(rendered, previousService, serviceExisted, previousTimer, timerExisted)
			if rollbackErr != nil {
				return RenderedUnits{}, fmt.Errorf("reload local systemd manager: %w (unit rollback failed: %v)", err, rollbackErr)
			}
			if _, rollbackReloadErr := s.run(ctx, "systemctl", "daemon-reload"); rollbackReloadErr != nil {
				return RenderedUnits{}, fmt.Errorf("reload local systemd manager: %w (restored units but rollback reload failed: %v)", err, rollbackReloadErr)
			}
			return RenderedUnits{}, fmt.Errorf("reload local systemd manager: %w", err)
		}
	}
	return rendered, nil
}

// VerifyInstalled re-renders the exact request and checks both unit files.
// Scheduled invocations use this immediately before their lifecycle call so
// a replaced CLI, workspace, spec path, or cadence cannot be silently used.
func (s *Scheduler) VerifyInstalled(request RenderRequest) error {
	if err := s.requireSystemd(context.Background()); err != nil {
		return err
	}
	rendered, err := s.Render(request)
	if err != nil {
		return err
	}
	if err := verifyInstalledUnits(rendered); err != nil {
		return err
	}
	return s.verifyEffectiveUnits(context.Background(), rendered)
}

// Enable idempotently enables and starts the exact workspace timer after
// checking that the installed unit bytes still match the requested CLI,
// workspace, user, and schedule.
func (s *Scheduler) Enable(ctx context.Context, request RenderRequest) error {
	if err := s.requireSystemd(ctx); err != nil {
		return err
	}
	rendered, err := s.Render(request)
	if err != nil {
		return err
	}
	if err := verifyInstalledUnits(rendered); err != nil {
		return err
	}
	if err := s.verifyEffectiveUnits(ctx, rendered); err != nil {
		return err
	}
	wasActive, statusErr := s.statusForUnits(ctx, rendered.Names)
	if statusErr != nil {
		return statusErr
	}
	if _, err := s.run(ctx, "systemctl", "enable", "--now", rendered.Names.Timer); err != nil {
		return fmt.Errorf("enable local backup timer: %w", err)
	}
	if wasActive.Active {
		if _, err := s.run(ctx, "systemctl", "restart", rendered.Names.Timer); err != nil {
			return fmt.Errorf("refresh local backup timer cadence: %w", err)
		}
	}
	status, err := s.statusForUnits(ctx, rendered.Names)
	if err != nil {
		return err
	}
	if !status.Enabled || !status.Active {
		return errors.New("local backup timer did not become enabled and active")
	}
	return nil
}

const systemdEffectiveProperties = "FragmentPath,DropInPaths,NeedDaemonReload,LoadState"

func (s *Scheduler) verifyEffectiveUnits(ctx context.Context, rendered RenderedUnits) error {
	for _, target := range []struct {
		name string
		path string
	}{
		{rendered.Names.Service, rendered.ServicePath},
		{rendered.Names.Timer, rendered.TimerPath},
	} {
		output, err := s.run(ctx, "systemctl", "show", "--property="+systemdEffectiveProperties, target.name)
		if err != nil {
			return fmt.Errorf("inspect effective systemd unit: %w", err)
		}
		properties, err := parseSystemdProperties(output)
		if err != nil {
			return err
		}
		fragment, ok := properties["FragmentPath"]
		if !ok || !sameAbsolutePath(fragment, target.path) {
			return errors.New("effective systemd unit fragment differs from the owned unit")
		}
		if dropIns := strings.TrimSpace(properties["DropInPaths"]); dropIns != "" {
			return errors.New("effective systemd unit has unsupported drop-in overrides")
		}
		if strings.TrimSpace(properties["NeedDaemonReload"]) != "no" {
			return errors.New("effective systemd unit still requires daemon reload")
		}
		if strings.TrimSpace(properties["LoadState"]) != "loaded" {
			return errors.New("effective systemd unit is not successfully loaded")
		}
	}
	return nil
}

func parseSystemdProperties(output []byte) (map[string]string, error) {
	properties := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.ContainsAny(key, " \t\r\n") {
			return nil, errors.New("systemd effective unit properties are malformed")
		}
		if _, exists := properties[key]; exists {
			return nil, errors.New("systemd effective unit properties contain duplicates")
		}
		properties[key] = value
	}
	for _, key := range []string{"FragmentPath", "DropInPaths", "NeedDaemonReload", "LoadState"} {
		if _, ok := properties[key]; !ok {
			return nil, errors.New("systemd effective unit properties are incomplete")
		}
	}
	return properties, nil
}

func sameAbsolutePath(left, right string) bool {
	leftPath, err := absolutePath(strings.TrimSpace(left), "systemd fragment path")
	if err != nil {
		return false
	}
	rightPath, err := absolutePath(right, "owned systemd fragment path")
	if err != nil {
		return false
	}
	return filepath.Clean(leftPath) == filepath.Clean(rightPath)
}

// Disable idempotently disables and stops the workspace timer. Unit files are
// retained for an explicit re-enable or a later owner-authorized replacement.
func (s *Scheduler) Disable(ctx context.Context, workspace string) error {
	if err := s.requireSystemd(ctx); err != nil {
		return err
	}
	names, servicePath, timerPath, err := s.unitPaths(workspace)
	if err != nil {
		return err
	}
	if err := verifyOwnedUnitFiles(workspace, names, servicePath, timerPath); err != nil {
		return err
	}
	if _, err := s.run(ctx, "systemctl", "disable", "--now", names.Timer); err != nil {
		status, statusErr := s.statusForUnits(ctx, names)
		if statusErr == nil && !status.Enabled && !status.Active {
			return nil
		}
		return fmt.Errorf("disable local backup timer: %w", err)
	}
	status, err := s.statusForUnits(ctx, names)
	if err != nil {
		return err
	}
	if status.Enabled || status.Active {
		return errors.New("local backup timer remained enabled or active after disable")
	}
	return nil
}

// Status reads the exact workspace timer without enabling, disabling, or
// changing any unit.
func (s *Scheduler) Status(ctx context.Context, workspace string) (UnitStatus, error) {
	if err := s.requireSystemd(ctx); err != nil {
		return UnitStatus{}, err
	}
	names, servicePath, timerPath, err := s.unitPaths(workspace)
	if err != nil {
		return UnitStatus{}, err
	}
	if err := verifyOwnedUnitFiles(workspace, names, servicePath, timerPath); err != nil {
		return UnitStatus{}, err
	}
	return s.statusForUnits(ctx, names)
}

func (s *Scheduler) statusForUnits(ctx context.Context, names UnitNames) (UnitStatus, error) {
	enabledOutput, enabledErr := s.run(ctx, "systemctl", "is-enabled", names.Timer)
	activeOutput, activeErr := s.run(ctx, "systemctl", "is-active", names.Timer)
	enabledState := normalizeUnitState(enabledOutput)
	activeState := normalizeUnitState(activeOutput)
	status := UnitStatus{
		Names: names, EnabledState: enabledState, ActiveState: activeState,
		Enabled: enabledState == "enabled" || enabledState == "enabled-runtime",
		Active:  activeState == "active",
	}
	if enabledState == "unknown" {
		return status, errors.Join(errors.New("local backup timer enabled state is unverified"), enabledErr)
	}
	if activeState == "unknown" {
		return status, errors.Join(errors.New("local backup timer active state is unverified"), activeErr)
	}
	return status, nil
}

func normalizeUnitState(output []byte) string {
	fields := strings.Fields(string(output))
	if len(fields) != 1 {
		return "unknown"
	}
	value := strings.ToLower(fields[0])
	for _, allowed := range []string{
		"enabled", "enabled-runtime", "disabled", "static", "indirect", "masked", "generated", "transient", "not-found",
		"active", "inactive", "failed", "activating", "deactivating", "maintenance", "unknown",
	} {
		if value == allowed {
			return value
		}
	}
	return "unknown"
}

func verifyInstalledUnits(rendered RenderedUnits) error {
	service, err := readUnit(rendered.ServicePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrUnitNotInstalled
		}
		return err
	}
	timer, err := readUnit(rendered.TimerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrUnitNotInstalled
		}
		return err
	}
	if !hasUnitOwnershipMarker(service, unitOwnershipMarkerFor(rendered, rendered.Names.Service)) ||
		!hasUnitOwnershipMarker(timer, unitOwnershipMarkerFor(rendered, rendered.Names.Timer)) {
		return errors.New("installed local backup units are not owned by this workspace scheduler")
	}
	if !bytes.Equal(service, rendered.ServiceBytes) || !bytes.Equal(timer, rendered.TimerBytes) {
		return errors.New("installed local backup units differ from the requested workspace authority")
	}
	return nil
}

func ensureUnitDirectory(directory string) error {
	directory, err := absolutePath(directory, "systemd unit directory")
	if err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect systemd unit directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("systemd unit directory must be an existing non-symlink directory")
	}
	return nil
}

func verifyInstallTargets(rendered RenderedUnits) error {
	for _, target := range []struct {
		path   string
		bytes  []byte
		marker []byte
	}{
		{rendered.ServicePath, rendered.ServiceBytes, unitOwnershipMarkerFor(rendered, rendered.Names.Service)},
		{rendered.TimerPath, rendered.TimerBytes, unitOwnershipMarkerFor(rendered, rendered.Names.Timer)},
	} {
		existing, err := readUnit(target.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if bytes.Equal(existing, target.bytes) {
			continue
		}
		if !hasUnitOwnershipMarker(existing, target.marker) {
			return errors.New("refusing to replace a foreign systemd unit")
		}
	}
	return nil
}

func verifyOwnedUnitFiles(workspace string, names UnitNames, servicePath, timerPath string) error {
	for _, target := range []struct {
		path string
		name string
	}{
		{servicePath, names.Service},
		{timerPath, names.Timer},
	} {
		content, err := readUnit(target.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		marker, err := unitOwnershipMarker(workspace, target.name)
		if err != nil {
			return err
		}
		if !hasUnitOwnershipMarker(content, []byte(marker)) {
			return errors.New("refusing to control a foreign systemd unit")
		}
	}
	return nil
}

func writeUnitIfChanged(path string, content, marker []byte) (bool, error) {
	if existing, err := readUnit(path); err == nil {
		if bytes.Equal(existing, content) {
			return false, nil
		}
		if !hasUnitOwnershipMarker(existing, marker) {
			return false, errors.New("refusing to replace a foreign systemd unit")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := writeUnitAtomic(path, content); err != nil {
		return false, err
	}
	return true, nil
}

func readUnit(path string) ([]byte, error) {
	directory, err := absolutePath(filepath.Dir(path), "systemd unit directory")
	if err != nil {
		return nil, err
	}
	if err := ensureUnitDirectory(directory); err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("open confined systemd unit directory: %w", err)
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer transaction.Close()
	content, _, err := transaction.ReadStable(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if err := transaction.VerifyPathIdentity(); err != nil {
		return nil, err
	}
	return content, nil
}

func snapshotUnit(path string) ([]byte, bool, error) {
	content, err := readUnit(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return content, true, nil
}

func rollbackInstalledUnits(rendered RenderedUnits, service []byte, serviceExisted bool, timer []byte, timerExisted bool) error {
	var firstErr error
	if err := rollbackUnit(rendered.ServicePath, service, serviceExisted, rendered.ServiceBytes); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := rollbackUnit(rendered.TimerPath, timer, timerExisted, rendered.TimerBytes); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func rollbackUnit(path string, previous []byte, existed bool, installed []byte) error {
	current, err := readUnit(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !existed {
			return nil
		}
		return err
	}
	if !bytes.Equal(current, installed) {
		return errors.New("refusing to roll back a systemd unit changed by another writer")
	}
	if !existed {
		if err := removeUnitIfContent(path, installed); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove newly installed systemd unit: %w", err)
		}
		return nil
	}
	return writeUnitAtomic(path, previous)
}

func writeUnitAtomic(path string, content []byte) error {
	directory, err := absolutePath(filepath.Dir(path), "systemd unit directory")
	if err != nil {
		return err
	}
	if err := ensureUnitDirectory(directory); err != nil {
		return err
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return fmt.Errorf("open confined systemd unit directory: %w", err)
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	// PID 1 reads these units as root. Reuse the common atomic writer; the
	// lifecycle lock serializes StackKits writers for this workspace pair.
	result, err := view.WriteAtomic0600(filepath.Base(path), content)
	if err != nil {
		return fmt.Errorf("install systemd unit: %w", err)
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("systemd unit was not durably installed")
	}
	return nil
}

func removeUnitIfContent(path string, expected []byte) error {
	directory, err := absolutePath(filepath.Dir(path), "systemd unit directory")
	if err != nil {
		return err
	}
	if err := ensureUnitDirectory(directory); err != nil {
		return err
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return fmt.Errorf("open confined systemd unit directory: %w", err)
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer transaction.Close()
	name := filepath.Base(path)
	content, _, err := transaction.ReadStable(name)
	if err != nil {
		return err
	}
	if !bytes.Equal(content, expected) {
		return errors.New("refusing to remove a systemd unit changed by another writer")
	}
	if err := transaction.VerifyPathIdentity(); err != nil {
		return err
	}
	return transaction.RemoveTree(name)
}

func absolutePath(value, label string) (string, error) {
	if strings.TrimSpace(value) == "" || !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("%s must be an absolute path", label)
	}
	clean := filepath.Clean(value)
	if clean == "." || !filepath.IsAbs(clean) {
		return "", fmt.Errorf("%s must be an absolute path", label)
	}
	return clean, nil
}

func existingDirectory(value, label string) (string, error) {
	clean, err := absolutePath(value, label)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s must be an existing non-symlink directory", label)
	}
	return clean, nil
}

// Scalar directory directives resolve specifiers but do not parse shell-style
// quotes or backslash escapes. Keep their encoding separate from ExecStart.
func systemdPathValue(value, label string) (string, error) {
	clean, err := absolutePath(value, label)
	if err != nil {
		return "", err
	}
	value = filepath.ToSlash(clean)
	if strings.TrimSpace(value) != value || strings.Contains(value, `\`) ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s cannot be represented by a systemd directory directive", label)
	}
	return strings.ReplaceAll(value, `%`, `%%`), nil
}

func systemdPathArg(value, label string) (string, error) {
	clean, err := absolutePath(value, label)
	if err != nil {
		return "", err
	}
	value = filepath.ToSlash(clean)
	if strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r)
	}) >= 0 {
		return "", fmt.Errorf("%s contains a control character", label)
	}
	if !strings.ContainsAny(value, " \t\"\\%$") {
		return value, nil
	}
	value = strings.ReplaceAll(value, `%`, `%%`)
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`, nil
}

func validateProcessUID(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("system service requires a numeric effective UID")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return errors.New("system service requires a canonical numeric effective UID")
	}
	return nil
}

func currentProcessUID() (string, error) {
	uid := os.Geteuid()
	if uid < 0 {
		return "", errors.New("current process has no local effective UID")
	}
	return strconv.Itoa(uid), nil
}

func existingFile(value, label string) (string, error) {
	clean, err := absolutePath(value, label)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s must be an existing non-symlink file", label)
	}
	return clean, nil
}

func unitOwnershipMarker(workspace, unitName string) (string, error) {
	canonical, err := absolutePath(workspace, "workspace")
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(filepath.ToSlash(canonical)))
	return fmt.Sprintf("# Managed-By=%s\n# Workspace-Digest=sha256:%s\n# Unit-Name=%s\n", unitOwnershipVersion, hex.EncodeToString(digest[:]), unitName), nil
}

func unitOwnershipMarkerFor(rendered RenderedUnits, unitName string) []byte {
	marker, err := unitOwnershipMarker(rendered.WorkspacePath, unitName)
	if err != nil {
		return nil
	}
	return []byte(marker)
}

func hasUnitOwnershipMarker(content, marker []byte) bool {
	return len(marker) > 0 && bytes.HasPrefix(content, marker)
}
