package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// kitOptionalUseCases is the CUE-owned optional workload list shared by
// Basement, Cloud, and Modern Homelab (stackfile.cue workloads.optional).
// Default L3 selection matches the kit comment: Files, Photos, Vault.
func kitOptionalUseCases() []choice {
	return []choice{
		{Key: "files", Display: "Files", Description: "File storage and sharing (Cloudreve)", IsDefault: true},
		{Key: "photos", Display: "Photos", Description: "Photo gallery and memories (Immich)", IsDefault: true},
		{Key: "vault", Display: "Vault", Description: "Password management (Vaultwarden)", IsDefault: true},
		{Key: "media", Display: "Media", Description: "Media streaming (Jellyfin)"},
		{Key: "smart-home", Display: "Smart Home", Description: "Home automation (Home Assistant)"},
		{Key: "ai", Display: "Private AI", Description: "Local chat (Ollama and Open WebUI)"},
		{Key: "dev", Display: "Developer Platform", Description: "Private Git hosting (Gitea)"},
		{Key: "documents", Display: "Documents", Description: "Document OCR and search (Paperless-ngx)"},
		{Key: "mail", Display: "Mail", Description: "Webmail for your existing mailbox (Roundcube)"},
	}
}

func pickKitOptionalUseCases(ui *os.File) ([]string, error) {
	return selectMany(ui, "Select use cases", kitOptionalUseCases())
}

func pickKitOptionalUseCasesIfTerminal(ui *os.File) ([]string, bool, error) {
	in, closer, err := openInteractiveInput()
	if closer != nil {
		defer closer()
	}
	if err != nil || !term.IsTerminal(int(in.Fd())) {
		return nil, false, nil
	}
	selected, err := pickKitOptionalUseCases(ui)
	return selected, true, err
}

var (
	useCasesPrint bool
)

var useCasesCmd = &cobra.Command{
	Use:   "use-cases",
	Short: "Select optional kit use cases",
	Annotations: map[string]string{
		noDeployObservabilityAnnotation: "true",
	},
}

var useCasesPickCmd = &cobra.Command{
	Use:   "pick",
	Short: "Interactively select optional use cases",
	Long: `List the kit-optional use cases and toggle them in the terminal.

Arrow keys or j/k move the cursor. Space toggles the highlighted item.
Enter confirms. Files, Photos, and Vault start selected (kit L3 defaults).

--print writes the selected IDs as a comma-separated line on stdout so
installers can pass them to stackkit init --use-case. Prompts go to stderr.
When stdin is not a terminal, the current defaults are returned without a prompt.`,
	Example: `  # Choose optional use cases in the terminal
  stackkit use-cases pick

  # Print the selected IDs as one comma-separated line for stackkit init --use-case
  stackkit use-cases pick --print`,
	Args: cobra.NoArgs,
	Annotations: map[string]string{
		noDeployObservabilityAnnotation: "true",
	},
	RunE: runUseCasesPick,
}

func init() {
	useCasesPickCmd.Flags().BoolVar(&useCasesPrint, "print", false, "Write selected use-case IDs as a comma-separated line on stdout")
	useCasesCmd.AddCommand(useCasesPickCmd)
	rootCmd.AddCommand(useCasesCmd)
}

func runUseCasesPick(cmd *cobra.Command, _ []string) error {
	selected, err := pickKitOptionalUseCases(os.Stderr)
	if err != nil {
		return err
	}
	joined := strings.Join(selected, ",")
	if useCasesPrint {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), joined)
		return err
	}
	if joined == "" {
		printInfo("No optional use cases selected (core only)")
		return nil
	}
	printSuccess("Selected use cases: %s", joined)
	return nil
}
