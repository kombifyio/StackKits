package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/spf13/cobra"
)

var addonCmd = &cobra.Command{
	Use:   "addon",
	Short: "Manage composable add-ons",
	Long: `Manage composable add-ons for your StackKit deployment.

Add-ons provide optional capabilities like monitoring, backup, VPN overlay,
media server, and more. They are declared in your stack-spec.yaml and
resolved at generate time.

Examples:
  stackkit addon list                List available add-ons
  stackkit addon add monitoring      Add monitoring add-on to spec
  stackkit addon remove monitoring   Remove monitoring add-on from spec`,
}

var addonListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available add-ons",
	Long: `List all available add-ons that can be added to your deployment.

Shows name, description, layer, and compatibility information.`,
	RunE: runAddonList,
}

var addonAddCmd = &cobra.Command{
	Use:   "add <addon-name>",
	Short: "Add an add-on to the stack specification",
	Long: `Add a composable add-on to your stack-spec.yaml.

This updates your spec file to include the add-on. Run 'stackkit generate'
afterwards to regenerate the deployment files.

Examples:
  stackkit addon add monitoring
  stackkit addon add backup
  stackkit addon add vpn-overlay`,
	Args: cobra.ExactArgs(1),
	RunE: runAddonAdd,
}

var addonRemoveCmd = &cobra.Command{
	Use:   "remove <addon-name>",
	Short: "Remove an add-on from the stack specification",
	Long: `Remove a composable add-on from your stack-spec.yaml.

Examples:
  stackkit addon remove monitoring`,
	Args: cobra.ExactArgs(1),
	RunE: runAddonRemove,
}

func init() {
	addonCmd.AddCommand(addonListCmd)
	addonCmd.AddCommand(addonAddCmd)
	addonCmd.AddCommand(addonRemoveCmd)
}

// addonInfo represents discovered add-on metadata
type addonInfo struct {
	Name        string
	DisplayName string
	Layer       string
	Description string
}

func runAddonList(cmd *cobra.Command, args []string) error {
	wd := getWorkDir()

	addons, err := discoverAddons(wd)
	if err != nil {
		return fmt.Errorf("failed to discover add-ons: %w", err)
	}

	if len(addons) == 0 {
		printWarning("No add-ons found")
		return nil
	}

	// Check which are already in the spec
	activeAddons := map[string]bool{}
	loader := config.NewLoader(wd)
	spec, err := loader.LoadStackSpec(specFile)
	if err == nil && spec != nil {
		for _, a := range spec.Addons {
			activeAddons[a] = true
		}
	}

	// Print table
	fmt.Printf("\n%-20s %-15s %-8s %s\n", "NAME", "LAYER", "STATUS", "DESCRIPTION")
	fmt.Printf("%-20s %-15s %-8s %s\n", "----", "-----", "------", "-----------")

	for _, a := range addons {
		status := "  "
		if activeAddons[a.Name] {
			status = green("active")
		}
		fmt.Printf("%-20s %-15s %-8s %s\n", a.Name, a.Layer, status, a.Description)
	}
	fmt.Println()

	return nil
}

func runAddonAdd(cmd *cobra.Command, args []string) error {
	addonName := args[0]
	wd := getWorkDir()

	// Verify addon exists
	addons, err := discoverAddons(wd)
	if err != nil {
		return fmt.Errorf("failed to discover add-ons: %w", err)
	}

	found := false
	for _, a := range addons {
		if a.Name == addonName {
			found = true
			break
		}
	}
	if !found {
		var names []string
		for _, a := range addons {
			names = append(names, a.Name)
		}
		return fmt.Errorf("add-on '%s' not found. Available: %s", addonName, strings.Join(names, ", "))
	}

	// Load spec
	loader := config.NewLoader(wd)
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		return fmt.Errorf("failed to load spec: %w (run 'stackkit init' first)", err)
	}

	// Check if already added
	for _, a := range spec.Addons {
		if a == addonName {
			printWarning("Add-on '%s' is already in the spec", addonName)
			return nil
		}
	}

	// Add addon
	spec.Addons = append(spec.Addons, addonName)
	sort.Strings(spec.Addons)

	// Save spec
	specPath := specFile
	if !filepath.IsAbs(specFile) {
		specPath = filepath.Join(wd, specFile)
	}
	if err := loader.SaveStackSpec(spec, specPath); err != nil {
		return fmt.Errorf("failed to save spec: %w", err)
	}

	printSuccess("Added add-on: %s", bold(addonName))
	printInfo("Run %s to regenerate deployment files", cyan("stackkit generate --force"))

	return nil
}

