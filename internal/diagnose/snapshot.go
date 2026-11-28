package diagnose

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed playbooks.cue
var EmbeddedPlaybooksCUE string

const (
	// SnapshotSchemaVersion versions the secret-free host observation snapshot
	// mounted read-only into stackkit-server (ADR-0051).
	SnapshotSchemaVersion = "stackkit.observation-snapshot/v1"
	// DefaultSnapshotRelPath is the workspace-relative secret-free snapshot file.
	DefaultSnapshotRelPath = ".stackkit/runtime/observation-snapshot.json"
	// DefaultDiskSafeFloorBytes stops automation when free disk drops below 2 GiB.
	DefaultDiskSafeFloorBytes uint64 = 2 * 1024 * 1024 * 1024
	// DefaultMaxClockSkew bounds acceptable clock skew before hard-stopping.
	DefaultMaxClockSkew = 60 * time.Second
	// DefaultMaxSnapshotAge marks snapshots older than 15 minutes as stale.
	DefaultMaxSnapshotAge = 15 * time.Minute
)

// BlastRadius identifies the isolated failure domain (ADR-0051 / §6.1).
type BlastRadius string

const (
	BlastRadiusCore      BlastRadius = "core"
	BlastRadiusApp       BlastRadius = "app"
	BlastRadiusExtension BlastRadius = "extension"
	BlastRadiusHost      BlastRadius = "host"
)

// ComponentHealth is a secret-free status summary for one core, app, or
// extension unit.
type ComponentHealth struct {
	Name        string      `json:"name"`
	BlastRadius BlastRadius `json:"blastRadius"`
	Status      string      `json:"status"` // healthy, degraded, bootstrap_degraded, stopped, unknown
	Detail      string      `json:"detail,omitempty"`
}

