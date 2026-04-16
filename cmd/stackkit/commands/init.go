package commands

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	initComputeTier    string
	initDomain         string
	initMode           string
	initOutputDir      string
	initForce          bool
	initNonInteractive bool
	initAdminEmail     string
)

type initDefaults struct {
	Context     models.NodeContext
	ComputeTier string
	Domain      string
	NetworkMode string
}

var initCmd = &cobra.Command{
	Use:   "init [stackkit]",
	Short: "Initialize a new deployment from a StackKit",
	Long: `Initialize a new deployment from a StackKit.

This command creates a new stack-spec.yaml file and sets up the deployment
directory structure based on the selected StackKit.

When run without arguments, an interactive wizard guides you through
StackKit selection, compute tier, domain, and email.

Examples:
  stackkit init                         Interactive mode
  stackkit init base-kit            Initialize with base-kit
  stackkit init ./my-stackkit           Initialize from local path
  stackkit init --non-interactive       Fail if arguments are missing`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initComputeTier, "compute-tier", "", "Compute tier (low, standard, high)")
	initCmd.Flags().StringVar(&initDomain, "domain", "", "Domain override for the generated stack spec")
	initCmd.Flags().StringVar(&initMode, "mode", "", "Deployment mode (simple, advanced)")
	initCmd.Flags().StringVarP(&initOutputDir, "output", "o", "deploy", "Output directory for generated files")
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "Overwrite existing files")
	initCmd.Flags().BoolVar(&initNonInteractive, "non-interactive", false, "Run in non-interactive mode (fail if input is required)")
	initCmd.Flags().StringVar(&initAdminEmail, "admin-email", "", "Admin email for login accounts (TinyAuth, Dokploy, Kuma)")
}

// selectStackKit prompts the user to pick a StackKit or returns the one
// already provided via CLI args. Returns the selected name.
func selectStackKit(p *prompter, availableKits []*models.StackKit, wd string) (string, error) {
	if len(availableKits) == 0 {
		return "", fmt.Errorf("no StackKits found in %s", wd)
	}

	var choices []choice
	for _, sk := range availableKits {
		choices = append(choices, choice{
			Key:         sk.Metadata.Name,
			Display:     sk.Metadata.DisplayName,
			Description: sk.Metadata.Description,
		})
	}
	if len(choices) > 0 {
		choices[0].IsDefault = true
	}

	selected, err := p.selectOne("Select a StackKit:", choices)
	if err != nil {
		return "", fmt.Errorf("stackkit selection: %w", err)
	}
	return selected, nil
}

// selectMode prompts for a deployment mode or applies the default.
func selectMode(p *prompter, stackkit *models.StackKit) (string, error) {
	if initMode == "" && p != nil {
		var modeChoices []choice
		if stackkit.Modes.Simple.Name != "" {
			modeChoices = append(modeChoices, choice{
				Key:         "simple",
				Display:     stackkit.Modes.Simple.Name,
				Description: stackkit.Modes.Simple.Description,
				IsDefault:   stackkit.Modes.Simple.Default,
			})
		}
		if stackkit.Modes.Advanced.Name != "" {
			modeChoices = append(modeChoices, choice{
				Key:         "advanced",
				Display:     stackkit.Modes.Advanced.Name,
				Description: stackkit.Modes.Advanced.Description,
				IsDefault:   stackkit.Modes.Advanced.Default,
			})
		}
		if len(modeChoices) > 1 {
			selected, err := p.selectOne("Select deployment mode:", modeChoices)
			if err != nil {
				return "", fmt.Errorf("mode selection: %w", err)
			}
			initMode = selected
		} else if len(modeChoices) == 1 {
			initMode = modeChoices[0].Key
		}
	}
	if initMode == "" {
		initMode = "simple"
	}
	return initMode, nil
}

