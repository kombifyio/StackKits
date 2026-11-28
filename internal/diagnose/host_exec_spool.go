package diagnose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/mcpagents"
)

const (
	SpoolPendingRelDir  = ".stackkit/mcp/spool/pending"
	SpoolReceiptsRelDir = ".stackkit/mcp/spool/receipts"
	SpoolJobSchema      = "stackkit.host-exec-job/v1"
	SpoolReceiptSchema  = "stackkit.host-exec-receipt/v1"
)

// SpoolJob is written by stackkit-server into the host-owned spool directory
// and consumed by the host-side `stackkit host-exec` oneshot (ADR-0051).
// Jobs carry argument values and workspace references only — NEVER secret values.
type SpoolJob struct {
	SchemaVersion string       `json:"schemaVersion"`
	JobID         string       `json:"jobId"`
	AgentID       string       `json:"agentId"`
	Action        RepairAction `json:"action"`
	SignatureID   string       `json:"signatureId,omitempty"`
	Target        string       `json:"target,omitempty"`
	Argv          []string     `json:"argv,omitempty"`
	ApprovalID    string       `json:"approvalId,omitempty"`
	Digest        string       `json:"digest"`
	CreatedAt     time.Time    `json:"createdAt"`
}

// SpoolReceipt is the durable, tamper-evident host-exec outcome record.
type SpoolReceipt struct {
	SchemaVersion string       `json:"schemaVersion"`
	JobID         string       `json:"jobId"`
	AgentID       string       `json:"agentId"`
	Action        RepairAction `json:"action"`
	Status        string       `json:"status"` // succeeded, denied_revoked, denied_approval, denied_circuit_breaker, denied_hard_stop, failed
	Detail        string       `json:"detail"`
	ExecutedAt    time.Time    `json:"executedAt"`
}

// WatchdogDNSPlan describes the exact fallback DNS policy when AdGuard dies
// (ADR-0051 / §6.1.3).
type WatchdogDNSPlan struct {
	OfflineLocalOnly bool     `json:"offlineLocalOnly"`
	FallbackDNS      []string `json:"fallbackDns,omitempty"`
	DegradedOnly     bool     `json:"degradedOnly"`
	Detail           string   `json:"detail"`
}

// ResolveWatchdogDNS enforces the strict ADR-0051 DNS resilience boundary:
//   - Connected mode: uses pinned fallback resolvers (Quad9/Cloudflare) if AdGuard dies.
//   - Offline / Local-Only mode (lab.home): uses ONLY an explicit local LAN/host
//     fallback resolver or reports `degraded` — NEVER public DNS!
func ResolveWatchdogDNS(offlineLocalOnly bool, localLANFallback string) WatchdogDNSPlan {
	if offlineLocalOnly {
		lan := strings.TrimSpace(localLANFallback)
		if lan != "" && isPrivateOrLoopbackIP(lan) {
			return WatchdogDNSPlan{
				OfflineLocalOnly: true,
				FallbackDNS:      []string{lan},
				DegradedOnly:     false,
				Detail:           "offline/local-only mode uses local LAN fallback resolver only (zero public DNS)",
			}
		}
		return WatchdogDNSPlan{
			OfflineLocalOnly: true,
			FallbackDNS:      nil,
			DegradedOnly:     true,
			Detail:           "offline/local-only mode refuses public DNS fallback; reporting degraded until local AdGuard recovers",
		}
	}
	return WatchdogDNSPlan{
		OfflineLocalOnly: false,
		FallbackDNS:      []string{"9.9.9.9", "1.1.1.1"},
		DegradedOnly:     false,
		Detail:           "connected mode uses pinned fallback resolvers while AdGuard recovers",
	}
}

func isPrivateOrLoopbackIP(ip string) bool {
	return strings.HasPrefix(ip, "127.") ||
		strings.HasPrefix(ip, "10.") ||
		strings.HasPrefix(ip, "192.168.") ||
		strings.HasPrefix(ip, "172.16.") ||
		strings.HasPrefix(ip, "172.17.") ||
		strings.HasPrefix(ip, "172.18.") ||
		strings.HasPrefix(ip, "172.19.") ||
		strings.HasPrefix(ip, "172.2") ||
		strings.HasPrefix(ip, "172.30.") ||
		strings.HasPrefix(ip, "172.31.") ||
		ip == "::1" ||
		strings.HasPrefix(strings.ToLower(ip), "fd")
}

