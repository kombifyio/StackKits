package architecturev2

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/rilactionv2"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

const emptyInventoryDocument = `{"schemaVersion":"stackkit.inventory/v1","nodes":{}}`

// CompilerContract is the explicit integration-owned compiler identity and
// artifact contract. There are deliberately no implicit compiler defaults.
type CompilerContract struct {
	CompilerVersion         string
	MinimumCLIVersion       string
	MinimumRuntimeVersion   string
	MinimumGeneratorVersion string
	RendererID              string
	RendererVersion         string
}

// StackKitsV2Contract is the governed Architecture v2 renderer contract.
// The minimum versions preserve the first supported v2 release line while
// buildVersion binds each resolution to the exact producing build.
func StackKitsV2Contract(buildVersion string) CompilerContract {
	return CompilerContract{
		CompilerVersion:         "stackkits-resolver/" + buildVersion,
		MinimumCLIVersion:       "0.6.0",
		MinimumRuntimeVersion:   "0.6.0",
		MinimumGeneratorVersion: "0.6.0",
		RendererID:              "stackkit",
		RendererVersion:         buildVersion,
	}
}

// StackKitsV06Contract is retained as a source-compatible transition alias.
// New product integrations must use StackKitsV2Contract.
//
// Deprecated: use StackKitsV2Contract.
func StackKitsV06Contract(buildVersion string) CompilerContract {
	return StackKitsV2Contract(buildVersion)
}

// ContractFixtureV1Contract returns the only compiler/renderer namespace that
// the isolated non-product authority accepts. This identity is intentionally
// disjoint from product plans even when both are built from the same SHA.
func ContractFixtureV1Contract(buildVersion string) CompilerContract {
	return CompilerContract{
		CompilerVersion:         "stackkits-contract-fixture/" + buildVersion,
		MinimumCLIVersion:       "0.6.0",
		MinimumRuntimeVersion:   "0.6.0",
		MinimumGeneratorVersion: "0.6.0",
		RendererID:              "stackkit-contract-fixture",
		RendererVersion:         buildVersion,
	}
}

// Service owns one immutable CUE authority snapshot and one deterministic
// compiler. It is safe for concurrent CLI/API resolution.
type Service struct {
	authority         *cueAuthority
	compiler          *resolvedplan.Compiler
	validator         *resolvedplan.CUEContractValidator
	generation        *generationCoordinator
	rilActionLedger   rilaction.ExecutionLedger
	productApplyTrust []productApplyTrustAnchor
}

// NewProductEmbeddedService creates the product authority used by native CLI
// execution. Its public evidence anchors come only from the fixed OS-level
// StackKit trust store; request callers cannot supply or override them.
func NewProductEmbeddedService(contract CompilerContract) (*Service, error) {
	trust, err := loadDefaultProductApplyTrust()
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, "load product Apply producer trust", err)
	}
	service, err := NewEmbeddedService(contract)
	if err != nil {
		return nil, err
	}
	service.productApplyTrust = trust
	return service, nil
}

// NewProductEmbeddedServiceWithRILActionLedger creates the product service
// with an integration-owned atomic replay/evidence store. The ledger supplies
// persistence only; it cannot select or authorize a runtime owner.
func NewProductEmbeddedServiceWithRILActionLedger(contract CompilerContract, ledger rilaction.ExecutionLedger) (*Service, error) {
	service, err := NewProductEmbeddedService(contract)
	if err != nil {
		return nil, err
	}
	return service.withRILActionLedger(ledger)
}

// NewService is the compatibility name for an explicit filesystem authority.
// New integrations should use NewEmbeddedService by default and select
// NewFilesystemService only for an intentional development/operator override.
func NewService(moduleRoot string, contract CompilerContract) (*Service, error) {
	return NewFilesystemService(moduleRoot, contract)
}

