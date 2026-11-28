package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// WatchdogClockAnchorRelPath stores the monotonic-vs-wall clock anchor used
	// to detect sudden wall-clock jumps even when NTP is disabled or offline.
	WatchdogClockAnchorRelPath = ".stackkit/runtime/watchdog-clock.json"
)

type clockAnchor struct {
	SchemaVersion string  `json:"schemaVersion"`
	WallUnixNano  int64   `json:"wallUnixNano"`
	UptimeSeconds float64 `json:"uptimeSeconds"`
}

// WatchdogDNSReport summarizes the live DNS/AdGuard posture for one watchdog
// cycle (ADR-0051).
type WatchdogDNSReport struct {
	Status            string   `json:"status"` // healthy, fallback, local-only, degraded
	FallbackResolvers []string `json:"fallbackResolvers"`
	OfflineLocalOnly  bool     `json:"offlineLocalOnly"`
	LocalDNSRunning   bool     `json:"localDnsRunning"`
	Detail            string   `json:"detail"`
}

// WatchdogRepairOutcome records one reversible repair attempt (or refusal)
// during a watchdog cycle.
type WatchdogRepairOutcome struct {
	Effect string `json:"effect"`
	Target string `json:"target,omitempty"`
	Status string `json:"status"` // executed, refused, failed
	Reason string `json:"reason,omitempty"`
}

// WatchdogRunOptions configures one host-side watchdog cycle.
type WatchdogRunOptions struct {
	Workspace        string
	Offline          bool
	LocalLANFallback string
	Now              time.Time
}

// WatchdogRunReport is the structured payload emitted by `stackkit watchdog --once --json`.
type WatchdogRunReport struct {
	SchemaVersion    string                    `json:"schemaVersion"`
	SnapshotPath     string                    `json:"snapshotPath"`
	DNS              WatchdogDNSReport         `json:"dns"`
	DNSPlan          WatchdogDNSPlan           `json:"dnsPlan"`
	Stopped          bool                      `json:"stopped"`
	StopReason       string                    `json:"stopReason,omitempty"`
	HardStop         bool                      `json:"hardStop"`
	HardStops        []string                  `json:"hardStops,omitempty"`
	Repairs          []WatchdogRepairOutcome   `json:"repairs"`
	Incident         *RedactedIncidentEnvelope `json:"incident,omitempty"`
	IncidentEnvelope *RedactedIncidentEnvelope `json:"incidentEnvelope,omitempty"`
}

// EnsureClockAnchor records an initial monotonic-vs-wall clock anchor when none
// exists yet (or after a host reboot), without clobbering an anchor while a
// wall-clock jump is in progress.
func EnsureClockAnchor(workspace string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	uptime, err := readLinuxUptimeSeconds()
	if err != nil {
		return nil
	}
	anchorPath := filepath.Join(workspace, filepath.FromSlash(WatchdogClockAnchorRelPath))
	if existing, ok := readClockAnchor(anchorPath); ok && uptime >= existing.UptimeSeconds {
		return nil
	}
	return writeClockAnchor(anchorPath, clockAnchor{
		SchemaVersion: "stackkit.watchdog-clock-anchor/v1",
		WallUnixNano:  now.UTC().UnixNano(),
		UptimeSeconds: uptime,
	})
}

// DetectHostClockSkew compares wall clock progression against Linux monotonic
// `/proc/uptime` progression since the saved workspace anchor. This reliably
// detects sudden wall-clock jumps (`date -s`) even when NTP is disabled or the
// host is offline.
func DetectHostClockSkew(workspace string, now time.Time) time.Duration {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	uptime, err := readLinuxUptimeSeconds()
	if err != nil {
		return 0
	}
	anchorPath := filepath.Join(workspace, filepath.FromSlash(WatchdogClockAnchorRelPath))
	existing, ok := readClockAnchor(anchorPath)
	if !ok || uptime < existing.UptimeSeconds {
		_ = writeClockAnchor(anchorPath, clockAnchor{
			SchemaVersion: "stackkit.watchdog-clock-anchor/v1",
			WallUnixNano:  now.UTC().UnixNano(),
			UptimeSeconds: uptime,
		})
		return 0
	}
	elapsedMono := time.Duration((uptime - existing.UptimeSeconds) * float64(time.Second))
	expectedWall := time.Unix(0, existing.WallUnixNano).UTC().Add(elapsedMono)
	skew := now.UTC().Sub(expectedWall)
	absSkew := skew
	if absSkew < 0 {
		absSkew = -absSkew
	}
	// Never overwrite the anchor while clock skew exceeds the safety threshold.
	if absSkew <= 5*time.Second {
		_ = writeClockAnchor(anchorPath, clockAnchor{
			SchemaVersion: "stackkit.watchdog-clock-anchor/v1",
			WallUnixNano:  now.UTC().UnixNano(),
			UptimeSeconds: uptime,
		})
	}
	return skew
}

