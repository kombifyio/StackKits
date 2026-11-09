package stackkitmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/standaloneoperations"
)

// The ceiling of a kombify agent at the node /mcp (homelab identity target
// §7): its Homelab account's groups (enforced by the Access Gate) and the
// capability of its lease, mapped here to the StackKits operations it may
// reach. Identity, secrets, vault and recovery operations need the Owner's
// step-up per action at the Authority and are never reachable by an envelope,
// read or write; the Owner runs them at the console or in the Hub.
const (
	agentCapabilityOperate     = "techstack.inventory.operate"
	agentCapabilityMaintenance = "techstack.server.maintenance"
)

// agentSensitiveOperations are operation ID prefixes of identity, secrets,
// vault and recovery: users and their passkey state, break-glass recovery
// bundles, identity projection and workload identity, secret custody, agent
// credentials, devices, emergency export and restore, Advanced trust, every
// federation operation (control, bindings and links adopt Owner custody) and
// application owner accounts.
var agentSensitiveOperations = []string{
	"stackkit.user.", "stackkit.break-glass.", "stackkit.identity.", "stackkit.secrets.", "stackkit.vault",
	"stackkit.device", "stackkit.agent", "stackkit.recovery", "stackkit.backup.emergency-",
	"stackkit.advanced.trust.", "stackkit.federation.", "stackkit.setup",
	"stackkit.backup.configure", "stackkit.backup.target.import", "stackkit.backup.target.rebind",
}

// envelopeAgentTools are the only tools outside the operation catalog an
// envelope agent may call: the embedded documentation and the workspace's own
// StackSpec reads and its compare-and-swap write (which still passes
// admitMutation). Every other tool must be a catalog operation, decided by
// its operation ID below; anything else, a tool added later included, is
// refused to an envelope agent before any handler runs.
var envelopeAgentTools = map[string]bool{
	"stackkit_docs_search": true, "stackkit_api_overview": true, "stackkit_api_endpoint": true, "stackkit_get_openapi_spec": true,
	"stackkit_application_delivery_compatibility": true, "stackkit_use_case_compute_tiers": true, "stackkit_module_profiles": true,
	"stackkit_install_plan": true, "stackkit_self_check_plan": true, "stackkit_state_console": true,
	"stackkit_validate_spec": true, "stackkit_generate_preview": true, "stackkit_compat_check": true,
	"stackkit_config_get": true, "stackkit_config_set": true,
}

// envelopeToolCallDenial decides one JSON-RPC message of an envelope agent
// before it reaches the MCP server: no batch, a tools/call only for an
// allowlisted tool or a catalog operation that is not identity, secrets, vault
// or recovery and, for a mutation, matches the lease capability, and never a
// workspace or spec path argument other than the default StackSpec. It returns
// the refusal reason, or "" when the message may pass.
func envelopeToolCallDenial(body []byte, capability string) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return "agent_batch_refused"
	}
	var message struct {
		Method string `json:"method"`
		Params struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if json.Unmarshal(trimmed, &message) != nil {
		return "agent_message_unreadable"
	}
	if message.Method != "tools/call" {
		return ""
	}
	if _, ok := message.Params.Arguments["base_dir"]; ok {
		return "agent_path_argument_refused"
	}
	if raw, ok := message.Params.Arguments["spec_path"]; ok {
		var specPath string
		if json.Unmarshal(raw, &specPath) != nil || (specPath != "" && specPath != config.GetDefaultSpecPath() && specPath != config.GetSpecAliasPath()) {
			return "agent_path_argument_refused"
		}
	}
	name := message.Params.Name
	if envelopeAgentTools[name] {
		return ""
	}
	operation, ok := standaloneoperations.LookupTool(name)
	if !ok {
		return "agent_tool_not_allowed"
	}
	if operationMatches(string(operation.ID), agentSensitiveOperations) {
		return "owner_step_up_required"
	}
	if operation.Mutation {
		required := agentCapabilityOperate
		if operationMatches(string(operation.ID), agentMaintenanceOperations) {
			required = agentCapabilityMaintenance
		}
		if capability != required {
			return "lease_capability_mismatch"
		}
	}
	return ""
}

// agentMaintenanceOperations are the server maintenance operations a
// techstack.server.maintenance lease reaches: upgrades, host updates and
// reboots, backups, restores, drift repair, service control and rollback.
// Every other mutation is an inventory operation of techstack.inventory.operate.
var agentMaintenanceOperations = []string{
	"stackkit.upgrade", "stackkit.host.", "stackkit.backup", "stackkit.restore", "stackkit.drift", "stackkit.service.",
	"stackkit.advanced.restore-drill.", "stackkit.advanced.rollback.", "stackkit.output-transaction.",
}

func operationMatches(operation string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if operation == strings.TrimSuffix(prefix, ".") || strings.HasPrefix(operation, prefix) {
			return true
		}
	}
	return false
}

// envelopeOperationDenial is nil when the envelope agent of ctx may reach the
// operation, and the refusal otherwise. A request without an envelope agent
// is not decided here.
func envelopeOperationDenial(ctx context.Context, tool, operation string, mutation bool) map[string]any {
	envelope, ok := EnvelopeAgentFromContext(ctx)
	if !ok {
		return nil
	}
	deny := func(reason string, err error) map[string]any {
		out := errorOutput(tool, err)
		out["lease_ceiling"] = envelope.Capability
		out["agent_denial"] = reason
		return out
	}
	if operationMatches(operation, agentSensitiveOperations) {
		return deny("owner_step_up_required", fmt.Errorf("%q is an identity, secrets, vault or recovery operation; only the Owner runs it, with a passkey step-up at the Authority", operation))
	}
	if !mutation {
		return nil
	}
	required := agentCapabilityOperate
	if operationMatches(operation, agentMaintenanceOperations) {
		required = agentCapabilityMaintenance
	}
	if envelope.Capability != required {
		return deny("lease_capability_mismatch", fmt.Errorf("the agent's lease (%s) does not reach %q; it needs %s", envelope.Capability, operation, required))
	}
	return nil
}
