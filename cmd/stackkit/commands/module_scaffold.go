package commands

// module_scaffold.go implements `stackkit module scaffold` (ADR-0027 Decision 1).
// It renders a module's artifacts deterministically from a schema-validated
// module_facts.json — module.cue, tests/reference-compose.yml, and the thin
// tests/integration_test.sh manifest that sources the shared smoke harness.
// The agent authors facts; this command authors CUE.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kombifyio/stackkits/internal/scaffold"
	"github.com/spf13/cobra"
)

var (
	moduleScaffoldFacts string
	moduleScaffoldOut   string
	moduleScaffoldForce bool
	moduleScaffoldPrint bool
)

var moduleScaffoldCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Render module artifacts deterministically from module_facts.json (ADR-0027)",
	Long: "Render modules/<slug>/module.cue, tests/reference-compose.yml and the thin\n" +
		"tests/integration_test.sh from a schema-validated module_facts.json. Output is\n" +
		"deterministic (gate G0): the same facts always render byte-identical files.",
	RunE: runModuleScaffold,
}

func init() {
	moduleScaffoldCmd.Flags().StringVar(&moduleScaffoldFacts, "facts", "", "Path to module_facts.json. Required.")
	moduleScaffoldCmd.Flags().StringVar(&moduleScaffoldOut, "out", "", "Output module directory (default modules/<slug>)")
	moduleScaffoldCmd.Flags().BoolVar(&moduleScaffoldForce, "force", false, "Overwrite existing files")
	moduleScaffoldCmd.Flags().BoolVar(&moduleScaffoldPrint, "print", false, "Print rendered artifacts to stdout instead of writing")

	moduleCmd.AddCommand(moduleScaffoldCmd)
}

func runModuleScaffold(cmd *cobra.Command, args []string) error {
	if moduleScaffoldFacts == "" {
		return fmt.Errorf("--facts is required")
	}
	data, err := os.ReadFile(moduleScaffoldFacts)
	if err != nil {
		return fmt.Errorf("read facts: %w", err)
	}
	facts, err := scaffold.LoadFacts(data)
	if err != nil {
		return err
	}

	artifacts, err := scaffold.Render(facts)
	if err != nil {
		return err
	}

	// deterministic iteration for stable output/logging
	names := make([]string, 0, len(artifacts))
	for n := range artifacts {
		names = append(names, n)
	}
	sort.Strings(names)

	if moduleScaffoldPrint {
		for _, n := range names {
			fmt.Printf("===== %s =====\n%s\n", n, artifacts[n])
		}
		return nil
	}

	outDir := moduleScaffoldOut
	if outDir == "" {
		outDir = filepath.Join("modules", facts.Slug)
	}

	// pre-flight: refuse to clobber unless --force
	if !moduleScaffoldForce {
		for _, n := range names {
			p, err := resolveModuleArtifactPath(outDir, n)
			if err != nil {
				return err
			}
			if _, err := os.Stat(p); err == nil { // #nosec G703 -- p is constrained to a rendered artifact path under outDir.
				return fmt.Errorf("%s already exists (use --force to overwrite)", p)
			}
		}
	}

	for _, n := range names {
		p, err := resolveModuleArtifactPath(outDir, n)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { // #nosec G703 -- p is constrained to a rendered artifact path under outDir.
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
		}
		mode := os.FileMode(0o644)
		if filepath.Base(n) == "integration_test.sh" {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(artifacts[n]), mode); err != nil { // #nosec G703 -- p is constrained to a rendered artifact path under outDir.
			return fmt.Errorf("write %s: %w", p, err)
		}
		printSuccess("wrote %s", p)
	}
	printInfo("scaffolded module %q (%d file(s)) — run `stackkit module lint --module %s` to gate it", facts.Slug, len(names), outDir)
	return nil
}