// NewFilesystemService loads all three kit Definitions and the governed
// catalog directly from moduleRoot.
func NewFilesystemService(moduleRoot string, contract CompilerContract) (*Service, error) {
	authority, err := loadCUEAuthority(moduleRoot)
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, err.Error(), err)
	}
	return newDevelopmentServiceWithAuthority(authority, contract)
}

// NewEmbeddedService loads the generated, drift-tested authority bundled with
// the binary. Resolution is therefore independent of repository checkout and
// process working directory while CUE remains the validating schema authority.
func NewEmbeddedService(contract CompilerContract) (*Service, error) {
	authority, err := loadEmbeddedAuthority()
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, err.Error(), err)
	}
	return newServiceWithAuthority(authority, contract)
}

// NewEmbeddedServiceWithRILActionLedger is the non-product counterpart for
// integrations and focused contract tests that provide their own durable SPI.
func NewEmbeddedServiceWithRILActionLedger(contract CompilerContract, ledger rilaction.ExecutionLedger) (*Service, error) {
	service, err := NewEmbeddedService(contract)
	if err != nil {
		return nil, err
	}
	return service.withRILActionLedger(ledger)
}

func (s *Service) withRILActionLedger(ledger rilaction.ExecutionLedger) (*Service, error) {
	if s == nil || nilRILActionLedger(ledger) {
		return nil, resolveError(ErrAuthorityLoad, "a RIL action execution ledger is required", nil)
	}
	s.rilActionLedger = ledger
	return s, nil
}

func nilRILActionLedger(ledger rilaction.ExecutionLedger) bool {
	if ledger == nil {
		return true
	}
	value := reflect.ValueOf(ledger)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// NewFilesystemContractFixtureService loads the deliberately non-product
// contract-fixture catalog from moduleRoot. It accepts only the Basement
// definition and exists for deterministic contract generation and tests; it
// must never be used to infer product readiness.
func NewFilesystemContractFixtureService(moduleRoot string, contract CompilerContract) (*Service, error) {
	authority, err := loadCUEContractFixtureAuthority(moduleRoot)
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, err.Error(), err)
	}
	return newContractFixtureServiceWithAuthority(authority, contract)
}

// NewEmbeddedContractFixtureService is the checkout-independent counterpart
// used by committed contract fixtures and the renderer E2E gate. The embedded
// fixture catalog is isolated from the product catalog by construction.
func NewEmbeddedContractFixtureService(contract CompilerContract) (*Service, error) {
	authority, err := loadEmbeddedContractFixtureAuthority()
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, err.Error(), err)
	}
	return newContractFixtureServiceWithAuthority(authority, contract)
}

func newContractFixtureServiceWithAuthority(authority *cueAuthority, contract CompilerContract) (*Service, error) {
	if authority == nil {
		return nil, resolveError(ErrAuthorityLoad, "Architecture v2 contract fixture authority is nil", nil)
	}
	if len(authority.definitions) != 1 || authority.definitions[stackspecmigration.KitProfileBasement] == nil {
		return nil, resolveError(ErrAuthorityLoad, "Architecture v2 contract fixture authority has no Basement definition", nil)
	}
	if authority.planAuthority != resolvedplan.ContractFixturePlanAuthority() {
		return nil, resolveError(ErrAuthorityLoad, "Architecture v2 contract fixture authority has a product authority identity", nil)
	}
	if !strings.HasPrefix(contract.CompilerVersion, "stackkits-contract-fixture/") ||
		contract.RendererID != "stackkit-contract-fixture" ||
		strings.TrimPrefix(contract.CompilerVersion, "stackkits-contract-fixture/") != contract.RendererVersion {
		return nil, resolveError(ErrAuthorityLoad, "contract fixture compiler and renderer must use the governed contract-fixture namespace", nil)
	}
	return newServiceWithValidatedAuthority(authority, contract)
}

