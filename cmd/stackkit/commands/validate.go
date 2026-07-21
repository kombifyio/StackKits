package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/iac"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/kombifyio/stackkits/internal/tofu"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var validateAll bool

var validateCmd = &cobra.Command{
	Use:   "validate [file]",
	Short: "Validate configuration files",
	Long: `Validate spec files and CUE schemas.

This command validates:
  • stack-spec.yaml against the schema
  • StackKit CUE definitions
  • OpenTofu configuration files

Examples:
  stackkit validate                  Validate current spec
  stackkit validate spec.yaml        Validate specific file
  stackkit validate --all            Validate all files`,
	Args: cobra.MaximumNArgs(1),
	RunE: runValidate,
}

func init() {
	validateCmd.Flags().BoolVar(&validateAll, "all", false, "Validate all configuration files")
}

func runValidate(cmd *cobra.Command, args []string) error {
	wd := getWorkDir()
	loader := config.NewLoader(wd)
	validator := cue.NewValidator(wd)

	targetFile := specFile
	if len(args) > 0 {
		targetFile = args[0]
	}
	if err := requireNativeV2StackSpec(wd, targetFile, architectureV2Validate); err != nil {
		return err
	}

	hasErrors := false
	v2Handled, v2Err := validateArchitectureV2Spec(wd, targetFile)
	if v2Handled {
		if v2Err != nil {
			return v2Err
		}
		if validateAll && validateCUESchemas(validator, wd) {
			return fmt.Errorf("validation failed")
		}
		fmt.Println()
		printSuccess("All validations passed!")
		return nil
	}

	// Validate spec file
	spec, specHasErrors := validateSpecFile(loader, validator, targetFile)
	if specHasErrors {
		hasErrors = true
	}

	// Validate all CUE files if requested
	if validateAll {
		if validateCUESchemas(validator, wd) {
			hasErrors = true
		}
	}

	// Validate OpenTofu files
	deployDir := filepath.Join(wd, config.GetDeployDir())
	if _, statErr := os.Stat(deployDir); statErr == nil {
		if validateIaCFiles(spec, deployDir) {
			hasErrors = true
		}
	}

	fmt.Println()
	if hasErrors {
		return fmt.Errorf("validation failed")
	}

	printSuccess("All validations passed!")
	return nil
}

func validateArchitectureV2Spec(wd, targetFile string) (bool, error) {
	rawSpec, sourceVersion, handled, err := classifyArchitectureV2ExecutionSpec(wd, targetFile)
	if err != nil || !handled {
		return handled, err
	}
	gate := newArchitectureV2ExecutionGate()
	if sourceVersion == stackspecmigration.SourceVersionV1 {
		if !gate.rejectV1 {
			return false, nil
		}
		return true, gate.rejectV1Execution(rawSpec, architectureV2Validate)
	}
	if gate.newAuthority == nil {
		return true, fmt.Errorf("validate: Architecture v2 authority is not configured")
	}
	authority, err := gate.newAuthority()
	if err != nil {
		return true, err
	}
	current, err := authority.ResolveCurrent(architecturev2.ResolveInput{StackSpec: rawSpec})
	if err != nil {
		return true, err
	}
	result, err := current.Result()
	if err != nil {
		return true, err
	}
	printSuccess("StackSpec v2 is valid (plan %s)", result.PlanHash)
	return true, nil
}

// validateSpecFile validates the stack-spec.yaml file and returns the parsed spec (may be nil) and whether errors occurred.
func validateSpecFile(loader *config.Loader, validator *cue.Validator, targetFile string) (*models.StackSpec, bool) {
	_, displayPath, aliasUsed, err := loader.ResolveStackSpecPathForRead(targetFile)
	if err != nil {
		printError("Failed to resolve spec path: %v", err)
		return nil, true
	}
	if aliasUsed {
		printInfo("Validating %s (alias for %s)...", displayPath, targetFile)
	} else {
		printInfo("Validating %s...", displayPath)
	}

	spec, err := loader.LoadStackSpec(targetFile)
	if err != nil {
		if os.IsNotExist(err) {
			printWarning("Spec file not found: %s", targetFile)
		} else {
			printError("Failed to load spec: %v", err)
			return nil, true
		}
		return nil, false
	}

	result, err := validator.ValidateSpec(spec)
	if err != nil {
		printError("Validation error: %v", err)
		return spec, true
	}
	if !result.Valid {
		printError("Spec validation failed:")
		for _, e := range result.Errors {
			fmt.Printf("  • %s: %s\n", red(e.Path), e.Message)
		}
		return spec, true
	}

	printSuccess("Spec file is valid")
	for _, w := range result.Warnings {
		printWarning("%s: %s", w.Path, w.Message)
	}
	return spec, false
}

