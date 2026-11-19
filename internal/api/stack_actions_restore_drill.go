package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
	sshpin "github.com/kombifyio/stackkits/internal/ssh"
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
)

func runRestoreDrillVerifierStackAction(ctx context.Context, resp stackActionResponse, command string, target *stackActionTarget, resolver StackActionReferenceResolver) (stackActionResponse, int, *skerrors.StackKitError) {
	command = strings.TrimSpace(command)
	if command == "" {
		return runBuiltInRestoreDrillVerifierStackAction(ctx, resp, target, resolver)
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return resp, http.StatusBadRequest, skerrors.NewValidationError("missing_restore_drill_command", "restore drill verifier command is empty")
	}

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, fields[0], fields[1:]...)
	if strings.TrimSpace(resp.TofuDir) != "" {
		cmd.Dir = filepath.Clean(resp.TofuDir)
	}
	cmd.Env = append(os.Environ(),
		"STACKKIT_STACK_ACTION="+string(resp.Action),
		"STACKKIT_STACK_ID="+resp.StackID,
		"STACKKIT_STACK_NAME="+resp.StackName,
		"STACKKIT_STACKKIT="+resp.StackKit,
		"STACKKIT_TOFU_DIR="+resp.TofuDir,
		"STACKKIT_UNIFIED_PATH="+resp.UnifiedPath,
	)
	output, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = "restore verifier completed"
	}
	if err != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "restore_drill_verifier", Status: stackaction.CheckStatusFailed, Detail: detail})
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", err, detail)
	}

	resp.Status = stackaction.StatusVerified
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "restore_drill_verifier", Status: stackaction.CheckStatusOK, Detail: detail})
	return resp, http.StatusOK, nil
}

func runBuiltInRestoreDrillVerifierStackAction(ctx context.Context, resp stackActionResponse, target *stackActionTarget, resolver StackActionReferenceResolver) (stackActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDirStackAction(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	resp.Checks = append(resp.Checks, stackActionCheck{
		Name:   "restore_drill_adapter",
		Status: "ok",
		Detail: "using built-in runtime smoke restore verifier",
	})

	statePath := filepath.Join(filepath.Clean(resp.TofuDir), "terraform.tfstate")
	info, err := os.Stat(statePath)
	if err != nil || info.Size() == 0 {
		detail := fmt.Sprintf("OpenTofu state missing or empty at %s", statePath)
		if err != nil {
			detail = detail + ": " + err.Error()
		}
		resp.Status = "failed"
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "opentofu_state", Status: "failed", Detail: detail})
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("%s", detail), detail)
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "opentofu_state", Status: "ok", Detail: statePath})

	remote, cleanup, remoteErr := prepareStackActionRemoteTarget(ctx, resp.TofuDir, target, resolver)
	if cleanup != nil {
		defer cleanup()
	}
	if remoteErr != nil {
		if reason := sshpin.HostKeyReason(remoteErr); reason != "" {
			return resp, http.StatusBadGateway, runtimeTargetPrepareErrorStackAction("restore_drill_failed", "Restore drill verifier failed", remoteErr)
		}
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", remoteErr, "runtime target preparation failed")
	}
	recordRuntimeTargetHostKeyStackAction(&resp, remote)

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "docker", "ps", "--format", "{{.Names}}\t{{.Status}}") // #nosec G204 -- static docker command.
	cmd.Dir = filepath.Clean(resp.TofuDir)
	cmd.Env = os.Environ()
	if remote != nil {
		cmd.Env = append(cmd.Env, remote.env...)
	}
	output, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if err != nil {
		if detail == "" {
			detail = err.Error()
		} else {
			detail = detail + ": " + err.Error()
		}
		resp.Status = "failed"
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "docker_runtime", Status: "failed", Detail: detail})
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", err, detail)
	}

	running := runtimeDockerPSLinesStackAction(detail)
	if len(running) == 0 {
		resp.Status = "failed"
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "docker_runtime", Status: "failed", Detail: "no running Docker containers"})
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("no running Docker containers"), detail)
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "docker_runtime", Status: "ok", Detail: fmt.Sprintf("%d running containers", len(running))})

	if baseKitCoolifyEnabledStackAction(resp.TofuDir) {
		if !runtimeDockerPSHasContainerStackAction(running, "coolify") {
			resp.Status = "failed"
			resp.Checks = append(resp.Checks, stackActionCheck{Name: "coolify_runtime", Status: "failed", Detail: detail})
			return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("coolify container is not running"), detail)
		}
		platformPath := filepath.Join(filepath.Clean(resp.TofuDir), ".stackkit", "platform.json")
		if info, err := os.Stat(platformPath); err != nil || info.Size() == 0 {
			detail := fmt.Sprintf("Coolify platform config missing or empty at %s", platformPath)
			if err != nil {
				detail = detail + ": " + err.Error()
			}
			resp.Status = "failed"
			resp.Checks = append(resp.Checks, stackActionCheck{Name: "coolify_platform_config", Status: "failed", Detail: detail})
			return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("%s", detail), detail)
		}
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "coolify_runtime", Status: "ok", Detail: "coolify container running"})
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "coolify_platform_config", Status: "ok", Detail: platformPath})
	}

	if metrics, err := collectRuntimeHostMetricsStackAction(ctx, remote); err != nil {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_metrics", Status: "warning", Detail: err.Error()})
	} else if metrics != nil {
		resp.RuntimeMetrics = metrics
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_metrics", Status: "ok", Detail: "host resource metrics collected"})
	}

	resp.Status = "verified"
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "restore_drill_verifier", Status: "ok", Detail: "built-in runtime smoke restore verifier completed"})
	return resp, http.StatusOK, nil
}