// LiveDriftItem is a secret-free runtime drift observation.
type LiveDriftItem struct {
	Root    string `json:"root"`
	Service string `json:"service,omitempty"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
}

// TLSStatus summarizes certificate health without any private key material.
type TLSStatus struct {
	Healthy       bool       `json:"healthy"`
	Mode          string     `json:"mode"` // acme-public, owner-ca, unknown
	EarliestExpAt *time.Time `json:"earliestExpiresAt,omitempty"`
	Detail        string     `json:"detail,omitempty"`
}

// HostResources captures disk, memory, and clock-skew posture.
type HostResources struct {
	DiskFreeBytes  uint64 `json:"diskFreeBytes"`
	DiskTotalBytes uint64 `json:"diskTotalBytes"`
	DiskSafeFloor  uint64 `json:"diskSafeFloorBytes"`
	DiskBelowFloor bool   `json:"diskBelowFloor"`
	ClockSkewSec   int64  `json:"clockSkewSeconds"`
	ClockSkewBad   bool   `json:"clockSkewExceeded"`
}

// BackupStatus captures Kopia backup recency without credentials or repo keys.
type BackupStatus struct {
	LastVerifiedAt *time.Time `json:"lastVerifiedAt,omitempty"`
	AgeSeconds     int64      `json:"ageSeconds,omitempty"`
	CustodyHealthy bool       `json:"custodyHealthy"`
	Detail         string     `json:"detail,omitempty"`
}

// RedactedLogTail holds secret-scrubbed recent log lines for one service
// (including Coolify's kombify-agent-apps, replacing Coolify get_logs).
type RedactedLogTail struct {
	Project string   `json:"project"`
	Service string   `json:"service"`
	Lines   []string `json:"lines"`
}

// SignatureMatch binds a detected runtime condition to a playbook signature ID.
type SignatureMatch struct {
	ID          string      `json:"id"`
	BlastRadius BlastRadius `json:"blastRadius"`
	HardStop    bool        `json:"hardStop"`
	Target      string      `json:"target,omitempty"`
	Detail      string      `json:"detail"`
}

// ObservationSnapshot is the single host-side secret-free JSON snapshot read by
// stackkit-server, CLI `diagnose --json`, and `stackkit-watchdog` (ADR-0051).
// Compromise of stackkit-server can read only this redacted file — never .env
// files, custody keys, or Docker container Env.
type ObservationSnapshot struct {
	SchemaVersion    string            `json:"schemaVersion"`
	GeneratedAt      time.Time         `json:"generatedAt"`
	WorkspaceHash    string            `json:"workspaceHash"`
	CoreHealth       ComponentHealth   `json:"coreHealth"`
	AppHealth        []ComponentHealth `json:"appHealth"`
	ExtHealth        []ComponentHealth `json:"extHealth,omitempty"`
	LiveDrift        []LiveDriftItem   `json:"liveDrift,omitempty"`
	TLSStatus        TLSStatus         `json:"tlsStatus"`
	DiskRAMClock     HostResources     `json:"diskRamClock"`
	BackupAge        BackupStatus      `json:"backupAge"`
	Signatures       []SignatureMatch  `json:"signatures,omitempty"`
	BlastRadius      BlastRadius       `json:"blastRadius"`
	HardStopReasons  []string          `json:"hardStopReasons,omitempty"`
	AgentAppLogTails []RedactedLogTail `json:"agentAppLogTails,omitempty"`
}

// RedactedIncidentEnvelope is emitted over /mcp or State Console whenever a
// hard-stop condition or tripped circuit breaker halts automation (ADR-0051).
type RedactedIncidentEnvelope struct {
	SchemaVersion   string           `json:"schemaVersion"`
	EmittedAt       time.Time        `json:"emittedAt"`
	WorkspaceHash   string           `json:"workspaceHash"`
	BlastRadius     BlastRadius      `json:"blastRadius"`
	HardStopReasons []string         `json:"hardStopReasons"`
	Signatures      []SignatureMatch `json:"signatures"`
	Guidance        []string         `json:"guidance"`
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bskmcp_[A-Za-z0-9._-]+\b`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api_key|apikey|app_key|private_key|access_key|client_secret|db_pass)[A-Za-z0-9_-]*\s*[:=]\s*)([^\s"',;]+)`),
	regexp.MustCompile(`(?i)(postgres(?:ql)?|mysql|redis|amqp|https?)://([^:\s/@]+):([^@\s/]+)@`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
}

// RedactSecrets scrubs credentials, tokens, PEM private keys, and URL passwords
// from any diagnostic detail or container log tail before writing the snapshot.
func RedactSecrets(input string) string {
	out := input
	out = secretPatterns[4].ReplaceAllString(out, "[REDACTED_PRIVATE_KEY]")
	out = secretPatterns[0].ReplaceAllString(out, "[REDACTED_MCP_TOKEN]")
	out = secretPatterns[1].ReplaceAllString(out, "${1}[REDACTED_TOKEN]")
	out = secretPatterns[2].ReplaceAllString(out, "${1}[REDACTED]")
	out = secretPatterns[3].ReplaceAllString(out, "${1}://${2}:[REDACTED]@")
	return out
}

// RedactLogLines returns a redacted copy of log lines bounded to maxLines.
func RedactLogLines(lines []string, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = 100
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = RedactSecrets(line)
	}
	return out
}

// CollectHostSnapshot gathers secret-free local host observations for workspace
// without reading any .env values, custody secrets, or container Env.
func CollectHostSnapshot(workspace string, clockSkew time.Duration, now time.Time) (ObservationSnapshot, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return ObservationSnapshot{}, err
	}
	sum := sha256.Sum256([]byte(absWorkspace))
	wsHash := hex.EncodeToString(sum[:8])

	diskFree, diskTotal := statDisk(absWorkspace)
	if varLibFree, varLibTotal := statDisk("/var/lib"); varLibTotal > 0 && (diskTotal == 0 || varLibFree < diskFree) {
		diskFree, diskTotal = varLibFree, varLibTotal
	}
	diskBelow := diskTotal > 0 && diskFree < DefaultDiskSafeFloorBytes
	skewAbs := clockSkew
	if skewAbs < 0 {
		skewAbs = -skewAbs
	}
	skewBad := skewAbs > DefaultMaxClockSkew

	custodyDir := filepath.Join(absWorkspace, ".stackkit", "custody")
	custodyHealthy := false
	custodyDetail := "custody directory absent"
	if info, err := os.Stat(custodyDir); err == nil && info.IsDir() {
		custodyHealthy = true
		custodyDetail = "custody directory present (contents never mounted into stackkit-server)"
	}

	snap := ObservationSnapshot{
		SchemaVersion: SnapshotSchemaVersion,
		GeneratedAt:   now.UTC(),
		WorkspaceHash: wsHash,
		CoreHealth: ComponentHealth{
			Name:        "core",
			BlastRadius: BlastRadiusCore,
			Status:      "healthy",
		},
		AppHealth: []ComponentHealth{},
		TLSStatus: TLSStatus{
			Healthy: true,
			Mode:    "unknown",
		},
		DiskRAMClock: HostResources{
			DiskFreeBytes:  diskFree,
			DiskTotalBytes: diskTotal,
			DiskSafeFloor:  DefaultDiskSafeFloorBytes,
			DiskBelowFloor: diskBelow,
			ClockSkewSec:   int64(clockSkew.Seconds()),
			ClockSkewBad:   skewBad,
		},
		BackupAge: BackupStatus{
			CustodyHealthy: custodyHealthy,
			Detail:         custodyDetail,
		},
		BlastRadius: BlastRadiusCore,
	}

	// Evaluate hard-stop host invariants.
	if diskBelow {
		snap.HardStopReasons = append(snap.HardStopReasons, "disk-full-floor")
		snap.Signatures = append(snap.Signatures, SignatureMatch{
			ID: "disk-full-floor", BlastRadius: BlastRadiusHost, HardStop: true,
			Detail: fmt.Sprintf("free disk %d bytes is below safe floor %d bytes", diskFree, DefaultDiskSafeFloorBytes),
		})
	}
	if skewBad {
		snap.HardStopReasons = append(snap.HardStopReasons, "clock-skew-exceeded")
		snap.Signatures = append(snap.Signatures, SignatureMatch{
			ID: "clock-skew-exceeded", BlastRadius: BlastRadiusHost, HardStop: true,
			Detail: fmt.Sprintf("host clock skew %s exceeds %s safety threshold", clockSkew, DefaultMaxClockSkew),
		})
	}
	if !custodyHealthy {
		snap.HardStopReasons = append(snap.HardStopReasons, "custody-missing-or-corrupt")
		snap.Signatures = append(snap.Signatures, SignatureMatch{
			ID: "custody-missing-or-corrupt", BlastRadius: BlastRadiusHost, HardStop: true,
			Detail: custodyDetail,
		})
	}
	if len(snap.HardStopReasons) > 0 {
		snap.BlastRadius = BlastRadiusHost
	}
	return snap, nil
}