func newServiceWithAuthority(authority *cueAuthority, contract CompilerContract) (*Service, error) {
	if authority == nil {
		return nil, resolveError(ErrAuthorityLoad, "Architecture v2 product authority is nil", nil)
	}
	if authority.planAuthority != resolvedplan.ProductPlanAuthority() {
		return nil, resolveError(ErrAuthorityLoad, "Architecture v2 product service received a non-product authority identity", nil)
	}
	if !strings.HasPrefix(contract.CompilerVersion, "stackkits-resolver/") ||
		contract.RendererID != "stackkit" ||
		strings.TrimPrefix(contract.CompilerVersion, "stackkits-resolver/") != contract.RendererVersion {
		return nil, resolveError(ErrAuthorityLoad, "product compiler and renderer must use the governed product namespace", nil)
	}
	return newServiceWithValidatedAuthority(authority, contract)
}

func newDevelopmentServiceWithAuthority(authority *cueAuthority, contract CompilerContract) (*Service, error) {
	if authority == nil {
		return nil, resolveError(ErrAuthorityLoad, "Architecture v2 development authority is nil", nil)
	}
	if authority.planAuthority != resolvedplan.DevelopmentPlanAuthority() {
		return nil, resolveError(ErrAuthorityLoad, "Architecture v2 filesystem service must use non-graduating development authority", nil)
	}
	if !strings.HasPrefix(contract.CompilerVersion, "stackkits-resolver/") ||
		contract.RendererID != "stackkit" ||
		strings.TrimPrefix(contract.CompilerVersion, "stackkits-resolver/") != contract.RendererVersion {
		return nil, resolveError(ErrAuthorityLoad, "development compiler and renderer must use the governed StackKits namespace", nil)
	}
	return newServiceWithValidatedAuthority(authority, contract)
}

func newServiceWithValidatedAuthority(authority *cueAuthority, contract CompilerContract) (*Service, error) {
	var validator *resolvedplan.CUEContractValidator
	var err error
	if len(authority.contractSources) > 0 {
		validator, err = resolvedplan.NewCUEContractValidatorFromSourcesForAuthority(authority.moduleRoot, authority.contractSources, authority.planAuthority)
	} else {
		validator, err = resolvedplan.NewCUEContractValidatorForAuthority(authority.moduleRoot, authority.planAuthority)
	}
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, err.Error(), err)
	}
	compiler, err := resolvedplan.NewCompiler(authority.catalog, resolvedplan.Options{
		CompilerVersion:         contract.CompilerVersion,
		ContractValidator:       validator,
		PlanAuthority:           authority.planAuthority,
		AuthorityDefinitions:    authorityDefinitionSet(authority.definitions),
		MinimumCLIVersion:       contract.MinimumCLIVersion,
		MinimumRuntimeVersion:   contract.MinimumRuntimeVersion,
		MinimumGeneratorVersion: contract.MinimumGeneratorVersion,
		RendererID:              contract.RendererID,
		RendererVersion:         contract.RendererVersion,
	})
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, "construct governed compiler: "+err.Error(), err)
	}
	generation, err := newGenerationCoordinator()
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, "construct Architecture v2 generation coordinator: "+err.Error(), err)
	}
	return &Service{
		authority: authority, compiler: compiler, validator: validator,
		generation: generation, rilActionLedger: newMemoryRILActionLedger(),
	}, nil
}

func authorityDefinitionSet(definitions map[stackspecmigration.KitProfile]resolvedplan.KitDefinition) []resolvedplan.KitDefinition {
	profiles := make([]string, 0, len(definitions))
	byProfile := make(map[string]resolvedplan.KitDefinition, len(definitions))
	for profile, definition := range definitions {
		profiles = append(profiles, string(profile))
		byProfile[string(profile)] = definition
	}
	sort.Strings(profiles)
	result := make([]resolvedplan.KitDefinition, 0, len(profiles))
	for _, profile := range profiles {
		result = append(result, byProfile[profile])
	}
	return result
}

