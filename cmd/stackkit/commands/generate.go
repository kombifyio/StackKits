package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/composition"
	"github.com/kombifyio/stackkits/internal/config"
	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/kombifyme"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/template"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	genOutputDir string
	genForce     bool
	genFragments bool
)

var generateCmd = &cobra.Command{
	Use:     "generate",
	Aliases: []string{"gen"},
	Short:   "Generate OpenTofu files from stack specification",
	Long: `Generate OpenTofu configuration files from your stack specification.

This command reads your stack-spec.yaml and the associated StackKit templates
to generate ready-to-apply OpenTofu files in the output directory.

Examples:
  stackkit generate                     Generate using defaults
  stackkit generate -o ./terraform      Output to custom directory
  stackkit generate --force             Overwrite existing files`,
	RunE: runGenerate,
}

func init() {
	generateCmd.Flags().StringVarP(&genOutputDir, "output", "o", "deploy", "Output directory for generated files")
	generateCmd.Flags().BoolVarP(&genForce, "force", "f", false, "Overwrite existing files")
	generateCmd.Flags().BoolVar(&genFragments, "fragments", false, "Generate experimental per-module OpenTofu fragments instead of the stable Base Kit template")
}

func runGenerate(cmd *cobra.Command, args []string) error {
	start := time.Now()
	wd := getWorkDir()

	// Validate output directory to prevent path traversal
	if strings.Contains(genOutputDir, "..") {
		return fmt.Errorf("output directory must not contain '..': %s", genOutputDir)
	}
	outputPath := filepath.Join(wd, genOutputDir)
	cleanOutput := filepath.Clean(outputPath)
	cleanWd := filepath.Clean(wd)
	if !strings.HasPrefix(cleanOutput, cleanWd+string(filepath.Separator)) && cleanOutput != cleanWd {
		return fmt.Errorf("output directory must be within working directory: %s", genOutputDir)
	}

	// Load spec (loader.resolvePath handles absolute vs relative paths)
	loader := config.NewLoader(wd)

	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		return fmt.Errorf("failed to load spec file: %w", err)
	}

	deployLog.Event("spec.loaded",
		slog.String("stackkit", spec.StackKit),
		slog.String("mode", spec.Mode),
		slog.String("domain", spec.Domain),
		slog.String("tier", spec.Compute.Tier),
	)

	// Apply --context flag override if provided
	if contextFlag != "" {
		spec.Context = contextFlag
	}

	// Resolve NodeContext: use stored capabilities or detect on-the-fly
	resolvedCtx := resolveNodeContextForGenerate(spec)
	if spec.Context == "" {
		spec.Context = string(resolvedCtx)
	}

	if err := ensureKombifyMeRegistration(loader, spec, resolvedCtx); err != nil {
		return err
	}

	cueValidator := cueval.NewValidator(wd)
	if specResult, valErr := cueValidator.ValidateSpec(spec); valErr != nil {
		return fmt.Errorf("spec validation failed: %w", valErr)
	} else if !specResult.Valid {
		for _, e := range specResult.Errors {
			printWarning("Spec: %s: %s", e.Path, e.Message)
		}
		return fmt.Errorf("spec validation failed with %d errors", len(specResult.Errors))
	}

	printInfo("Generating OpenTofu files for: %s", bold(spec.Name))
	printInfo("StackKit: %s, Mode: %s, Context: %s", spec.StackKit, spec.Mode, netenv.FormatNodeContext(resolvedCtx))

	// Find StackKit directory
	stackkitDir, err := loader.FindStackKitDir(spec.StackKit)
	if err != nil {
		// Try parent directories for development
		parentDir := filepath.Dir(wd)
		loader = config.NewLoader(parentDir)
		stackkitDir, err = loader.FindStackKitDir(spec.StackKit)
		if err != nil {
			return fmt.Errorf("stackkit '%s' not found: %w", spec.StackKit, err)
		}
	}

	// Load StackKit. FindStackKitDir can return a path outside the current
	// workdir (for example a deployment folder next to the repo root), so load
	// the definition through a loader scoped to the kit directory.
	stackkitPath := filepath.Join(stackkitDir, "stackkit.yaml")
	stackkitLoader := config.NewLoader(stackkitDir)
	stackkit, err := stackkitLoader.LoadStackKit(stackkitPath)
	if err != nil {
		return fmt.Errorf("failed to load stackkit: %w", err)
	}

	// Validate CUE schemas before generating
	if cueResult, valErr := cueValidator.ValidateStackKit(stackkitDir); valErr != nil {
		deployLog.Error("cue.validation",
			slog.String("status", "error"),
			slog.String("error", valErr.Error()),
		)
		return fmt.Errorf("CUE validation failed: %w", valErr)
	} else if !cueResult.Valid {
		for _, e := range cueResult.Errors {
			printWarning("CUE: %s: %s", e.Path, e.Message)
		}
		deployLog.Error("cue.validation",
			slog.String("status", "invalid"),
			slog.Int("error_count", len(cueResult.Errors)),
		)
		return fmt.Errorf("CUE validation failed with %d errors — fix schema issues before generating", len(cueResult.Errors))
	} else {
		deployLog.Event("cue.validation",
			slog.String("status", "valid"),
		)
	}

	// Load service catalog from CUE module contracts. BaseKit keeps modules at
	// repo root; published standalone kits may carry a local modules/ directory.
	modulesDir := resolveModulesDir(stackkitDir, wd)
	catalog, catalogErr := cueval.ServiceCatalogFromModules(modulesDir)
	if catalogErr != nil {
		printWarning("CUE catalog: %v (falling back to hardcoded catalog)", catalogErr)
		catalog = cueval.ServiceCatalog()
		deployLog.Warn("cue.catalog",
			slog.String("status", "fallback"),
			slog.String("error", catalogErr.Error()),
		)
	} else {
		deployLog.Event("cue.catalog",
			slog.String("status", "loaded"),
			slog.Int("service_count", len(catalog)),
		)
	}

	domains, domainErr := cueval.DomainEntriesFromModules(modulesDir)
	if domainErr != nil {
		domains = cueval.DomainEntries()
	}

	// Validate module dependency graph and run composition engine
	var compositionResult *composition.CompositionResult
	var loadedContracts []cueval.ModuleContract
	moduleReader := cueval.NewModuleReader()
	if contracts, readErr := moduleReader.ReadAllModules(modulesDir); readErr == nil {
		loadedContracts = contracts
		graph := composition.BuildGraph(contracts)
		if depErrs := graph.Validate(); len(depErrs) > 0 {
			for _, e := range depErrs {
				printWarning("Dependency: %s", e.Error())
			}
			deployLog.Warn("composition.validate",
				slog.Int("error_count", len(depErrs)),
			)
		} else if order, sortErr := graph.TopologicalSort(); sortErr == nil {
			deployLog.Event("composition.order",
				slog.String("order", strings.Join(order, " → ")),
			)
		}

		// Run composition engine to determine enabled modules and identity config
		engine := composition.NewCompositionEngine(contracts, stackkit, spec)
		if result, resolveErr := engine.Resolve(); resolveErr != nil {
			deployLog.Warn("composition.resolve",
				slog.String("error", resolveErr.Error()),
			)
			return fmt.Errorf("composition engine: %w", resolveErr)
		} else {
			compositionResult = result
			deployLog.Event("composition.resolve",
				slog.String("enabled", strings.Join(result.EnabledModules, ", ")),
				slog.Int("warning_count", len(result.Warnings)),
			)
			for _, w := range result.Warnings {
				printWarning("Composition: %s", w)
			}
			if result.Identity != nil {
				deployLog.Event("composition.identity",
					slog.Bool("pocketid", result.Identity.PocketIDEnabled),
					slog.Bool("tinyauth", result.Identity.TinyAuthEnabled),
					slog.Bool("oidc_enabled", result.Identity.TinyAuthOAuthEnabled),
					slog.String("issuer", result.Identity.OIDCIssuerURL),
				)
			}
		}
	}

	// Determine template directory: runtime overrides mode for native deployments
	templateKey := spec.Mode
	if spec.Runtime == models.RuntimeNative {
		templateKey = models.RuntimeNative
	}
	templateDir := filepath.Join(stackkitDir, "templates", templateKey)
	templateFallback := false
	if _, statErr := os.Stat(templateDir); os.IsNotExist(statErr) {
		// Fall back to simple mode
		templateFallback = true
		templateDir = filepath.Join(stackkitDir, "templates", "simple")
		if _, statErr2 := os.Stat(templateDir); os.IsNotExist(statErr2) {
			return fmt.Errorf("no templates found for mode '%s' in %s", templateKey, stackkitDir)
		}
	}
	deployLog.Event("decision.template",
		slog.String("template_key", templateKey),
		slog.Bool("fallback_to_simple", templateFallback),
		slog.String("template_dir", templateDir),
	)

	// Create output directory
	outputPath = filepath.Join(wd, genOutputDir)
	if _, statErr := os.Stat(outputPath); statErr == nil && !genForce {
		return fmt.Errorf("output directory already exists: %s (use --force to overwrite)", outputPath)
	}

	if mkdirErr := os.MkdirAll(outputPath, 0750); mkdirErr != nil {
		return fmt.Errorf("failed to create output directory: %w", mkdirErr)
	}

	// Generate terraform.tfvars.json from spec before fragment rendering so the
	// fragment generator can declare and reference every runtime variable it uses.
	tfvarsData, err := composition.GenerateTFVarsJSON(spec, compositionResult)
	if err != nil {
		return fmt.Errorf("failed to generate tfvars: %w", err)
	}
	tfvarsMap := map[string]any{}
	if err := json.Unmarshal(tfvarsData, &tfvarsMap); err != nil {
		return fmt.Errorf("failed to parse generated tfvars: %w", err)
	}

	// Phase 1 / Task 12 + Task 14 stopper-fix: inject the persisted PocketID
	// secrets so the fragment generator both declares the variables (env
	// block of the pocketid container references {{.pocketid_static_api_key}}
	// and {{.pocketid_encryption_key}}) and writes the concrete values into
	// terraform.tfvars.json. Both Ensure* calls are idempotent — if a prior
	// generate ran they return the existing values; if not, they create
	// 0600-mode files under <wd>/.stackkit/.
	//
	// Gated on the composition result actually enabling PocketID. Kits that
	// don't deploy PocketID (e.g. base-kit out of the box) skip this so the
	// .stackkit/ directory stays empty and we don't surface confusing
	// "PocketID secrets stored in ..." messages on init/generate runs that
	// will never start a PocketID container.
	if pocketIDShouldProvisionSecrets(compositionResult, tfvarsMap) {
		staticAPIKey, err := identity.EnsureStaticAPIKey(wd)
		if err != nil {
			return fmt.Errorf("provision pocketid static api key: %w", err)
		}
		encryptionKey, err := identity.EnsureEncryptionKey(wd)
		if err != nil {
			return fmt.Errorf("provision pocketid encryption key: %w", err)
		}
		tfvarsMap["pocketid_static_api_key"] = staticAPIKey
		tfvarsMap["pocketid_encryption_key"] = encryptionKey
		tfvarsData, err = json.MarshalIndent(tfvarsMap, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to re-marshal tfvars after pocketid secrets injection: %w", err)
		}
		tfvarsData = append(tfvarsData, '\n')
	}

	// Fragment-based generation: produce per-module .tf files from CUE contracts.
	// Keep this behind an explicit flag until the module contracts can render
	// the full Base Kit Hub, PaaS, monitoring, and bootstrapping surface.
	fragmentsGenerated := false
	if genFragments && compositionResult != nil && len(loadedContracts) > 0 {
		moduleGraph, graphErr := buildModuleGraphFromComposition(loadedContracts, compositionResult)
		if graphErr != nil {
			printWarning("Fragment generation: %v (falling back to monolithic template)", graphErr)
			deployLog.Warn("fragments.build",
				slog.String("error", graphErr.Error()),
			)
		} else {
			domain := spec.Domain
			if domain == "" {
				domain = models.DomainHomeLab
			}
			gen := cueval.NewGeneratorWithVariables(domain, tfvarsMap).
				WithDockerMemoryLimits(dockerMemoryLimitsEnabled(loadDockerCapabilities()))
			if genErr := gen.GenerateAll(moduleGraph, outputPath); genErr != nil {
				printWarning("Fragment generation: %v (falling back to monolithic template)", genErr)
				deployLog.Warn("fragments.generate",
					slog.String("error", genErr.Error()),
				)
			} else {
				fragmentsGenerated = true
				printSuccess("Generated per-module fragments: %s", strings.Join(moduleGraph.Ordered, ", "))
				deployLog.Event("fragments.generated",
					slog.Int("module_count", len(moduleGraph.Ordered)),
					slog.String("modules", strings.Join(moduleGraph.Ordered, ", ")),
				)
			}
		}
	} else if !genFragments {
		deployLog.Event("fragments.skipped",
			slog.String("reason", "explicit flag not set"),
		)
	}

	// Stable path: copy monolithic templates when fragments aren't requested or
	// aren't available.
	if !fragmentsGenerated {
		if genFragments {
			printWarning("Using monolithic template generation (fragment generation unavailable)")
			deployLog.Warn("fragments.fallback",
				slog.String("reason", "fragment generation failed or composition unavailable"),
			)
		} else {
			printInfo("Using stable Base Kit template generation")
		}
		err = copyOrRenderTemplates(templateDir, outputPath, spec, stackkit, catalog, domains)
		if err != nil {
			return fmt.Errorf("failed to generate files: %w", err)
		}

		// Generate main.tf if not present
		mainTfPath := filepath.Join(outputPath, "main.tf")
		if _, statErr := os.Stat(mainTfPath); os.IsNotExist(statErr) {
			renderCtx := &template.RenderContext{
				Spec:     spec,
				StackKit: stackkit,
				Catalog:  catalog,
				Domains:  domains,
			}
			mainTf, err := template.GenerateMainTf(renderCtx)
			if err != nil {
				return fmt.Errorf("failed to generate main.tf: %w", err)
			}
			if err := os.WriteFile(mainTfPath, []byte(mainTf), 0600); err != nil {
				return fmt.Errorf("failed to write main.tf: %w", err)
			}
			printSuccess("Generated: main.tf")
		}
	}

	if err := cueval.GenerateAppsTF(spec, outputPath); err != nil {
		return fmt.Errorf("failed to generate app resources: %w", err)
	}
	if len(spec.Apps) > 0 {
		printSuccess("Generated user app resources: apps.tf")
	}

	// Write terraform.tfvars.json from spec (JSON format for consistency with API).
	tfvarsPath := filepath.Join(outputPath, "terraform.tfvars.json")
	if err := os.WriteFile(tfvarsPath, tfvarsData, 0600); err != nil {
		return fmt.Errorf("failed to write terraform.tfvars.json: %w", err)
	}
	printSuccess("Generated: terraform.tfvars.json")
	printWarning("terraform.tfvars.json contains sensitive data (passwords, tokens). Do not commit it to version control.")

	// Print summary
	files, _ := countFiles(outputPath)
	fmt.Println()
	printSuccess("Generated %d files in: %s", files, outputPath)

	// Print next steps
	fmt.Println()
	printInfo("Next steps:")
	fmt.Printf("  1. Review generated files: %s\n", cyan("ls "+genOutputDir))
	fmt.Printf("  2. Initialize OpenTofu:    %s\n", cyan("cd "+genOutputDir+" && tofu init"))
	fmt.Printf("  3. Or use StackKit:        %s\n", cyan("stackkit plan"))

	deployLog.Event("generate.complete",
		slog.Int("file_count", files),
		slog.Int64("elapsed_ms", time.Since(start).Milliseconds()),
	)

	return nil
}

