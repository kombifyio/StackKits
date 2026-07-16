package resolvedplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cueapi "cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/parser"
)

// CUEContractValidator is the non-substitutable schema authority used by
// Compiler. Its state can only be initialized by NewCUEContractValidator, so a
// caller cannot inject a no-op implementation and bypass #KitSpecBinding or
// #ResolvedPlan validation.
type CUEContractValidator struct {
	moduleRoot      string
	authoritySource map[string][]byte
	planAuthority   PlanAuthority
	boundAuthority  *expectedAuthorityBinding
	boundCatalog    *expectedCatalogBodyBinding
	initialized     bool
}

// NewCUEContractValidatorFromSources binds validation to an immutable,
// in-memory CUE module. virtualModuleRoot is an absolute namespace only; the
// constructor and subsequent validation never trust or materialize authority
// from that host path.
// Source keys are module-relative slash paths such as cue.mod/module.cue.
func NewCUEContractValidatorFromSources(virtualModuleRoot string, sources map[string][]byte) (*CUEContractValidator, error) {
	return NewCUEContractValidatorFromSourcesForAuthority(virtualModuleRoot, sources, DevelopmentPlanAuthority())
}

// NewCUEContractValidatorFromSourcesForAuthority additionally binds every
// verified plan to one exact product or contract-fixture authority class.
func NewCUEContractValidatorFromSourcesForAuthority(virtualModuleRoot string, sources map[string][]byte, authority PlanAuthority) (*CUEContractValidator, error) {
	if err := validatePlanAuthority(authority); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(virtualModuleRoot) {
		return nil, fmt.Errorf("CUE virtual module root must be absolute: %q", virtualModuleRoot)
	}
	moduleRoot := filepath.Clean(virtualModuleRoot)
	if len(sources) == 0 {
		return nil, fmt.Errorf("CUE in-memory authority sources are required")
	}
	frozen := make(map[string][]byte, len(sources))
	for relativePath, source := range sources {
		cleanPath := filepath.ToSlash(filepath.Clean(relativePath))
		if cleanPath == "." || filepath.IsAbs(relativePath) || strings.HasPrefix(cleanPath, "../") {
			return nil, fmt.Errorf("unsafe CUE authority source path %q", relativePath)
		}
		if len(bytes.TrimSpace(source)) == 0 {
			return nil, fmt.Errorf("CUE authority source %s is empty", cleanPath)
		}
		if _, exists := frozen[cleanPath]; exists {
			return nil, fmt.Errorf("duplicate CUE authority source path %s", cleanPath)
		}
		frozen[cleanPath] = bytes.Clone(source)
	}
	for _, required := range []string{
		"cue.mod/module.cue",
		"base/architecture_v2_profiles.cue",
		"base/architecture_v2.cue",
		"base/architecture_v2_definition_binding.cue",
	} {
		if len(frozen[required]) == 0 {
			return nil, fmt.Errorf("CUE in-memory authority is missing %s", required)
		}
	}
	validator := &CUEContractValidator{moduleRoot: moduleRoot, authoritySource: frozen, planAuthority: authority, initialized: true}
	if err := validator.validateExpression("constructor", "base.#ArchitectureAPIVersion"); err != nil {
		return nil, fmt.Errorf("load StackKits Architecture v2 in-memory CUE authority: %w", err)
	}
	return validator, nil
}

type cueBindingError struct {
	cause           error
	profileMismatch bool
}

func (e *cueBindingError) Error() string { return e.cause.Error() }
func (e *cueBindingError) Unwrap() error { return e.cause }

// NewCUEContractValidator binds validation to a concrete StackKits CUE module.
// moduleRoot must contain cue.mod/module.cue, the authority profile projection,
// base/architecture_v2.cue, and the semantic Definition binding.
func NewCUEContractValidator(moduleRoot string) (*CUEContractValidator, error) {
	return NewCUEContractValidatorForAuthority(moduleRoot, DevelopmentPlanAuthority())
}

