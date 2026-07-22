package resolvedplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
)

// expectedAuthorityBinding is an immutable projection of the exact CUE-owned
// catalog, Definition set, and compiler/renderer identity used to create a
// service. A valid plan hash and a self-consistent authority label are not
// sufficient: persisted plans must still reference contracts from this exact
// projection.
type expectedAuthorityBinding struct {
	authority     PlanAuthority
	definitions   map[string]authorityDefinitionIdentity
	capabilities  map[string]authorityContractIdentity
	providers     map[string]authorityContractIdentity
	modules       map[string]authorityContractIdentity
	workloads     map[string]authorityContractIdentity
	addons        map[string]authorityContractIdentity
	approvals     map[string]map[string]string
	compiler      string
	rendererID    string
	rendererVer   string
	compatibility map[string]any
}

type authorityDefinitionIdentity struct {
	version    string
	hash       string
	evidence   []string
	normalized KitDefinition
}

type authorityContractIdentity struct {
	version  string
	hash     string
	evidence []string
}

//nolint:gocyclo // Authority binding deliberately rejects every incomplete or contradictory source section before hashing it.
func (v *CUEContractValidator) bindExpectedAuthority(catalog Catalog, definitions []KitDefinition, options Options) (PlanAuthority, error) {
	if v == nil || !v.initialized {
		return PlanAuthority{}, fmt.Errorf("CUE contract validator is not initialized")
	}
	authority := options.PlanAuthority
	binding := &expectedAuthorityBinding{
		authority:    authority,
		definitions:  make(map[string]authorityDefinitionIdentity, len(definitions)),
		capabilities: make(map[string]authorityContractIdentity, len(catalog.Capabilities)),
		providers:    make(map[string]authorityContractIdentity, len(catalog.Providers)),
		modules:      make(map[string]authorityContractIdentity, len(catalog.Modules)),
		workloads:    make(map[string]authorityContractIdentity, len(catalog.Workloads)),
		addons:       make(map[string]authorityContractIdentity, len(catalog.AddOns)),
		approvals:    make(map[string]map[string]string, len(catalog.PrivilegedInterfaceApprovals)),
		compiler:     options.CompilerVersion,
		rendererID:   options.RendererID,
		rendererVer:  options.RendererVersion,
		compatibility: map[string]any{
			"minCLI": options.MinimumCLIVersion, "minRuntime": options.MinimumRuntimeVersion,
			"minGenerator":   options.MinimumGeneratorVersion,
			"specAPIVersion": "stackkit/v2alpha1", "planAPIVersion": ResolvedPlanAPIVersion,
		},
	}
	normalizedDefinitions := make([]KitDefinition, 0, len(definitions))
	for index, definition := range definitions {
		normalized, err := v.normalizeDefinition(definition)
		if err != nil {
			return PlanAuthority{}, fmt.Errorf("normalize authority Definition %d: %w", index, err)
		}
		metadata, err := objectField(map[string]any(normalized), fmt.Sprintf("definitions[%d]", index), "metadata")
		if err != nil {
			return PlanAuthority{}, err
		}
		slug, err := stringField(metadata, fmt.Sprintf("definitions[%d].metadata", index), "slug")
		if err != nil {
			return PlanAuthority{}, err
		}
		version, err := stringField(metadata, fmt.Sprintf("definitions[%d].metadata", index), "version")
		if err != nil {
			return PlanAuthority{}, err
		}
		hash, err := canonicalHash(normalized, true)
		if err != nil {
			return PlanAuthority{}, fmt.Errorf("hash authority Definition %s: %w", slug, err)
		}
		if _, duplicate := binding.definitions[slug]; duplicate {
			return PlanAuthority{}, fmt.Errorf("authority Definition %s is duplicated", slug)
		}
		evidence, err := stringListField(map[string]any(normalized), fmt.Sprintf("definitions[%d]", index), "evidenceScenarios", true)
		if err != nil {
			return PlanAuthority{}, err
		}
		binding.definitions[slug] = authorityDefinitionIdentity{
			version: version, hash: hash, evidence: sortStringsUnique(evidence), normalized: normalized,
		}
		normalizedDefinitions = append(normalizedDefinitions, normalized)
	}
	if authority.Class == "product" {
		if len(v.authoritySource) == 0 {
			return PlanAuthority{}, fmt.Errorf("product authority requires immutable in-memory authority sources")
		}
		fingerprint, err := ComputeDistributionFingerprint(v.authoritySource, catalog, normalizedDefinitions)
		if err != nil {
			return PlanAuthority{}, fmt.Errorf("compute product distribution fingerprint: %w", err)
		}
		if fingerprint != pinnedProductDistributionFingerprint || authority.DistributionFingerprint != fingerprint {
			return PlanAuthority{}, fmt.Errorf("product Catalog, Definition set, or CUE sources do not match the pinned product distribution fingerprint")
		}
	}
	if err := bindCatalogIdentities("capabilities", catalog.Capabilities, binding.capabilities); err != nil {
		return PlanAuthority{}, err
	}
	if err := bindCatalogIdentities("providers", catalog.Providers, binding.providers); err != nil {
		return PlanAuthority{}, err
	}
	if err := bindCatalogIdentities("modules", catalog.Modules, binding.modules); err != nil {
		return PlanAuthority{}, err
	}
	if err := bindCatalogIdentities("workloads", catalog.Workloads, binding.workloads); err != nil {
		return PlanAuthority{}, err
	}
	if err := bindCatalogIdentities("addons", catalog.AddOns, binding.addons); err != nil {
		return PlanAuthority{}, err
	}
	for index, approval := range catalog.PrivilegedInterfaceApprovals {
		object := map[string]any(approval)
		id, err := stringField(object, fmt.Sprintf("catalog.privilegedInterfaceApprovals[%d]", index), "id")
		if err != nil {
			return PlanAuthority{}, err
		}
		projection := make(map[string]string)
		for _, field := range []string{"id", "kind", "moduleRef", "unitRef", "providerRef", "daemonRef", "policyProfile", "reasonCode", "evidenceRef"} {
			if value, ok := object[field].(string); ok {
				projection[field] = value
			}
		}
		binding.approvals[id] = projection
	}
	if v.boundAuthority != nil && !reflect.DeepEqual(v.boundAuthority, binding) {
		return PlanAuthority{}, fmt.Errorf("CUE contract validator is already bound to a different catalog, Definition set, or compiler identity")
	}
	v.planAuthority = authority
	v.boundAuthority = binding
	return authority, nil
}