// ComputeSpoolJobDigest binds agent, action, target, and argv deterministically.
func ComputeSpoolJobDigest(job SpoolJob) string {
	payload := strings.Join([]string{
		strings.TrimSpace(job.AgentID),
		string(job.Action),
		strings.TrimSpace(job.Target),
		strings.Join(job.Argv, "\x00"),
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EnqueueSpoolJob writes a validated job into `.stackkit/mcp/spool/pending/`.
func EnqueueSpoolJob(workspace string, job SpoolJob) (string, error) {
	if strings.TrimSpace(job.JobID) == "" || strings.TrimSpace(job.AgentID) == "" || strings.TrimSpace(string(job.Action)) == "" {
		return "", errors.New("spool job requires jobId, agentId, and action")
	}
	if strings.ContainsAny(job.JobID, "/\\..") {
		return "", errors.New("spool jobId must be a safe single filename token")
	}
	job.SchemaVersion = SpoolJobSchema
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	job.Digest = ComputeSpoolJobDigest(job)

	dir := filepath.Join(workspace, filepath.FromSlash(SpoolPendingRelDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, job.JobID+".json")
	encoded, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", target, os.Getpid())
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return target, nil
}

// ProcessSpoolOnce drains pending host-exec jobs under ADR-0051 rules:
// 1. Verifies digest integrity.
// 2. Discards any job from a revoked agent (recording denied_revoked receipt).
// 3. Evaluates hard-stop snapshot posture & effect-based authorization.
// 4. Enforces circuit breaker (max 2 auto-repairs/signature/hour) on open-default runs.
// 5. Runs executor callback and writes durable receipt.
func ProcessSpoolOnce(
	workspace string,
	snapshot ObservationSnapshot,
	now time.Time,
	execute func(job SpoolJob) error,
) ([]SpoolReceipt, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	pendingDir := filepath.Join(workspace, filepath.FromSlash(SpoolPendingRelDir))
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	policy, err := LoadRepairPolicy(workspace)
	if err != nil {
		return nil, err
	}
	breaker := OpenCircuitBreaker(workspace)
	breaker.Now = func() time.Time { return now }
	agents := mcpagents.Open(workspace)

	var receipts []SpoolReceipt
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		jobPath := filepath.Join(pendingDir, entry.Name())
		raw, readErr := os.ReadFile(jobPath)
		if readErr != nil {
			continue
		}
		var job SpoolJob
		if err := json.Unmarshal(raw, &job); err != nil {
			_ = os.Remove(jobPath)
			continue
		}
		receipt := SpoolReceipt{
			SchemaVersion: SpoolReceiptSchema,
			JobID:         job.JobID,
			AgentID:       job.AgentID,
			Action:        job.Action,
			ExecutedAt:    now.UTC(),
		}

		// 1. Verify digest.
		if job.Digest != ComputeSpoolJobDigest(job) {
			receipt.Status = "failed"
			receipt.Detail = "spool job digest mismatch; discarded"
			receipts = append(receipts, finalizeSpoolJob(workspace, jobPath, receipt))
			continue
		}

		// 2. Verify agent is not revoked (local agents stored in .stackkit/mcp/agents.json).
		if !mcpagents.IsGatewayAgent(job.AgentID) && job.AgentID != "owner-cli" && job.AgentID != "watchdog" {
			known, active, checkErr := isAgentActive(agents, job.AgentID)
			if checkErr != nil || !known || !active {
				receipt.Status = "denied_revoked"
				receipt.Detail = "agent credential is unknown or revoked; queued job discarded"
				receipts = append(receipts, finalizeSpoolJob(workspace, jobPath, receipt))
				continue
			}
		}

		// 3. Hard-stop check on stale or host-degraded snapshot.
		if snapshot.IsStale(now, DefaultMaxSnapshotAge) || len(snapshot.HardStopReasons) > 0 {
			receipt.Status = "denied_hard_stop"
			receipt.Detail = fmt.Sprintf("hard-stop active (stale=%v reasons=%v); manual intervention required", snapshot.IsStale(now, DefaultMaxSnapshotAge), snapshot.HardStopReasons)
			receipts = append(receipts, finalizeSpoolJob(workspace, jobPath, receipt))
			continue
		}

		// 4. Verify effect-based authorization (and owner approval if required).
		hasApproval := strings.TrimSpace(job.ApprovalID) != ""
		decision, authErr := EvaluateRepairAuthorization(job.Action, policy, hasApproval)
		if authErr != nil {
			receipt.Status = "denied_approval"
			receipt.Detail = authErr.Error()
			receipts = append(receipts, finalizeSpoolJob(workspace, jobPath, receipt))
			continue
		}

		// 5. Enforce circuit breaker on open-default autonomous repairs.
		if decision.AllowedWithoutApprove {
			sig := strings.TrimSpace(job.SignatureID)
			if sig == "" {
				sig = string(job.Action)
			}
			if _, cbErr := breaker.CheckAndRecord(sig); cbErr != nil {
				receipt.Status = "denied_circuit_breaker"
				receipt.Detail = cbErr.Error()
				receipts = append(receipts, finalizeSpoolJob(workspace, jobPath, receipt))
				continue
			}
		}

		// 6. Execute host-side repair callback.
		if execute == nil {
			receipt.Status = "succeeded"
			receipt.Detail = "dry-run host-exec admitted"
		} else if execErr := execute(job); execErr != nil {
			receipt.Status = "failed"
			receipt.Detail = RedactSecrets(execErr.Error())
		} else {
			receipt.Status = "succeeded"
			receipt.Detail = decision.Reason
		}
		receipts = append(receipts, finalizeSpoolJob(workspace, jobPath, receipt))
	}
	return receipts, nil
}

func isAgentActive(store *mcpagents.Store, agentID string) (known bool, active bool, err error) {
	list, err := store.List()
	if err != nil {
		return false, false, err
	}
	for _, a := range list {
		if a.ID == agentID {
			return true, a.Active(), nil
		}
	}
	return false, false, nil
}

func finalizeSpoolJob(workspace, jobPath string, receipt SpoolReceipt) SpoolReceipt {
	_ = os.Remove(jobPath)
	receiptsDir := filepath.Join(workspace, filepath.FromSlash(SpoolReceiptsRelDir))
	_ = os.MkdirAll(receiptsDir, 0o700)
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(receiptsDir, receipt.JobID+".json"), append(encoded, '\n'), 0o600)
	}
	return receipt
}

// RenderSystemdUnits returns the host-side systemd unit definitions for
// `stackkit-watchdog.timer` (5-min oneshot) and `stackkit-host-exec.path`
// (ADR-0051 — zero always-on daemon).
func RenderSystemdUnits(stackkitBinary, workspace string) map[string]string {
	return map[string]string{
		"stackkit-watchdog.service": fmt.Sprintf(`[Unit]
Description=StackKits Host-Side Watchdog & Redacted Observation Snapshot (ADR-0051)
After=network-online.target docker.service

[Service]
Type=oneshot
WorkingDirectory=%s
ExecStart=%s watchdog --once
`, workspace, stackkitBinary),
		"stackkit-watchdog.timer": `[Unit]
Description=Run StackKits host-side watchdog every 5 minutes (ADR-0051)

[Timer]
OnBootSec=2min
OnUnitActiveSec=5min
AccuracySec=30s

[Install]
WantedBy=timers.target
`,
		"stackkit-host-exec.path": fmt.Sprintf(`[Unit]
Description=Watch StackKits MCP host-exec spool directory (ADR-0051)

[Path]
DirectoryNotEmpty=%s/.stackkit/mcp/spool/pending
MakeDirectory=true

[Install]
WantedBy=multi-user.target
`, workspace),
		"stackkit-host-exec.service": fmt.Sprintf(`[Unit]
Description=Execute one queued StackKits MCP host-exec repair spool batch (ADR-0051)
After=docker.service

[Service]
Type=oneshot
WorkingDirectory=%s
ExecStart=%s host-exec --once
`, workspace, stackkitBinary),
	}
}
