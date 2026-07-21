package resolvedplan

import (
	"errors"
	"fmt"
	"strings"
)

// Compiler owns an immutable governed catalog and produces ResolvedPlan v1.
// It is safe for concurrent use after construction.
type Compiler struct {
	catalog *indexedCatalog
	options Options
}

// NewCompiler validates and freezes the governed contract catalog.
func NewCompiler(catalog Catalog, options Options) (*Compiler, error) {
	if err := validateCompilerOptions(&options); err != nil {
		return nil, err
	}
	if err := validateExplicitImplementationInterfaceContracts(catalog); err != nil {
		return nil, err
	}
	normalizedCatalog, err := options.ContractValidator.normalizeCatalog(catalog)
	if err != nil {
		return nil, fail(ErrContractValidation, "catalog", "CUE #ArchitectureV2CatalogContract rejected catalog: %v", err)
	}
	if err := options.ContractValidator.bindExpectedCatalogBodies(normalizedCatalog); err != nil {
		return nil, fail(ErrContractValidation, "catalog.authority", "bind compiler to governed catalog bodies: %v", err)
	}
	boundAuthority, err := options.ContractValidator.bindExpectedAuthority(normalizedCatalog, options.AuthorityDefinitions, options)
	if err != nil {
		return nil, fail(ErrContractValidation, "catalog.authority", "bind compiler to governed authority projection: %v", err)
	}
	options.PlanAuthority = boundAuthority
	options.AuthorityDefinitions = nil
	frozen, err := freezeCatalog(normalizedCatalog)
	if err != nil {
		return nil, err
	}
	indexed, err := indexCatalog(frozen)
	if err != nil {
		return nil, err
	}
	return &Compiler{catalog: indexed, options: options}, nil
}

func validateCompilerOptions(options *Options) error {
	options.CompilerVersion = strings.TrimSpace(options.CompilerVersion)
	if options.CompilerVersion == "" {
		return fail(ErrInvalidInput, "options.compilerVersion", "compiler version is required")
	}
	if options.ContractValidator == nil || !options.ContractValidator.initialized {
		return fail(ErrInvalidInput, "options.contractValidator", "a concrete CUE contract validator is required")
	}
	if len(options.AuthorityDefinitions) == 0 {
		return fail(ErrInvalidInput, "options.authorityDefinitions", "at least one service-owned KitDefinition is required")
	}
	if err := validatePlanAuthority(options.PlanAuthority); err != nil {
		return err
	}
	if !samePlanAuthorityClass(options.ContractValidator.planAuthority, options.PlanAuthority) {
		return fail(ErrInvalidInput, "options.planAuthority", "compiler authority must exactly match its CUE validator authority")
	}
	if err := validateMinimumVersions(*options); err != nil {
		return err
	}
	if strings.TrimSpace(options.RendererID) == "" || strings.TrimSpace(options.RendererVersion) == "" {
		return fail(ErrInvalidInput, "options.renderer", "renderer ID and version are required")
	}
	if err := validateAuthorityCompilerNamespace(*options); err != nil {
		return err
	}
	return nil
}

func validateAuthorityCompilerNamespace(options Options) error {
	switch options.PlanAuthority.Class {
	case "product":
		if options.RendererID != "stackkit" ||
			!strings.HasPrefix(options.CompilerVersion, "stackkits-resolver/") ||
			strings.TrimPrefix(options.CompilerVersion, "stackkits-resolver/") != options.RendererVersion {
			return fail(ErrInvalidInput, "options.compilerVersion", "product authority requires the exact stackkits-resolver/<rendererVersion> and stackkit renderer namespace")
		}
	case "contract-fixture":
		if options.RendererID != "stackkit-contract-fixture" ||
			!strings.HasPrefix(options.CompilerVersion, "stackkits-contract-fixture/") ||
			strings.TrimPrefix(options.CompilerVersion, "stackkits-contract-fixture/") != options.RendererVersion {
			return fail(ErrInvalidInput, "options.compilerVersion", "contract-fixture authority requires its exact compiler and renderer namespace")
		}
	}
	return nil
}

func validatePlanAuthority(authority PlanAuthority) error {
	if authority.CatalogHash != "" || authority.AuthorityFingerprint != "" {
		return fail(ErrInvalidInput, "options.planAuthority", "semantic authorityFingerprint and catalogHash are compiler-derived per plan")
	}
	switch authority.Class {
	case "product":
		if authority.Document != "catalog" || !authority.GraduationEligible ||
			authority.Issuer != "stackkits-product-authority/v1" ||
			authority.DistributionFingerprint != pinnedProductDistributionFingerprint ||
			!canonicalSHA256Pattern.MatchString(authority.DistributionFingerprint) {
			return fail(ErrInvalidInput, "options.planAuthority", "product authority requires the pinned issuer and distribution fingerprint")
		}
	case "contract-fixture":
		if authority.Document != "contractFixtureCatalog" || authority.GraduationEligible ||
			authority.Issuer != "stackkits-contract-fixture-authority/v1" || authority.DistributionFingerprint != "" {
			return fail(ErrInvalidInput, "options.planAuthority", "contract-fixture authority requires document contractFixtureCatalog and graduationEligible=false")
		}
	case "development":
		if authority.Document != "catalog" || authority.GraduationEligible ||
			authority.Issuer != "stackkits-development-authority/v1" || authority.DistributionFingerprint != "" {
			return fail(ErrInvalidInput, "options.planAuthority", "development authority requires document catalog and graduationEligible=false")
		}
	default:
		return fail(ErrInvalidInput, "options.planAuthority.class", "unsupported plan authority class %q", authority.Class)
	}
	return nil
}

func samePlanAuthorityClass(left, right PlanAuthority) bool {
	return left.Class == right.Class && left.Document == right.Document &&
		left.GraduationEligible == right.GraduationEligible && left.Issuer == right.Issuer &&
		left.DistributionFingerprint == right.DistributionFingerprint &&
		left.AuthorityFingerprint == right.AuthorityFingerprint && left.CatalogHash == right.CatalogHash
}