// ComputeDistributionFingerprint binds the immutable CUE source bytes to the
// complete concrete Catalog and Definition projections shipped in one build.
// It is a bundle/build attestation only. It must never be copied into a
// ResolvedPlan, whose authority is derived from the selected semantic closure.
func ComputeDistributionFingerprint(sources map[string][]byte, catalog Catalog, definitions []KitDefinition) (string, error) {
	if len(sources) == 0 {
		return "", fmt.Errorf("authority sources are required")
	}
	sourceHashes := make(map[string]any, len(sources))
	for relativePath, source := range sources {
		sum := sha256.Sum256(source)
		sourceHashes[relativePath] = "sha256:" + hex.EncodeToString(sum[:])
	}
	catalogHash, err := canonicalAuthorityCatalogHash(catalog)
	if err != nil {
		return "", err
	}
	definitionHashes := make(map[string]any, len(definitions))
	for index, definition := range definitions {
		metadata, err := objectField(map[string]any(definition), fmt.Sprintf("definitions[%d]", index), "metadata")
		if err != nil {
			return "", err
		}
		slug, err := stringField(metadata, fmt.Sprintf("definitions[%d].metadata", index), "slug")
		if err != nil {
			return "", err
		}
		if _, duplicate := definitionHashes[slug]; duplicate {
			return "", fmt.Errorf("authority Definition %s is duplicated", slug)
		}
		hash, err := canonicalHash(definition, true)
		if err != nil {
			return "", err
		}
		definitionHashes[slug] = hash
	}
	return canonicalHash(map[string]any{
		"schemaVersion": "stackkits-product-distribution-fingerprint/v1",
		"sourceHashes":  sourceHashes,
		"catalogHash":   catalogHash,
		"definitions":   definitionHashes,
	}, false)
}