// selectComputeTier prompts for a compute tier or applies the default.
func selectComputeTier(p *prompter, stackkit *models.StackKit, defaults initDefaults) (string, error) {
	if initComputeTier == "" && p != nil {
		tierChoices := []choice{
			{Key: models.ComputeTierLow, Display: "Low", Description: fmt.Sprintf("Minimum: %d CPU / %d GB RAM / %d GB disk",
				stackkit.Requirements.Minimum.CPU, stackkit.Requirements.Minimum.RAM, stackkit.Requirements.Minimum.Disk), IsDefault: defaults.ComputeTier == models.ComputeTierLow},
			{Key: models.ComputeTierStandard, Display: "Standard", Description: "Balanced resources for typical workloads", IsDefault: defaults.ComputeTier == models.ComputeTierStandard},
			{Key: models.ComputeTierHigh, Display: "High", Description: fmt.Sprintf("Recommended: %d CPU / %d GB RAM / %d GB disk",
				stackkit.Requirements.Recommended.CPU, stackkit.Requirements.Recommended.RAM, stackkit.Requirements.Recommended.Disk), IsDefault: defaults.ComputeTier == models.ComputeTierHigh},
		}
		selected, err := p.selectOne("Select compute tier:", tierChoices)
		if err != nil {
			return "", fmt.Errorf("compute tier selection: %w", err)
		}
		initComputeTier = selected
	}
	if initComputeTier == "" {
		initComputeTier = defaults.ComputeTier
	}
	return initComputeTier, nil
}

// promptOptionalConfig asks for domain, email, and admin email when running interactively.
// When running in kombify Cloud context with KOMBIFY_USER_EMAIL set, emails are
// auto-filled and the user is not prompted for them.
func promptOptionalConfig(p *prompter, defaults initDefaults) (domain, email, adminEmail string) {
	if initDomain != "" {
		domain = initDomain
	}

	// Priority 1: explicit --admin-email flag
	if initAdminEmail != "" {
		adminEmail = initAdminEmail
	}

	// Priority 2: kombify Cloud injects KOMBIFY_USER_EMAIL
	if adminEmail == "" {
		if cloudEmail := netenv.GetCloudUserEmail(); cloudEmail != "" {
			adminEmail = cloudEmail
			printInfo("Using Auth0 account email: %s", adminEmail)
		}
	}

	// Non-interactive or no TTY: return what we have (flag/env), no prompts
	if p == nil || initNonInteractive {
		if domain == "" {
			domain = defaults.Domain
		}
		return domain, adminEmail, adminEmail
	}

	fmt.Println()
	printInfo("Optional configuration (press Enter to skip):")
	fmt.Println()

	// Only prompt for admin email if not already set via flag or Cloud env
	if adminEmail == "" {
		a, err := p.inputString("Admin email (for login accounts)", "")
		if err == nil {
			adminEmail = a
		}
	}

	domainDefault := defaults.Domain
	if domain != "" {
		domainDefault = domain
	}
	d, err := p.inputString("Domain (e.g. home.example.com)", domainDefault)
	if err == nil {
		domain = d
	}

	// Let's Encrypt email defaults to admin email; skip prompt if already set via Cloud
	if adminEmail != "" && netenv.GetCloudUserEmail() != "" {
		// Cloud context: LE email = Auth0 email, no prompt needed
		email = adminEmail
	} else {
		defaultEmail := adminEmail
		e, err := p.inputString("Email (for Let's Encrypt certificates)", defaultEmail)
		if err == nil {
			email = e
		}
	}

	return domain, email, adminEmail
}

func resolveInitDefaults(currentDomain string) initDefaults {
	spec := &models.StackSpec{}
	if contextFlag != "" {
		spec.Context = contextFlag
	}

	caps := loadDockerCapabilities()
	ctx := resolveNodeContextFromCaps(caps, spec)

	computeTier := initComputeTier
	if computeTier == "" {
		if caps != nil && caps.CPUCores > 0 && caps.MemoryGB > 0 {
			computeTier = autoDetectComputeTier(caps.CPUCores, caps.MemoryGB)
		} else {
			computeTier = models.ComputeTierStandard
		}
	}

	domain, _ := netenv.SuggestDomainForContext(ctx, currentDomain)
	if domain == "" {
		domain = currentDomain
	}

	networkMode := "local"
	if netenv.NodeContextIsCloud(ctx) {
		networkMode = "public"
	}

	return initDefaults{
		Context:     ctx,
		ComputeTier: computeTier,
		Domain:      domain,
		NetworkMode: networkMode,
	}
}

