//go:build publisher

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/parser"
	skcue "github.com/kombifyio/stackkits/internal/cue"
	"github.com/spf13/cobra"
)

type foundationOwnedImage struct{ module, component string }

var observedImageTag = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)
var observedImageDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func validateObservedImage(tag, digest string) error {
	if !observedImageTag.MatchString(tag) || !observedImageDigest.MatchString(digest) {
		return fmt.Errorf("an observed image tag and lowercase sha256 digest are required")
	}
	return nil
}

// Follow the actual module import, not a separate slug-to-owner registry. An
// imported tag that cannot be resolved here must never be replaced by a literal.
func foundationImageOwner(path string, source []byte, service string) (*foundationOwnedImage, error) {
	file, err := parser.ParseFile(path, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var tags []ast.Expr
	astutil.Apply(file, func(c astutil.Cursor) bool {
		if value, ok := c.Node().(*ast.StructLit); ok && structHasNameField(value, service) {
			if tag := bumpField(value.Elts, "tag"); tag != nil {
				tags = append(tags, tag.Value)
			}
		}
		return true
	}, nil)
	if len(tags) != 1 {
		return nil, fmt.Errorf("service %q must have one authored image tag", service)
	}
	if _, literalTag := tags[0].(*ast.BasicLit); literalTag {
		return nil, nil
	}
	selected, ok := tags[0].(*ast.SelectorExpr)
	if !ok || bumpLabel(selected.Sel) != "tag" {
		return nil, fmt.Errorf("unsupported imported image authority for %q", service)
	}
	component, ok := selected.X.(*ast.IndexExpr)
	if !ok {
		return nil, fmt.Errorf("unsupported imported component for %q", service)
	}
	variable, ok := component.X.(*ast.Ident)
	if !ok {
		return nil, fmt.Errorf("unsupported imported image binding for %q", service)
	}
	binding := bumpField(file.Decls, variable.Name)
	if binding == nil {
		return nil, fmt.Errorf("missing imported image binding for %q", service)
	}
	module, ok := binding.Value.(*ast.IndexExpr)
	if !ok {
		return nil, fmt.Errorf("unsupported imported module for %q", service)
	}
	projection, ok := module.X.(*ast.SelectorExpr)
	if !ok || bumpLabel(projection.Sel) != "ArchitectureV2ModuleImages" {
		return nil, fmt.Errorf("unsupported image projection for %q", service)
	}
	alias, ok := projection.X.(*ast.Ident)
	if !ok {
		return nil, fmt.Errorf("unsupported Foundation import for %q", service)
	}
	canonicalImport := false
	for _, imported := range file.Imports {
		name := "foundation"
		if imported.Name != nil {
			name = imported.Name.Name
		}
		value, _ := literal.Unquote(imported.Path.Value)
		canonicalImport = canonicalImport || (name == alias.Name && value == "github.com/kombifyio/stackkits/foundation")
	}
	moduleID, moduleOK := bumpString(module.Index)
	componentID, componentOK := bumpString(component.Index)
	if !canonicalImport || !moduleOK || !componentOK || moduleID == "" || componentID == "" {
		return nil, fmt.Errorf("image binding does not select a concrete canonical Foundation owner")
	}
	return &foundationOwnedImage{module: moduleID, component: componentID}, nil
}

func bumpField(declarations []ast.Decl, name string) *ast.Field {
	for _, declaration := range declarations {
		if field, ok := declaration.(*ast.Field); ok {
			if label, _, err := ast.LabelName(field.Label); err == nil && label == name {
				return field
			}
		}
	}
	return nil
}

func bumpLabel(label ast.Label) string {
	name, _, _ := ast.LabelName(label)
	return name
}

func bumpString(expression ast.Expr) (string, bool) {
	value, ok := expression.(*ast.BasicLit)
	if !ok {
		return "", false
	}
	text, err := literal.Unquote(value.Value)
	return text, err == nil
}

func bumpFoundationImage(cmd *cobra.Command, moduleDirectory string, before skcue.ModuleContract, owner foundationOwnedImage) error {
	if err := validateObservedImage(moduleBumpTag, moduleBumpDigest); err != nil {
		return err
	}
	root := moduleDirectory
	for {
		if info, err := os.Stat(filepath.Join(root, "cue.mod", "module.cue")); err == nil && info.Mode().IsRegular() {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return fmt.Errorf("Foundation image owner has no CUE module root")
		}
		root = parent
	}
	path, err := resolveModuleArtifactPath(filepath.Join(root, "foundation"), "architecture_v2_catalog.cue")
	if err != nil {
		return err
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	service := before.Services[moduleBumpService]
	oldTag, oldDigest, bound := strings.Cut(service.Tag, "@")
	if !bound || !observedImageDigest.MatchString(oldDigest) {
		return fmt.Errorf("current Foundation projection has no immutable image digest")
	}
	newTag := moduleBumpTag + "@" + moduleBumpDigest
	if service.Tag == newTag {
		printInfo("%s/%s already at image %s:%s", before.Metadata.Name, moduleBumpService, service.Image, newTag)
		return nil
	}
	rewritten, err := rewriteFoundationImage(path, source, owner, service.Image+":"+oldTag, oldDigest, service.Image+":"+moduleBumpTag, moduleBumpDigest)
	if err != nil {
		return err
	}
	hashBefore, err := skcue.ContractHash(moduleContractToCanonicalMap(before))
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, rewritten, info.Mode().Perm()); err != nil {
		return err
	}
	restore := func(cause error) error {
		if err := os.WriteFile(path, source, info.Mode().Perm()); err != nil {
			return fmt.Errorf("%v; restore Foundation authority: %w", cause, err)
		}
		return cause
	}
	after, err := skcue.NewModuleReader().ReadModule(moduleDirectory)
	if err != nil {
		return restore(err)
	}
	hashAfter, err := skcue.ContractHash(moduleContractToCanonicalMap(after))
	if err != nil || hashBefore != hashAfter || after.Services[moduleBumpService].Image != service.Image || after.Services[moduleBumpService].Tag != newTag {
		return restore(fmt.Errorf("Foundation rewrite changed the module contract or did not propagate the exact image: %v", err))
	}
	if moduleBumpDryRun {
		if err := restore(nil); err != nil {
			return err
		}
		_, err := cmd.OutOrStdout().Write(rewritten)
		return err
	}
	printSuccess("bumped Foundation %s/%s to %s:%s; regenerate with mise run generate:architecture-v2", owner.module, owner.component, service.Image, newTag)
	return nil
}

func rewriteFoundationImage(path string, source []byte, owner foundationOwnedImage, oldRef, oldDigest, newRef, newDigest string) ([]byte, error) {
	file, err := parser.ParseFile(path, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var images []*ast.StructLit
	astutil.Apply(file, func(c astutil.Cursor) bool {
		module, ok := c.Node().(*ast.StructLit)
		if !ok {
			return true
		}
		metadata := bumpField(module.Elts, "metadata")
		if metadata == nil {
			return true
		}
		identity, ok := metadata.Value.(*ast.StructLit)
		if !ok {
			return true
		}
		id := bumpField(identity.Elts, "id")
		if id == nil {
			return true
		}
		if name, _ := bumpString(id.Value); name != owner.module {
			return true
		}
		astutil.Apply(module, func(cursor astutil.Cursor) bool {
			component, ok := cursor.Node().(*ast.StructLit)
			if !ok {
				return true
			}
			id, image := bumpField(component.Elts, "id"), bumpField(component.Elts, "image")
			if id != nil && image != nil {
				if name, _ := bumpString(id.Value); name == owner.component {
					if value, ok := image.Value.(*ast.StructLit); ok {
						images = append(images, value)
					}
				}
			}
			return true
		}, nil)
		return false
	}, nil)
	if len(images) != 1 {
		return nil, fmt.Errorf("Foundation owner %s/%s must select exactly one concrete image", owner.module, owner.component)
	}
	type replacement struct {
		start, end int
		value      string
	}
	var changes []replacement
	for _, item := range []struct{ field, old, next string }{{"ref", oldRef, newRef}, {"digest", oldDigest, newDigest}} {
		field := bumpField(images[0].Elts, item.field)
		if field == nil {
			return nil, fmt.Errorf("Foundation owner has no concrete %s", item.field)
		}
		if value, ok := bumpString(field.Value); !ok || value != item.old {
			return nil, fmt.Errorf("Foundation %s differs from the consumed module projection", item.field)
		}
		changes = append(changes, replacement{field.Value.Pos().Offset(), field.Value.End().Offset(), strconv.Quote(item.next)})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].start > changes[j].start })
	rewritten := append([]byte(nil), source...)
	for _, change := range changes {
		rewritten = append(append(append([]byte(nil), rewritten[:change.start]...), change.value...), rewritten[change.end:]...)
	}
	return rewritten, nil
}
