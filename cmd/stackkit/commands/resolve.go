package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/spf13/cobra"
)

const architectureAuthorityRootEnv = "STACKKIT_MODULE_ROOT"

type resolveCLIOptions struct {
	inventoryPath string
	outputPath    string
	moduleRoot    string
}

var resolveCmd = newResolveCommand()

func newResolveCommand() *cobra.Command {
	options := &resolveCLIOptions{}
	cmd := &cobra.Command{
		Use:           "resolve [spec-file]",
		Short:         "Resolve Architecture v2 intent into a canonical deployment plan",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Resolve a StackSpec v2 document and optional observed Inventory into the
canonical ResolvedPlan consumed by generators and runtimes.

The command is read-only unless --output is supplied. StackSpec v1 is never
silently upgraded or compiled: it returns the shared typed migration report and
requires an explicit migration workflow.

Examples:
  stackkit resolve --spec stack-spec.yaml
  stackkit resolve stack-spec.yaml --inventory inventory.yaml
  stackkit resolve stack-spec.yaml --output deploy/.stackkit/resolved-plan.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResolve(cmd, args, options, getWorkDir())
		},
	}
	cmd.Flags().StringVar(&options.inventoryPath, "inventory", "", "Path to an optional observed Inventory document")
	cmd.Flags().StringVarP(&options.outputPath, "output", "o", "", "Write canonical ResolvedPlan JSON to this path instead of stdout")
	cmd.Flags().StringVar(&options.moduleRoot, "module-root", "", "Architecture authority root containing cue.mod, base, and all kit definitions")
	return cmd
}

func runResolve(cmd *cobra.Command, args []string, options *resolveCLIOptions, wd string) error {
	if options == nil {
		return fmt.Errorf("resolve options are not initialized")
	}

	specPath := specFile
	if len(args) == 1 {
		specPath = args[0]
	}
	specPath = resolvePathFromWorkDir(wd, specPath)
	specData, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read StackSpec %s: %w", specPath, err)
	}

	var inventoryData []byte
	if strings.TrimSpace(options.inventoryPath) != "" {
		inventoryPath := resolvePathFromWorkDir(wd, options.inventoryPath)
		inventoryData, err = os.ReadFile(inventoryPath)
		if err != nil {
			return fmt.Errorf("read Inventory %s: %w", inventoryPath, err)
		}
	}

	service, err := newArchitectureV2CLIService(wd, options.moduleRoot, os.Getenv(architectureAuthorityRootEnv))
	if err != nil {
		return err
	}
	result, err := service.Resolve(architecturev2.ResolveInput{
		StackSpec: specData,
		Inventory: inventoryData,
	})
	if err != nil {
		var resolveErr *architecturev2.ResolveError
		if errors.As(err, &resolveErr) && resolveErr.Report != nil {
			// Migration diagnostics are an adapter contract, not prose. Emit the
			// complete report on stderr while leaving stdout empty and preserving
			// the typed service error for in-process callers.
			encoder := json.NewEncoder(cmd.ErrOrStderr())
			encoder.SetEscapeHTML(false)
			if encodeErr := encoder.Encode(resolveErr); encodeErr != nil {
				return fmt.Errorf("emit structured migration report: %v: %w", encodeErr, err)
			}
		}
		// Keep the service's *architecturev2.ResolveError intact so callers can
		// classify migration_required and migration_blocked with errors.As.
		return err
	}

	if strings.TrimSpace(options.outputPath) == "" || options.outputPath == "-" {
		return writeResolvedPlan(cmd.OutOrStdout(), result.CanonicalPlan)
	}
	target := resolvePathFromWorkDir(wd, options.outputPath)
	if _, err := service.PersistCanonicalPlan(target, result.CanonicalPlan); err != nil {
		return fmt.Errorf("persist canonical ResolvedPlan %s: %w", target, err)
	}
	return nil
}

func resolvePathFromWorkDir(wd, path string) string {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(wd, path)
}

// newArchitectureV2CLIService keeps authority selection separate from command
// execution. Installed commands always use the generated embedded authority;
// filesystem CUE is available only through an intentional operator override.
func newArchitectureV2CLIService(wd, flagRoot, environmentRoot string) (*architecturev2.Service, error) {
	root, overridden, err := resolveArchitectureAuthorityOverride(wd, flagRoot, environmentRoot)
	if err != nil {
		return nil, err
	}
	contract := architecturev2.StackKitsV2Contract(version)
	if !overridden {
		return architecturev2.NewEmbeddedService(contract)
	}
	return architecturev2.NewFilesystemService(root, contract)
}

func resolveArchitectureAuthorityOverride(wd, flagRoot, environmentRoot string) (string, bool, error) {
	if root := strings.TrimSpace(flagRoot); root != "" {
		root = resolvePathFromWorkDir(wd, root)
		if err := validateArchitectureAuthorityRoot(root); err != nil {
			return "", false, fmt.Errorf("architecture v2 authority bundle from --module-root is invalid: %w", err)
		}
		return filepath.Clean(root), true, nil
	}
	if root := strings.TrimSpace(environmentRoot); root != "" {
		root = resolvePathFromWorkDir(wd, root)
		if err := validateArchitectureAuthorityRoot(root); err != nil {
			return "", false, fmt.Errorf("architecture v2 authority bundle from %s is invalid: %w", architectureAuthorityRootEnv, err)
		}
		return filepath.Clean(root), true, nil
	}
	return "", false, nil
}

func validateArchitectureAuthorityRoot(root string) error {
	root = filepath.Clean(root)
	required := []string{
		filepath.Join("cue.mod", "module.cue"),
		filepath.Join("base", "architecture_v2_profiles.cue"),
		filepath.Join("base", "architecture_v2.cue"),
		filepath.Join("base", "architecture_v2_catalog.cue"),
	}
	for _, relativePath := range required {
		info, err := os.Stat(filepath.Join(root, relativePath))
		if err != nil {
			return fmt.Errorf("missing %s: %w", filepath.ToSlash(relativePath), err)
		}
		if info.IsDir() {
			return fmt.Errorf("%s is a directory, want a file", filepath.ToSlash(relativePath))
		}
	}
	return nil
}

func writeResolvedPlan(stdout io.Writer, canonical []byte) error {
	_, err := stdout.Write(canonical)
	return err
}