func validateMinimumVersions(options Options) error {
	for optionPath, version := range map[string]string{
		"options.minimumCLIVersion":       options.MinimumCLIVersion,
		"options.minimumRuntimeVersion":   options.MinimumRuntimeVersion,
		"options.minimumGeneratorVersion": options.MinimumGeneratorVersion,
	} {
		if _, err := parseSemanticVersion(version); err != nil {
			return fail(ErrInvalidInput, optionPath, "semantic version is required: %v", err)
		}
	}
	return nil
}

// Compile resolves one profile-bound desired spec and its observed inventory.
// The returned plan contains no plaintext secret values and its planHash covers
// the complete plan except the planHash field itself.
func (c *Compiler) Compile(input Input) (ResolvedPlan, error) {
	sourceIntentHash, err := canonicalHash(input.Spec, true)
	if err != nil {
		return nil, fmt.Errorf("source intent hash: %w", err)
	}
	for _, document := range []struct {
		path  string
		value map[string]any
	}{{"definition", input.Definition}, {"spec", input.Spec}, {"inventory", input.Inventory}} {
		if err := validateSecretReferences(document.value, document.path, ""); err != nil {
			return nil, err
		}
	}
	if err := validateRawInventoryRuntimeDaemonSocketPaths(input.Inventory); err != nil {
		return nil, err
	}
	normalizedDefinition, normalizedSpec, err := c.options.ContractValidator.normalizeBinding(input.Definition, input.Spec)
	if err != nil {
		code := ErrContractValidation
		var bindingErr *cueBindingError
		if errors.As(err, &bindingErr) && bindingErr.profileMismatch {
			code = ErrProfileMismatch
		}
		return nil, fail(code, "input.binding", "CUE #KitSpecBinding rejected definition/spec: %v", err)
	}
	normalizedInventory, err := c.options.ContractValidator.normalizeInventory(input.Inventory)
	if err != nil {
		return nil, fail(ErrContractValidation, "input.inventory", "CUE #InventoryFacts rejected inventory: %v", err)
	}
	normalizedInput := Input{Definition: normalizedDefinition, Spec: normalizedSpec, Inventory: normalizedInventory}
	profile, spec, err := validateInputs(normalizedInput)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveContracts(profile, spec, c.catalog)
	if err != nil {
		return nil, err
	}
	plan, err := c.buildPlan(profile, spec, resolved, sourceIntentHash)
	if err != nil {
		return nil, err
	}
	normalizedPlan, err := c.options.ContractValidator.normalizePlan(plan)
	if err != nil {
		return nil, fail(ErrContractValidation, "resolvedPlan", "CUE #ResolvedPlan rejected compiler output: %v", err)
	}
	delete(normalizedPlan, "planHash")
	planHash, err := canonicalHash(normalizedPlan, true)
	if err != nil {
		return nil, fmt.Errorf("normalized plan hash: %w", err)
	}
	normalizedPlan["planHash"] = planHash
	finalPlan, err := c.options.ContractValidator.normalizePlan(normalizedPlan)
	if err != nil {
		return nil, fail(ErrContractValidation, "resolvedPlan", "CUE #ResolvedPlan rejected normalized compiler output: %v", err)
	}
	return finalPlan, nil
}

func freezeCatalog(catalog Catalog) (Catalog, error) {
	frozen := Catalog{}
	for _, artifact := range catalog.PlanArtifacts {
		if err := validateSecretReferences(map[string]any(artifact), "catalog.planArtifacts", ""); err != nil {
			return Catalog{}, err
		}
		clone, err := cloneObject(map[string]any(artifact), false)
		if err != nil {
			return Catalog{}, err
		}
		frozen.PlanArtifacts = append(frozen.PlanArtifacts, PlanArtifactContract(clone))
	}
	for _, contract := range catalog.Capabilities {
		if err := validateSecretReferences(map[string]any(contract), "catalog.capabilities", ""); err != nil {
			return Catalog{}, err
		}
		clone, err := cloneObject(map[string]any(contract), false)
		if err != nil {
			return Catalog{}, err
		}
		frozen.Capabilities = append(frozen.Capabilities, CapabilityContract(clone))
	}
	for _, provider := range catalog.Providers {
		if err := validateSecretReferences(map[string]any(provider), "catalog.providers", ""); err != nil {
			return Catalog{}, err
		}
		clone, err := cloneObject(map[string]any(provider), false)
		if err != nil {
			return Catalog{}, err
		}
		frozen.Providers = append(frozen.Providers, CapabilityProvider(clone))
	}
	for _, addon := range catalog.AddOns {
		if err := validateSecretReferences(map[string]any(addon), "catalog.addons", ""); err != nil {
			return Catalog{}, err
		}
		clone, err := cloneObject(map[string]any(addon), false)
		if err != nil {
			return Catalog{}, err
		}
		frozen.AddOns = append(frozen.AddOns, AddOnContract(clone))
	}
	for _, module := range catalog.Modules {
		if err := validateSecretReferences(map[string]any(module), "catalog.modules", ""); err != nil {
			return Catalog{}, err
		}
		clone, err := cloneObject(map[string]any(module), false)
		if err != nil {
			return Catalog{}, err
		}
		frozen.Modules = append(frozen.Modules, ModuleContract(clone))
	}
	for _, workload := range catalog.Workloads {
		if err := validateSecretReferences(map[string]any(workload), "catalog.workloads", ""); err != nil {
			return Catalog{}, err
		}
		clone, err := cloneObject(map[string]any(workload), false)
		if err != nil {
			return Catalog{}, err
		}
		frozen.Workloads = append(frozen.Workloads, WorkloadContract(clone))
	}
	for _, approval := range catalog.PrivilegedInterfaceApprovals {
		if err := validateSecretReferences(map[string]any(approval), "catalog.privilegedInterfaceApprovals", ""); err != nil {
			return Catalog{}, err
		}
		clone, err := cloneObject(map[string]any(approval), false)
		if err != nil {
			return Catalog{}, err
		}
		frozen.PrivilegedInterfaceApprovals = append(frozen.PrivilegedInterfaceApprovals, PrivilegedInterfaceApproval(clone))
	}
	return frozen, nil
}