func canonicalAuthorityCatalogHash(catalog Catalog) (string, error) {
	projection := map[string]any{}
	var err error
	if projection["capabilities"], err = sortedAuthorityContracts("capabilities", catalog.Capabilities, true); err != nil {
		return "", err
	}
	if projection["providers"], err = sortedAuthorityContracts("providers", catalog.Providers, true); err != nil {
		return "", err
	}
	if projection["addons"], err = sortedAuthorityContracts("addons", catalog.AddOns, true); err != nil {
		return "", err
	}
	if projection["modules"], err = sortedAuthorityContracts("modules", catalog.Modules, true); err != nil {
		return "", err
	}
	if projection["workloads"], err = sortedAuthorityContracts("workloads", catalog.Workloads, true); err != nil {
		return "", err
	}
	if projection["privilegedInterfaceApprovals"], err = sortedAuthorityContracts("privilegedInterfaceApprovals", catalog.PrivilegedInterfaceApprovals, false); err != nil {
		return "", err
	}
	if projection["rilActionPrimitives"], err = sortedAuthorityContracts("rilActionPrimitives", catalog.RILActionPrimitives, false); err != nil {
		return "", err
	}
	if projection["planArtifacts"], err = sortedAuthorityContracts("planArtifacts", catalog.PlanArtifacts, false); err != nil {
		return "", err
	}
	return canonicalHash(projection, true)
}

// deriveSemanticPlanAuthority creates the portable authority carried by one
// plan. Only the selected Definition identity and catalog contracts contribute
// to this authority; unrelated profiles, unselected contracts, source comments,
// and private distribution-only extensions cannot rotate a public plan.
func deriveSemanticPlanAuthority(plan ResolvedPlan, catalog *indexedCatalog, base PlanAuthority) (PlanAuthority, error) {
	if catalog == nil {
		return PlanAuthority{}, fmt.Errorf("semantic plan authority requires a bound catalog")
	}
	kit, err := objectField(map[string]any(plan), "resolvedPlan", "kit")
	if err != nil {
		return PlanAuthority{}, err
	}
	slug, err := stringField(kit, "resolvedPlan.kit", "slug")
	if err != nil {
		return PlanAuthority{}, err
	}
	version, err := stringField(kit, "resolvedPlan.kit", "version")
	if err != nil {
		return PlanAuthority{}, err
	}
	definitionHash, err := stringField(kit, "resolvedPlan.kit", "definitionHash")
	if err != nil {
		return PlanAuthority{}, err
	}
	if !canonicalSHA256Pattern.MatchString(definitionHash) {
		return PlanAuthority{}, fmt.Errorf("resolvedPlan.kit.definitionHash is not a canonical SHA-256 digest")
	}
	closure, err := selectedAuthorityCatalogClosure(plan, catalog)
	if err != nil {
		return PlanAuthority{}, err
	}
	catalogHash, err := canonicalHash(closure, true)
	if err != nil {
		return PlanAuthority{}, fmt.Errorf("hash selected catalog authority closure: %w", err)
	}
	result := base
	result.CatalogHash = catalogHash
	result.AuthorityFingerprint = ""
	if result.Class == "product" {
		result.AuthorityFingerprint, err = canonicalHash(map[string]any{
			"schemaVersion": "stackkits-semantic-plan-authority/v1",
			"authority": map[string]any{
				"class": result.Class, "document": result.Document,
				"graduationEligible": result.GraduationEligible, "issuer": result.Issuer,
			},
			"kit": map[string]any{
				"slug": slug, "version": version, "definitionHash": definitionHash,
			},
			"catalogHash": catalogHash,
		}, false)
		if err != nil {
			return PlanAuthority{}, fmt.Errorf("hash semantic product plan authority: %w", err)
		}
	}
	return result, nil
}