// Sanitize enforces secret redaction across every human-readable field of the
// snapshot before serialization.
func (s *ObservationSnapshot) Sanitize() {
	s.CoreHealth.Detail = RedactSecrets(s.CoreHealth.Detail)
	for i := range s.AppHealth {
		s.AppHealth[i].Detail = RedactSecrets(s.AppHealth[i].Detail)
	}
	for i := range s.ExtHealth {
		s.ExtHealth[i].Detail = RedactSecrets(s.ExtHealth[i].Detail)
	}
	for i := range s.LiveDrift {
		s.LiveDrift[i].Detail = RedactSecrets(s.LiveDrift[i].Detail)
	}
	s.TLSStatus.Detail = RedactSecrets(s.TLSStatus.Detail)
	s.BackupAge.Detail = RedactSecrets(s.BackupAge.Detail)
	for i := range s.Signatures {
		s.Signatures[i].Detail = RedactSecrets(s.Signatures[i].Detail)
	}
	for i := range s.AgentAppLogTails {
		s.AgentAppLogTails[i].Lines = RedactLogLines(s.AgentAppLogTails[i].Lines, 100)
	}
}

// IsStale reports whether the snapshot exceeded maxAge (hard-stop condition).
func (s ObservationSnapshot) IsStale(now time.Time, maxAge time.Duration) bool {
	if maxAge <= 0 {
		maxAge = DefaultMaxSnapshotAge
	}
	if s.GeneratedAt.IsZero() {
		return true
	}
	return now.Sub(s.GeneratedAt) > maxAge
}

// BuildIncidentEnvelope produces a secret-free incident envelope when
// automation hard-stops.
func (s ObservationSnapshot) BuildIncidentEnvelope(now time.Time) RedactedIncidentEnvelope {
	reasons := append([]string(nil), s.HardStopReasons...)
	sort.Strings(reasons)
	guidance := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if pb, ok := BuiltinPlaybook(r); ok {
			guidance = append(guidance, pb.Guidance)
		}
	}
	return RedactedIncidentEnvelope{
		SchemaVersion:   "stackkit.incident-envelope/v1",
		EmittedAt:       now.UTC(),
		WorkspaceHash:   s.WorkspaceHash,
		BlastRadius:     s.BlastRadius,
		HardStopReasons: reasons,
		Signatures:      s.Signatures,
		Guidance:        guidance,
	}
}

// WriteSnapshot atomically writes the sanitized secret-free snapshot so
// stackkit-server can mount ONLY this file read-only.
func WriteSnapshot(path string, snap ObservationSnapshot) error {
	snap.Sanitize()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(snap, "", "  ")
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

// ReadSnapshot loads a previously written observation snapshot.
func ReadSnapshot(path string) (ObservationSnapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ObservationSnapshot{}, err
	}
	var snap ObservationSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return ObservationSnapshot{}, err
	}
	if snap.SchemaVersion != SnapshotSchemaVersion {
		return ObservationSnapshot{}, errors.New("unexpected observation snapshot schemaVersion")
	}
	return snap, nil
}

// PlaybookEntry mirrors #FailurePlaybook from playbooks.cue in Go for zero-dep
// runtime dispatch.
type PlaybookEntry struct {
	ID                    string      `json:"id"`
	BlastRadius           BlastRadius `json:"blastRadius"`
	Summary               string      `json:"summary"`
	AutoRepairEffect      string      `json:"autoRepairEffect"`
	OpenDefaultAllowed    bool        `json:"openDefaultAllowed"`
	RequiresHumanApproval bool        `json:"requiresHumanApproval"`
	HardStop              bool        `json:"hardStop"`
	Guidance              string      `json:"guidance"`
}

