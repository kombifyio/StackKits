package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// kitOptionalUseCases retains the legacy installer/init selection contract.
// Native --kit previews use the embedded CUE catalog instead.
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
	useCasesPrint     bool
	useCasesKit       string
	useCasesInventory string
	useCasesNode      string
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
Enter confirms. Without --kit, the legacy Files, Photos and Vault selection is used.

With --kit (Basement or Cloud), choices and defaults come from the embedded CUE kit catalog.
Each choice shows its standard profile minimum (CPU / RAM GiB / disk GiB).
The running numeric preview includes required Core and selected default alternatives.
It observes this host unless --inventory and --node select another target.
Data budgets, add-ons, profile changes and resident policy need a resolved plan.
This preview writes no configuration and grants no Apply admission.

--print writes the selected IDs as a comma-separated line on stdout so
installers can pass them to stackkit init --use-case. Prompts go to stderr.
When stdin is not a terminal, defaults are returned without a prompt; a kit preview
is still printed to stderr. Unknown facts are shown as ?, never zero.`,
	Example: `  # Choose optional use cases in the terminal
  stackkit use-cases pick

  # Print the selected IDs as one comma-separated line for stackkit init --use-case
  stackkit use-cases pick --print

  # Preview standard footprints and capacity on this target, from its kit catalog
  stackkit use-cases pick --kit basement-kit --print

  # Preview one explicitly selected Inventory node
  stackkit use-cases pick --kit cloud-kit --inventory inventory.yaml --node cloud-node --print`,
	Args: cobra.NoArgs,
	Annotations: map[string]string{
		noDeployObservabilityAnnotation: "true",
	},
	RunE: runUseCasesPick,
}

func init() {
	useCasesPickCmd.Flags().BoolVar(&useCasesPrint, "print", false, "Write selected use-case IDs as a comma-separated line on stdout")
	useCasesPickCmd.Flags().StringVar(&useCasesKit, "kit", "", "Preview main use cases admitted by this kit at standard profiles; observe this host by default")
	useCasesPickCmd.Flags().StringVar(&useCasesInventory, "inventory", "", "Read capacity from this Inventory instead of observing the local host; requires --kit and --node")
	useCasesPickCmd.Flags().StringVar(&useCasesNode, "node", "", "Explicit Inventory node for the kit capacity preview")
	useCasesCmd.AddCommand(useCasesPickCmd)
	rootCmd.AddCommand(useCasesCmd)
}

func runUseCasesPick(cmd *cobra.Command, _ []string) error {
	var selected []string
	var err error
	if useCasesKit != "" {
		selected, err = pickNativeUseCases(cmd)
	} else if useCasesInventory != "" || useCasesNode != "" {
		return fmt.Errorf("--inventory and --node require --kit")
	} else {
		selected, err = pickKitOptionalUseCases(os.Stderr)
	}
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