// copyOrRenderTemplates renders template files using the template.Renderer,
// falling back to plain copy for non-template files.
func copyOrRenderTemplates(srcDir, dstDir string, spec *models.StackSpec, stackkit *models.StackKit, catalog, domains []cueval.CatalogEntry) error {
	renderer := template.NewRenderer(srcDir, dstDir)
	renderCtx := &template.RenderContext{
		Spec:     spec,
		StackKit: stackkit,
		Catalog:  catalog,
		Domains:  domains,
	}
	return renderer.Render(renderCtx)
}

func resolveModulesDir(stackkitDir, workDir string) string {
	candidates := []string{
		filepath.Join(stackkitDir, "modules"),
		filepath.Join(workDir, "modules"),
		filepath.Join(filepath.Dir(stackkitDir), "modules"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}

// buildModuleGraphFromComposition creates a ModuleGraph from loaded contracts
// and the composition result, marking only enabled modules as active.
// The input contracts slice is NOT mutated; a copy is used internally.
func buildModuleGraphFromComposition(contracts []cueval.ModuleContract, cr *composition.CompositionResult) (*cueval.ModuleGraph, error) {
	enabledSet := make(map[string]bool, len(cr.EnabledModules))
	for _, name := range cr.EnabledModules {
		enabledSet[name] = true
	}

	// Clone contracts to avoid mutating the caller's slice
	cloned := make([]cueval.ModuleContract, len(contracts))
	copy(cloned, contracts)
	for i := range cloned {
		cloned[i].Enabled = enabledSet[cloned[i].Metadata.Name]
	}

	resolver := cueval.NewResolver()
	return resolver.Resolve(cloned)
}

func generateTfvarsJSON(spec *models.StackSpec, cr *composition.CompositionResult) ([]byte, error) {
	return composition.GenerateTFVarsJSON(spec, cr)
}

// pocketIDIsEnabled reports whether PocketID is part of the deployment per
// the composition result. Used to gate file-based secret provisioning so
// kits that don't deploy PocketID don't litter .stackkit/ with unused
// STATIC_API_KEY / ENCRYPTION_KEY files. Returns false when composition
// failed to resolve (cr == nil or cr.Identity == nil) — in that case the
// callers that have already generated tfvars should use
// pocketIDShouldProvisionSecrets so the bridge's mandatory PocketID default
// still receives persisted secrets if composition is unavailable.
func pocketIDIsEnabled(cr *composition.CompositionResult) bool {
	if cr == nil || cr.Identity == nil {
		return false
	}
	return cr.Identity.PocketIDEnabled
}

func pocketIDShouldProvisionSecrets(cr *composition.CompositionResult, tfvars map[string]any) bool {
	if pocketIDIsEnabled(cr) {
		return true
	}
	if enabled, ok := tfvars["enable_pocketid"].(bool); ok {
		return enabled
	}
	return false
}

func generateRandomPassword(length int) (string, error) {
	return composition.GenerateRandomPassword(length)
}

func bcryptHash(password string) (string, error) {
	return composition.BcryptHash(password)
}

// countFiles counts files in a directory
// loadDockerCapabilities reads the capabilities file written by `stackkit prepare`.
func loadDockerCapabilities() *models.DockerCapabilities {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".stackkits", "capabilities.json"))
	if err != nil {
		return nil
	}
	var caps models.DockerCapabilities
	if err := json.Unmarshal(data, &caps); err != nil {
		return nil
	}
	return &caps
}

