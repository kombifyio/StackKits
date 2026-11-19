package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const homelabGuardService = "techstack-agent.service"

// Homelab bootstraps Guard before invoking StackKits. Preparation only reads
// that installation: restarting or replacing it here could interrupt the Guard
// executing this command, or overwrite its host key and durable journal.
const homelabGuardObservationScript = `set -eu
systemctl is-active --quiet techstack-agent.service
systemctl is-enabled --quiet techstack-agent.service
file=/etc/techstack/agent-enrollment.json
fallback=/var/lib/techstack/guard/agent-enrollment.json
if test -f "$fallback" && { test ! -f "$file" || test "$fallback" -nt "$file"; }; then
  file=$fallback
fi
test -f "$file" && test ! -L "$file"
case "$(stat -c '%a' "$file")" in 400|600) ;; *) exit 1 ;; esac
test "$(wc -c < "$file")" -le 262144
head -c 262145 "$file"
`

type homelabGuardObservation struct {
	RuntimeAgentID   string                   `json:"runtime_agent_id"`
	WorkerID         string                   `json:"worker_id"`
	ServerID         string                   `json:"server_id"`
	TenantID         string                   `json:"tenant_id"`
	OwnerID          string                   `json:"owner_id"`
	StackID          string                   `json:"stack_id"`
	LeaseID          string                   `json:"lease_id"`
	HeartbeatURL     string                   `json:"heartbeat_url"`
	InventoryURL     string                   `json:"inventory_url"`
	ChannelBootstrap *homelabGuardObservation `json:"channel_bootstrap"`
	Data             *homelabGuardObservation `json:"data"`
}

func probeHomelabGuardLocal(ctx context.Context) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("canonical Guard verification requires Linux/systemd")
	}
	command := exec.CommandContext(ctx, "sh", "-c", homelabGuardObservationScript)
	if os.Geteuid() != 0 {
		command = exec.CommandContext(ctx, "sudo", "-n", "sh", "-c", homelabGuardObservationScript)
	}
	// The enrollment is consumed privately by the Go decoder; neither stdout
	// nor stderr from this secret-file probe is published.
	return command.Output()
}

func verifyHomelabGuard(ctx context.Context, handoff homelabHandoff, target string, probe func(context.Context) ([]byte, error)) error {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	data, err := probe(probeCtx)
	if err != nil {
		return homelabGuardVerificationError("canonical service or protected enrollment could not be observed; Linux/systemd and read access are required")
	}
	var observed homelabGuardObservation
	if len(data) > 262144 || json.Unmarshal(data, &observed) != nil {
		return homelabGuardVerificationError("canonical enrollment observation is invalid")
	}
	if observed.Data != nil {
		observed = *observed.Data
	}
	if observed.RuntimeAgentID == "" {
		observed.RuntimeAgentID = observed.WorkerID
	}
	if observed.ChannelBootstrap != nil {
		bootstrap := observed.ChannelBootstrap
		for _, pair := range []struct {
			value    *string
			fallback string
		}{
			{&observed.RuntimeAgentID, bootstrap.RuntimeAgentID}, {&observed.ServerID, bootstrap.ServerID},
			{&observed.TenantID, bootstrap.TenantID}, {&observed.OwnerID, bootstrap.OwnerID},
			{&observed.StackID, bootstrap.StackID}, {&observed.LeaseID, bootstrap.LeaseID},
			{&observed.HeartbeatURL, bootstrap.HeartbeatURL}, {&observed.InventoryURL, bootstrap.InventoryURL},
		} {
			if *pair.value == "" {
				*pair.value = pair.fallback
			}
		}
	}
	for _, binding := range []struct{ field, actual, expected string }{
		{"runtime_agent_id", observed.RuntimeAgentID, handoff.RuntimeAgentID},
		{"server_id", observed.ServerID, handoff.ServerID},
		{"tenant_id", observed.TenantID, handoff.TenantID},
		{"stack_id", observed.StackID, handoff.StackID},
	} {
		if binding.actual == "" || binding.actual != binding.expected {
			return homelabGuardVerificationError("canonical enrollment does not match the " + binding.field + " handoff")
		}
	}
	if observed.LeaseID != handoff.LeaseID {
		return homelabGuardVerificationError("canonical enrollment does not match the lease_id handoff")
	}
	if handoff.OwnerID != "" && observed.OwnerID != handoff.OwnerID {
		return homelabGuardVerificationError("canonical enrollment does not match the owner_id handoff")
	}
	for _, endpoint := range []struct{ actual, supplied, suffix string }{
		{observed.HeartbeatURL, handoff.HeartbeatURL, "/heartbeat"},
		{observed.InventoryURL, handoff.InventoryURL, "/inventory"},
	} {
		expected := handoff.ServerURL + "/api/v1/workers/" + url.PathEscape(handoff.RuntimeAgentID) + endpoint.suffix
		if endpoint.actual != expected || (endpoint.supplied != "" && endpoint.supplied != expected) {
			return homelabGuardVerificationError("canonical enrollment endpoints do not match the control-plane handoff")
		}
	}
	// Persist only the nonsecret handoff after observation succeeds. Existing
	// Guard configuration, enrollment, identity and journals are never written.
	evidence := map[string]any{
		"status": "verified", "target": target, "service": homelabGuardService,
		"verification_scope": "active-enabled-service-and-enrollment-binding",
		"server_url":         handoff.ServerURL, "server_id": observed.ServerID,
		"runtime_agent_id": observed.RuntimeAgentID, "tenant_id": observed.TenantID,
		"owner_id": observed.OwnerID, "stack_id": observed.StackID, "lease_id": observed.LeaseID,
		"heartbeat_url": observed.HeartbeatURL, "inventory_url": observed.InventoryURL,
		"verified_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(getWorkDir(), ".stackkit", "runs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "homelab-guard-evidence.json"), append(encoded, '\n'), 0600)
}

func homelabGuardVerificationError(reason string) error {
	return fmt.Errorf("homelab_guard_not_verified: %s; complete or recover Guard bootstrap in Homelab before retrying prepare; StackKits does not install or restart Guard", strings.TrimSpace(reason))
}
