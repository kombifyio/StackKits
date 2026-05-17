package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentInstallPlanJSON(t *testing.T) {
	out, err := executeCommand("agent", "install-plan", "--json")
	require.NoError(t, err)

	var plan agentInstallPlan
	require.NoError(t, json.Unmarshal([]byte(out), &plan))
	assert.Equal(t, "basekit-autonomous-rollout", plan.Scenario)
	assert.Equal(t, "base-kit", plan.Kit)
	assert.Contains(t, plan.ReadinessNote, "BaseKit is release-ready")
	require.NotEmpty(t, plan.Commands)
	assert.Contains(t, plan.Commands[len(plan.Commands)-1].Command, "stackkit verify --http --json")
}

func TestAgentPromptListAndPrompt(t *testing.T) {
	out, err := executeCommand("agent", "prompt", "--list")
	require.NoError(t, err)
	assert.Contains(t, out, "basekit-autonomous-rollout")

	agentPromptList = false
	require.NoError(t, agentPromptCmd.Flags().Set("list", "false"))

	prompt, err := executeCommand("agent", "prompt", "basekit-autonomous-rollout")
	require.NoError(t, err)
	assert.Contains(t, prompt, "stackkit verify --http --json")
	assert.Contains(t, prompt, "http://base.home.localhost")
}

func TestAgentMCPConfigCodex(t *testing.T) {
	out, err := executeCommand("agent", "mcp-config", "--client", "codex", "--mode", "docs,local")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, "[mcp_servers.stackkit]"))
	assert.Contains(t, out, "stackkit-mcp")
	assert.Contains(t, out, "docs,local")
}