// validateCUESchemas validates all CUE files in the working directory. Returns true if errors were found.
func validateCUESchemas(validator *cue.Validator, wd string) bool {
	fmt.Println()
	printInfo("Validating CUE schemas...")

	cueFiles, err := findCUEFiles(wd)
	if err != nil {
		printWarning("Could not find CUE files: %v", err)
		return false
	}

	hasErrors := false
	for _, cueFile := range cueFiles {
		relPath, _ := filepath.Rel(wd, cueFile)
		result, err := validator.ValidateCUEFile(cueFile)
		if err != nil {
			printError("%s: %v", relPath, err)
			hasErrors = true
		} else if !result.Valid {
			printError("%s:", relPath)
			for _, e := range result.Errors {
				fmt.Printf("  • %s\n", e.Message)
			}
			hasErrors = true
		} else {
			printSuccess("%s is valid", relPath)
		}
	}
	return hasErrors
}

// validateIaCFiles validates OpenTofu/Terramate configuration files. Returns true if errors were found.
func validateIaCFiles(spec *models.StackSpec, deployDir string) bool {
	fmt.Println()
	printInfo("Validating OpenTofu configuration...")

	hasTF, err := tofu.HasTerraformFiles(deployDir)
	if err != nil {
		printWarning("Could not check for .tf files: %v", err)
		return false
	}
	if !hasTF {
		printWarning("No .tf files found in %s", deployDir)
		return false
	}

	executor, err := createIaCExecutor(spec, deployDir)
	if err != nil {
		printError("Failed to create executor: %v", err)
		return true
	}
	if !executor.IsInstalled() {
		printWarning("%s is not installed — skipping validate", executor.Mode())
		return false
	}

	// Initialize if needed (validate requires init)
	tfStatePath := filepath.Join(deployDir, ".terraform")
	if _, statErr := os.Stat(tfStatePath); os.IsNotExist(statErr) {
		printInfo("Initializing %s...", executor.Mode())
		ctx := context.Background()
		if initErr := executor.Init(ctx); initErr != nil {
			printError("Init error: %v", initErr)
			return true
		}
	}

	ctx := context.Background()
	result, err := executor.Validate(ctx)
	if err != nil {
		printError("Validate error: %v", err)
		return true
	}
	if !result.Success {
		printIaCValidationErrors(result)
		return true
	}

	printSuccess("IaC configuration is valid")
	return false
}

func createIaCExecutor(spec *models.StackSpec, deployDir string) (iac.Executor, error) {
	if spec != nil {
		return iac.NewExecutorFromSpec(spec, deployDir)
	}
	return iac.NewExecutor(&iac.Config{WorkDir: deployDir, Mode: iac.ModeOpenTofu})
}

func printIaCValidationErrors(result *iac.ExecResult) {
	printError("Validation failed:")

	var valResult struct {
		Valid       bool `json:"valid"`
		ErrorCount  int  `json:"error_count"`
		Diagnostics []struct {
			Severity string `json:"severity"`
			Summary  string `json:"summary"`
			Detail   string `json:"detail"`
		} `json:"diagnostics"`
	}

	if jsonErr := json.Unmarshal([]byte(result.Stdout), &valResult); jsonErr == nil {
		for _, d := range valResult.Diagnostics {
			if d.Severity == "error" {
				fmt.Printf("  • %s: %s\n", red(d.Summary), d.Detail)
			} else {
				printWarning("%s: %s", d.Summary, d.Detail)
			}
		}
		return
	}

	// Fallback: print raw output
	if result.Stderr != "" {
		fmt.Println(result.Stderr)
	}
	if result.Stdout != "" {
		fmt.Println(result.Stdout)
	}
}

func findCUEFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip hidden directories and node_modules
		if info.IsDir() {
			name := info.Name()
			if name == "node_modules" || name == ".git" || name == "cue.mod" {
				return filepath.SkipDir
			}
		}
		// Include .cue files
		if !info.IsDir() && filepath.Ext(path) == ".cue" {
			files = append(files, path)
		}
		return nil
	})

	return files, err
}
