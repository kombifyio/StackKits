package commands

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/internal/stackspecadmission"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	initAPIVersion                string
	initComputeTier               string
	initModuleComputeProfiles     []string
	initModuleStorageProfiles     []string
	initModuleAcceleratorProfiles []string
	initUseCaseAlternatives       []string
	initHardwareProfile           string
	initName                      string
	initDomain                    string
	initMode                      string
	initOutputDir                 string
	initForce                     bool
	initExpectedSpecHash          string
	initNonInteractive            bool
	// Native v2 authoring overrides (validated by the CUE authority).
	initPlatform           string
	initEnableCapabilities []string
	initUseCases           []string
	initAdminEmail         string
	initLocalDNS           bool
	initLocalName          string
	initServiceProfile     string

	// Phase 1 owner-breakglass provisioning flags. Wired into init.go's
	// flag block; consumed by resolveOwnerBootstrapConfig in init_owner.go
	// and by the apply-bootstrap path.
	initClusterMode             string
	initOwnerBootstrapMode      string
	initOwnerSource             string
	initOwnerEmail              string
	initOwnerUsername           string
	initOwnerDisplayName        string
	initCloudOIDCIssuer         string
	initCloudOIDCClientID       string
	initCloudOIDCSecretRef      string
	initCloudOIDCForeignSubject string
	initRecoveryPassphraseHash  string
	initRecoveryMaterialRef     string
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

Native Architecture v2 init is CUE-owned. Without a Kit argument it selects
basement-kit. --owner-source=local establishes local owner custody plus the
CUE-owned PocketID/step-ca and Site/node/execution-channel projection.

Native v2alpha2 selects each module's compute profile independently. Required
and optional workloads use explicit --use-case-alternative selections. The
--compute-tier flag is available only with --api-version stackkit/v2alpha1,
the explicitly marked legacy graph adapter. Mode, local-path, local-DNS, service, cluster, cloud-owner, and
output switches remain available only to an explicitly versioned v0.6
compatibility binary and are rejected by development and v0.7+ builds.