func readLinuxUptimeSeconds() (float64, error) {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, errors.New("empty /proc/uptime")
	}
	return strconv.ParseFloat(fields[0], 64)
}

func readClockAnchor(path string) (clockAnchor, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return clockAnchor{}, false
	}
	var anchor clockAnchor
	if err := json.Unmarshal(raw, &anchor); err != nil || anchor.WallUnixNano == 0 {
		return clockAnchor{}, false
	}
	return anchor, true
}

func writeClockAnchor(path string, anchor clockAnchor) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(anchor, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// RunWatchdogCycle executes one complete host-side ADR-0051 watchdog cycle:
// clock/disk/custody hard-stop evaluation, redacted snapshot refresh, live
// DNS resilience probe, Coolify proxy drift reconciliation, and circuit-broken
// workload restart.
func RunWatchdogCycle(ctx context.Context, opts WatchdogRunOptions) (WatchdogRunReport, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	wd := opts.Workspace
	skew := DetectHostClockSkew(wd, now)
	snap, err := CollectHostSnapshot(wd, skew, now)
	if err != nil {
		return WatchdogRunReport{}, err
	}
	snapPath := filepath.Join(wd, filepath.FromSlash(DefaultSnapshotRelPath))
	if err := WriteSnapshot(snapPath, snap); err != nil {
		return WatchdogRunReport{}, err
	}

	dnsReport, dnsPlan := ProbeWatchdogDNS(ctx, opts.Offline, opts.LocalLANFallback)
	report := WatchdogRunReport{
		SchemaVersion: "stackkit.watchdog-run/v1",
		SnapshotPath:  snapPath,
		DNS:           dnsReport,
		DNSPlan:       dnsPlan,
		HardStop:      len(snap.HardStopReasons) > 0,
		HardStops:     snap.HardStopReasons,
		Repairs:       []WatchdogRepairOutcome{},
	}

	// Hard-stop invariants take strict precedence over every repair action.
	switch {
	case snap.DiskRAMClock.DiskBelowFloor:
		report.Stopped = true
		report.StopReason = "disk_floor"
		env := snap.BuildIncidentEnvelope(now)
		report.Incident = &env
		report.IncidentEnvelope = &env
		return report, nil
	case snap.DiskRAMClock.ClockSkewBad:
		report.Stopped = true
		report.StopReason = "clock_skew"
		env := snap.BuildIncidentEnvelope(now)
		report.Incident = &env
		report.IncidentEnvelope = &env
		return report, nil
	case !snap.BackupAge.CustodyHealthy:
		report.Stopped = true
		report.StopReason = "custody_missing"
		env := snap.BuildIncidentEnvelope(now)
		report.Incident = &env
		report.IncidentEnvelope = &env
		return report, nil
	}

	policy, err := LoadRepairPolicy(wd)
	if err != nil {
		return WatchdogRunReport{}, err
	}

	// 1. Reconcile drifting Coolify proxy if running (ADR-0054 Option A).
	if outcome, drifted := reconcileCoolifyProxyWithPolicy(ctx, policy); drifted {
		report.Repairs = append(report.Repairs, outcome)
	}

	// 2. Restart stopped non-core StackKit app containers under the rolling
	//    2/signature/hour circuit breaker.
	appOutcomes, breakerTripped := repairStoppedWorkloadsLive(ctx, wd, policy, now)
	report.Repairs = append(report.Repairs, appOutcomes...)
	if breakerTripped {
		snap.HardStopReasons = append(snap.HardStopReasons, "circuit-breaker-tripped")
		snap.Signatures = append(snap.Signatures, SignatureMatch{
			ID:          "circuit-breaker-tripped",
			BlastRadius: BlastRadiusHost,
			HardStop:    true,
			Detail:      "auto-repair circuit breaker tripped after 2 repairs within 1 hour",
		})
		env := snap.BuildIncidentEnvelope(now)
		report.Incident = &env
		report.IncidentEnvelope = &env
	}

	return report, nil
}

// ProbeWatchdogDNS observes the real local DNS container and outbound public
// DNS reachability, enforcing ADR-0051's rule that Offline/Local-Only nodes
// never configure public fallback resolvers.
func ProbeWatchdogDNS(ctx context.Context, offlineFlag bool, localLANFallback string) (WatchdogDNSReport, WatchdogDNSPlan) {
	localRunning := isLocalDNSContainerRunning(ctx)
	isOffline := offlineFlag || !isPublicDNSReachable(ctx)
	plan := ResolveWatchdogDNS(isOffline, localLANFallback)

	fallbacks := make([]string, len(plan.FallbackDNS))
	copy(fallbacks, plan.FallbackDNS)

	status := "healthy"
	if isOffline {
		if len(fallbacks) > 0 {
			status = "local-only"
		} else {
			status = "degraded"
		}
	} else if !localRunning {
		status = "fallback"
	}

	return WatchdogDNSReport{
		Status:            status,
		FallbackResolvers: fallbacks,
		OfflineLocalOnly:  plan.OfflineLocalOnly,
		LocalDNSRunning:   localRunning,
		Detail:            plan.Detail,
	}, plan
}

func isLocalDNSContainerRunning(ctx context.Context) bool {
	rows, err := listDockerContainers(ctx, false)
	if err != nil {
		return false
	}
	for _, row := range rows {
		lower := strings.ToLower(row.Name)
		if strings.Contains(lower, "adguard") || strings.Contains(lower, "lan-dns") {
			return true
		}
	}
	return false
}

func isPublicDNSReachable(ctx context.Context) bool {
	resolvers := []string{"9.9.9.9:53", "1.1.1.1:53"}
	dialer := net.Dialer{Timeout: 1500 * time.Millisecond}
	for _, addr := range resolvers {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if probeUDPDNS(ctx, addr) {
			return true
		}
	}
	return false
}

func probeUDPDNS(ctx context.Context, addr string) bool {
	dialer := net.Dialer{Timeout: 1500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(1500 * time.Millisecond))
	// Minimal RFC 1035 DNS query for root NS (id=0x4b53, RD=1, QNAME=".", QTYPE=NS, QCLASS=IN).
	query := []byte{
		0x4b, 0x53, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x02, 0x00, 0x01,
	}
	if _, err := conn.Write(query); err != nil {
		return false
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	return err == nil && n >= 12
}

// ReconcileCoolifyProxyLive removes any running, stopped, or created
// `coolify-proxy` container that drifted onto the host, returning the affected
// container names.
func ReconcileCoolifyProxyLive(ctx context.Context) ([]string, error) {
	rows, err := listDockerContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, row := range rows {
		if !strings.Contains(strings.ToLower(row.Name), "coolify-proxy") {
			continue
		}
		if rmErr := runDockerCommand(ctx, "rm", "-f", row.ID); rmErr != nil {
			return removed, fmt.Errorf("remove drifting coolify-proxy %s: %w", row.Name, rmErr)
		}
		removed = append(removed, row.Name)
	}
	return removed, nil
}

func reconcileCoolifyProxyWithPolicy(ctx context.Context, policy RepairPolicy) (WatchdogRepairOutcome, bool) {
	rows, err := listDockerContainers(ctx, true)
	if err != nil {
		return WatchdogRepairOutcome{}, false
	}
	var drifting []dockerContainerRow
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.Name), "coolify-proxy") {
			drifting = append(drifting, row)
		}
	}
	if len(drifting) == 0 {
		return WatchdogRepairOutcome{}, false
	}
	if _, authErr := EvaluateRepairAuthorization(RepairReconcileCoolifyProxy, policy, false); authErr != nil {
		return WatchdogRepairOutcome{
			Effect: string(RepairReconcileCoolifyProxy),
			Target: drifting[0].Name,
			Status: "refused",
			Reason: "approval_required",
		}, true
	}
	for _, row := range drifting {
		if err := runDockerCommand(ctx, "rm", "-f", row.ID); err != nil {
			return WatchdogRepairOutcome{
				Effect: string(RepairReconcileCoolifyProxy),
				Target: row.Name,
				Status: "failed",
				Reason: RedactSecrets(err.Error()),
			}, true
		}
	}
	return WatchdogRepairOutcome{
		Effect: string(RepairReconcileCoolifyProxy),
		Target: drifting[0].Name,
		Status: "executed",
		Reason: "removed drifting coolify-proxy to preserve Kit-Traefik port ownership",
	}, true
}

