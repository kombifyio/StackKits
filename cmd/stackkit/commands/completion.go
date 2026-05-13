package commands

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for stackkit.

To load completions:

Bash:
  $ source <(stackkit completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ stackkit completion bash > /etc/bash_completion.d/stackkit
  # macOS:
  $ stackkit completion bash > $(brew --prefix)/etc/bash_completion.d/stackkit

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it. Execute once:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ stackkit completion zsh > "${fpath[1]}/_stackkit"

  # You will need to start a new shell for this to take effect.

Fish:
  $ stackkit completion fish | source

  # To load completions for each session, execute once:
  $ stackkit completion fish > ~/.config/fish/completions/stackkit.fish

PowerShell:
  PS> stackkit completion powershell | Out-String | Invoke-Expression

  # To load completions for each session, execute once and source this file
  # from your PowerShell profile.
  PS> stackkit completion powershell > stackkit.ps1
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
		}
		return nil
	},
}