Examples:
  stackkit init basement-kit --use-case-alternative basement-core=standalone-lite --module-compute-profile stackkits-basement-core-lite-runtime=low
  stackkit init basement-kit --api-version stackkit/v2alpha1 --compute-tier standard
  stackkit init ./basement-kit          v0.6 compatibility only: local definition path
  stackkit init basement-kit --use-case-alternative basement-core=standalone --module-compute-profile stackkits-basement-core-runtime=standard --owner-source=local --non-interactive`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initName, "name", "", "Deployment contract ID (defaults to a normalized working-directory name)")
	initCmd.Flags().StringVar(&initAPIVersion, "api-version", stackspecmigration.APIVersionV2Alpha2, "StackSpec contract (stackkit/v2alpha2; explicit legacy adapter: stackkit/v2alpha1)")
	initCmd.Flags().StringVar(&initComputeTier, "compute-tier", "", "Legacy v2alpha1 only: declared kit graph (low, standard, high)")
	initCmd.Flags().StringArrayVar(&initModuleComputeProfiles, "module-compute-profile", nil, "Native v2alpha2: module-id=profile; repeat for every selected workload module")
	initCmd.Flags().StringArrayVar(&initModuleStorageProfiles, "module-storage-profile", nil, "Native v2alpha2: module-id=storage-profile for a declared storage dimension")
	initCmd.Flags().StringArrayVar(&initModuleAcceleratorProfiles, "module-accelerator-profile", nil, "Native v2alpha2: module-id=accelerator-profile for a declared accelerator dimension")
	initCmd.Flags().StringArrayVar(&initUseCaseAlternatives, "use-case-alternative", nil, "Native v2alpha2: use-case-id=alternative; include required core workloads")
	initCmd.Flags().StringVar(&initHardwareProfile, "hardware-profile", "", "Device class for nodes[0].hardware.profile (standard, pi, gpu, storage). pi is a constrained homelab device, not Raspberry-only. Not auto-detected from inventory")
	initCmd.Flags().StringVar(&initDomain, "domain", "", "Domain override for the generated stack spec")
	initCmd.Flags().BoolVar(&initLocalDNS, "local-dns", false, "v0.6 compatibility only: use Kombify Point local DNS names")
	initCmd.Flags().StringVar(&initLocalName, "local-name", "", "v0.6 compatibility only: local DNS short name for --local-dns")
	initCmd.Flags().StringVar(&initMode, "mode", "", "v0.6 compatibility only: installation mode (bare, bootstrapped, advanced)")
	initCmd.Flags().StringVarP(&initOutputDir, "output", "o", "deploy", "v0.6 compatibility only: output directory for generated files")
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "v0.6 compatibility only: overwrite existing files")
	initCmd.Flags().StringVar(&initExpectedSpecHash, "expected-spec-hash", "", "Native v2 only: exact current CUE-normalized spec hash required for replacement")
	initCmd.Flags().StringVar(&initPlatform, "platform", "", "Native v2 only: selected-provider platform adapter (install.platform.providerRef, e.g. coolify or komodo)")
	initCmd.Flags().StringSliceVar(&initUseCases, "use-case", nil, "Optional kit workloads to enable (e.g. photos,files,vault); v2alpha2 requires explicit alternatives")
	initCmd.Flags().StringSliceVar(&initEnableCapabilities, "enable", nil, "Native v2 only: optional kit capabilities to enable (capabilities.enable, e.g. lan-dns,internal-pki)")
	initCmd.Flags().BoolVar(&initNonInteractive, "non-interactive", false, "Run in non-interactive mode (fail if input is required)")
	initCmd.Flags().StringVar(&initAdminEmail, "admin-email", "", "v0.6 compatibility only: admin email for login accounts")
	initCmd.Flags().StringVar(&initServiceProfile, "service-profile", "", "v0.6 compatibility only: BaseKit service profile")

	// Owner-breakglass flags. bootstrap-mode separates orchestrator-managed
	// SaaS owner prep, explicit self-hosted owner bootstrap, and OSS/BYOS noop.
	initCmd.Flags().StringVar(&initClusterMode, "cluster-mode", "first", "v0.6 compatibility only: cluster mode (first|join)")
	initCmd.Flags().StringVar(&initOwnerBootstrapMode, "owner-bootstrap-mode", "", "v0.6 compatibility only: owner bootstrap mode (auto|custom|none)")
	initCmd.Flags().StringVar(&initOwnerSource, "owner-source", "", "Owner custody source (native standalone: local; v0.6 compatibility: local|cloud)")
	initCmd.Flags().StringVar(&initOwnerEmail, "owner-email", "", "Desired PocketID owner email for --owner-source=local")
	initCmd.Flags().StringVar(&initOwnerUsername, "owner-username", "", "Desired PocketID owner username for --owner-source=local")
	initCmd.Flags().StringVar(&initOwnerDisplayName, "owner-display-name", "", "Desired PocketID owner display name for --owner-source=local")
	initCmd.Flags().StringVar(&initCloudOIDCIssuer, "cloud-oidc-issuer", "", "Cloud OIDC issuer URL for auto/cloud owner handoff")
	initCmd.Flags().StringVar(&initCloudOIDCClientID, "cloud-oidc-client-id", "", "Cloud OIDC client ID")
	initCmd.Flags().StringVar(&initCloudOIDCSecretRef, "cloud-oidc-client-secret-ref", "", "Cloud OIDC client secret reference (e.g. doppler:// or secret://)")
	initCmd.Flags().StringVar(&initCloudOIDCForeignSubject, "cloud-oidc-foreign-subject", "", "Cloud user's foreign subject ID")
	initCmd.Flags().StringVar(&initRecoveryPassphraseHash, "recovery-passphrase-hash", "", "Recovery passphrase hash (argon2id PHC). If missing, prompts interactively.")
	initCmd.Flags().StringVar(&initRecoveryMaterialRef, "recovery-material-ref", "", "Reference to orchestrator-owned recovery material. Plaintext recovery passphrases are never accepted in stack specs.")
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
		if stackkit.Modes.Bare.Name != "" {
			modeChoices = append(modeChoices, choice{
				Key:         models.InstallModeBare,
				Display:     stackkit.Modes.Bare.Name,
				Description: stackkit.Modes.Bare.Description,
				IsDefault:   stackkit.Modes.Bare.Default,
			})
		}
		if stackkit.Modes.Bootstrapped.Name != "" {
			modeChoices = append(modeChoices, choice{
				Key:         models.InstallModeBootstrapped,
				Display:     stackkit.Modes.Bootstrapped.Name,
				Description: stackkit.Modes.Bootstrapped.Description,
				IsDefault:   stackkit.Modes.Bootstrapped.Default,
			})
		} else if stackkit.Modes.Simple.Name != "" {
			modeChoices = append(modeChoices, choice{
				Key:         models.InstallModeBootstrapped,
				Display:     stackkit.Modes.Simple.Name,
				Description: stackkit.Modes.Simple.Description,
				IsDefault:   stackkit.Modes.Simple.Default,
			})
		}
		if stackkit.Modes.Advanced.Name != "" {
			modeChoices = append(modeChoices, choice{
				Key:         models.InstallModeAdvanced,
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
		initMode = models.InstallModeBootstrapped
	}
	if !models.IsKnownInstallMode(initMode) {
		return "", fmt.Errorf("invalid --mode %q (use bare, bootstrapped, or advanced)", initMode)
	}
	if models.IsLegacyInstallMode(initMode) {
		printWarning("Mode %q is legacy; using %q.", initMode, models.NormalizeInstallMode(initMode))
	}
	initMode = models.NormalizeInstallMode(initMode)
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

// promptOptionalConfig asks for domain, email, and admin email when running
// interactively. Standalone init reads only explicit local input.
func promptOptionalConfig(p *prompter, defaults initDefaults) (domain, email, adminEmail string) {
	if initDomain != "" {
		domain = initDomain
	}

	// Priority 1: explicit --admin-email flag
	if initAdminEmail != "" {
		adminEmail = initAdminEmail
	}

	// Non-interactive or no TTY: return what we have, no prompts.
	if p == nil || initNonInteractive {
		if domain == "" {
			domain = defaults.Domain
		}
		return domain, adminEmail, adminEmail
	}

	fmt.Println()
	printInfo("Optional configuration (press Enter to skip):")
	fmt.Println()

	// Only prompt for admin email if it was not supplied explicitly.
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

	// Let's Encrypt email defaults to the explicitly supplied owner email.
	defaultEmail := adminEmail
	e, err := p.inputString("Email (for Let's Encrypt certificates)", defaultEmail)
	if err == nil {
		email = e
	}

	return domain, email, adminEmail
}

func resolveInitDefaults(currentDomain string) initDefaults {
	spec := &models.StackSpec{}
	if contextFlag != "" {
		spec.Context = contextFlag
	}
	if initLocalDNS {
		currentDomain = models.LocalDNSDomain(initLocalName)
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
	if initLocalDNS {
		domain = models.LocalDNSDomain(initLocalName)
	}

	networkMode := "local"
	if !initLocalDNS && netenv.NodeContextIsCloud(ctx) {
		networkMode = "public"
	}

	return initDefaults{
		Context:     ctx,
		ComputeTier: computeTier,
		Domain:      domain,
		NetworkMode: networkMode,
	}
}

func validateInitLocalDNSFlags() error {
	if initLocalName != "" && !initLocalDNS {
		return fmt.Errorf("--local-name requires --local-dns")
	}
	if initLocalDNS && initDomain != "" {
		return fmt.Errorf("--local-dns and --domain cannot be used together")
	}
	if initLocalDNS {
		domain := models.LocalDNSDomain(initLocalName)
		if strings.Contains(domain, "arpa") {
			return fmt.Errorf("--local-name must stay in the local .home namespace")
		}
		if strings.Contains(domain, "kombify.me") {
			return fmt.Errorf("--local-name must not create a public kombify.me domain")
		}
	}
	return nil
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
		initMode = models.InstallModeBootstrapped
	}
	if !models.IsKnownInstallMode(initMode) {
		return fmt.Errorf("invalid --mode %q (use bare, bootstrapped, or advanced)", initMode)
	}
	initMode = models.NormalizeInstallMode(initMode)
	return nil
}

func bootstrapDefaultsForInitMode(mode string) models.BootstrapSpec {
	switch models.NormalizeInstallMode(mode) {
	case models.InstallModeBare:
		return models.BootstrapSpec{
			PlatformPolicy:           models.SetupPolicyManual,
			ApplicationDefaultPolicy: models.SetupPolicyManual,
		}
	default:
		return models.BootstrapSpec{
			PlatformPolicy:           models.SetupPolicyAutomatic,
			ApplicationDefaultPolicy: models.SetupPolicyOnDemand,
		}
	}
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

	// FindStackKitDir may return a directory outside the loader's basePath
	// (e.g., basePath/../name). Use a loader scoped to the stackkit directory
	// to load the definition, keeping the original loader for downstream use.
	stackkitPath := filepath.Join(stackkitDir, "stackkit.yaml")
	stackkitLoader := config.NewLoader(stackkitDir)
	stackkit, err := stackkitLoader.LoadStackKit(stackkitPath)
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
	if err := persistLegacyV06StackSpec(loader, spec, specPath, "init"); err != nil {
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
	if stackspecadmission.RejectOperationalV1(version) {
		return runArchitectureV2Init(cmd, args, wd)
	}
	if strings.TrimSpace(initExpectedSpecHash) != "" {
		return fmt.Errorf("--expected-spec-hash is a native Architecture v2 authoring contract and is unavailable on the exact-v0.6 compatibility line")
	}
	if commandFlagChanged(cmd, "api-version") || len(initModuleComputeProfiles)+len(initModuleStorageProfiles)+len(initModuleAcceleratorProfiles)+len(initUseCaseAlternatives) > 0 {
		return fmt.Errorf("module profile authoring is unavailable on the exact-v0.6 compatibility line")
	}
	if err := validateInitLocalDNSFlags(); err != nil {
		return err
	}
	if initClusterMode != "" && initClusterMode != "first" && initClusterMode != "join" {
		return fmt.Errorf("--cluster-mode must be 'first' or 'join'")
	}

	loader := config.NewLoader(wd)
	availableKits, err := discoverStackKits(loader, wd)
	if err != nil {
		return fmt.Errorf("failed to discover StackKits: %w", err)
	}

	stackkitName, p, err := resolveStackKitName(args, availableKits, wd)
	if err != nil {
		return err
	}
	isLocalPath := strings.Contains(stackkitName, "/") || strings.Contains(stackkitName, "\\")
	if !isLocalPath {
		if err := validateInitializableStackKit(stackkitName); err != nil {
			return err
		}
	}

	deployLog.Event("init.stackkit_selected",
		slog.String("name", stackkitName),
	)

	printInfo("Initializing StackKit: %s", bold(stackkitName))

	loader, stackkit, err := loadStackKit(loader, stackkitName, wd)
	if err != nil {
		return err
	}
	if err := validateInitializableStackKit(stackkit.Metadata.Name); err != nil {
		return fmt.Errorf("local StackKit definition is not an active product: %w", err)
	}
	stackkitName = stackkit.Metadata.Name
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

	// Resolve owner-bootstrap fields. When --owner-source is set, these are
	// persisted into spec.Owner so `stackkit apply` (which runs in a separate
	// process) can read them without re-prompting the user.
	//
	// Non-interactive callers without --owner-source skip owner provisioning
	// entirely; resolveOwnerSpec returns hasOwner=false in that case.
	var ownerCfg models.OwnerConfig
	hasOwnerData := false
	if p == nil && !initNonInteractive {
		// gatherInitChoices may have run without a prompter when all the
		// stackkit-selection answers were already provided via flags. Build
		// one here so the owner resolver has something to prompt against.
		p = newPrompter()
	}

	ownerCfg, hasOwnerData, err = resolveOwnerBootstrapConfig(ownerFlags{
		BootstrapMode:       initOwnerBootstrapMode,
		Source:              initOwnerSource,
		Email:               initOwnerEmail,
		Username:            initOwnerUsername,
		DisplayName:         initOwnerDisplayName,
		RecoveryHash:        initRecoveryPassphraseHash,
		RecoveryMaterialRef: initRecoveryMaterialRef,
		CloudIssuer:         initCloudOIDCIssuer,
		CloudClientID:       initCloudOIDCClientID,
		CloudSecretRef:      initCloudOIDCSecretRef,
		ForeignSubject:      initCloudOIDCForeignSubject,
	}, p, initNonInteractive)
	if err != nil {
		return fmt.Errorf("resolve owner bootstrap: %w", err)
	}

	if err := validateInitPublicIdentity(defaults, domain, email, adminEmail, ownerCfg.Email, initNonInteractive); err != nil {
		return err
	}

	if hasOwnerData && ownerCfg.EffectiveBootstrapMode() == models.OwnerBootstrapModeCustom {
		// Only first nodes run local owner provisioning; join nodes don't
		// provision owners. Validate up-front rather than failing inside
		// runOwnerBootstrap on apply.
		if initClusterMode != "" && initClusterMode != "first" {
			return fmt.Errorf("--cluster-mode=%q is not supported with --owner-source (Phase 1 supports 'first' only)", initClusterMode)
		}
	}

	sshSpec := models.SSHSpec{
		User: "root",
		Port: 22,
	}
	isLocalOnlyDomain := models.IsLocalDomain(domain) && !models.IsKombifyMeDomain(domain)
	if stackkitName == "basement-kit" && defaults.Context == models.ContextLocal && isLocalOnlyDomain {
		sshSpec.User = "admin"
		sshSpec.KeyPath = "~/.ssh/id_ed25519"
	}

	services, err := baseKitInitServices(stackkitName, defaults.Context, isLocalOnlyDomain, initServiceProfile)
	if err != nil {
		return err
	}
	specName := strings.TrimSpace(initName)
	if specName == "" {
		specName = filepath.Base(wd)
	}

	spec := &models.StackSpec{
		Name:       specName,
		StackKit:   stackkitName,
		Mode:       mode,
		Context:    string(defaults.Context),
		Domain:     domain,
		Email:      email,
		AdminEmail: adminEmail,
		Network: models.NetworkSpec{
			Mode: defaults.NetworkMode,
		},
		Compute: models.ComputeSpec{
			Tier: computeTier,
		},
		SSH:       sshSpec,
		Services:  services,
		Bootstrap: bootstrapDefaultsForInitMode(mode),
		Owner:     ownerCfg,
	}
	if hasOwnerData {
		deployLog.Event("init.owner_persisted",
			slog.String("source", ownerCfg.Source),
			slog.String("username", ownerCfg.Username),
			slog.Bool("has_recovery_hash", ownerCfg.RecoveryPassphraseHash != ""),
		)
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

	// PocketID's STATIC_API_KEY and ENCRYPTION_KEY are intentionally NOT
	// provisioned here. `stackkit generate` owns that path: it runs the
	// composition engine, decides whether PocketID is actually deployed,
	// and only then writes <wd>/.stackkit/pocketid-* (gated, idempotent,
	// 0600). This keeps the .stackkit/ directory empty for kits that don't
	// enable PocketID (basement-kit out of the box).

	printInitSummary(stackkitName, mode, computeTier, defaults.Context, domain, email)
	return nil
}

func baseKitLocalReleaseDefaultServices() map[string]any {
	return map[string]any{
		"homepage":    map[string]any{"enabled": true},
		"uptime-kuma": map[string]any{"enabled": true},
		"whoami":      map[string]any{"enabled": true},
		"vaultwarden": map[string]any{"enabled": true},
		"jellyfin":    map[string]any{"enabled": false},
		"immich":      map[string]any{"enabled": true},
		"files":       map[string]any{"enabled": true, "provider": "cloudreve"},
	}
}

func baseKitInitServices(stackkitName string, ctx models.NodeContext, isLocalOnlyDomain bool, profile string) (map[string]any, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	switch profile {
	case "", "default":
		if stackkitName == "basement-kit" && ctx == models.ContextLocal && isLocalOnlyDomain {
			return baseKitLocalReleaseDefaultServices(), nil
		}
		return nil, nil
	case "admin-only":
		if stackkitName != "basement-kit" && stackkitName != "cloud-kit" {
			return nil, fmt.Errorf("--service-profile=%s is only supported for basement-kit or cloud-kit", profile)
		}
		return baseKitAdminOnlyServices(), nil
	default:
		return nil, fmt.Errorf("unsupported --service-profile %q; expected default or admin-only", profile)
	}
}

func baseKitAdminOnlyServices() map[string]any {
	return map[string]any{
		"homepage":    map[string]any{"enabled": true},
		"uptime-kuma": map[string]any{"enabled": true},
		"whoami":      map[string]any{"enabled": true},
		"vault":       map[string]any{"enabled": false},
		"vaultwarden": map[string]any{"enabled": false},
		"media":       map[string]any{"enabled": false},
		"jellyfin":    map[string]any{"enabled": false},
		"photos":      map[string]any{"enabled": false},
		"immich":      map[string]any{"enabled": false},
		"files":       map[string]any{"enabled": false},
	}
}

func validateInitPublicIdentity(defaults initDefaults, domain, email, adminEmail, ownerEmail string, nonInteractive bool) error {
	if !nonInteractive || !initRequiresRealOwnerEmail(defaults, domain) {
		return nil
	}
	for _, candidate := range []string{ownerEmail, adminEmail, email} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && strings.Contains(candidate, "@") && !models.NeedsSyntheticAdminEmail(candidate) {
			return nil
		}
	}
	return fmt.Errorf("owner/admin email is required for public or managed StackKit configs")
}

func initRequiresRealOwnerEmail(defaults initDefaults, domain string) bool {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return false
	}
	if defaults.NetworkMode != "public" && defaults.Context != models.ContextCloud {
		return false
	}
	return models.IsKombifyMeDomain(domain) || !models.IsLocalDomain(domain)
}

const legacyHAKitSlug = stackspecmigration.LegacyHAKitSlug

// validateInitializableStackKit keeps the retired HA shape available only to
// explicit migration/closure commands. It must never be installable as a
// fourth product kit even when a legacy source directory is present.
func validateInitializableStackKit(name string) error {
	if strings.TrimSpace(name) == legacyHAKitSlug {
		return fmt.Errorf(
			"%s is retired and cannot be initialized; choose %s and configure addons/ha explicitly",
			legacyHAKitSlug,
			strings.Join(productkits.Slugs(), ", "),
		)
	}
	return productkits.Validate(name)
}

// discoverStackKits scans the working directory (and parent) for stackkit.yaml
// files. Legacy migration adapters are intentionally excluded from product
// discovery; direct migration readers remain available elsewhere.
func discoverStackKits(loader *config.Loader, wd string) ([]*models.StackKit, error) {
	kits, err := loader.DiscoverStackKits(wd, filepath.Dir(wd))
	if err != nil {
		return nil, err
	}
	kits = filterDiscoverableStackKits(kits)
	sort.Slice(kits, func(i, j int) bool { return kits[i].Metadata.Name < kits[j].Metadata.Name })
	return kits, nil
}

func filterDiscoverableStackKits(kits []*models.StackKit) []*models.StackKit {
	discoverable := make([]*models.StackKit, 0, len(kits))
	for _, kit := range kits {
		if !productkits.IsActive(kit.Metadata.Name) {
			continue
		}
		discoverable = append(discoverable, kit)
	}
	return discoverable
}

// stackKitNames returns a sorted list of StackKit names.
func stackKitNames(kits []*models.StackKit) []string {
	var names []string
	for _, k := range kits {
		names = append(names, k.Metadata.Name)
	}
	return names
}