var builtinPlaybooks = map[string]PlaybookEntry{
	"container-stopped":          {ID: "container-stopped", BlastRadius: BlastRadiusApp, Summary: "Declared container is stopped or exited unexpectedly.", AutoRepairEffect: string(RepairRestartService), OpenDefaultAllowed: true, Guidance: "Run `stackkit repair restart-service --target <service>`."},
	"container-oom":              {ID: "container-oom", BlastRadius: BlastRadiusApp, Summary: "Container was OOM-killed.", AutoRepairEffect: string(RepairRestartService), OpenDefaultAllowed: true, Guidance: "Restart once within circuit-breaker limits; raise module memory profile if repeated."},
	"image-digest-drift":         {ID: "image-digest-drift", BlastRadius: BlastRadiusApp, Summary: "Running image digest differs from ResolvedPlan pin.", AutoRepairEffect: string(RepairReconcileCompose), OpenDefaultAllowed: true, Guidance: "Run `stackkit repair reconcile-compose`."},
	"router-stale":               {ID: "router-stale", BlastRadius: BlastRadiusCore, Summary: "Kit-Traefik router probe failed.", AutoRepairEffect: string(RepairReloadRouter), OpenDefaultAllowed: true, Guidance: "Run `stackkit repair reload-router`."},
	"tls-cert-expiring":          {ID: "tls-cert-expiring", BlastRadius: BlastRadiusCore, Summary: "TLS certificate missing or within renewal window.", AutoRepairEffect: string(RepairRenewTLS), OpenDefaultAllowed: true, Guidance: "Run `stackkit repair renew-tls`."},
	"appsetup-degraded":          {ID: "appsetup-degraded", BlastRadius: BlastRadiusApp, Summary: "Optional Tier-2 app personalization failed.", AutoRepairEffect: string(RepairRetryAppSetup), OpenDefaultAllowed: true, Guidance: "Run `stackkit retry-appsetup <workload>`."},
	"coolify-proxy-port-drift":   {ID: "coolify-proxy-port-drift", BlastRadius: BlastRadiusExtension, Summary: "Coolify proxy drifted toward host ports 80/443 or ACME.", AutoRepairEffect: string(RepairReconcileCoolifyProxy), OpenDefaultAllowed: true, Guidance: "Run `stackkit repair reconcile-coolify-proxy`."},
	"adguard-dns-down":           {ID: "adguard-dns-down", BlastRadius: BlastRadiusCore, Summary: "Local AdGuard Home resolver is unreachable.", AutoRepairEffect: string(RepairRestartService), OpenDefaultAllowed: true, Guidance: "Restart AdGuard; Connected uses pinned fallback DNS, Offline stays local-only."},
	"disk-full-floor":            {ID: "disk-full-floor", BlastRadius: BlastRadiusHost, Summary: "Free disk space dropped below safe floor.", RequiresHumanApproval: true, HardStop: true, Guidance: "Hard-stop automation; free disk space manually (gc-dangling-images requires explicit owner approval)."},
	"clock-skew-exceeded":        {ID: "clock-skew-exceeded", BlastRadius: BlastRadiusHost, Summary: "Host clock skew exceeds safety threshold.", RequiresHumanApproval: true, HardStop: true, Guidance: "Hard-stop automation; restore NTP/RTC time synchronization."},
	"custody-missing-or-corrupt": {ID: "custody-missing-or-corrupt", BlastRadius: BlastRadiusHost, Summary: "Node custody directory missing or unreadable.", RequiresHumanApproval: true, HardStop: true, Guidance: "Hard-stop automation; restore custody from verified Kopia backup with Owner approval."},
	"circuit-breaker-tripped":    {ID: "circuit-breaker-tripped", BlastRadius: BlastRadiusHost, Summary: "Auto-repair reached max 2 repairs/signature/hour.", RequiresHumanApproval: true, HardStop: true, Guidance: "Automation halted; inspect incident envelope and resolve manually."},
	"active-lease-held":          {ID: "active-lease-held", BlastRadius: BlastRadiusHost, Summary: "Unified node action_lease is actively held.", RequiresHumanApproval: true, HardStop: true, Guidance: "Wait for active writer to finish; never clear active leases from agent/watchdog."},
}

// BuiltinPlaybook returns the playbook metadata for one failure signature ID.
func BuiltinPlaybook(id string) (PlaybookEntry, bool) {
	entry, ok := builtinPlaybooks[strings.TrimSpace(id)]
	return entry, ok
}