// NewCUEContractValidatorForAuthority binds filesystem-backed validation to
// one exact plan authority class.
func NewCUEContractValidatorForAuthority(moduleRoot string, authority PlanAuthority) (*CUEContractValidator, error) {
	if err := validatePlanAuthority(authority); err != nil {
		return nil, err
	}
	if authority.Class == "product" {
		return nil, fmt.Errorf("graduation-eligible product authority requires immutable in-memory sources matching the pinned product distribution fingerprint")
	}
	absoluteRoot, err := filepath.Abs(moduleRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve CUE module root: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	for _, required := range []string{
		filepath.Join(absoluteRoot, "cue.mod", "module.cue"),
		filepath.Join(absoluteRoot, "base", "architecture_v2_profiles.cue"),
		filepath.Join(absoluteRoot, "base", "architecture_v2.cue"),
		filepath.Join(absoluteRoot, "base", "architecture_v2_definition_binding.cue"),
	} {
		info, err := os.Stat(required)
		if err != nil {
			return nil, fmt.Errorf("CUE contract authority %s: %w", required, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("CUE contract authority %s is not a file", required)
		}
	}

	validator := &CUEContractValidator{moduleRoot: absoluteRoot, planAuthority: authority, initialized: true}
	if err := validator.validateExpression("constructor", "base.#ArchitectureAPIVersion"); err != nil {
		return nil, fmt.Errorf("load StackKits Architecture v2 CUE authority: %w", err)
	}
	return validator, nil
}

func (v *CUEContractValidator) normalizeBinding(definition KitDefinition, spec StackSpecV2) (KitDefinition, StackSpecV2, error) {
	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal KitDefinition: %w", err)
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal StackSpecV2: %w", err)
	}
	value, err := v.normalizeExpression("binding", "base.#KitSpecBinding & {definition: "+string(definitionJSON)+", spec: "+string(specJSON)+"}")
	if err != nil {
		// If both documents are valid on their own, a failure of the binding is
		// specifically a definition/spec profile mismatch. If either standalone
		// document is invalid (for example an unknown field), preserve that as a
		// general contract-validation failure.
		definitionErr := v.validateExpression("definition", "base.#KitDefinition & "+string(definitionJSON))
		specErr := v.validateExpression("spec", "base.#StackSpecV2 & "+string(specJSON))
		return nil, nil, &cueBindingError{cause: err, profileMismatch: definitionErr == nil && specErr == nil}
	}
	normalizedDefinition, err := decodeCUEField[KitDefinition](value, "definition")
	if err != nil {
		return nil, nil, err
	}
	normalizedSpec, err := decodeCUEField[StackSpecV2](value, "spec")
	if err != nil {
		return nil, nil, err
	}
	return normalizedDefinition, normalizedSpec, nil
}

func (v *CUEContractValidator) normalizeDefinition(definition KitDefinition) (KitDefinition, error) {
	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("marshal KitDefinition: %w", err)
	}
	value, err := v.normalizeExpression("definition", "base.#KitDefinition & "+string(definitionJSON))
	if err != nil {
		return nil, err
	}
	data, err := value.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("export normalized KitDefinition: %w", err)
	}
	return DecodeDocument[KitDefinition](data)
}

func (v *CUEContractValidator) normalizeInventory(inventory InventoryFacts) (InventoryFacts, error) {
	inventoryJSON, err := json.Marshal(inventory)
	if err != nil {
		return nil, fmt.Errorf("marshal InventoryFacts: %w", err)
	}
	value, err := v.normalizeExpression("inventory", "base.#InventoryFacts & "+string(inventoryJSON))
	if err != nil {
		return nil, err
	}
	data, err := value.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("export normalized InventoryFacts: %w", err)
	}
	return DecodeDocument[InventoryFacts](data)
}

