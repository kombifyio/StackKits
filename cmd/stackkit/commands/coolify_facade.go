package commands

import (
	"github.com/kombifyio/stackkits/internal/extensions/coolify"
	"github.com/spf13/cobra"
)

func newCoolifyCommand() *cobra.Command {
	root := &cobra.Command{Use: "coolify", Short: "Inspect the Coolify integration"}
	facade := &cobra.Command{Use: "facade", Short: "Inspect agent authorization policy"}
	root.AddCommand(facade)
	scopes := []string{coolify.ScopeRead, coolify.ScopeWrite, coolify.ScopeDeploy}
	describe := &cobra.Command{Use: "describe", Short: "Describe available tools and token scopes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return writeCommandResult(cmd, "coolify facade describe", map[string]any{"project": coolify.AgentProjectName, "tools": coolify.AgentToolNames(), "agentToken": map[string]any{"scopes": scopes}})
	}}
	var tool, project string
	call := &cobra.Command{Use: "call", Short: "Evaluate a tool request without executing it", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		policy, err := coolify.NewAgentFacade(coolify.AgentFacadeConfig{ProjectName: coolify.AgentProjectName, ProjectUUID: coolify.AgentProjectName, TokenScopes: scopes})
		if err != nil {
			return err
		}
		decision, err := policy.AuthorizeContext(cmd.Context(), coolify.ToolCallRequest{Tool: tool, ProjectUUID: project})
		result := "denied"
		if err == nil && decision.Allowed {
			result = "allowed"
		}
		return writeCommandResult(cmd, "coolify facade call", map[string]any{"decision": result, "reason": decision.Reason})
	}}
	call.Flags().StringVar(&tool, "tool", "", "Requested tool")
	call.Flags().StringVar(&project, "project", "", "Requested project")
	_ = call.MarkFlagRequired("tool")
	_ = call.MarkFlagRequired("project")
	facade.PersistentFlags().Bool("json", false, "Emit machine-readable policy")
	facade.AddCommand(describe, call)
	return root
}