// applyNonInteractiveDefaults fills in missing flag values when running
// without a TTY. Returns an error if the stackkit name is missing.
func applyNonInteractiveDefaults(stackkitName string, availableKits []*models.StackKit) error {
	if stackkitName == "" {
		return fmt.Errorf("stackkit name required in non-interactive mode\n\nAvailable StackKits: %v", stackKitNames(availableKits))
	}
	if initComputeTier == "" {
		initComputeTier = models.ComputeTierStandard
	}
	if initMode == "" {
		initMode = "simple"
	}
	return nil
}

// loadStackKit finds and loads a StackKit definition, falling back to the
// parent directory for development layouts.
func loadStackKit(loader *config.Loader, stackkitName, wd string) (*config.Loader, *models.StackKit, error) {
	stackkitDir, err := loader.FindStackKitDir(stackkitName)
	if err != nil {
		parentDir := filepath.Dir(wd)
		loader = config.NewLoader(parentDir)
		stackkitDir, err = loader.FindStackKitDir(stackkitName)
		if err != nil {
			return nil, nil, fmt.Errorf("stackkit '%s' not found: %w", stackkitName, err)
		}
	}

	stackkitPath := filepath.Join(stackkitDir, "stackkit.yaml")
	stackkit, err := loader.LoadStackKit(stackkitPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load stackkit: %w", err)
	}
	return loader, stackkit, nil
}

// resolveSpecPath returns the absolute path for the spec file and checks
// whether it already exists (unless --force is set).
func resolveSpecPath(wd string) (string, error) {
	specPath := specFile
	if !filepath.IsAbs(specFile) {
		specPath = filepath.Join(wd, specFile)
	}
	if _, err := os.Stat(specPath); err == nil && !initForce {
		return "", fmt.Errorf("spec file already exists: %s (use --force to overwrite)", specPath)
	}
	return specPath, nil
}

