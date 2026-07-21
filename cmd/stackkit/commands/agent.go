package commands

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/stackspecadmission"
	"github.com/spf13/cobra"
)

type agentCommandStep struct {
	Command  string `json:"command"`
	Purpose  string `json:"purpose"`
	Mutation bool   `json:"mutation"`
}

type agentInstallPlan struct {
	Scenario      string             `json:"scenario"`
	Kit           string             `json:"kit"`
	Target        string             `json:"target"`
	Workspace     string             `json:"workspace"`
	Commands      []agentCommandStep `json:"commands"`
	Evidence      []string           `json:"evidence"`
	ReadinessNote string             `json:"readinessNote"`
}

type agentSelfCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
}

var (
	agentInstallPlanJSON bool
	agentSelfCheckJSON   bool
	agentPromptJSON      bool
	agentKit             string
	agentTarget          string
	agentWorkspace       string
	agentClient          string
	agentMode            string
	agentServerURL       string
	agentPromptList      bool
)

var agentPromptBodies = map[string]string{
	"basekit-autonomous-rollout": `You are operating StackKits autonomously on a fresh controlled host. Deploy BaseKit only.

Run:
stackkit init basement-kit --non-interactive
stackkit prepare --dry-run
stackkit validate
stackkit resolve --inventory <observed-inventory.json> --output deploy/.stackkit/resolved-plan.json
stackkit generate
stackkit plan
stackkit apply
stackkit verify --json

Do not put provider lifecycle, credentials, management addresses, or observed host facts into StackSpec. Preserve logs and evidence. Do not hand-edit generated files under deploy/, .stackkit/, or generated snapshots.`,
	"inspect-existing-rollout": `Inspect an existing StackKits workspace without mutation.

Run:
stackkit status --json
stackkit verify --json
stackkit logs list --json
stackkit doctor --json

Report current StackKit, mode, Hub URL, service URLs, failing checks, latest run ID, and evidence paths.`,
	"diagnose-failed-rollout": `Diagnose a failed StackKits rollout with read-only evidence first.

Run:
stackkit logs list --json
stackkit logs latest --json
stackkit doctor --json
stackkit verify --json
stackkit status --json

Classify the failure as host-prerequisite, docker-daemon, image-pull, network-or-dns, generated-config, opentofu-plan, opentofu-apply, service-health, or unknown.`,
	"ssh-rollout": `Prepare an externally handed-over host for a governed Basement Kit rollout. Raw SSH target selection is not StackSpec intent.

Require an observed Inventory plus any ExternalHostBinding/HostConformanceReceipt from the host or TechStack owner. Never put provider credentials, lifecycle, or management addresses into StackSpec.

Run:
stackkit init basement-kit --non-interactive
stackkit prepare --dry-run
stackkit validate
stackkit resolve --inventory <observed-inventory.json> --output deploy/.stackkit/resolved-plan.json
stackkit generate
stackkit plan
stackkit apply
stackkit verify --json`,
}

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Agent-native helpers for StackKits automation",
	Long:  "Agent-native helpers emit install plans, self-checks, prompts, and MCP configuration without mutating a rollout.",
}