func runAddonRemove(cmd *cobra.Command, args []string) error {
	addonName := args[0]
	wd := getWorkDir()

	// Load spec
	loader := config.NewLoader(wd)
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		return fmt.Errorf("failed to load spec: %w", err)
	}

	// Find and remove
	found := false
	var newAddons []string
	for _, a := range spec.Addons {
		if a == addonName {
			found = true
			continue
		}
		newAddons = append(newAddons, a)
	}

	if !found {
		return fmt.Errorf("add-on '%s' is not in the spec", addonName)
	}

	spec.Addons = newAddons

	// Save spec
	specPath := specFile
	if !filepath.IsAbs(specFile) {
		specPath = filepath.Join(wd, specFile)
	}
	if err := loader.SaveStackSpec(spec, specPath); err != nil {
		return fmt.Errorf("failed to save spec: %w", err)
	}

	printSuccess("Removed add-on: %s", bold(addonName))
	printInfo("Run %s to regenerate deployment files", cyan("stackkit generate --force"))

	return nil
}

// discoverAddons scans the addons/ directory for available add-ons.
// It reads the _addon metadata from each addon.cue file.
func discoverAddons(wd string) ([]addonInfo, error) {
	var addons []addonInfo
	seen := make(map[string]bool)

	scanDir := func(baseDir string) {
		addonsDir := filepath.Join(baseDir, "addons")
		entries, err := os.ReadDir(addonsDir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() || seen[entry.Name()] {
				continue
			}
			addonCue := filepath.Join(addonsDir, entry.Name(), "addon.cue")
			if _, err := os.Stat(addonCue); err != nil {
				continue
			}

			info := parseAddonMetadata(addonCue, entry.Name())
			seen[entry.Name()] = true
			addons = append(addons, info)
		}
	}

	scanDir(wd)
	scanDir(filepath.Dir(wd))

	sort.Slice(addons, func(i, j int) bool { return addons[i].Name < addons[j].Name })
	return addons, nil
}

// parseAddonMetadata extracts _addon metadata from a CUE file using simple text parsing.
// This avoids requiring the full CUE evaluator for a discovery listing.
func parseAddonMetadata(path string, fallbackName string) addonInfo {
	info := addonInfo{
		Name:        fallbackName,
		DisplayName: fallbackName,
		Layer:       "UNKNOWN",
		Description: "",
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return info
	}

	// Simple extraction from CUE source text
	// This is intentionally basic — full CUE evaluation would be heavier
	content := string(data)
	info.Name = extractCUEField(content, "name")
	if info.Name == "" {
		info.Name = fallbackName
	}
	if dn := extractCUEField(content, "displayName"); dn != "" {
		info.DisplayName = dn
	}
	if layer := extractCUEField(content, "layer"); layer != "" {
		info.Layer = layer
	}
	if desc := extractCUEField(content, "description"); desc != "" {
		info.Description = desc
	}

	return info
}

// extractCUEField does a simple string-based extraction of a field value from CUE source.
func extractCUEField(content, field string) string {
	// Look for pattern: field: "value" or field:  "value"
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		prefix := field + ":"
		if strings.HasPrefix(trimmed, prefix) {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			// Remove surrounding quotes
			rest = strings.Trim(rest, "\"")
			return rest
		}
	}
	return ""
}