// VerifyCanonicalPlan revalidates a persisted plan through this service's
// already-bound embedded or explicit filesystem CUE authority. Callers never
// receive the authority pointer and cannot substitute a weaker validator.
func (s *Service) VerifyCanonicalPlan(canonical []byte) (generationartifact.VerifiedPlan, error) {
	if s == nil || s.validator == nil {
		return generationartifact.VerifiedPlan{}, resolveError(ErrAuthorityLoad, "Architecture v2 plan contract validator is not initialized", nil)
	}
	return generationartifact.VerifyPlan(canonical, s.validator)
}

// PersistCanonicalPlan applies the same embedded-authority verification before
// atomically replacing a regular non-symlink target with mode 0600.
func (s *Service) PersistCanonicalPlan(path string, canonical []byte) (generationartifact.VerifiedPlan, error) {
	if s == nil || s.validator == nil {
		return generationartifact.VerifiedPlan{}, resolveError(ErrAuthorityLoad, "Architecture v2 plan contract validator is not initialized", nil)
	}
	return generationartifact.PersistPlan(path, canonical, s.validator)
}

// ReadCanonicalPlan verifies a persisted plan through the same immutable CUE
// authority used for resolution and rejects symlinked/non-regular inputs.
func (s *Service) ReadCanonicalPlan(path string) (generationartifact.VerifiedPlan, error) {
	if s == nil || s.validator == nil {
		return generationartifact.VerifiedPlan{}, resolveError(ErrAuthorityLoad, "Architecture v2 plan contract validator is not initialized", nil)
	}
	return generationartifact.ReadPlan(path, s.validator)
}

// ResolveInput contains raw desired intent plus separately observed inventory.
// TargetKitProfile is used only to make a v1 migration report more precise; it
// never changes or selects a v2 kit.
type ResolveInput struct {
	StackSpec        []byte
	Inventory        []byte
	TargetKitProfile stackspecmigration.KitProfile
}

// Result is the only successful architecture boundary. CanonicalPlan is stable
// JSON and PlanHash is the hash embedded in that exact normalized plan.
type Result struct {
	Plan          resolvedplan.ResolvedPlan
	CanonicalPlan []byte
	PlanHash      string
}

// StackSpecValidation is spec-only governed evidence. It deliberately makes
// no inventory-specific ResolvedPlan or execution-readiness claim.
type StackSpecValidation struct {
	KitProfile         stackspecmigration.KitProfile
	CanonicalStackSpec []byte
	SpecHash           string
}

// ValidateStackSpec validates canonical desired intent against its selected
// Kit Definition without substituting an empty Inventory.
func (s *Service) ValidateStackSpec(raw []byte) (StackSpecValidation, error) {
	if s == nil || s.authority == nil || s.validator == nil {
		return StackSpecValidation{}, resolveError(ErrResolveFailed, "service is not initialized", nil)
	}
	document, err := stackspecmigration.Read(raw)
	if err != nil {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, err.Error(), err)
	}
	if document.Version == stackspecmigration.SourceVersionV1 {
		_, report, migrationErr := stackspecmigration.MigrateDocument(document, stackspecmigration.Options{})
		code := ErrMigrationRequired
		message := "StackSpec v1 must be explicitly migrated and completed as CUE-valid v2 intent before validation"
		if migrationErr != nil {
			code = ErrMigrationBlocked
			message = migrationErr.Error()
		}
		return StackSpecValidation{}, &ResolveError{Code: code, Message: message, Report: &report, Cause: migrationErr}
	}
	if document.Version != stackspecmigration.SourceVersionV2Alpha1 || document.V2 == nil {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "StackSpec reader returned no canonical v2 identity", nil)
	}
	specDocument, err := decodeYAMLObject(document.Raw, "StackSpec")
	if err != nil {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, err.Error(), err)
	}
	definition, exists := s.authority.definitions[document.V2.KitProfile]
	if !exists {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("no governed Definition exists for %q", document.V2.KitProfile), nil)
	}
	normalized, err := s.validator.NormalizeStackSpecBinding(definition, resolvedplan.StackSpecV2(specDocument))
	if err != nil {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, err.Error(), err)
	}
	canonical, err := resolvedplan.CanonicalJSON(normalized)
	if err != nil {
		return StackSpecValidation{}, resolveError(ErrResolveFailed, "marshal canonical StackSpec: "+err.Error(), err)
	}
	hash, err := resolvedplan.CanonicalSHA256(normalized)
	if err != nil {
		return StackSpecValidation{}, resolveError(ErrResolveFailed, "hash canonical StackSpec: "+err.Error(), err)
	}
	return StackSpecValidation{
		KitProfile:         document.V2.KitProfile,
		CanonicalStackSpec: canonical,
		SpecHash:           hash,
	}, nil
}