func (c *Compiler) buildPlan(profile *profileView, spec *specView, resolved *resolution, sourceIntentHash string) (ResolvedPlan, error) {
	hashes, err := buildPlanHashes(spec)
	if err != nil {
		return nil, err
	}
	contracts, err := c.buildPlanContracts(spec, resolved)
	if err != nil {
		return nil, err
	}
	deployment, err := c.buildPlanDeployment(profile, spec, resolved, contracts, hashes, sourceIntentHash)
	if err != nil {
		return nil, err
	}
	if err := c.attachProviderOwnerGateRefs(contracts.providers, deployment.gates); err != nil {
		return nil, err
	}
	topology, err := c.buildPlanTopology(profile, spec, resolved)
	if err != nil {
		return nil, err
	}
	privilegedInterfaceApprovals, err := c.resolvePrivilegedInterfaceApprovals(contracts.modules, deployment.gates)
	if err != nil {
		return nil, err
	}
	planEvidence := append(append([]string(nil), profile.evidence...), contracts.evidence...)
	planEvidence = sortStringsUnique(append(planEvidence, availabilityEvidence(profile, spec)...))
	outputRoot, err := stringField(deployment.generation, "generation", "outputRoot")
	if err != nil {
		return nil, err
	}
	resolvedBridge, err := buildResolvedBridgePlan(spec, contracts.modules, contracts.providers, topology)
	if err != nil {
		return nil, err
	}
	resolvedKit := map[string]any{"slug": profile.slug, "version": profile.version, "definitionHash": hashes.definition}
	identityTrust, err := buildResolvedIdentityTrust(profile.identityTrust, spec, contracts)
	if err != nil {
		return nil, err
	}
	localReachability, homeLANDiscovery, err := buildHomeNetworkProjections(spec, deployment.network, topology.sites)
	if err != nil {
		return nil, err
	}
	externalHostBindings, hostConformanceReceipts, err := buildExternalHostProjection(spec, hashes.spec, hashes.inventory, deployment.system)
	if err != nil {
		return nil, err
	}
	homeAccessRequirements, externalHomeAccessBindings, err := buildHomeAccessProjection(
		spec, hashes.spec, topology.sites, topology.nodes, contracts.capabilities, contracts.modules,
	)
	if err != nil {
		return nil, err
	}
	if err := bindResolvedModulePlanInputs(contracts.modules, modulePlanInputSource{
		stackID: spec.stackID, kit: resolvedKit, sites: topology.sites,
		controlPlane: topology.controlPlane, bridge: resolvedBridge,
		identity: deployment.identity, identityTrust: identityTrust, data: topology.data, failurePolicy: topology.failurePolicy,
		localReachability: localReachability, homeLANDiscovery: homeLANDiscovery,
		homeAccessRequirements: homeAccessRequirements, externalHomeAccessBindings: externalHomeAccessBindings,
		nodes: topology.nodes, capabilities: contracts.capabilities, providers: contracts.providers,
		install: deployment.install, system: deployment.system, storage: deployment.storage, network: deployment.network,
	}); err != nil {
		return nil, err
	}
	if err := bindResolvedModuleRenderInputs(contracts.modules, moduleRenderInputSource{
		identity: deployment.identity,
		network:  deployment.network,
		gates:    deployment.gates,
	}); err != nil {
		return nil, err
	}
	executionReadiness, err := buildExecutionReadiness(contracts.providers, contracts.modules, deployment.artifacts, planEvidence, c.options.RendererID, outputRoot, deployment.routeHealth, resolvedBridge)
	if err != nil {
		return nil, err
	}

	plan := ResolvedPlan{
		"apiVersion":                   "stackkit.resolved-plan/v1",
		"kind":                         "ResolvedPlan",
		"stackId":                      spec.stackID,
		"kit":                          resolvedKit,
		"compilerVersion":              c.options.CompilerVersion,
		"sourceIntentHash":             sourceIntentHash,
		"specHash":                     hashes.spec,
		"inventoryHash":                hashes.inventory,
		"install":                      deployment.install,
		"compatibility":                c.buildCompatibility(),
		"generation":                   deployment.generation,
		"source":                       deployment.source,
		"sites":                        topology.sites,
		"nodes":                        topology.nodes,
		"externalHostBindings":         externalHostBindings,
		"hostConformanceReceipts":      hostConformanceReceipts,
		"homeAccessRequirements":       homeAccessRequirements,
		"externalHomeAccessBindings":   externalHomeAccessBindings,
		"controlPlane":                 topology.controlPlane,
		"capabilities":                 contracts.capabilities,
		"providers":                    contracts.providers,
		"workloads":                    contracts.workloads,
		"modules":                      contracts.modules,
		"runtimeNetworks":              contracts.runtimeNetworks,
		"privilegedInterfaceApprovals": privilegedInterfaceApprovals,
		"placement":                    deployment.placement,
		"availability":                 topology.availability,
		"identity":                     deployment.identity,
		"identityTrust":                identityTrust,
		"data":                         topology.data,
		"failurePolicy":                topology.failurePolicy,
		"system":                       deployment.system,
		"storage":                      deployment.storage,
		"network":                      deployment.network,
		"homeLANDiscovery":             homeLANDiscovery,
		"gates":                        deployment.gates,
		"executionReadiness":           executionReadiness,
		"evidence":                     planEvidence,
	}
	return c.finalizePlan(plan, spec, resolved, resolvedBridge)
}

func (c *Compiler) finalizePlan(plan ResolvedPlan, spec *specView, resolved *resolution, resolvedBridge map[string]any) (ResolvedPlan, error) {
	if spec.fleetRef != "" {
		plan["fleetRef"] = spec.fleetRef
	}
	if len(resolved.addonSelection) > 0 {
		plan["addons"] = resolved.addonSelection
	}
	if spec.access != nil {
		access, err := cloneObject(spec.access, true)
		if err != nil {
			return nil, err
		}
		plan["access"] = access
	}
	if resolvedBridge != nil {
		plan["bridge"] = resolvedBridge
	}
	semanticAuthority, err := deriveSemanticPlanAuthority(plan, c.catalog, c.options.PlanAuthority)
	if err != nil {
		return nil, fmt.Errorf("derive semantic plan authority: %w", err)
	}
	plan["authority"] = serializedPlanAuthority(semanticAuthority)
	planHash, err := canonicalHash(plan, true)
	if err != nil {
		return nil, fmt.Errorf("plan hash: %w", err)
	}
	plan["planHash"] = planHash
	return plan, nil
}

