package commands

// module_bump.go implements `stackkit module bump` (ADR-0028 Decision 3): the
// deterministic tag-rewrite the tool-update-pr.yml workflow runs when the Admin
// watch job dispatches an in-policy upstream release. It rewrites exactly one
// service's image tag in module.cue via the CUE AST (so unrelated services and
// formatting are untouched), re-validates the module, and asserts the module
// contract hash is unchanged — a tag is version/runtime data, never part of the
// contract, so a hash delta means an unexpected structural edit.

import (
	"fmt"
	"os"
	"path/filepath"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/parser"
	skcue "github.com/kombifyio/stackkits/internal/cue"
	"github.com/spf13/cobra"
)

var (
	moduleBumpModule  string
	moduleBumpService string
	moduleBumpTag     string
	moduleBumpDryRun  bool
)

var moduleBumpCmd = &cobra.Command{
	Use:   "bump",
	Short: "Rewrite a single service's image tag deterministically (ADR-0028 tool-update)",
	Long: "Rewrite one service's `tag` in modules/<slug>/module.cue via the CUE AST,\n" +
		"re-validate the module, and assert the contract hash is unchanged. Idempotent:\n" +
		"bumping to the current tag is a no-op. Used by the tool-update-pr.yml workflow.",
	RunE: runModuleBump,
}

func init() {
	moduleBumpCmd.Flags().StringVar(&moduleBumpModule, "module", "", "Path to the module directory (containing module.cue). Required.")
	moduleBumpCmd.Flags().StringVar(&moduleBumpService, "service", "", "Service name whose tag to rewrite. Required.")
	moduleBumpCmd.Flags().StringVar(&moduleBumpTag, "tag", "", "New image tag. Required.")
	moduleBumpCmd.Flags().BoolVar(&moduleBumpDryRun, "dry-run", false, "Print the result without writing")

	moduleCmd.AddCommand(moduleBumpCmd)
}

func runModuleBump(cmd *cobra.Command, args []string) error {
	if moduleBumpModule == "" || moduleBumpService == "" || moduleBumpTag == "" {
		return fmt.Errorf("--module, --service and --tag are all required")
	}
	absPath, err := filepath.Abs(moduleBumpModule)
	if err != nil {
		return fmt.Errorf("resolve module path: %w", err)
	}
	cuePath, err := resolveModuleArtifactPath(absPath, "module.cue")
	if err != nil {
		return err
	}
	src, err := os.ReadFile(cuePath)
	if err != nil {
		return fmt.Errorf("read module.cue: %w", err)
	}

	// hash before (tag must not move the contract hash)
	reader := skcue.NewModuleReader()
	before, err := reader.ReadModule(absPath)
	if err != nil {
		return fmt.Errorf("read module (pre-bump): %w", err)
	}
	if _, ok := before.Services[moduleBumpService]; !ok {
		return fmt.Errorf("service %q not found in module %s", moduleBumpService, before.Metadata.Name)
	}
	oldTag := before.Services[moduleBumpService].Tag
	hashBefore, err := skcue.ContractHash(moduleContractToCanonicalMap(before))
	if err != nil {
		return fmt.Errorf("hash (pre-bump): %w", err)
	}

	if oldTag == moduleBumpTag {
		printInfo("%s/%s already at tag %q — no change", before.Metadata.Name, moduleBumpService, moduleBumpTag)
		return nil
	}

	rewritten, replaced, err := rewriteServiceTag(cuePath, src, moduleBumpService, moduleBumpTag)
	if err != nil {
		return err
	}
	if !replaced {
		return fmt.Errorf("could not locate tag field for service %q in %s", moduleBumpService, cuePath)
	}

	// Stage the rewrite in place so the reader resolves the base import against
	// the same module root, validate, and restore the original on any failure.
	restore := func() { _ = os.WriteFile(cuePath, src, 0o644) }     // #nosec G703 -- cuePath is the fixed module.cue artifact resolved under the operator-supplied module directory.
	if err := os.WriteFile(cuePath, rewritten, 0o644); err != nil { // #nosec G703 -- cuePath is the fixed module.cue artifact resolved under the operator-supplied module directory.
		return fmt.Errorf("stage rewritten: %w", err)
	}

	after, verr := skcue.NewModuleReader().ReadModule(absPath)
	if verr != nil {
		restore()
		return fmt.Errorf("rewritten module fails to validate (cue): %w", verr)
	}
	hashAfter, herr := skcue.ContractHash(moduleContractToCanonicalMap(after))
	if herr != nil {
		restore()
		return fmt.Errorf("hash (post-bump): %w", herr)
	}
	if after.Services[moduleBumpService].Tag != moduleBumpTag {
		restore()
		return fmt.Errorf("rewrite did not take effect (tag=%q)", after.Services[moduleBumpService].Tag)
	}
	if hashBefore != hashAfter {
		restore()
		return fmt.Errorf("contract hash changed by tag bump (%s -> %s) — the edit was not tag-only", shortHash(hashBefore), shortHash(hashAfter))
	}

	if moduleBumpDryRun {
		restore()
		fmt.Print(string(rewritten))
		printInfo("--dry-run: %s/%s %s -> %s (contract_hash stable %s)", before.Metadata.Name, moduleBumpService, oldTag, moduleBumpTag, shortHash(hashAfter))
		return nil
	}

	printSuccess("bumped %s/%s %s -> %s (contract_hash stable %s)", before.Metadata.Name, moduleBumpService, oldTag, moduleBumpTag, shortHash(hashAfter))
	return nil
}

// rewriteServiceTag parses module.cue, finds the struct literal that defines the
// target service (identified by its `name:` field equalling the service name),
// replaces that struct's `tag` field value, and returns formatted CUE. It never
// touches other services or reflows unrelated formatting.
func rewriteServiceTag(path string, src []byte, service, newTag string) ([]byte, bool, error) {
	file, err := parser.ParseFile(path, src, parser.ParseComments)
	if err != nil {
		return nil, false, fmt.Errorf("parse module.cue: %w", err)
	}

	replaced := false
	astutil.Apply(file, func(c astutil.Cursor) bool {
		st, ok := c.Node().(*ast.StructLit)
		if !ok {
			return true
		}
		if replaced || !structHasNameField(st, service) {
			return true
		}
		for _, el := range st.Elts {
			f, ok := el.(*ast.Field)
			if !ok {
				continue
			}
			if name, _, err := ast.LabelName(f.Label); err == nil && name == "tag" {
				f.Value = ast.NewString(newTag)
				replaced = true
				return false
			}
		}
		return true
	}, nil)

	if !replaced {
		return nil, false, nil
	}
	formatted, err := format.Node(file)
	if err != nil {
		return nil, false, fmt.Errorf("format rewritten CUE: %w", err)
	}
	return formatted, true, nil
}

// structHasNameField reports whether a struct literal contains `name: "<want>"`.
func structHasNameField(st *ast.StructLit, want string) bool {
	for _, el := range st.Elts {
		f, ok := el.(*ast.Field)
		if !ok {
			continue
		}
		name, _, err := ast.LabelName(f.Label)
		if err != nil || name != "name" {
			continue
		}
		if bl, ok := f.Value.(*ast.BasicLit); ok {
			if s, err := literal.Unquote(bl.Value); err == nil && s == want {
				return true
			}
		}
	}
	return false
}