func countFiles(dir string) (int, error) {
	count := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count, err
}

// resolveNodeContextForGenerate resolves the NodeContext for the generate command.
// Uses stored capabilities from prepare, or detects on-the-fly.
func resolveNodeContextForGenerate(spec *models.StackSpec) models.NodeContext {
	// If --context flag was provided, use it directly
	if spec.Context != "" {
		return models.NodeContext(spec.Context)
	}

	caps := loadDockerCapabilities()
	return resolveNodeContextFromCaps(caps, spec)
}

// resolveNodeContextFromCaps resolves NodeContext from DockerCapabilities.
// If capabilities aren't available, detects on-the-fly.
func resolveNodeContextFromCaps(caps *models.DockerCapabilities, spec *models.StackSpec) models.NodeContext {
	// If --context flag was set on spec, honor it
	if spec.Context != "" {
		return models.NodeContext(spec.Context)
	}

	// Use stored resolved context from prepare
	if caps != nil && caps.ResolvedContext != "" {
		return caps.ResolvedContext
	}

	// Detect on-the-fly if prepare wasn't run
	detected := netenv.Detect(context.Background())

	// Update caps with detection results for downstream use
	if caps == nil {
		caps = &models.DockerCapabilities{}
	}
	caps.NetworkEnv = detected.Environment
	caps.PublicIP = detected.PublicIP
	caps.PrivateIP = detected.PrivateIP
	caps.IsNAT = detected.IsNAT
	caps.HasPublicInterface = detected.HasPublicInterface

	resolved := netenv.ResolveFromResult(detected, caps.CPUCores, caps.MemoryGB)
	caps.ResolvedContext = resolved
	return resolved
}