func buildResolvedBridgePlan(spec *specView, modules, providers []any, topology planTopology) (map[string]any, error) {
	if spec.bridge == nil {
		return nil, nil
	}
	selectedSiteRefs := make([]string, 0, len(spec.sites))
	for _, site := range spec.sites {
		selectedSiteRefs = append(selectedSiteRefs, site.id)
	}
	return resolveBridgePlan(
		spec.bridge,
		modules,
		selectedSiteRefs,
		spec.authoritySiteRef,
		resolvedNodeSiteIndex(spec.nodes),
		topology.data,
		spec.access,
		providers,
	)
}

func serializedPlanAuthority(authority PlanAuthority) map[string]any {
	result := map[string]any{
		"class": authority.Class, "document": authority.Document,
		"graduationEligible": authority.GraduationEligible, "issuer": authority.Issuer,
		"catalogHash": authority.CatalogHash,
	}
	if authority.AuthorityFingerprint != "" {
		result["authorityFingerprint"] = authority.AuthorityFingerprint
	}
	return result
}

type planHashes struct {
	definition string
	spec       string
	inventory  string
}

func buildPlanHashes(spec *specView) (planHashes, error) {
	var hashes planHashes
	var err error
	if hashes.definition, err = canonicalHash(spec.originalDefinition, true); err != nil {
		return hashes, fmt.Errorf("definition hash: %w", err)
	}
	if hashes.spec, err = canonicalHash(spec.originalSpec, true); err != nil {
		return hashes, fmt.Errorf("spec hash: %w", err)
	}
	if hashes.inventory, err = canonicalInventoryHash(spec.originalInventory); err != nil {
		return hashes, fmt.Errorf("inventory hash: %w", err)
	}
	return hashes, nil
}

type planContracts struct {
	capabilities    []any
	providers       []any
	workloads       []any
	modules         []any
	runtimeNetworks []any
	evidence        []string
	providerSites   map[string][]string
	providerNodes   map[string][]string
	moduleSites     map[string][]string
	moduleNodes     map[string][]string
}

func (c *Compiler) buildPlanContracts(spec *specView, resolved *resolution) (planContracts, error) {
	var contracts planContracts
	var err error
	if contracts.capabilities, contracts.evidence, err = c.buildCapabilities(spec, resolved); err != nil {
		return contracts, err
	}
	var providerEvidence []string
	if contracts.providers, providerEvidence, contracts.providerSites, contracts.providerNodes, err = c.buildProviders(spec, resolved); err != nil {
		return contracts, err
	}
	contracts.evidence = append(contracts.evidence, providerEvidence...)
	if contracts.modules, contracts.runtimeNetworks, contracts.moduleSites, contracts.moduleNodes, err = c.buildModules(spec, resolved, contracts.providerSites, contracts.providerNodes); err != nil {
		return contracts, err
	}
	if contracts.workloads, err = c.buildWorkloads(resolved, contracts.modules); err != nil {
		return contracts, err
	}
	contracts.evidence = append(contracts.evidence, collectModuleEvidence(c.catalog, sortedStringMapKeys(contracts.moduleSites))...)
	return contracts, nil
}

type planDeployment struct {
	install     map[string]any
	generation  map[string]any
	artifacts   []any
	source      map[string]any
	system      map[string]any
	storage     map[string]any
	network     map[string]any
	routeHealth []any
	gates       map[string]any
	placement   []any
	identity    map[string]any
}

func (c *Compiler) buildPlanDeployment(profile *profileView, spec *specView, resolved *resolution, contracts planContracts, hashes planHashes, sourceIntentHash string) (planDeployment, error) {
	var deployment planDeployment
	var err error
	if deployment.install, err = c.buildInstall(spec, resolved); err != nil {
		return deployment, err
	}
	if deployment.generation, deployment.artifacts, err = c.buildGeneration(profile, spec, hashes.definition, contracts.modules); err != nil {
		return deployment, err
	}
	if deployment.source, err = buildSource(spec, sourceIntentHash, hashes.spec, hashes.inventory, hashes.definition); err != nil {
		return deployment, err
	}
	if deployment.system, err = buildSystem(spec); err != nil {
		return deployment, err
	}
	// Materialize the exact CUE defaults before deriving an external-host
	// requirements hash. The hash must be reproducible from the persisted plan,
	// not from an earlier partially defaulted intent representation.
	if deployment.system, err = c.options.ContractValidator.normalizeResolvedSystem(deployment.system); err != nil {
		return deployment, fmt.Errorf("normalize resolved system for external host admission: %w", err)
	}
	if deployment.storage, err = cloneObject(spec.storage, true); err != nil {
		return deployment, err
	}
	if deployment.network, deployment.routeHealth, err = buildNetwork(profile, spec, resolved, contracts.modules, contracts.providerSites, c.catalog); err != nil {
		return deployment, err
	}
	nodeSites := make(map[string]string, len(spec.nodeByID))
	for nodeRef, node := range spec.nodeByID {
		nodeSites[nodeRef] = node.siteRef
	}
	if deployment.gates, err = buildGates(profile, resolved, c.catalog, contracts.providerSites, contracts.providerNodes, contracts.moduleSites, contracts.moduleNodes, nodeSites, deployment.artifacts, deployment.routeHealth); err != nil {
		return deployment, err
	}
	if deployment.placement, err = buildPlacement(contracts.workloads); err != nil {
		return deployment, err
	}
	deployment.identity, err = buildIdentity(profile, spec)
	return deployment, err
}

type planTopology struct {
	sites         []any
	nodes         []any
	controlPlane  map[string]any
	availability  map[string]any
	failurePolicy map[string]any
	data          map[string]any
}