func repairStoppedWorkloadsLive(ctx context.Context, workspace string, policy RepairPolicy, now time.Time) ([]WatchdogRepairOutcome, bool) {
	rows, err := listDockerContainers(ctx, true)
	if err != nil {
		return nil, false
	}
	breaker := OpenCircuitBreaker(workspace)
	breaker.Now = func() time.Time { return now }

	var (
		outcomes       []WatchdogRepairOutcome
		breakerTripped bool
	)
	for _, row := range rows {
		if !strings.HasPrefix(row.Project, "stackkit") {
			continue
		}
		if strings.ToLower(row.State) != "exited" {
			continue
		}
		if isProtectedFromGenericAppRestart(row.Name) {
			continue
		}
		if _, authErr := EvaluateRepairAuthorization(RepairRestartService, policy, false); authErr != nil {
			outcomes = append(outcomes, WatchdogRepairOutcome{
				Effect: string(RepairRestartService),
				Target: row.Name,
				Status: "refused",
				Reason: "approval_required",
			})
			continue
		}
		sig := "container-stopped:" + row.Name
		if _, cbErr := breaker.CheckAndRecord(sig); cbErr != nil {
			reason := "circuit_breaker_tripped"
			if !errors.Is(cbErr, ErrCircuitBreakerTripped) {
				reason = RedactSecrets(cbErr.Error())
			} else {
				breakerTripped = true
			}
			outcomes = append(outcomes, WatchdogRepairOutcome{
				Effect: string(RepairRestartService),
				Target: row.Name,
				Status: "refused",
				Reason: reason,
			})
			continue
		}
		if startErr := runDockerCommand(ctx, "start", row.ID); startErr != nil {
			outcomes = append(outcomes, WatchdogRepairOutcome{
				Effect: string(RepairRestartService),
				Target: row.Name,
				Status: "failed",
				Reason: RedactSecrets(startErr.Error()),
			})
			continue
		}
		outcomes = append(outcomes, WatchdogRepairOutcome{
			Effect: string(RepairRestartService),
			Target: row.Name,
			Status: "executed",
			Reason: "restarted stopped workload container within circuit-breaker limit",
		})
	}
	return outcomes, breakerTripped
}

func isProtectedFromGenericAppRestart(name string) bool {
	lower := strings.ToLower(name)
	for _, token := range []string{
		"traefik",
		"core-router",
		"pocket",
		"adguard",
		"lan-dns",
		"server",
		"coolify",
		"cookie-scrubber",
		"step-ca",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

type dockerContainerRow struct {
	ID      string
	Name    string
	State   string
	Project string
}

func listDockerContainers(ctx context.Context, includeStopped bool) ([]dockerContainerRow, error) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return nil, err
	}
	args := []string{"ps", "--no-trunc", "--format", `{{.ID}}	{{.Names}}	{{.State}}	{{.Label "com.docker.compose.project"}}`}
	if includeStopped {
		args = append([]string{"ps", "--all"}, args[1:]...)
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, dockerPath, args...).Output()
	if err != nil {
		return nil, err
	}
	var rows []dockerContainerRow
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		row := dockerContainerRow{
			ID:    strings.TrimSpace(parts[0]),
			Name:  strings.TrimSpace(parts[1]),
			State: strings.TrimSpace(parts[2]),
		}
		if len(parts) >= 4 {
			row.Project = strings.TrimSpace(parts[3])
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func runDockerCommand(ctx context.Context, args ...string) error {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, dockerPath, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