func dockerMemoryLimitsEnabled(caps *models.DockerCapabilities) bool {
	if caps == nil {
		return true
	}
	if caps.CgroupVersion == "v2" && !caps.UnshareAvailable {
		return false
	}
	if caps.CgroupVersion != "" && !caps.MemoryLimits {
		return false
	}
	return true
}

// isKombifyMeDomain returns true if the domain is kombify.me (the subdomain service).
func isKombifyMeDomain(domain string) bool {
	return strings.EqualFold(domain, models.DomainKombifyMe)
}

func ensureKombifyMeRegistration(loader *config.Loader, spec *models.StackSpec, resolvedCtx models.NodeContext) error {
	domain := spec.Domain
	if suggested, _ := netenv.SuggestDomainForContext(resolvedCtx, domain); suggested != "" {
		domain = suggested
	}
	if !isKombifyMeDomain(domain) {
		return nil
	}
	spec.Domain = models.DomainKombifyMe

	// Existing prefixed specs are portable/offline; refresh registration when
	// a key is available, but do not block local regeneration solely because the
	// key is absent.
	if spec.SubdomainPrefix != "" {
		if apiKey, _ := kombifyme.LoadAPIKey(); apiKey == "" {
			return nil
		}
	}

	if err := registerKombifyMeSubdomains(spec); err != nil {
		deployLog.Warn("kombifyme.registration",
			slog.String("error", err.Error()),
		)
		if spec.SubdomainPrefix == "" {
			return fmt.Errorf("kombify.me registration failed and no subdomainPrefix is configured: %w", err)
		}
		printWarning("kombify.me registration: %v", err)
		printInfo("Continuing with existing subdomainPrefix: %s", spec.SubdomainPrefix)
		return nil
	}

	if saveErr := loader.SaveStackSpec(spec, specFile); saveErr != nil {
		printWarning("Could not persist assigned kombify.me subdomainPrefix: %v", saveErr)
		deployLog.Warn("kombifyme.persist_prefix",
			slog.String("error", saveErr.Error()),
		)
	}
	deployLog.Event("kombifyme.registration",
		slog.String("prefix", spec.SubdomainPrefix),
	)
	return nil
}