func (c *Compiler) buildPlanTopology(profile *profileView, spec *specView, resolved *resolution) (planTopology, error) {
	var topology planTopology
	var err error
	if topology.sites, err = cloneSites(spec.sites); err != nil {
		return topology, err
	}
	if topology.nodes, err = cloneNodes(spec.nodes); err != nil {
		return topology, err
	}
	if topology.controlPlane, err = cloneObject(spec.controlPlane, true); err != nil {
		return topology, err
	}
	if topology.availability, err = buildResolvedAvailability(profile, spec); err != nil {
		return topology, err
	}
	if topology.failurePolicy, err = cloneObject(spec.partitionPolicy, true); err != nil {
		return topology, err
	}
	topology.data, err = c.buildData(profile, spec, resolved)
	return topology, err
}

func (c *Compiler) buildCapabilities(spec *specView, resolved *resolution) ([]any, []string, error) {
	result := make([]any, 0, len(resolved.capabilityIDs))
	var evidence []string
	for _, id := range resolved.capabilityIDs {
		contract := c.catalog.capabilities[id]
		supported, err := stringListField(contract, "catalog.capabilities."+id, "supportedSiteKinds", true)
		if err != nil {
			return nil, nil, err
		}
		eligible := false
		for kind := range spec.siteKinds {
			if contains(supported, kind) {
				eligible = true
				break
			}
		}
		if !eligible {
			return nil, nil, fail(ErrUnrealizedCapability, "capabilities."+id, "capability contract does not support any selected site kind")
		}
		contractHash, err := canonicalHash(contract, true)
		if err != nil {
			return nil, nil, err
		}
		entry := map[string]any{"id": id, "providerRef": resolved.providerByCap[id], "contractHash": contractHash}
		if tlsProfile, exists, err := optionalObjectField(contract, "catalog.capabilities."+id, "tlsProfile"); err != nil {
			return nil, nil, err
		} else if exists {
			entry["tlsProfile"], err = cloneObject(tlsProfile, true)
			if err != nil {
				return nil, nil, err
			}
		}
		if settings := resolved.settingsByCap[id]; len(settings) > 0 {
			clean, err := cloneObject(settings, true)
			if err != nil {
				return nil, nil, err
			}
			delete(clean, "providerRef")
			if len(clean) > 0 {
				entry["settings"] = clean
			}
		}
		if refs := resolved.secretRefsByCap[id]; len(refs) > 0 {
			entry["secretRefs"] = refs
		}
		result = append(result, entry)
		contractEvidence, err := stringListField(contract, "catalog.capabilities."+id, "evidence", false)
		if err != nil {
			return nil, nil, err
		}
		evidence = append(evidence, contractEvidence...)
	}
	return result, evidence, nil
}

func (c *Compiler) buildProviders(spec *specView, resolved *resolution) ([]any, []string, map[string][]string, map[string][]string, error) {
	result := make([]any, 0, len(resolved.providerIDs))
	providerSites := make(map[string][]string, len(resolved.providerIDs))
	providerNodes := make(map[string][]string, len(resolved.providerIDs))
	var evidence []string
	for _, id := range resolved.providerIDs {
		provider, err := c.buildProvider(spec, resolved, id)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		result = append(result, provider.entry)
		providerSites[id] = provider.siteRefs
		providerNodes[id] = provider.nodeRefs
		evidence = append(evidence, provider.evidence...)
	}
	return result, evidence, providerSites, providerNodes, nil
}

type builtProvider struct {
	entry    map[string]any
	evidence []string
	siteRefs []string
	nodeRefs []string
}

func (c *Compiler) buildProvider(spec *specView, resolved *resolution, id string) (builtProvider, error) {
	contract := c.catalog.providers[id]
	version, err := metadataVersion(contract, "catalog.providers."+id)
	if err != nil {
		return builtProvider{}, err
	}
	supported, err := stringListField(contract, "catalog.providers."+id, "supportedSiteKinds", true)
	if err != nil {
		return builtProvider{}, err
	}
	siteRefs, nodeRefs := providerTargetRefs(spec, supported)
	if resolved.availability != nil && id == resolved.availability.providerRef && resolved.availability.selector == "control-plane-members" {
		siteRefs, nodeRefs, err = controlPlaneMemberTargetRefs(spec, supported)
		if err != nil {
			return builtProvider{}, err
		}
	}
	if len(siteRefs) == 0 || len(nodeRefs) == 0 {
		return builtProvider{}, fail(ErrUnrealizedCapability, "providers."+id, "provider has no enabled target nodes")
	}
	contractHash, err := canonicalHash(contract, true)
	if err != nil {
		return builtProvider{}, err
	}
	realization, err := objectField(contract, "catalog.providers."+id, "realization")
	if err != nil {
		return builtProvider{}, err
	}
	resolvedRealization, err := cloneObject(realization, true)
	if err != nil {
		return builtProvider{}, err
	}
	kind, err := stringField(realization, "catalog.providers."+id+".realization", "kind")
	if err != nil {
		return builtProvider{}, err
	}
	providerEvidence, err := stringListField(contract, "catalog.providers."+id, "evidence", false)
	if err != nil {
		return builtProvider{}, err
	}
	owner, err := buildProviderOwner(id, kind, version, contractHash, contract, realization, providerEvidence, resolved)
	if err != nil {
		return builtProvider{}, err
	}
	entry := map[string]any{
		"id": id, "version": version, "contractHash": contractHash,
		"provides":     stringSliceAny(providerCapabilities(id, resolved)),
		"workloadRefs": stringSliceAny(providerWorkloads(id, resolved)),
		"siteRefs":     stringSliceAny(siteRefs), "realization": resolvedRealization,
	}
	if rawIssuers, exists := contract["certificateIssuers"]; exists {
		cloned, err := cloneObject(map[string]any{"certificateIssuers": rawIssuers}, true)
		if err != nil {
			return builtProvider{}, err
		}
		entry["certificateIssuers"] = cloned["certificateIssuers"]
	}
	for _, field := range []string{"overlayContracts", "remoteActionContracts"} {
		if rawContracts, exists := contract[field]; exists {
			cloned, err := cloneObject(map[string]any{field: rawContracts}, true)
			if err != nil {
				return builtProvider{}, err
			}
			entry[field] = cloned[field]
		}
	}
	if owner != nil {
		entry["owner"] = owner
	}
	return builtProvider{entry: entry, evidence: providerEvidence, siteRefs: siteRefs, nodeRefs: nodeRefs}, nil
}