var agentInstallPlanCmd = &cobra.Command{
	Use:   "install-plan",
	Short: "Print a non-interactive StackKits install plan",
	RunE: func(cmd *cobra.Command, args []string) error {
		plan := buildAgentInstallPlan(agentKit, agentTarget, agentWorkspace)
		if agentInstallPlanJSON {
			return writeAgentJSON(cmd, plan)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Scenario: %s\nKit: %s\nTarget: %s\nWorkspace: %s\n\n", plan.Scenario, plan.Kit, plan.Target, plan.Workspace)
		for i, step := range plan.Commands {
			mutation := "read-only"
			if step.Mutation {
				mutation = "mutating"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d. %s\n   %s (%s)\n", i+1, step.Command, step.Purpose, mutation)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\nEvidence:\n")
		for _, item := range plan.Evidence {
			fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", item)
		}
		return nil
	},
}

var agentSelfCheckCmd = &cobra.Command{
	Use:   "self-check",
	Short: "Check local agent-facing StackKits prerequisites",
	RunE: func(cmd *cobra.Command, args []string) error {
		checks := buildAgentSelfChecks(agentServerURL)
		if agentSelfCheckJSON {
			return writeAgentJSON(cmd, map[string]any{"checks": checks})
		}
		for _, check := range checks {
			target := check.Target
			if target != "" {
				target = " " + target
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s%s: %s\n", check.Name, target, check.Message)
		}
		return nil
	},
}

var agentPromptCmd = &cobra.Command{
	Use:   "prompt [scenario]",
	Short: "Print a copy-ready StackKits agent prompt",
	Args: func(cmd *cobra.Command, args []string) error {
		if agentPromptList {
			return nil
		}
		if len(args) != 1 {
			return fmt.Errorf("scenario is required; use --list to see options")
		}
		if _, ok := agentPromptBodies[args[0]]; !ok {
			return fmt.Errorf("unknown scenario %q; use --list to see options", args[0])
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		names := agentPromptNames()
		if agentPromptList {
			if agentPromptJSON {
				return writeAgentJSON(cmd, map[string]any{"scenarios": names})
			}
			for _, name := range names {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), name); err != nil {
					return err
				}
			}
			return nil
		}
		name := args[0]
		body := agentPromptBodies[name]
		if agentPromptJSON {
			return writeAgentJSON(cmd, map[string]any{"scenario": name, "prompt": body})
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), body)
		return err
	},
}

var agentMCPConfigCmd = &cobra.Command{
	Use:   "mcp-config",
	Short: "Print one StackKits MCP client connection config",
	Long: `Print a ready-to-paste MCP client configuration named "stackkit".

For users this is one StackKits MCP connection. Locally it starts the
stackkit-mcp adapter; after install the same connector can also be reached as
stackkit-server /mcp when a protected endpoint is explicitly enabled.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mode := normalizeAgentMode(agentMode)
		serverURL := strings.TrimSpace(agentServerURL)
		client := strings.ToLower(strings.TrimSpace(agentClient))
		if client == "" {
			client = "generic"
		}
		switch client {
		case "codex":
			fmt.Fprintf(cmd.OutOrStdout(), "[mcp_servers.stackkit]\ncommand = \"stackkit-mcp\"\nargs = [\"--mode\", %q", mode)
			if serverURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), ", \"--server-url\", %q", serverURL)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "]"); err != nil {
				return err
			}
		case "claude":
			return writeAgentJSON(cmd, map[string]any{
				"mcpServers": map[string]any{
					"stackkit": mcpConfigMap(mode, serverURL),
				},
			})
		default:
			return writeAgentJSON(cmd, map[string]any{
				"name":   "stackkit",
				"config": mcpConfigMap(mode, serverURL),
			})
		}
		return nil
	},
}

func init() {
	agentCmd.AddCommand(agentInstallPlanCmd, agentSelfCheckCmd, agentPromptCmd, agentMCPConfigCmd)

	agentInstallPlanCmd.Flags().BoolVar(&agentInstallPlanJSON, "json", false, "Emit JSON")
	agentInstallPlanCmd.Flags().StringVar(&agentKit, "kit", "basement-kit", "StackKit to plan for")
	agentInstallPlanCmd.Flags().StringVar(&agentTarget, "target", "local", "Target kind: local, ssh, vm, or ci")
	agentInstallPlanCmd.Flags().StringVar(&agentWorkspace, "dir", "my-homelab", "Workspace directory")

	agentSelfCheckCmd.Flags().BoolVar(&agentSelfCheckJSON, "json", false, "Emit JSON")
	agentSelfCheckCmd.Flags().StringVar(&agentServerURL, "server-url", "http://localhost:8082", "stackkit-server URL")

	agentPromptCmd.Flags().BoolVar(&agentPromptJSON, "json", false, "Emit JSON")
	agentPromptCmd.Flags().BoolVar(&agentPromptList, "list", false, "List available prompt scenarios")

	agentMCPConfigCmd.Flags().StringVar(&agentClient, "client", "generic", "Client format: generic, codex, or claude")
	agentMCPConfigCmd.Flags().StringVar(&agentMode, "mode", "docs,local,server", "MCP modes")
	agentMCPConfigCmd.Flags().StringVar(&agentServerURL, "server-url", "http://localhost:8082", "stackkit-server URL")
}

func buildAgentInstallPlan(kit, target, workspace string) agentInstallPlan {
	kit = strings.TrimSpace(kit)
	if kit == "" {
		kit = "basement-kit"
	}
	target = strings.TrimSpace(target)
	if target == "" {
		target = "local"
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = "my-homelab"
	}
	if !stackspecadmission.RejectOperationalV1(version) {
		return legacyAgentInstallPlan(kit, target, workspace)
	}
	initCommand := "stackkit init " + kit + " --non-interactive"
	if kit == "cloud-kit" || kit == "modern-homelab" {
		initCommand += " --domain <domain-base>"
	}
	return agentInstallPlan{
		Scenario:  "basekit-autonomous-rollout",
		Kit:       kit,
		Target:    target,
		Workspace: workspace,
		Commands: []agentCommandStep{
			{Command: "stackkit version", Purpose: "require the exact native v0.7 candidate bundle; never fall back to public v0.6", Mutation: false},
			{Command: "mkdir -p " + workspace + " && cd " + workspace, Purpose: "create a clean workspace", Mutation: true},
			{Command: initCommand, Purpose: "materialize canonical StackSpec v2 from the embedded CUE authoring contract", Mutation: true},
			{Command: "stackkit prepare --dry-run", Purpose: "check host prerequisites without changing the host", Mutation: false},
			{Command: "stackkit validate", Purpose: "validate desired StackSpec v2 intent against the embedded CUE authority", Mutation: false},
			{Command: "stackkit resolve --inventory <observed-inventory.json> --output deploy/.stackkit/resolved-plan.json", Purpose: "bind desired intent to externally observed host Inventory", Mutation: true},
			{Command: "stackkit generate", Purpose: "render the exact authorized ResolvedPlan", Mutation: true},
			{Command: "stackkit plan", Purpose: "preview the exact persisted Architecture v2 plan", Mutation: false},
			{Command: "stackkit apply", Purpose: "apply only when the ResolvedPlan reports apply readiness", Mutation: true},
			{Command: "stackkit verify --json", Purpose: "verify the exact spec, plan, manifest, receipt, and outputs", Mutation: false},
		},
		Evidence: []string{
			"stackkit verify --json output",
			".stackkit/logs/<runID>.jsonl",
			".stackkit/runs/<runID>/summary.json",
			"manifest matching stackkit-agent-run-manifest.schema.json",
			"functional result matching stackkit-agent-functional-result.schema.json",
		},
		ReadinessNote: "StackSpec validity is not generation or apply readiness; the Inventory-bound ResolvedPlan is authoritative.",
	}
}

func legacyAgentInstallPlan(kit, target, workspace string) agentInstallPlan {
	return agentInstallPlan{
		Scenario: "basekit-autonomous-rollout", Kit: kit, Target: target, Workspace: workspace,
		Commands: []agentCommandStep{
			{Command: "curl -sSL https://base.stackkit.cc | sh", Purpose: "install the exact v0.6 compatibility bundle", Mutation: true},
			{Command: "mkdir -p " + workspace + " && cd " + workspace, Purpose: "create a clean workspace", Mutation: true},
			{Command: "stackkit init " + kit + " --non-interactive --admin-email <operator-email>", Purpose: "write the exact v0.6 StackSpec", Mutation: true},
			{Command: "stackkit prepare --dry-run", Purpose: "check host prerequisites", Mutation: false},
			{Command: "stackkit validate", Purpose: "validate v0.6 intent", Mutation: false},
			{Command: "stackkit generate --force", Purpose: "generate v0.6 artifacts", Mutation: true},
			{Command: "stackkit plan", Purpose: "preview v0.6 changes", Mutation: false},
			{Command: "stackkit apply", Purpose: "apply the approved v0.6 rollout", Mutation: true},
			{Command: "stackkit verify --http --json", Purpose: "produce v0.6 evidence", Mutation: false},
		},
		Evidence:      []string{"stackkit verify --http --json output", ".stackkit/logs/<runID>.jsonl", ".stackkit/runs/<runID>/summary.json"},
		ReadinessNote: "Exact v0.6 compatibility workflow; migrate to StackSpec v2 before native v0.7 operations.",
	}
}

func buildAgentSelfChecks(serverURL string) []agentSelfCheck {
	checks := []agentSelfCheck{
		binaryCheck("stackkit"),
		binaryCheck("stackkit-server"),
		binaryCheck("stackkit-mcp"),
		{Name: "server-url", Status: "info", Target: serverURL, Message: "used by the single StackKits MCP connection when local server tools are enabled"},
		{Name: "mcp-write-gate", Status: "info", Target: "STACKKIT_MCP_ALLOW_WRITE", Message: "write tools are disabled unless this variable is true"},
		{Name: "mcp-http-token", Status: "info", Target: "STACKKIT_MCP_TOKEN", Message: "required when management/write tools are exposed beyond loopback"},
	}
	return checks
}

func binaryCheck(name string) agentSelfCheck {
	path, err := exec.LookPath(name)
	if err != nil {
		return agentSelfCheck{Name: "binary", Status: "warn", Target: name, Message: name + " not found on PATH"}
	}
	return agentSelfCheck{Name: "binary", Status: "pass", Target: path, Message: name + " is on PATH"}
}

func agentPromptNames() []string {
	names := make([]string, 0, len(agentPromptBodies))
	for name := range agentPromptBodies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func normalizeAgentMode(mode string) string {
	parts := strings.Split(mode, ",")
	var out []string
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return "docs,local,server"
	}
	return strings.Join(out, ",")
}

func mcpConfigMap(mode, serverURL string) map[string]any {
	args := []string{"--mode", mode}
	if strings.TrimSpace(serverURL) != "" {
		args = append(args, "--server-url", strings.TrimSpace(serverURL))
	}
	return map[string]any{
		"name":    "stackkit",
		"command": "stackkit-mcp",
		"args":    args,
	}
}

func writeAgentJSON(cmd *cobra.Command, value any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