// registerKombifyMeSubdomains registers base + service subdomains on the kombify.me API
// and sets spec.SubdomainPrefix if not already set.
func registerKombifyMeSubdomains(spec *models.StackSpec) error {
	// Try loading API key from env or keystore
	apiKey, loadErr := kombifyme.LoadAPIKey()

	if apiKey == "" {
		// No key found — attempt self-registration
		email := spec.AdminEmail
		if email == "" {
			email = spec.Email
		}
		if email == "" {
			return fmt.Errorf("no KOMBIFY_API_KEY found and no adminEmail/email in spec for auto-registration")
		}

		fingerprint := kombifyme.DeviceFingerprint()

		printInfo("No kombify.me API key found. Registering with %s...", email)
		regResp, err := kombifyme.Register(email, fingerprint)
		if err != nil {
			return fmt.Errorf("auto-registration failed (%v): %w\nSet KOMBIFY_API_KEY manually or see https://kombify.me/docs", loadErr, err)
		}

		apiKey = regResp.APIKey
		savedPath, saveErr := kombifyme.SaveAPIKey(apiKey)
		if saveErr != nil {
			printWarning("Could not save API key: %v", saveErr)
			printInfo("Set KOMBIFY_API_KEY=%s to persist manually", apiKey)
		} else {
			printSuccess("API key saved to %s", savedPath)
		}

		if regResp.Status == "pending_verification" {
			printWarning("Please verify your email at %s to activate subdomains", email)
			printInfo("Subdomains will be created now but remain inactive until verified")
		}
	}

	homelabName := spec.Name
	if homelabName == "" {
		return fmt.Errorf("spec name is required for kombify.me registration")
	}

	// Device fingerprint: use existing prefix suffix or generate one
	fingerprint := ""
	if spec.SubdomainPrefix != "" {
		// Extract fingerprint from existing prefix: "sh-name-FINGERPRINT"
		if strings.HasPrefix(spec.SubdomainPrefix, "sh-") {
			if idx := strings.LastIndex(spec.SubdomainPrefix, "-"); idx > len("sh-") {
				fingerprint = spec.SubdomainPrefix[idx+1:]
			}
		}
	}
	if fingerprint == "" {
		fingerprint = kombifyme.DeviceFingerprint()
	}

	tier := spec.Compute.Tier
	if tier == "" {
		tier = models.ComputeTierStandard
	}

	printInfo("Registering subdomains on kombify.me...")

	result, err := kombifyme.RegisterAll(apiKey, homelabName, fingerprint, tier)
	if err != nil {
		return err
	}

	// Update spec with the registered prefix
	spec.SubdomainPrefix = result.Prefix

	printSuccess("Registered base subdomain: %s.kombify.me", result.Prefix)
	for _, svc := range result.Services {
		printSuccess("  Service: %s.kombify.me (exposed)", svc.Name)
	}

	return nil
}