func providerWorkloads(providerID string, resolved *resolution) []string {
	var workloads []string
	for id, workload := range resolved.workloads {
		if workload.providerID == providerID {
			workloads = append(workloads, id)
		}
	}
	return sortStringsUnique(workloads)
}

func providerCapabilities(providerID string, resolved *resolution) []string {
	var provides []string
	for capability, provider := range resolved.providerByCap {
		if provider == providerID {
			provides = append(provides, capability)
		}
	}
	return sortStringsUnique(provides)
}

func providerTargetRefs(spec *specView, supported []string) ([]string, []string) {
	var siteRefs, nodeRefs []string
	for _, node := range spec.nodes {
		if !node.enabled || !contains(supported, node.siteKind) {
			continue
		}
		nodeRefs = append(nodeRefs, node.id)
		siteRefs = append(siteRefs, node.siteRef)
	}
	return sortStringsUnique(siteRefs), sortStringsUnique(nodeRefs)
}

func controlPlaneMemberTargetRefs(spec *specView, supported []string) ([]string, []string, error) {
	members, err := stringListField(spec.controlPlane, "spec.controlPlane", "members", true)
	if err != nil {
		return nil, nil, err
	}
	var siteRefs, nodeRefs []string
	for _, member := range members {
		node, exists := spec.nodeByID[member]
		if !exists || !node.enabled || !contains(supported, node.siteKind) {
			return nil, nil, fail(ErrUnrealizedCapability, "spec.controlPlane.members", "HA provider cannot target control member %q", member)
		}
		nodeRefs = append(nodeRefs, node.id)
		siteRefs = append(siteRefs, node.siteRef)
	}
	return sortStringsUnique(siteRefs), sortStringsUnique(nodeRefs), nil
}

func buildProviderOwner(providerID, kind, version, contractHash string, contract, realization map[string]any, evidence []string, resolved *resolution) (map[string]any, error) {
	switch kind {
	case "host", "external":
		return buildHostExternalProviderOwner(providerID, kind, version, contractHash, contract, realization, evidence, resolved)
	case "modules", "none", "contract", "topology":
		return nil, nil
	default:
		return nil, fail(ErrInvalidInput, "catalog.providers."+providerID+".realization.kind", "unsupported realization %q", kind)
	}
}

func buildHostExternalProviderOwner(providerID, kind, version, contractHash string, contract, realization map[string]any, evidence []string, resolved *resolution) (map[string]any, error) {
	path := "catalog.providers." + providerID + ".realization"
	ownerRef, err := stringField(realization, path, "ownerRef")
	if err != nil {
		return nil, err
	}
	if ownerRef != providerID {
		return nil, fail(ErrContractConflict, path+".ownerRef", "ownerRef must identify the provider contract itself until a governed owner catalog exists")
	}
	support, err := objectField(realization, path, "realizationSupport")
	if err != nil {
		return nil, err
	}
	resolvedSupport, err := cloneObject(support, true)
	if err != nil {
		return nil, err
	}
	ownerInputs, err := resolveProviderOwnerInputs(providerID, realization, resolved)
	if err != nil {
		return nil, err
	}
	healthContracts, err := objectListOptional(contract, "health")
	if err != nil {
		return nil, err
	}
	if len(healthContracts) == 0 || len(evidence) == 0 {
		return nil, fail(ErrUnrealizedCapability, path+".ownerRef", "host/external owner requires governed health and evidence contracts")
	}
	return map[string]any{
		"ref": ownerRef, "kind": kind, "version": version, "contractHash": contractHash,
		"realizationSupport": resolvedSupport, "inputs": ownerInputs,
	}, nil
}

func resolveProviderOwnerInputs(providerID string, realization map[string]any, resolved *resolution) (map[string]any, error) {
	bindings, exists, err := optionalObjectField(realization, "catalog.providers."+providerID+".realization", "inputBindings")
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	secretRefs := map[string]any{}
	if !exists {
		return map[string]any{"values": values, "secretRefs": secretRefs}, nil
	}
	for _, inputRef := range sortedStringMapKeys(bindings) {
		binding, err := asObject(bindings[inputRef], "catalog.providers."+providerID+".realization.inputBindings."+inputRef)
		if err != nil {
			return nil, err
		}
		source, err := stringField(binding, "catalog.providers."+providerID+".realization.inputBindings."+inputRef, "source")
		if err != nil {
			return nil, err
		}
		capabilityRef, err := stringField(binding, "catalog.providers."+providerID+".realization.inputBindings."+inputRef, "capabilityRef")
		if err != nil {
			return nil, err
		}
		if resolved.providerByCap[capabilityRef] != providerID {
			return nil, fail(ErrContractConflict, "catalog.providers."+providerID+".realization.inputBindings."+inputRef+".capabilityRef", "input binding capability %q is not selected through this provider", capabilityRef)
		}
		key, err := stringField(binding, "catalog.providers."+providerID+".realization.inputBindings."+inputRef, "key")
		if err != nil {
			return nil, err
		}
		switch source {
		case "capability-setting":
			value, bound := resolved.settingsByCap[capabilityRef][key]
			if !bound {
				continue
			}
			clone, err := cloneObject(map[string]any{"value": value}, true)
			if err != nil {
				return nil, err
			}
			values[inputRef] = clone["value"]
		case "capability-secret":
			value, bound := resolved.secretRefsByCap[capabilityRef][key]
			if bound {
				secretRefs[inputRef] = value
			}
		default:
			return nil, fail(ErrInvalidInput, "catalog.providers."+providerID+".realization.inputBindings."+inputRef+".source", "unsupported owner input source %q", source)
		}
	}
	return map[string]any{"values": values, "secretRefs": secretRefs}, nil
}