// writeSpecAndOutput creates the spec YAML and the output directory.
func writeSpecAndOutput(loader *config.Loader, spec *models.StackSpec, specPath, wd string) error {
	if err := loader.SaveStackSpec(spec, specPath); err != nil {
		return fmt.Errorf("failed to save spec file: %w", err)
	}
	printSuccess("Created spec file: %s", specPath)

	outputPath := filepath.Join(wd, initOutputDir)
	if err := os.MkdirAll(outputPath, 0750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	printSuccess("Created output directory: %s", outputPath)
	return nil
}

// printInitSummary displays the final configuration and next-step hints.
func printInitSummary(stackkitName, mode, computeTier string, ctx models.NodeContext, domain, email string) {
	fmt.Println()
	printInfo("Configuration:")
	fmt.Printf("  %s: %s\n", bold("StackKit"), stackkitName)
	fmt.Printf("  %s: %s\n", bold("Mode"), mode)
	fmt.Printf("  %s: %s\n", bold("Compute"), computeTier)
	fmt.Printf("  %s: %s\n", bold("Context"), netenv.FormatNodeContext(ctx))
	if domain != "" {
		fmt.Printf("  %s: %s\n", bold("Domain"), domain)
	}
	if email != "" {
		fmt.Printf("  %s: %s\n", bold("Email"), email)
	}

	fmt.Println()
	printInfo("Next steps:")
	fmt.Printf("  1. Review configuration:  %s\n", cyan("cat "+specFile))
	fmt.Printf("  2. Prepare system:        %s\n", cyan("stackkit prepare --spec "+specFile))
	fmt.Printf("  3. Preview changes:       %s\n", cyan("stackkit plan"))
	fmt.Printf("  4. Deploy:                %s\n", cyan("stackkit apply"))
}

// resolveStackKitName determines the StackKit name from CLI args, applying
// non-interactive defaults or prompting the user as needed. It also returns
// a prompter when interactive input is available.
func resolveStackKitName(args []string, availableKits []*models.StackKit, wd string) (string, *prompter, error) {
	stackkitName := ""
	if len(args) > 0 {
		stackkitName = args[0]
	}

	needsInteractive := stackkitName == "" || initComputeTier == "" || initMode == ""

	if needsInteractive && initNonInteractive {
		if err := applyNonInteractiveDefaults(stackkitName, availableKits); err != nil {
			return "", nil, err
		}
	}

	var p *prompter
	if stackkitName == "" || (needsInteractive && !initNonInteractive) {
		p = newPrompter()
	}

	if stackkitName == "" {
		selected, err := selectStackKit(p, availableKits, wd)
		if err != nil {
			return "", nil, err
		}
		stackkitName = selected
	}

	return stackkitName, p, nil
}

// gatherInitChoices prompts (or defaults) all user choices for the init wizard.
func gatherInitChoices(p *prompter, stackkit *models.StackKit, defaults initDefaults) (mode, computeTier, domain, email, adminEmail string, err error) {
	mode, err = selectMode(p, stackkit)
	if err != nil {
		return
	}

	computeTier, err = selectComputeTier(p, stackkit, defaults)
	if err != nil {
		return
	}

	domain, email, adminEmail = promptOptionalConfig(p, defaults)
	return
}

func runInit(cmd *cobra.Command, args []string) error {
	wd := getWorkDir()

	loader := config.NewLoader(wd)
	availableKits, err := discoverStackKits(loader, wd)
	if err != nil {
		return fmt.Errorf("failed to discover StackKits: %w", err)
	}

	stackkitName, p, err := resolveStackKitName(args, availableKits, wd)
	if err != nil {
		return err
	}

	deployLog.Event("init.stackkit_selected",
		slog.String("name", stackkitName),
	)

	printInfo("Initializing StackKit: %s", bold(stackkitName))

	loader, stackkit, err := loadStackKit(loader, stackkitName, wd)
	if err != nil {
		return err
	}
	printSuccess("Found StackKit: %s v%s", stackkit.Metadata.Name, stackkit.Metadata.Version)
	deployLog.Event("init.stackkit_loaded",
		slog.String("name", stackkit.Metadata.Name),
		slog.String("version", stackkit.Metadata.Version),
	)

	defaults := resolveInitDefaults(initDomain)

	mode, computeTier, domain, email, adminEmail, err := gatherInitChoices(p, stackkit, defaults)
	if err != nil {
		return err
	}
	deployLog.Event("init.choices",
		slog.String("mode", mode),
		slog.String("compute_tier", computeTier),
		slog.String("domain", domain),
		slog.String("email", email),
	)

	specPath, err := resolveSpecPath(wd)
	if err != nil {
		return err
	}

	spec := &models.StackSpec{
		Name:       filepath.Base(wd),
		StackKit:   stackkitName,
		Mode:       mode,
		Context:    string(defaults.Context),
		Domain:     domain,
		Email:      email,
		AdminEmail: adminEmail,
		Network: models.NetworkSpec{
			Mode:   defaults.NetworkMode,
			Subnet: "172.20.0.0/16",
		},
		Compute: models.ComputeSpec{
			Tier: computeTier,
		},
		SSH: models.SSHSpec{
			User: "root",
			Port: 22,
		},
	}
	deployLog.Event("init.spec_created",
		slog.String("name", spec.Name),
		slog.String("stackkit", spec.StackKit),
		slog.String("mode", spec.Mode),
		slog.String("domain", spec.Domain),
		slog.String("compute_tier", spec.Compute.Tier),
		slog.String("network_mode", spec.Network.Mode),
		slog.String("subnet", spec.Network.Subnet),
	)

	if err := writeSpecAndOutput(loader, spec, specPath, wd); err != nil {
		return err
	}
	deployLog.Event("init.spec_written",
		slog.String("spec_path", specPath),
	)

	printInitSummary(stackkitName, mode, computeTier, defaults.Context, domain, email)
	return nil
}

// discoverStackKits scans the working directory (and parent) for stackkit.yaml files.
func discoverStackKits(loader *config.Loader, wd string) ([]*models.StackKit, error) {
	kits, err := loader.DiscoverStackKits(wd, filepath.Dir(wd))
	if err != nil {
		return nil, err
	}
	sort.Slice(kits, func(i, j int) bool { return kits[i].Metadata.Name < kits[j].Metadata.Name })
	return kits, nil
}

// stackKitNames returns a sorted list of StackKit names.
func stackKitNames(kits []*models.StackKit) []string {
	var names []string
	for _, k := range kits {
		names = append(names, k.Metadata.Name)
	}
	return names
}