func (v *CUEContractValidator) normalizeCatalog(catalog Catalog) (Catalog, error) {
	if catalog.Capabilities == nil {
		catalog.Capabilities = []CapabilityContract{}
	}
	if catalog.Providers == nil {
		catalog.Providers = []CapabilityProvider{}
	}
	if catalog.AddOns == nil {
		catalog.AddOns = []AddOnContract{}
	}
	if catalog.Modules == nil {
		catalog.Modules = []ModuleContract{}
	}
	if catalog.PrivilegedInterfaceApprovals == nil {
		catalog.PrivilegedInterfaceApprovals = []PrivilegedInterfaceApproval{}
	}
	document := map[string]any{
		"capabilities":                 catalog.Capabilities,
		"providers":                    catalog.Providers,
		"addons":                       catalog.AddOns,
		"modules":                      catalog.Modules,
		"privilegedInterfaceApprovals": catalog.PrivilegedInterfaceApprovals,
	}
	// A nil slice means the caller did not provide an authority value. Let CUE
	// materialize its governed minimal default instead of inventing one in Go.
	if catalog.PlanArtifacts != nil {
		document["planArtifacts"] = catalog.PlanArtifacts
	}
	catalogJSON, err := json.Marshal(document)
	if err != nil {
		return Catalog{}, fmt.Errorf("marshal Architecture v2 catalog: %w", err)
	}
	value, err := v.normalizeExpression("catalog", "base.#ArchitectureV2CatalogContract & "+string(catalogJSON))
	if err != nil {
		return Catalog{}, err
	}
	data, err := value.MarshalJSON()
	if err != nil {
		return Catalog{}, fmt.Errorf("export normalized Architecture v2 catalog: %w", err)
	}
	var normalized Catalog
	decoder := json.NewDecoder(bytesReader(data))
	decoder.UseNumber()
	var wire struct {
		Capabilities                 []CapabilityContract          `json:"capabilities"`
		Providers                    []CapabilityProvider          `json:"providers"`
		AddOns                       []AddOnContract               `json:"addons"`
		Modules                      []ModuleContract              `json:"modules"`
		PrivilegedInterfaceApprovals []PrivilegedInterfaceApproval `json:"privilegedInterfaceApprovals"`
		PlanArtifacts                []PlanArtifactContract        `json:"planArtifacts"`
	}
	if err := decoder.Decode(&wire); err != nil {
		return Catalog{}, fmt.Errorf("decode normalized Architecture v2 catalog: %w", err)
	}
	normalized.Capabilities = wire.Capabilities
	normalized.Providers = wire.Providers
	normalized.AddOns = wire.AddOns
	normalized.Modules = wire.Modules
	normalized.PrivilegedInterfaceApprovals = wire.PrivilegedInterfaceApprovals
	normalized.PlanArtifacts = wire.PlanArtifacts
	return normalized, nil
}

func (v *CUEContractValidator) validatePlan(plan ResolvedPlan) error {
	_, err := v.normalizePlanSchema(plan)
	return err
}

// ValidateCanonicalPlan re-runs the governed CUE #ResolvedPlan contract and
// requires the supplied plan to already equal the fully normalized result.
// This is deliberately a method on the non-substitutable concrete validator:
// downstream apply/generation gates cannot claim semantic verification from a
// self-consistent planHash alone or inject a no-op schema validator.
func (v *CUEContractValidator) ValidateCanonicalPlan(plan ResolvedPlan) error {
	normalized, err := v.normalizePlan(plan)
	if err != nil {
		return err
	}
	originalJSON, err := plan.MarshalCanonical()
	if err != nil {
		return fmt.Errorf("marshal supplied ResolvedPlan: %w", err)
	}
	normalizedJSON, err := normalized.MarshalCanonical()
	if err != nil {
		return fmt.Errorf("marshal CUE-normalized ResolvedPlan: %w", err)
	}
	if !bytes.Equal(originalJSON, normalizedJSON) {
		return fmt.Errorf("ResolvedPlan is CUE-valid only after normalization; canonical authority-bound plan shape is required")
	}
	return nil
}

func (v *CUEContractValidator) normalizePlan(plan ResolvedPlan) (ResolvedPlan, error) {
	normalized, err := v.normalizePlanSchema(plan)
	if err != nil {
		return nil, err
	}
	if err := v.validateBoundDefinition(normalized); err != nil {
		return nil, fmt.Errorf("ResolvedPlan Definition binding rejected plan: %w", err)
	}
	if err := v.validateBoundAuthority(normalized); err != nil {
		return nil, fmt.Errorf("ResolvedPlan authority projection rejected plan: %w", err)
	}
	if err := v.validateBoundCatalogBodies(normalized); err != nil {
		return nil, fmt.Errorf("ResolvedPlan catalog body binding rejected plan: %w", err)
	}
	return normalized, nil
}

// validateBoundDefinition selects the expected normalized Definition from the
// service-owned authority binding. The persisted plan never supplies this
// document, so recomputing planHash cannot replace or weaken its kit semantics.
func (v *CUEContractValidator) validateBoundDefinition(plan ResolvedPlan) error {
	if v == nil || v.boundAuthority == nil || len(v.boundAuthority.definitions) == 0 {
		return nil
	}
	kit, err := objectField(map[string]any(plan), "resolvedPlan", "kit")
	if err != nil {
		return err
	}
	slug, err := stringField(kit, "resolvedPlan.kit", "slug")
	if err != nil {
		return err
	}
	expected, exists := v.boundAuthority.definitions[slug]
	if !exists {
		return fmt.Errorf("resolvedPlan.kit.slug %q is not owned by this Definition authority", slug)
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal ResolvedPlan for Definition binding: %w", err)
	}
	definitionJSON, err := json.Marshal(expected.normalized)
	if err != nil {
		return fmt.Errorf("marshal authority KitDefinition %s: %w", slug, err)
	}
	_, err = v.normalizeExpression(
		"plan-definition-binding",
		"base.#ResolvedPlanDefinitionBinding & {definition: "+string(definitionJSON)+", plan: "+string(planJSON)+"}",
	)
	return err
}