// Resolve accepts canonical v2 only. v1 is classified through the shared
// migration reader and always returns MigrationRequired/MigrationBlocked with
// a report; there is no raw-spec or partial-projection compiler fallback.
func (s *Service) Resolve(input ResolveInput) (Result, error) {
	if s == nil || s.authority == nil || s.compiler == nil || s.validator == nil {
		return Result{}, resolveError(ErrResolveFailed, "service is not initialized", nil)
	}
	document, err := stackspecmigration.Read(input.StackSpec)
	if err != nil {
		return Result{}, resolveError(ErrInvalidStackSpec, err.Error(), err)
	}
	if document.Version == stackspecmigration.SourceVersionV1 {
		_, report, migrationErr := stackspecmigration.MigrateDocument(document, stackspecmigration.Options{
			TargetKitProfile: input.TargetKitProfile,
		})
		code := ErrMigrationRequired
		message := "StackSpec v1 must be explicitly migrated and completed as CUE-valid v2 intent before resolution"
		if migrationErr != nil {
			code = ErrMigrationBlocked
			message = migrationErr.Error()
		}
		return Result{}, &ResolveError{Code: code, Message: message, Report: &report, Cause: migrationErr}
	}
	if document.Version != stackspecmigration.SourceVersionV2Alpha1 || document.V2 == nil {
		return Result{}, resolveError(ErrInvalidStackSpec, "StackSpec reader returned no canonical v2 identity", nil)
	}

	specDocument, err := decodeYAMLObject(document.Raw, "StackSpec")
	if err != nil {
		return Result{}, resolveError(ErrInvalidStackSpec, err.Error(), err)
	}
	definition, exists := s.authority.definitions[document.V2.KitProfile]
	if !exists {
		return Result{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("no governed Definition exists for %q", document.V2.KitProfile), nil)
	}

	inventoryData := input.Inventory
	if len(bytes.TrimSpace(inventoryData)) == 0 {
		inventoryData = []byte(emptyInventoryDocument)
	}
	inventoryDocument, err := decodeYAMLObject(inventoryData, "Inventory")
	if err != nil {
		return Result{}, resolveError(ErrInvalidInventory, err.Error(), err)
	}

	plan, err := s.compiler.Compile(resolvedplan.Input{
		Definition: definition,
		Spec:       resolvedplan.StackSpecV2(specDocument),
		Inventory:  resolvedplan.InventoryFacts(inventoryDocument),
	})
	if err != nil {
		return Result{}, resolveError(ErrResolveFailed, err.Error(), err)
	}
	canonical, err := plan.MarshalCanonical()
	if err != nil {
		return Result{}, resolveError(ErrResolveFailed, "marshal canonical ResolvedPlan: "+err.Error(), err)
	}
	planHash, ok := plan["planHash"].(string)
	if !ok || !strings.HasPrefix(planHash, "sha256:") {
		return Result{}, resolveError(ErrResolveFailed, "compiler returned no canonical planHash", nil)
	}
	return Result{Plan: plan, CanonicalPlan: canonical, PlanHash: planHash}, nil
}