func collectRuntimeHostMetricsStackAction(ctx context.Context, remote *preparedRuntimeTargetStackAction) (*stackActionRuntimeMetrics, error) {
	if remote == nil || remote.target == nil || strings.TrimSpace(remote.keyPath) == "" {
		return nil, nil
	}
	args := append(runtimeTargetSSHArgsStackAction(remote.target, remote.keyPath), "sh", "-s")
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "ssh", args...) // #nosec G204 -- command args are assembled without shell interpolation.
	cmd.Stdin = strings.NewReader(runtimeHostMetricsScriptStackAction())
	output, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if err != nil {
		if detail != "" {
			return nil, fmt.Errorf("collect runtime host metrics: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("collect runtime host metrics: %w", err)
	}
	return runtimeHostMetricsFromOutputStackAction(detail, time.Now().UTC())
}

func runtimeHostMetricsScriptStackAction() string {
	return `set -eu
read -r _ user nice system idle iowait irq softirq steal _ < /proc/stat
total1=$((user + nice + system + idle + iowait + irq + softirq + steal))
idle1=$((idle + iowait))
sleep 1
read -r _ user nice system idle iowait irq softirq steal _ < /proc/stat
total2=$((user + nice + system + idle + iowait + irq + softirq + steal))
idle2=$((idle + iowait))
cpu=$(awk -v t1="$total1" -v i1="$idle1" -v t2="$total2" -v i2="$idle2" 'BEGIN { dt=t2-t1; di=i2-i1; if (dt<=0) printf "0"; else printf "%.1f", (dt-di)*100/dt }')
mem=$(awk '/MemTotal:/ {t=$2} /MemAvailable:/ {a=$2} END { if (t>0) printf "%.1f", (t-a)*100/t; else printf "0" }' /proc/meminfo)
disk=$(df -P / | awk 'NR==2 { gsub("%","",$5); printf "%.1f", $5 }')
uptime=$(awk '{ printf "%.0f", $1 }' /proc/uptime)
printf 'cpu_percent=%s\nmemory_percent=%s\ndisk_percent=%s\nuptime_seconds=%s\n' "$cpu" "$mem" "$disk" "$uptime"
`
}

func runtimeHostMetricsFromOutputStackAction(raw string, updatedAt time.Time) (*stackActionRuntimeMetrics, error) {
	metrics := &stackActionRuntimeMetrics{}
	parsed := 0
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("parse runtime host metric %s: %w", key, err)
		}
		switch strings.TrimSpace(key) {
		case "cpu_percent":
			metrics.CPUPercent = number
			parsed++
		case "memory_percent":
			metrics.MemoryPercent = number
			parsed++
		case "disk_percent":
			metrics.DiskPercent = number
			parsed++
		case "uptime_seconds":
			metrics.UptimeSeconds = number
			parsed++
		}
	}
	if parsed == 0 {
		return nil, fmt.Errorf("runtime host metrics output did not contain metrics")
	}
	metrics.UpdatedAt = updatedAt.Format(time.RFC3339)
	return metrics, nil
}

func runtimeDockerPSLinesStackAction(output string) []string {
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func runtimeDockerPSHasContainerStackAction(lines []string, name string) bool {
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

func baseKitCoolifyEnabledStackAction(tofuDir string) bool {
	data, err := os.ReadFile(filepath.Join(filepath.Clean(tofuDir), "terraform.tfvars.json"))
	if err != nil {
		return false
	}
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		return false
	}
	enabled, ok := values["enable_coolify"].(bool)
	return ok && enabled
}
