package commands

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/kombifyme"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/template"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
)

var (
	genOutputDir string
	genForce     bool
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
}

func runGenerate(cmd *cobra.Command, args []string) error {
	start := time.Now()
	wd := getWorkDir()

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

	// Load StackKit
	stackkitPath := filepath.Join(stackkitDir, "stackkit.yaml")
	stackkit, err := loader.LoadStackKit(stackkitPath)
	if err != nil {
		return fmt.Errorf("failed to load stackkit: %w", err)
	}

	// Validate CUE schemas before generating
	cueValidator := cueval.NewValidator(wd)
	if cueResult, valErr := cueValidator.ValidateStackKit(stackkitDir); valErr != nil {
		printWarning("CUE validation: %v", valErr)
		deployLog.Warn("cue.validation",
			slog.String("status", "error"),
			slog.String("error", valErr.Error()),
		)
	} else if !cueResult.Valid {
		for _, e := range cueResult.Errors {
			printWarning("CUE: %s: %s", e.Path, e.Message)
		}
		deployLog.Warn("cue.validation",
			slog.String("status", "invalid"),
			slog.Int("error_count", len(cueResult.Errors)),
		)
	} else {
		deployLog.Event("cue.validation",
			slog.String("status", "valid"),
		)
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
	outputPath := filepath.Join(wd, genOutputDir)
	if _, statErr := os.Stat(outputPath); statErr == nil && !genForce {
		return fmt.Errorf("output directory already exists: %s (use --force to overwrite)", outputPath)
	}

	if mkdirErr := os.MkdirAll(outputPath, 0750); mkdirErr != nil {
		return fmt.Errorf("failed to create output directory: %w", mkdirErr)
	}

	// Check if templates use Go templating or are plain files
	err = copyOrRenderTemplates(templateDir, outputPath, spec, stackkit)
	if err != nil {
		return fmt.Errorf("failed to generate files: %w", err)
	}

	// Generate main.tf if not present
	mainTfPath := filepath.Join(outputPath, "main.tf")
	if _, statErr := os.Stat(mainTfPath); os.IsNotExist(statErr) {
		// Generate a basic main.tf
		renderCtx := &template.RenderContext{
			Spec:     spec,
			StackKit: stackkit,
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

	// kombify.me subdomain registration (when domain is kombify.me)
	if isKombifyMeDomain(spec.Domain) {
		if err := registerKombifyMeSubdomains(spec); err != nil {
			printWarning("kombify.me registration: %v", err)
			printInfo("Continuing with existing subdomainPrefix if set")
			deployLog.Warn("kombifyme.registration",
				slog.String("error", err.Error()),
			)
		} else {
			deployLog.Event("kombifyme.registration",
				slog.String("prefix", spec.SubdomainPrefix),
			)
		}
	}

	// Generate terraform.tfvars.json from spec (JSON format for consistency with API)
	tfvarsPath := filepath.Join(outputPath, "terraform.tfvars.json")
	tfvarsData, err := generateTfvarsJSON(spec)
	if err != nil {
		return fmt.Errorf("failed to generate tfvars: %w", err)
	}
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
func copyOrRenderTemplates(srcDir, dstDir string, spec *models.StackSpec, stackkit *models.StackKit) error {
	renderer := template.NewRenderer(srcDir, dstDir)
	renderCtx := &template.RenderContext{
		Spec:     spec,
		StackKit: stackkit,
	}
	return renderer.Render(renderCtx)
}

// generateRandomPassword generates a cryptographically random alphanumeric password.
func generateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", fmt.Errorf("generate random password: %w", err)
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}

// bcryptHash returns a bcrypt hash of the given password.
func bcryptHash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt hash: %w", err)
	}
	return string(hash), nil
}

// generateTfvarsJSON generates terraform.tfvars.json matching the template variables.
// Service enablement is driven by compute tier (v4 architecture):
//   - L1/L2 core (traefik, tinyauth, pocketid) = ALWAYS enabled
//   - L2 PAAS = tier-dependent (Dokploy for standard/high, Dockge for low)
//   - Monitoring = tier-dependent
//
// Per-service overrides can be applied via spec.Services[name]["enabled"].
func generateTfvarsJSON(spec *models.StackSpec) ([]byte, error) { //nolint:gocyclo
	bridge := cueval.NewTerraformBridge(".")
	return bridge.GenerateTFVarsBytesFromSpec(spec)
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

// isKombifyMeDomain returns true if the domain is kombify.me (the subdomain service).
func isKombifyMeDomain(domain string) bool {
	return strings.EqualFold(domain, models.DomainKombifyMe)
}

// registerKombifyMeSubdomains registers base + service subdomains on the kombify.me API
// and sets spec.SubdomainPrefix if not already set.
func registerKombifyMeSubdomains(spec *models.StackSpec) error {
	apiKey := os.Getenv("KOMBIFY_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("KOMBIFY_API_KEY environment variable is required for kombify.me domain")
	}

	homelabName := spec.Name
	if homelabName == "" {
		return fmt.Errorf("spec name is required for kombify.me registration")
	}

	// Device fingerprint: use existing prefix suffix or generate one
	fingerprint := ""
	if spec.SubdomainPrefix != "" {
		// Extract fingerprint from existing prefix: "sh-name-FINGERPRINT"
		parts := strings.SplitN(spec.SubdomainPrefix, "-", 3)
		if len(parts) >= 3 {
			fingerprint = parts[2]
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