func selectedAuthorityCatalogClosure(plan ResolvedPlan, catalog *indexedCatalog) (map[string]any, error) {
	capabilityIDs, err := selectedResolvedObjectIDs(plan, "capabilities")
	if err != nil {
		return nil, err
	}
	providerIDs, err := selectedResolvedObjectIDs(plan, "providers")
	if err != nil {
		return nil, err
	}
	moduleIDs, err := selectedResolvedObjectIDs(plan, "modules")
	if err != nil {
		return nil, err
	}
	workloadIDs, err := selectedResolvedObjectIDs(plan, "workloads")
	if err != nil {
		return nil, err
	}
	addonIDs, err := selectedResolvedAddonIDs(plan)
	if err != nil {
		return nil, err
	}
	approvalIDs, err := selectedResolvedObjectIDs(plan, "privilegedInterfaceApprovals")
	if err != nil {
		return nil, err
	}
	artifactIDs, err := selectedGenerationArtifactIDs(plan)
	if err != nil {
		return nil, err
	}

	capabilities, err := selectMetadataAuthorityBodies("capabilities", capabilityIDs, catalog.capabilities)
	if err != nil {
		return nil, err
	}
	providers, err := selectMetadataAuthorityBodies("providers", providerIDs, catalog.providers)
	if err != nil {
		return nil, err
	}
	modules, err := selectMetadataAuthorityBodies("modules", moduleIDs, catalog.modules)
	if err != nil {
		return nil, err
	}
	workloads, err := selectMetadataAuthorityBodies("workloads", workloadIDs, catalog.workloads)
	if err != nil {
		return nil, err
	}
	addons, err := selectMetadataAuthorityBodies("addons", addonIDs, catalog.addons)
	if err != nil {
		return nil, err
	}
	approvals, err := selectPlainAuthorityBodies("privilegedInterfaceApprovals", approvalIDs, catalog.privilegedInterfaceApprovals, true)
	if err != nil {
		return nil, err
	}
	// Module-owned generation artifacts are already covered by their selected
	// ModuleContract body. Only catalog-level plan artifacts are added here;
	// unselected plan-artifact contracts remain distribution-local.
	planArtifacts, err := selectPlainAuthorityBodies("planArtifacts", artifactIDs, catalog.planArtifacts, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"schemaVersion":                "stackkits-selected-catalog-closure/v1",
		"capabilities":                 capabilities,
		"providers":                    providers,
		"modules":                      modules,
		"workloads":                    workloads,
		"addons":                       addons,
		"privilegedInterfaceApprovals": approvals,
		"planArtifacts":                planArtifacts,
	}, nil
}

