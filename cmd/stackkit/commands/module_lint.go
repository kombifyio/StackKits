package commands

// module_lint.go implements `stackkit module lint` (ADR-0027 Decision 3, gates
// G1+G3). It extracts each target module's contract from CUE and runs the
// deterministic hygiene rules in internal/lint. Single-module runs default to
// strict (non-zero exit on any error); `--all` is advisory unless `--strict`
// is given, so wiring it over the existing corpus never reds main while the
// gate still fails clean single-module proposal/tool-update PRs.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	skcue "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/lint"
	"github.com/spf13/cobra"
)

var (
	moduleLintModule     string
	moduleLintAll        bool
	moduleLintModulesDir string
	moduleLintStrict     bool
	moduleLintJSON       bool
)

var moduleLintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Lint module CUE for pin/health/security/access/placement hygiene (ADR-0027 G1/G3)",
	Long: "Run the deterministic module-hygiene gate over one module (--module) or the\n" +
		"whole tree (--all). Checks: image tags pinned (no :latest), daemon healthCheck present\n" +
		"(bounded automation jobs use restart=no plus process exit status),\n" +
		"security block (noNewPrivileges + capDrop ALL), accessPolicy for routed services,\n" +
		"no plaintext secrets, draft modules claim no scenarios, and docker-socket modules\n" +
		"are not managed-serverless-eligible.\n\n" +
		"Single-module runs exit non-zero on any error. --all is advisory (exit 0) unless\n" +
		"--strict is passed.",
	RunE: runModuleLint,
}

func init() {
	moduleLintCmd.Flags().StringVar(&moduleLintModule, "module", "", "Path to a single module directory (containing module.cue)")
	moduleLintCmd.Flags().BoolVar(&moduleLintAll, "all", false, "Lint every module under --modules-dir")
	moduleLintCmd.Flags().StringVar(&moduleLintModulesDir, "modules-dir", "modules", "Root modules directory (used with --all)")
	moduleLintCmd.Flags().BoolVar(&moduleLintStrict, "strict", false, "Exit non-zero on any error finding (implied for single --module)")
	moduleLintCmd.Flags().BoolVar(&moduleLintJSON, "json", false, "Emit findings as JSON")

	moduleCmd.AddCommand(moduleLintCmd)
}

func runModuleLint(cmd *cobra.Command, args []string) error {
	paths, err := resolveLintTargets()
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no modules to lint (use --module <dir> or --all)")
	}

	reader := skcue.NewModuleReader()
	var all []lint.Finding
	readErrors := 0
	for _, p := range paths {
		contract, err := reader.ReadModule(p)
		if err != nil {
			readErrors++
			all = append(all, lint.Finding{
				Module:   filepath.Base(p),
				Code:     "read-failed",
				Severity: lint.SeverityError,
				Message:  fmt.Sprintf("cannot read module: %v", err),
			})
			continue
		}
		all = append(all, lint.Module(contract)...)
	}

	if moduleLintJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if all == nil {
			all = []lint.Finding{}
		}
		if err := enc.Encode(all); err != nil {
			return err
		}
	} else {
		for _, f := range all {
			switch f.Severity {
			case lint.SeverityError:
				printError("%s", f.String())
			default:
				printWarning("%s", f.String())
			}
		}
	}

	errCount, warnCount := lint.CountBySeverity(all)
	if !moduleLintJSON {
		printInfo("lint: %d module(s), %d error(s), %d warning(s)", len(paths), errCount, warnCount)
	}

	// Single --module is always strict; --all needs --strict to gate.
	strict := moduleLintStrict || (moduleLintModule != "" && !moduleLintAll)
	if errCount > 0 && strict {
		return fmt.Errorf("module lint: %d error finding(s)", errCount)
	}
	if readErrors > 0 && strict {
		return fmt.Errorf("module lint: %d module(s) failed to read", readErrors)
	}
	return nil
}

// resolveLintTargets returns the list of module directories to lint.
func resolveLintTargets() ([]string, error) {
	switch {
	case moduleLintAll:
		entries, err := os.ReadDir(moduleLintModulesDir)
		if err != nil {
			return nil, fmt.Errorf("read modules dir: %w", err)
		}
		var paths []string
		for _, e := range entries {
			if !e.IsDir() || e.Name()[0] == '_' || e.Name()[0] == '.' {
				continue
			}
			p := filepath.Join(moduleLintModulesDir, e.Name())
			if _, err := os.Stat(filepath.Join(p, "module.cue")); err == nil {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		return paths, nil
	case moduleLintModule != "":
		return []string{moduleLintModule}, nil
	default:
		return nil, nil
	}
}