func (c *Compiler) attachProviderOwnerGateRefs(providers []any, gates map[string]any) error {
	healthGates, err := objectListField(gates, "gates", "health")
	if err != nil {
		return err
	}
	evidenceGates, err := objectListField(gates, "gates", "evidence")
	if err != nil {
		return err
	}
	healthByProvider := make(map[string][]string)
	for _, gate := range healthGates {
		targetKind, err := stringField(gate, "gates.health", "targetKind")
		if err != nil {
			return err
		}
		if targetKind != "provider" {
			continue
		}
		targetRef, err := stringField(gate, "gates.health", "targetRef")
		if err != nil {
			return err
		}
		gateID, err := stringField(gate, "gates.health", "id")
		if err != nil {
			return err
		}
		healthByProvider[targetRef] = append(healthByProvider[targetRef], gateID)
	}
	evidenceByScenario := make(map[string]string, len(evidenceGates))
	for _, gate := range evidenceGates {
		scenario, err := stringField(gate, "gates.evidence", "scenario")
		if err != nil {
			return err
		}
		gateID, err := stringField(gate, "gates.evidence", "id")
		if err != nil {
			return err
		}
		evidenceByScenario[scenario] = gateID
	}
	for _, rawProvider := range providers {
		provider, err := asObject(rawProvider, "providers")
		if err != nil {
			return err
		}
		rawOwner, exists := provider["owner"]
		if !exists {
			continue
		}
		owner, err := asObject(rawOwner, "providers.owner")
		if err != nil {
			return err
		}
		providerID, err := stringField(provider, "providers", "id")
		if err != nil {
			return err
		}
		healthRefs := sortStringsUnique(healthByProvider[providerID])
		if len(healthRefs) == 0 {
			return fail(ErrUnrealizedCapability, "providers."+providerID+".owner.healthGateRefs", "owner has no provider health gate")
		}
		scenarios, err := stringListField(c.catalog.providers[providerID], "catalog.providers."+providerID, "evidence", true)
		if err != nil {
			return err
		}
		evidenceRefs := make([]string, 0, len(scenarios))
		for _, scenario := range scenarios {
			gateID, exists := evidenceByScenario[scenario]
			if !exists {
				return fail(ErrUnrealizedCapability, "providers."+providerID+".owner.evidenceGateRefs", "owner evidence scenario %q has no resolved gate", scenario)
			}
			evidenceRefs = append(evidenceRefs, gateID)
		}
		owner["healthGateRefs"] = stringSliceAny(healthRefs)
		owner["evidenceGateRefs"] = stringSliceAny(sortStringsUnique(evidenceRefs))
	}
	return nil
}

func (c *Compiler) buildWorkloads(resolved *resolution, modules []any) ([]any, error) {
	result := make([]any, 0, len(resolved.workloadIDs))
	moduleByID := make(map[string]map[string]any, len(modules))
	for _, rawModule := range modules {
		module := rawModule.(map[string]any)
		id, err := stringField(module, "modules", "id")
		if err != nil {
			return nil, err
		}
		moduleByID[id] = module
	}
	for _, id := range resolved.workloadIDs {
		selection := resolved.workloads[id]
		path := "catalog.workloads." + id
		version, err := metadataVersion(selection.contract, path)
		if err != nil {
			return nil, err
		}
		contractHash, err := canonicalHash(selection.contract, true)
		if err != nil {
			return nil, err
		}
		alternativeHash, err := canonicalHash(selection.alternative, true)
		if err != nil {
			return nil, err
		}
		kind, err := stringField(selection.contract, path, "kind")
		if err != nil {
			return nil, err
		}
		functionalCapabilities, err := stringListField(selection.contract, path, "functionalCapabilities", true)
		if err != nil {
			return nil, err
		}
		dataClasses, err := stringListField(selection.contract, path, "dataClasses", false)
		if err != nil {
			return nil, err
		}
		route, err := objectField(selection.alternative, path+".alternatives."+selection.alternativeID, "route")
		if err != nil {
			return nil, err
		}
		setup, err := objectField(selection.alternative, path+".alternatives."+selection.alternativeID, "setup")
		if err != nil {
			return nil, err
		}
		module, exists := moduleByID[selection.moduleID]
		if !exists {
			return nil, fail(ErrUnrealizedModule, "spec.workloads."+id, "resolved workload module %q is missing", selection.moduleID)
		}
		runtime, err := objectField(module, "modules."+selection.moduleID, "runtime")
		if err != nil {
			return nil, err
		}
		resolvedRuntime := map[string]any{}
		for _, field := range []string{"kind", "delivery"} {
			value, err := stringField(runtime, "modules."+selection.moduleID+".runtime", field)
			if err != nil {
				return nil, err
			}
			resolvedRuntime[field] = value
		}
		settings, err := cloneObject(selection.settings, true)
		if err != nil {
			return nil, err
		}
		secretRefs, err := cloneObject(selection.secretRefs, true)
		if err != nil {
			return nil, err
		}
		resolvedRoute, err := cloneObject(route, true)
		if err != nil {
			return nil, err
		}
		resolvedSetup, err := cloneObject(setup, true)
		if err != nil {
			return nil, err
		}
		result = append(result, map[string]any{
			"id": id, "version": version, "contractHash": contractHash, "kind": kind,
			"functionalCapabilities": stringSliceAny(functionalCapabilities),
			"dataClasses":            stringSliceAny(dataClasses),
			"alternative": map[string]any{
				"id": selection.alternativeID, "contractHash": alternativeHash,
				"providerRef": selection.providerID, "moduleRef": selection.moduleID,
				"route": resolvedRoute, "runtime": resolvedRuntime, "setup": resolvedSetup,
			},
			"siteRefs": stringSliceAny(selection.siteRefs), "nodeRefs": stringSliceAny(selection.nodeRefs),
			"settings": settings, "secretRefs": secretRefs,
		})
	}
	return result, nil
}

func buildPlacement(workloads []any) ([]any, error) {
	if len(workloads) == 0 {
		return []any{}, nil
	}
	result := make([]any, 0, len(workloads))
	for _, rawWorkload := range workloads {
		workload, err := asObject(rawWorkload, "workloads")
		if err != nil {
			return nil, err
		}
		id, err := stringField(workload, "workloads", "id")
		if err != nil {
			return nil, err
		}
		siteRefs, err := stringListField(workload, "workloads."+id, "siteRefs", true)
		if err != nil {
			return nil, err
		}
		nodeRefs, err := stringListField(workload, "workloads."+id, "nodeRefs", true)
		if err != nil {
			return nil, err
		}
		result = append(result, map[string]any{
			"workloadRef": id, "siteRefs": stringSliceAny(siteRefs),
			"nodeRefs": stringSliceAny(nodeRefs),
			"reason":   "matched explicit site, node, and role constraints",
		})
	}
	return result, nil
}