func selectedResolvedObjectIDs(plan ResolvedPlan, field string) ([]string, error) {
	values, err := objectListField(map[string]any(plan), "resolvedPlan", field)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		id, err := stringField(value, fmt.Sprintf("resolvedPlan.%s[%d]", field, index), "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("resolvedPlan.%s duplicates selected authority contract %q", field, id)
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func selectedResolvedAddonIDs(plan ResolvedPlan) ([]string, error) {
	raw, exists := plan["addons"]
	if !exists {
		return []string{}, nil
	}
	addons, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resolvedPlan.addons is %T, want object", raw)
	}
	return sortedStringMapKeys(addons), nil
}

func selectedGenerationArtifactIDs(plan ResolvedPlan) ([]string, error) {
	generation, err := objectField(map[string]any(plan), "resolvedPlan", "generation")
	if err != nil {
		return nil, err
	}
	artifacts, err := objectListField(generation, "resolvedPlan.generation", "artifacts")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(artifacts))
	for index, artifact := range artifacts {
		id, err := stringField(artifact, fmt.Sprintf("resolvedPlan.generation.artifacts[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return sortStringsUnique(ids), nil
}

func selectMetadataAuthorityBodies(name string, ids []string, catalog map[string]map[string]any) ([]any, error) {
	result := make([]any, 0, len(ids))
	for _, id := range ids {
		contract, exists := catalog[id]
		if !exists {
			return nil, fmt.Errorf("resolvedPlan selects catalog.%s %q outside its bound authority", name, id)
		}
		result = append(result, contract)
	}
	return result, nil
}

// selectPlainAuthorityBodies selects id-keyed catalog contracts. When strict
// is false, selected IDs owned by another catalog family are ignored; this is
// used for generation artifacts because ModuleContract-owned artifacts share
// the same resolved list as catalog-level plan artifacts.
func selectPlainAuthorityBodies(name string, selectedIDs []string, contracts []map[string]any, strict bool) ([]any, error) {
	selected := make(map[string]struct{}, len(selectedIDs))
	for _, id := range selectedIDs {
		selected[id] = struct{}{}
	}
	byID := make(map[string]map[string]any, len(contracts))
	for index, contract := range contracts {
		id, err := stringField(contract, fmt.Sprintf("catalog.%s[%d]", name, index), "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := byID[id]; duplicate {
			return nil, fmt.Errorf("catalog.%s duplicates %q", name, id)
		}
		byID[id] = contract
	}
	var ids []string
	for id := range selected {
		if _, exists := byID[id]; exists {
			ids = append(ids, id)
		} else if strict {
			return nil, fmt.Errorf("resolvedPlan selects catalog.%s %q outside its bound authority", name, id)
		}
	}
	sort.Strings(ids)
	result := make([]any, 0, len(ids))
	for _, id := range ids {
		result = append(result, byID[id])
	}
	return result, nil
}

func sortedAuthorityContracts[T ~map[string]any](name string, contracts []T, metadataKey bool) ([]any, error) {
	type keyedContract struct {
		key      string
		contract map[string]any
	}
	keyed := make([]keyedContract, 0, len(contracts))
	for index, contract := range contracts {
		object := map[string]any(contract)
		var key string
		var err error
		if metadataKey {
			key, err = metadataID(object, fmt.Sprintf("catalog.%s[%d]", name, index))
		} else {
			key, err = stringField(object, fmt.Sprintf("catalog.%s[%d]", name, index), "id")
		}
		if err != nil {
			return nil, err
		}
		keyed = append(keyed, keyedContract{key: key, contract: object})
	}
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })
	result := make([]any, len(keyed))
	for index := range keyed {
		result[index] = keyed[index].contract
	}
	return result, nil
}

func bindCatalogIdentities[T ~map[string]any](name string, contracts []T, target map[string]authorityContractIdentity) error {
	for index, contract := range contracts {
		object := map[string]any(contract)
		id, err := metadataID(object, fmt.Sprintf("catalog.%s[%d]", name, index))
		if err != nil {
			return err
		}
		version, err := metadataVersion(object, fmt.Sprintf("catalog.%s[%d]", name, index))
		if err != nil {
			return err
		}
		hash, err := canonicalHash(object, true)
		if err != nil {
			return fmt.Errorf("hash catalog %s %s: %w", name, id, err)
		}
		evidence, err := stringListField(object, fmt.Sprintf("catalog.%s[%d]", name, index), "evidence", false)
		if err != nil {
			return err
		}
		target[id] = authorityContractIdentity{version: version, hash: hash, evidence: sortStringsUnique(evidence)}
	}
	return nil
}

//nolint:gocyclo // This is the fail-closed integrity boundary for all independently bound authority sections.
func (v *CUEContractValidator) validateBoundAuthority(plan ResolvedPlan) error {
	if v == nil || v.boundAuthority == nil {
		return fmt.Errorf("CUE contract validator has no bound catalog authority")
	}
	expected := v.boundAuthority
	authority, err := objectField(map[string]any(plan), "resolvedPlan", "authority")
	if err != nil {
		return err
	}
	actualAuthority := PlanAuthority{}
	actualAuthority.Class, err = stringField(authority, "resolvedPlan.authority", "class")
	if err != nil {
		return err
	}
	actualAuthority.Document, err = stringField(authority, "resolvedPlan.authority", "document")
	if err != nil {
		return err
	}
	actualAuthority.GraduationEligible, err = boolFieldDefault(authority, "resolvedPlan.authority", "graduationEligible", false)
	if err != nil {
		return err
	}
	actualAuthority.Issuer, err = stringField(authority, "resolvedPlan.authority", "issuer")
	if err != nil {
		return err
	}
	actualAuthority.AuthorityFingerprint, _, err = optionalStringField(authority, "resolvedPlan.authority", "authorityFingerprint")
	if err != nil {
		return err
	}
	actualAuthority.CatalogHash, err = stringField(authority, "resolvedPlan.authority", "catalogHash")
	if err != nil {
		return err
	}
	if v.boundCatalog == nil || v.boundCatalog.catalog == nil {
		return fmt.Errorf("CUE contract validator has no bound catalog bodies for semantic authority verification")
	}
	semanticAuthority, err := deriveSemanticPlanAuthority(plan, v.boundCatalog.catalog, expected.authority)
	if err != nil {
		return fmt.Errorf("recompute resolvedPlan semantic authority: %w", err)
	}
	if !sameSerializedPlanAuthority(actualAuthority, semanticAuthority) {
		return fmt.Errorf("resolvedPlan.authority does not match its selected Definition and catalog closure")
	}
	compiler, err := stringField(map[string]any(plan), "resolvedPlan", "compilerVersion")
	if err != nil {
		return err
	}
	if compiler != expected.compiler {
		return fmt.Errorf("resolvedPlan.compilerVersion %q does not match authority compiler %q", compiler, expected.compiler)
	}
	compatibility, err := objectField(map[string]any(plan), "resolvedPlan", "compatibility")
	if err != nil {
		return err
	}
	if equal, err := canonicalEqual(compatibility, expected.compatibility); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("resolvedPlan.compatibility does not match the authority compiler compatibility contract")
	}
	generation, err := objectField(map[string]any(plan), "resolvedPlan", "generation")
	if err != nil {
		return err
	}
	renderer, err := objectField(generation, "resolvedPlan.generation", "renderer")
	if err != nil {
		return err
	}
	rendererID, err := stringField(renderer, "resolvedPlan.generation.renderer", "id")
	if err != nil {
		return err
	}
	rendererVersion, err := stringField(renderer, "resolvedPlan.generation.renderer", "version")
	if err != nil {
		return err
	}
	if rendererID != expected.rendererID || rendererVersion != expected.rendererVer {
		return fmt.Errorf("resolvedPlan renderer %s/%s does not match authority renderer %s/%s", rendererID, rendererVersion, expected.rendererID, expected.rendererVer)
	}
	selectedKit := ""
	if len(expected.definitions) > 0 {
		kit, err := objectField(map[string]any(plan), "resolvedPlan", "kit")
		if err != nil {
			return err
		}
		slug, err := stringField(kit, "resolvedPlan.kit", "slug")
		if err != nil {
			return err
		}
		identity, ok := expected.definitions[slug]
		if !ok {
			return fmt.Errorf("resolvedPlan.kit.slug %q is not owned by this authority", slug)
		}
		version, err := stringField(kit, "resolvedPlan.kit", "version")
		if err != nil {
			return err
		}
		definitionHash, err := stringField(kit, "resolvedPlan.kit", "definitionHash")
		if err != nil {
			return err
		}
		if version != identity.version || definitionHash != identity.hash {
			return fmt.Errorf("resolvedPlan.kit %s does not match this authority's exact Definition", slug)
		}
		selectedKit = slug
	}
	capabilityIDs, err := validateResolvedContractIdentities(plan, "capabilities", expected.capabilities, false)
	if err != nil {
		return err
	}
	providerIDs, err := validateResolvedContractIdentities(plan, "providers", expected.providers, true)
	if err != nil {
		return err
	}
	moduleIDs, err := validateResolvedContractIdentities(plan, "modules", expected.modules, true)
	if err != nil {
		return err
	}
	workloadIDs, err := validateResolvedContractIdentities(plan, "workloads", expected.workloads, true)
	if err != nil {
		return err
	}
	if selectedKit != "" {
		if err := validateResolvedWorkloadPolicy(workloadIDs, expected.definitions[selectedKit].normalized); err != nil {
			return err
		}
	}
	if err := validateResolvedApprovals(plan, expected.approvals); err != nil {
		return err
	}
	var addonIDs []string
	if addons, ok := plan["addons"].(map[string]any); ok {
		addonIDs = make([]string, 0, len(addons))
		for id := range addons {
			addonIDs = append(addonIDs, id)
		}
		sort.Strings(addonIDs)
		for _, id := range addonIDs {
			if _, exists := expected.addons[id]; !exists {
				return fmt.Errorf("resolvedPlan.addons.%s is not owned by this authority", id)
			}
		}
	}
	if selectedKit != "" {
		expectedEvidence := append([]string{}, expected.definitions[selectedKit].evidence...)
		for _, selection := range []struct {
			ids       []string
			contracts map[string]authorityContractIdentity
		}{{capabilityIDs, expected.capabilities}, {providerIDs, expected.providers}, {moduleIDs, expected.modules}} {
			for _, id := range selection.ids {
				expectedEvidence = append(expectedEvidence, selection.contracts[id].evidence...)
			}
		}
		expectedTopEvidence := sortStringsUnique(expectedEvidence)
		actualEvidence, err := stringListField(map[string]any(plan), "resolvedPlan", "evidence", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(actualEvidence, expectedTopEvidence) {
			return fmt.Errorf("resolvedPlan.evidence is not the exact evidence union of its bound Definition and selected contracts")
		}
		expectedGateEvidence := append([]string{}, expectedTopEvidence...)
		for _, id := range addonIDs {
			expectedGateEvidence = append(expectedGateEvidence, expected.addons[id].evidence...)
		}
		expectedGateEvidence = sortStringsUnique(expectedGateEvidence)
		gates, err := objectField(map[string]any(plan), "resolvedPlan", "gates")
		if err != nil {
			return err
		}
		gateEvidence, err := objectListField(gates, "resolvedPlan.gates", "evidence")
		if err != nil {
			return err
		}
		gateScenarios := make([]string, 0, len(gateEvidence))
		for index, gate := range gateEvidence {
			scenario, err := stringField(gate, fmt.Sprintf("resolvedPlan.gates.evidence[%d]", index), "scenario")
			if err != nil {
				return err
			}
			gateScenarios = append(gateScenarios, scenario)
		}
		if !reflect.DeepEqual(gateScenarios, expectedGateEvidence) {
			return fmt.Errorf("resolvedPlan.gates.evidence scenarios are not the exact bound evidence union")
		}
	}
	return nil
}

func validateResolvedWorkloadPolicy(selected []string, definition KitDefinition) error {
	policy, err := objectField(map[string]any(definition), "definition", "workloads")
	if err != nil {
		return err
	}
	required, err := stringListField(policy, "definition.workloads", "required", false)
	if err != nil {
		return err
	}
	defaults, err := stringListField(policy, "definition.workloads", "defaults", false)
	if err != nil {
		return err
	}
	optional, err := stringListField(policy, "definition.workloads", "optional", false)
	if err != nil {
		return err
	}
	forbidden, err := stringListField(policy, "definition.workloads", "forbidden", false)
	if err != nil {
		return err
	}
	selectedSet := stringSet(selected)
	allowed := stringSet(append(append(append([]string{}, required...), defaults...), optional...))
	for _, id := range append(append([]string{}, required...), defaults...) {
		if _, exists := selectedSet[id]; !exists {
			return fmt.Errorf("resolvedPlan.workloads omits kit-mandated workload %q", id)
		}
	}
	for _, id := range selected {
		if _, denied := stringSet(forbidden)[id]; denied {
			return fmt.Errorf("resolvedPlan.workloads selects kit-forbidden workload %q", id)
		}
		if _, exists := allowed[id]; !exists {
			return fmt.Errorf("resolvedPlan.workloads selects workload %q outside the bound KitDefinition policy", id)
		}
	}
	return nil
}

func sameSerializedPlanAuthority(left, right PlanAuthority) bool {
	return left.Class == right.Class && left.Document == right.Document &&
		left.GraduationEligible == right.GraduationEligible && left.Issuer == right.Issuer &&
		left.AuthorityFingerprint == right.AuthorityFingerprint && left.CatalogHash == right.CatalogHash
}

func validateResolvedContractIdentities(plan ResolvedPlan, field string, expected map[string]authorityContractIdentity, requireVersion bool) ([]string, error) {
	values, err := objectListField(map[string]any(plan), "resolvedPlan", field)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.%s[%d]", field, index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("%s duplicates authority contract %q", path, id)
		}
		seen[id] = struct{}{}
		identity, ok := expected[id]
		if !ok {
			return nil, fmt.Errorf("%s id %q is not owned by this authority", path, id)
		}
		hash, err := stringField(value, path, "contractHash")
		if err != nil {
			return nil, err
		}
		if hash != identity.hash {
			return nil, fmt.Errorf("%s contractHash does not match this authority", path)
		}
		if requireVersion {
			version, err := stringField(value, path, "version")
			if err != nil {
				return nil, err
			}
			if version != identity.version {
				return nil, fmt.Errorf("%s version does not match this authority", path)
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func validateResolvedApprovals(plan ResolvedPlan, expected map[string]map[string]string) error {
	values, err := objectListField(map[string]any(plan), "resolvedPlan", "privilegedInterfaceApprovals")
	if err != nil {
		return err
	}
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.privilegedInterfaceApprovals[%d]", index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return err
		}
		projection, ok := expected[id]
		if !ok {
			return fmt.Errorf("%s id %q is not owned by this authority", path, id)
		}
		for field, want := range projection {
			got, err := stringField(value, path, field)
			if err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("%s.%s does not match this authority", path, field)
			}
		}
	}
	return nil
}
