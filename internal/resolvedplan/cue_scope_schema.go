package resolvedplan

import (
	"path"
	"strconv"

	"cuelang.org/go/cue/ast"
)

// concreteProjectionLabels are the package-level fields of the governed
// foundation module that only project the concrete catalog for build-time
// exporters (bundle generation, the filesystem authority loader):
// ArchitectureV2Catalog and the values derived from it. Product validation
// never reads them, because every runtime contract is a schema (#Definition)
// and the catalog itself arrives as the hash-verified bundle document. CUE
// evaluates the whole package eagerly, so keeping them in the long-lived
// authority scope costs about 380 MB of live heap and six seconds per process
// for data that is already exported verbatim.
var concreteProjectionLabels = map[string]struct{}{
	"ArchitectureV2Catalog":      {},
	"ArchitectureV2ModuleImages": {},
	"NeutralImageProfiles":       {},
}

// omitConcreteProjections removes the concrete catalog projection fields from
// a parsed foundation file, together with any import that only they used.
// Schemas, hidden helper values and every other regular field are untouched.
func omitConcreteProjections(file *ast.File) {
	if file == nil {
		return
	}
	kept := file.Decls[:0:0]
	dropped := false
	for _, decl := range file.Decls {
		if field, ok := decl.(*ast.Field); ok {
			if ident, ok := field.Label.(*ast.Ident); ok {
				if _, projection := concreteProjectionLabels[ident.Name]; projection {
					dropped = true
					continue
				}
			}
		}
		kept = append(kept, decl)
	}
	if !dropped {
		return
	}
	file.Decls = kept
	used := map[string]struct{}{}
	for _, decl := range file.Decls {
		if _, isImport := decl.(*ast.ImportDecl); isImport {
			continue
		}
		ast.Walk(decl, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok {
				used[ident.Name] = struct{}{}
			}
			return true
		}, nil)
	}
	decls := file.Decls[:0:0]
	for _, decl := range file.Decls {
		importDecl, ok := decl.(*ast.ImportDecl)
		if !ok {
			decls = append(decls, decl)
			continue
		}
		specs := importDecl.Specs[:0:0]
		for _, spec := range importDecl.Specs {
			if _, referenced := used[importedName(spec)]; referenced {
				specs = append(specs, spec)
			}
		}
		if len(specs) == 0 {
			continue
		}
		importDecl.Specs = specs
		decls = append(decls, importDecl)
	}
	file.Decls = decls
}

func importedName(spec *ast.ImportSpec) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	importPath, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		return spec.Path.Value
	}
	return path.Base(importPath)
}