func buildIdentity(profile *profileView, spec *specView) (map[string]any, error) {
	identity := map[string]any{
		"humanAuthoritySiteRef":   spec.authoritySiteRef,
		"possessionBoundSessions": true,
		"lanLocationIsIdentity":   false,
	}
	if spec.deviceEnrollment != nil {
		device, err := cloneObject(spec.deviceEnrollment, true)
		if err != nil {
			return nil, err
		}
		authority, err := stringField(device, "spec.deviceEnrollment", "authoritySiteRef")
		if err != nil {
			return nil, err
		}
		identity["deviceAuthoritySiteRef"] = authority
		identity["deviceEnrollment"] = device
	}
	var edgeRefs []string
	for _, site := range spec.sites {
		if contains(profile.bridgeEdgeKinds, site.kind) {
			edgeRefs = append(edgeRefs, site.id)
		}
	}
	if edgeRefs = sortStringsUnique(edgeRefs); len(edgeRefs) > 0 {
		identity["edgeVerifierSiteRefs"] = stringSliceAny(edgeRefs)
	}
	return identity, nil
}

func (c *Compiler) buildData(profile *profileView, spec *specView, resolved *resolution) (map[string]any, error) {
	data := map[string]any{"defaultAuthority": profile.dataAuthority}
	var err error
	if spec.data != nil {
		data, err = cloneObject(spec.data, true)
		if err != nil {
			return nil, err
		}
	}
	bindings, _, err := optionalObjectField(data, "resolvedPlan.data", "bindings")
	if err != nil {
		return nil, err
	}
	if bindings == nil {
		bindings = map[string]any{}
	}
	defaultAuthority, err := stringField(data, "resolvedPlan.data", "defaultAuthority")
	if err != nil {
		return nil, err
	}
	for _, workloadID := range resolved.workloadIDs {
		workload := resolved.workloads[workloadID]
		bindingRef, err := c.workloadDataBindingRef(workload)
		if err != nil {
			return nil, err
		}
		if bindingRef == "" {
			continue
		}
		if _, exists := bindings[bindingRef]; exists {
			continue
		}
		if defaultAuthority == "per-workload" {
			return nil, fail(ErrInvalidInput, "spec.data.bindings."+bindingRef, "data-bearing workload %q requires an explicit binding", workloadID)
		}
		var authoritySites []string
		for _, site := range spec.sites {
			if site.kind == defaultAuthority && contains(workload.siteRefs, site.id) {
				authoritySites = append(authoritySites, site.id)
			}
		}
		if len(authoritySites) != 1 {
			return nil, fail(ErrInvalidInput, "spec.data.bindings."+bindingRef, "data-bearing workload %q needs one explicit primary site; default authority %q matched %d workload sites", workloadID, defaultAuthority, len(authoritySites))
		}
		classes, err := stringListField(workload.contract, "catalog.workloads."+workloadID, "dataClasses", true)
		if err != nil {
			return nil, err
		}
		bindings[bindingRef] = map[string]any{
			"classes": stringSliceAny(classes), "primarySiteRef": authoritySites[0],
			"replicaSiteRefs": []any{}, "cloudCopyAllowed": false,
		}
	}
	data["bindings"] = bindings
	return data, nil
}

func (c *Compiler) workloadDataBindingRef(workload *resolvedWorkloadSelection) (string, error) {
	route, err := objectField(workload.alternative, "catalog.workloads."+workload.id+".alternative", "route")
	if err != nil {
		return "", err
	}
	serviceRef, err := stringField(route, "catalog.workloads."+workload.id+".alternative.route", "serviceRef")
	if err != nil {
		return "", err
	}
	healthRef, err := stringField(route, "catalog.workloads."+workload.id+".alternative.route", "healthRef")
	if err != nil {
		return "", err
	}
	module := c.catalog.modules[workload.moduleID]
	units, err := objectListField(module, "catalog.modules."+workload.moduleID, "renderUnits")
	if err != nil {
		return "", err
	}
	for _, unit := range units {
		endpoints, err := objectListOptional(unit, "serviceEndpoints")
		if err != nil {
			return "", err
		}
		for _, endpoint := range endpoints {
			candidateService, err := stringField(endpoint, "catalog.modules."+workload.moduleID+".serviceEndpoints", "serviceRef")
			if err != nil {
				return "", err
			}
			candidateHealth, err := stringField(endpoint, "catalog.modules."+workload.moduleID+".serviceEndpoints", "healthRef")
			if err != nil {
				return "", err
			}
			if candidateService != serviceRef || candidateHealth != healthRef {
				continue
			}
			endpointData, exists, err := optionalObjectField(endpoint, "catalog.modules."+workload.moduleID+".serviceEndpoints", "data")
			if err != nil || !exists {
				return "", err
			}
			return stringField(endpointData, "catalog.modules."+workload.moduleID+".serviceEndpoints.data", "bindingRef")
		}
	}
	return "", fail(ErrContractConflict, "catalog.workloads."+workload.id+".alternative.route", "selected route has no exact module service endpoint")
}

func cloneSites(sites []siteView) ([]any, error) {
	result := make([]any, 0, len(sites))
	for _, site := range sites {
		clone, err := cloneObject(site.object, true)
		if err != nil {
			return nil, err
		}
		result = append(result, clone)
	}
	return result, nil
}

func cloneNodes(nodes []nodeView) ([]any, error) {
	result := make([]any, 0, len(nodes))
	for _, node := range nodes {
		clone, err := cloneObject(node.object, true)
		if err != nil {
			return nil, err
		}
		clone["enabled"] = node.enabled
		if roles, ok := clone["roles"].([]any); ok {
			var strings []string
			for _, role := range roles {
				strings = append(strings, fmt.Sprint(role))
			}
			clone["roles"] = stringSliceAny(sortStringsUnique(strings))
		}
		result = append(result, clone)
	}
	return result, nil
}

func stringSliceAny(values []string) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}
