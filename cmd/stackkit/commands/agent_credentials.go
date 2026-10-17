package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/kombifyio/stackkits/internal/mcpagents"
	"github.com/spf13/cobra"
)

// Owner-side management of per-agent MCP credentials and per-call write
// approvals. These commands run on the server host in the owner's shell; the
// MCP surface deliberately has no tool for them (an agent must not mint,
// widen or approve its own access), see the standaloneoperations exceptions.

var (
	agentCredentialName      string
	agentCredentialTokenFile string
	agentCredentialJSON      bool
)

var agentCredentialCmd = &cobra.Command{
	Use:   "credential",
	Short: "Mint, list, rotate and revoke per-agent MCP credentials",
	Long: `Each agent that attaches to the StackKits MCP endpoint of this server holds its own credential.
The server stores only a digest, shows a token exactly once, and re-checks the credential on every request,
so revoking one denies that agent on its next call without touching the others.`,
}

var agentCredentialMintCmd = &cobra.Command{
	Use:   "mint --name <agent>",
	Short: "Create a credential for one agent",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		agent, token, err := mcpagents.Open(getWorkDir()).Mint(agentCredentialName)
		if err != nil {
			return err
		}
		return emitCredential(cmd, agent, token)
	},
}

var agentCredentialRotateCmd = &cobra.Command{
	Use:   "rotate <agent-id>",
	Short: "Replace an agent's token; the old token stops working at once",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent, token, err := mcpagents.Open(getWorkDir()).Rotate(args[0])
		if err != nil {
			return err
		}
		return emitCredential(cmd, agent, token)
	},
}

var agentCredentialRevokeCmd = &cobra.Command{
	Use:   "revoke <agent-id>",
	Short: "Deny an agent from its next request on",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := mcpagents.Open(getWorkDir()).Revoke(args[0]); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "revoked %s\n", args[0])
		return err
	},
}

var agentCredentialListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agent credentials (never their tokens)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		agents, err := mcpagents.Open(getWorkDir()).List()
		if err != nil {
			return err
		}
		if agentCredentialJSON {
			return writeAgentJSON(cmd, agents)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tNAME\tSTATE\tCREATED")
		for _, agent := range agents {
			state := "active"
			if !agent.Active() {
				state = "revoked"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", agent.ID, agent.Name, state, agent.CreatedAt.Format("2006-01-02 15:04"))
		}
		return w.Flush()
	},
}

var agentApprovalCmd = &cobra.Command{
	Use:   "approval",
	Short: "Answer per-call write approval requests from attached agents",
	Long: `A mutating MCP call from an agent with its own credential is denied until you approve that exact call.
The denial names an approval request; approving it lets the agent repeat the identical call once, within 15 minutes.`,
}

var agentApprovalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List open and recent approval requests",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		approvals, err := mcpagents.Open(getWorkDir()).Approvals()
		if err != nil {
			return err
		}
		if agentCredentialJSON {
			return writeAgentJSON(cmd, approvals)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tAGENT\tOPERATION\tSTATE\tEXPIRES\tCALL")
		for _, a := range approvals {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, a.AgentID, a.Operation, a.State, a.ExpiresAt.Format("15:04:05"), a.Summary)
		}
		return w.Flush()
	},
}

var agentApprovalApproveCmd = &cobra.Command{
	Use:   "approve <approval-id>",
	Short: "Allow the agent to run the requested call once",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return decideApproval(cmd, args[0], true)
	},
}

var agentApprovalDenyCmd = &cobra.Command{
	Use:   "deny <approval-id>",
	Short: "Refuse the requested call",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return decideApproval(cmd, args[0], false)
	},
}

func decideApproval(cmd *cobra.Command, id string, approve bool) error {
	decided, err := mcpagents.Open(getWorkDir()).Decide(id, approve)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s for %s (%s)\n", decided.State, decided.ID, decided.AgentID, decided.Operation)
	return err
}

// emitCredential shows the token exactly once: into a private file when one is
// named, otherwise on stdout with a notice on stderr.
func emitCredential(cmd *cobra.Command, agent mcpagents.Agent, token string) error {
	if agentCredentialTokenFile != "" {
		path, err := filepath.Abs(agentCredentialTokenFile)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = mcpagents.Open(getWorkDir()).Revoke(agent.ID)
			return fmt.Errorf("token file: %w (the new credential %s was revoked)", err, agent.ID)
		}
		_, writeErr := f.WriteString(token + "\n")
		if closeErr := f.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			_ = mcpagents.Open(getWorkDir()).Revoke(agent.ID)
			return fmt.Errorf("token file: %w (the new credential %s was revoked)", writeErr, agent.ID)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "agent %s (%s): token written to %s\n", agent.ID, agent.Name, path)
		return err
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "The token is shown once and cannot be recovered; store it in the agent's secret store.")
	if agentCredentialJSON {
		return writeAgentJSON(cmd, map[string]any{"agent": agent, "token": token})
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "agent: %s (%s)\ntoken: %s\n", agent.ID, agent.Name, token)
	return err
}

func init() {
	agentCmd.AddCommand(agentCredentialCmd, agentApprovalCmd)
	agentCredentialCmd.AddCommand(agentCredentialMintCmd, agentCredentialRotateCmd, agentCredentialRevokeCmd, agentCredentialListCmd)
	agentApprovalCmd.AddCommand(agentApprovalListCmd, agentApprovalApproveCmd, agentApprovalDenyCmd)

	agentCredentialMintCmd.Flags().StringVar(&agentCredentialName, "name", "", "Agent name (for example claude-laptop)")
	_ = agentCredentialMintCmd.MarkFlagRequired("name")
	for _, c := range []*cobra.Command{agentCredentialMintCmd, agentCredentialRotateCmd} {
		c.Flags().StringVar(&agentCredentialTokenFile, "token-file", "", "Write the token to this new private file instead of stdout")
	}
	for _, c := range []*cobra.Command{agentCredentialMintCmd, agentCredentialRotateCmd, agentCredentialListCmd, agentApprovalListCmd} {
		c.Flags().BoolVar(&agentCredentialJSON, "json", false, "Emit JSON")
	}
}