// normalizePlanSchema is an internal CUE-contract-only seam used by compiler
// schema tests. Runtime persistence and generation boundaries always call
// normalizePlan/ValidateCanonicalPlan and therefore still require the bound
// catalog, Definition, compiler, and renderer authority projection.
func (v *CUEContractValidator) normalizePlanSchema(plan ResolvedPlan) (ResolvedPlan, error) {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("marshal ResolvedPlan: %w", err)
	}
	authorityJSON, err := json.Marshal(v.planAuthority)
	if err != nil {
		return nil, fmt.Errorf("marshal expected plan authority: %w", err)
	}
	value, err := v.normalizeExpression("plan", "base.#ResolvedPlanValidation & {plan: "+string(planJSON)+"} & {plan: {authority: "+string(authorityJSON)+"}}")
	if err != nil {
		return nil, err
	}
	validatedPlan := value.LookupPath(cueapi.ParsePath("plan"))
	if err := validatedPlan.Err(); err != nil {
		return nil, fmt.Errorf("select validated ResolvedPlan: %w", err)
	}
	data, err := validatedPlan.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("export normalized ResolvedPlan: %w", err)
	}
	normalized, err := DecodeDocument[ResolvedPlan](data)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func (v *CUEContractValidator) validateExpression(label, expression string) error {
	_, err := v.normalizeExpression(label, expression)
	return err
}

func (v *CUEContractValidator) normalizeExpression(label, expression string) (cueapi.Value, error) {
	if v == nil || !v.initialized || v.moduleRoot == "" {
		return cueapi.Value{}, fmt.Errorf("CUE contract validator is not initialized")
	}
	virtualDir := filepath.Join(v.moduleRoot, ".stackkit-cue-validator")
	virtualFile := filepath.Join(virtualDir, label+".cue")
	source := []byte("package resolvedplan_contract\n\nimport \"github.com/kombifyio/stackkits/base\"\n\nvalue: " + expression + "\n")
	overlay := make(map[string]load.Source, len(v.authoritySource)+1)
	allowedFiles := make(map[string]struct{}, len(v.authoritySource)+1)
	for relativePath, authoritySource := range v.authoritySource {
		absolutePath := filepath.Join(v.moduleRoot, filepath.FromSlash(relativePath))
		overlay[absolutePath] = load.FromBytes(bytes.Clone(authoritySource))
		allowedFiles[filepath.Clean(absolutePath)] = struct{}{}
	}
	overlay[virtualFile] = load.FromBytes(source)
	allowedFiles[filepath.Clean(virtualFile)] = struct{}{}
	config := &load.Config{
		Dir: virtualDir, ModuleRoot: v.moduleRoot,
		Overlay: overlay,
	}
	if len(v.authoritySource) > 0 {
		// load.Overlay merges with the host filesystem. ParseFile closes that
		// seam: a same-user file created under the virtual root is rejected
		// rather than becoming additional CUE authority.
		config.ParseFile = func(name string, src interface{}, config parser.Config) (*ast.File, error) {
			if _, ok := allowedFiles[filepath.Clean(name)]; !ok {
				return nil, fmt.Errorf("CUE in-memory authority rejected host file %s", name)
			}
			return parser.ParseFile(name, src, config)
		}
	}
	instances := load.Instances([]string{"."}, config)
	if len(instances) != 1 {
		return cueapi.Value{}, fmt.Errorf("CUE loader returned %d instances, want 1", len(instances))
	}
	if instances[0].Err != nil {
		return cueapi.Value{}, instances[0].Err
	}
	root := cuecontext.New().BuildInstance(instances[0])
	// All() ensures every regular field in validation envelopes is evaluated,
	// including the regular projections of package-hidden cross-graph proofs.
	// A concrete public projection alone is insufficient for those envelopes.
	if err := root.Validate(cueapi.Concrete(true), cueapi.All()); err != nil {
		return cueapi.Value{}, err
	}
	value := root.LookupPath(cueapi.ParsePath("value"))
	if err := value.Err(); err != nil {
		return cueapi.Value{}, err
	}
	return value, nil
}

func decodeCUEField[T ~map[string]any](value cueapi.Value, path string) (T, error) {
	field := value.LookupPath(cueapi.ParsePath(path))
	if err := field.Err(); err != nil {
		return nil, err
	}
	data, err := field.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("export normalized %s: %w", path, err)
	}
	return DecodeDocument[T](data)
}
