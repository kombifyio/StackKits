package resolvedplan

import (
	"fmt"
	"sort"
)

type providerModuleSelection struct {
	selected map[string]string // selected module ID -> governing selected provider ID
	required map[string]string // required module ID -> governing selected provider ID
	optional map[string]string // optional module ID -> governing selected provider ID
	governed map[string]string // every module exposed by a selected provider
}

func intersectStrings(left, right []string) []string {
	allowed := stringSet(right)
	var result []string
	for _, value := range left {
		if _, exists := allowed[value]; exists {
			result = append(result, value)
		}
	}
	return sortStringsUnique(result)
}

func (c *Compiler) buildModules(spec *specView, resolved *resolution, providerSites, providerNodes map[string][]string) ([]any, []any, map[string][]string, map[string][]string, error) {
	selected, err := c.selectProviderModules(resolved)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := c.selectWorkloadModules(resolved, selected); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := c.validateExplicitModuleIntent(spec, selected); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := c.closeModuleDependencies(selected); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := detectModuleCycles(selected.selected, c.catalog); err != nil {
		return nil, nil, nil, nil, err
	}
	modules, moduleSites, moduleNodes, err := c.resolveSelectedModules(spec, resolved, selected.selected, providerSites, providerNodes)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := c.validateModuleCoverage(resolved, modules); err != nil {
		return nil, nil, nil, nil, err
	}
	runtimeNetworks, err := resolveImplementationInterfaces(modules, spec.nodeByID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return modules, runtimeNetworks, moduleSites, moduleNodes, nil
}

func (c *Compiler) selectWorkloadModules(resolved *resolution, selection *providerModuleSelection) error {
	for _, workloadID := range resolved.workloadIDs {
		workload := resolved.workloads[workloadID]
		providerID, governed := selection.governed[workload.moduleID]
		if !governed || providerID != workload.providerID {
			return fail(ErrUnrealizedModule, "spec.workloads."+workloadID+".alternative", "module %q is not governed by provider %q", workload.moduleID, workload.providerID)
		}
		selection.selected[workload.moduleID] = workload.providerID
		if workload.runtimeAdapterModuleID != "" {
			adapterProviderID, governed := selection.governed[workload.runtimeAdapterModuleID]
			if !governed || adapterProviderID != workload.runtimeAdapterProviderID {
				return fail(ErrUnrealizedModule, "spec.workloads."+workloadID+".runtimeAdapterRef", "runtime adapter module %q is not governed by provider %q", workload.runtimeAdapterModuleID, workload.runtimeAdapterProviderID)
			}
			selection.selected[workload.runtimeAdapterModuleID] = workload.runtimeAdapterProviderID
		}
	}
	return nil
}

func (c *Compiler) selectProviderModules(resolved *resolution) (*providerModuleSelection, error) {
	selection := &providerModuleSelection{
		selected: make(map[string]string),
		required: make(map[string]string),
		optional: make(map[string]string),
		governed: make(map[string]string),
	}
	for _, providerID := range resolved.providerIDs {
		provider := c.catalog.providers[providerID]
		realization, err := objectField(provider, "catalog.providers."+providerID, "realization")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(realization, "catalog.providers."+providerID+".realization", "kind")
		if err != nil {
			return nil, err
		}
		switch kind {
		case "none":
			return nil, fail(ErrUnrealizedCapability, "catalog.providers."+providerID+".realization", "selected provider has no approved realization")
		case "contract":
			continue
		case "topology":
			if err := validateTopologyProviderRealization(providerID, provider, realization); err != nil {
				return nil, err
			}
			continue
		case "host", "external":
			ownerRef, err := stringField(realization, "catalog.providers."+providerID+".realization", "ownerRef")
			if err != nil {
				return nil, err
			}
			if ownerRef != providerID {
				return nil, fail(ErrContractConflict, "catalog.providers."+providerID+".realization.ownerRef", "ownerRef must identify the selected provider contract")
			}
			if _, err := objectField(realization, "catalog.providers."+providerID+".realization", "realizationSupport"); err != nil {
				return nil, err
			}
			continue
		case "modules":
			moduleRefs, err := objectField(realization, "catalog.providers."+providerID+".realization", "moduleRefs")
			if err != nil {
				return nil, err
			}
			required, err := stringListField(moduleRefs, "catalog.providers."+providerID+".realization.moduleRefs", "required", false)
			if err != nil {
				return nil, err
			}
			optional, err := stringListField(moduleRefs, "catalog.providers."+providerID+".realization.moduleRefs", "optional", false)
			if err != nil {
				return nil, err
			}
			if len(required)+len(optional) == 0 {
				return nil, fail(ErrUnrealizedModule, "catalog.providers."+providerID+".realization.moduleRefs", "modules realization must govern at least one required or optional module")
			}
			if err := c.registerProviderModuleRefs(selection, providerID, required, true); err != nil {
				return nil, err
			}
			if err := c.registerProviderModuleRefs(selection, providerID, optional, false); err != nil {
				return nil, err
			}
		default:
			return nil, fail(ErrInvalidInput, "catalog.providers."+providerID+".realization.kind", "unsupported realization %q", kind)
		}
	}
	return selection, nil
}

func validateTopologyProviderRealization(providerID string, provider, realization map[string]any) error {
	path := "catalog.providers." + providerID
	topology, err := objectField(realization, path+".realization", "topology")
	if err != nil {
		return err
	}
	siteKinds, err := stringListField(topology, path+".realization.topology", "siteKinds", true)
	if err != nil {
		return err
	}
	supported, err := stringListField(provider, path, "supportedSiteKinds", true)
	if err != nil {
		return err
	}
	siteKinds = sortStringsUnique(siteKinds)
	supported = sortStringsUnique(supported)
	if len(siteKinds) != len(supported) {
		return fail(ErrContractConflict, path+".realization.topology.siteKinds", "must exactly match supportedSiteKinds")
	}
	for index := range siteKinds {
		if siteKinds[index] != supported[index] {
			return fail(ErrContractConflict, path+".realization.topology.siteKinds", "must exactly match supportedSiteKinds")
		}
	}
	return nil
}

func (c *Compiler) registerProviderModuleRefs(selection *providerModuleSelection, providerID string, moduleIDs []string, required bool) error {
	for _, moduleID := range moduleIDs {
		contract, exists := c.catalog.modules[moduleID]
		if !exists {
			return fail(ErrUnknownModule, "catalog.providers."+providerID+".realization.moduleRefs", "provider references unknown module %q", moduleID)
		}
		if existing, exists := selection.governed[moduleID]; exists {
			return fail(ErrContractConflict, "catalog.providers."+providerID+".realization.moduleRefs", "module %q is governed more than once by %q and %q", moduleID, existing, providerID)
		}
		if declaredProvider, hasProvider, err := optionalStringField(contract, "catalog.modules."+moduleID, "providerRef"); err != nil {
			return err
		} else if hasProvider && declaredProvider != providerID {
			return fail(ErrContractConflict, "catalog.modules."+moduleID+".providerRef", "module declares provider %q but realization is governed by %q", declaredProvider, providerID)
		}
		selection.governed[moduleID] = providerID
		if required {
			selection.required[moduleID] = providerID
			selection.selected[moduleID] = providerID
		} else {
			selection.optional[moduleID] = providerID
		}
	}
	return nil
}

func (c *Compiler) validateExplicitModuleIntent(spec *specView, selection *providerModuleSelection) error {
	// Explicit module intent can refine a governed selected module, but cannot
	// manufacture a module outside the provider realization graph.
	for moduleID, rawIntent := range spec.modules {
		contract, exists := c.catalog.modules[moduleID]
		if !exists {
			return fail(ErrUnknownModule, "spec.modules."+moduleID, "no governed module contract exists")
		}
		role, err := stringField(contract, "catalog.modules."+moduleID, "role")
		if err != nil {
			return err
		}
		if role == "workload" {
			return fail(ErrUnrealizedModule, "spec.modules."+moduleID, "workload modules are selected only through spec.workloads")
		}
		providerID, governed := selection.governed[moduleID]
		if !governed {
			return fail(ErrUnrealizedModule, "spec.modules."+moduleID, "module is not governed by any selected provider realization")
		}
		intent, err := asObject(rawIntent, "spec.modules."+moduleID)
		if err != nil {
			return err
		}
		enabled, err := boolFieldDefault(intent, "spec.modules."+moduleID, "enabled", true)
		if err != nil {
			return err
		}
		_, required := selection.required[moduleID]
		if !enabled && required {
			return fail(ErrUnrealizedModule, "spec.modules."+moduleID, "a provider-required module cannot be disabled")
		}
		if enabled {
			selection.selected[moduleID] = providerID
		}
	}
	return nil
}

func (c *Compiler) closeModuleDependencies(selection *providerModuleSelection) error {
	// Required provider modules are already selected. A dependency may point to
	// one of those modules, but it may not silently promote an optional module;
	// optional selection is exclusively explicit StackSpec intent.
	for changed := true; changed; {
		changed = false
		for _, moduleID := range sortedStringMapKeys(selection.selected) {
			contract, exists := c.catalog.modules[moduleID]
			if !exists {
				return fail(ErrUnknownModule, "catalog.modules."+moduleID, "provider references an unknown module")
			}
			requires, err := stringListField(contract, "catalog.modules."+moduleID, "requires", false)
			if err != nil {
				return err
			}
			for _, dependencyID := range requires {
				if _, exists := c.catalog.modules[dependencyID]; !exists {
					return fail(ErrUnknownModule, "catalog.modules."+moduleID+".requires", "unknown module dependency %q", dependencyID)
				}
				providerID, governed := selection.governed[dependencyID]
				if !governed {
					return fail(ErrUnrealizedModule, "catalog.modules."+moduleID+".requires", "dependency %q is not governed by a selected provider realization", dependencyID)
				}
				if _, exists := selection.selected[dependencyID]; !exists {
					if _, optional := selection.optional[dependencyID]; optional {
						return fail(ErrUnrealizedModule, "catalog.modules."+moduleID+".requires", "optional dependency %q must be explicitly enabled through spec.modules", dependencyID)
					}
					selection.selected[dependencyID] = providerID
					changed = true
				}
			}
		}
	}
	return nil
}

func (c *Compiler) resolveSelectedModules(spec *specView, resolved *resolution, selected map[string]string, providerSites, providerNodes map[string][]string) ([]any, map[string][]string, map[string][]string, error) {
	moduleSites := make(map[string][]string, len(selected))
	moduleNodes := make(map[string][]string, len(selected))
	result := make([]any, 0, len(selected))
	for _, moduleID := range sortedStringMapKeys(selected) {
		module, siteRefs, nodeRefs, err := c.resolveSelectedModule(spec, resolved, moduleID, selected[moduleID], providerSites, providerNodes)
		if err != nil {
			return nil, nil, nil, err
		}
		result = append(result, module)
		moduleSites[moduleID], moduleNodes[moduleID] = siteRefs, nodeRefs
	}
	return result, moduleSites, moduleNodes, nil
}

func (c *Compiler) validateModuleCoverage(resolved *resolution, modules []any) error {
	// Every selected capability is realized either by a selected governed
	// module or by the explicit host/external owner contract.
	for capability, providerID := range resolved.providerByCap {
		realization, err := objectField(c.catalog.providers[providerID], "catalog.providers."+providerID, "realization")
		if err != nil {
			return err
		}
		kind, err := stringField(realization, "catalog.providers."+providerID+".realization", "kind")
		if err != nil {
			return err
		}
		switch kind {
		case "host", "external":
			ownerRef, err := stringField(realization, "catalog.providers."+providerID+".realization", "ownerRef")
			if err != nil {
				return err
			}
			if ownerRef != providerID {
				return fail(ErrUnrealizedCapability, "capabilities."+capability, "provider %q has no explicit matching owner contract", providerID)
			}
			continue
		case "contract":
			continue
		case "topology":
			if err := validateTopologyProviderRealization(providerID, c.catalog.providers[providerID], realization); err != nil {
				return err
			}
			continue
		case "none":
			return fail(ErrUnrealizedCapability, "capabilities."+capability, "provider %q has no approved realization", providerID)
		case "modules":
		default:
			return fail(ErrInvalidInput, "catalog.providers."+providerID+".realization.kind", "unsupported realization %q", kind)
		}
		covered := false
		for _, rawModule := range modules {
			module := rawModule.(map[string]any)
			if module["providerRef"] == providerID && anyStringListContains(module["provides"], capability) {
				covered = true
				break
			}
		}
		if !covered {
			return fail(ErrUnrealizedCapability, "capabilities."+capability, "provider %q has no governed module covering the capability", providerID)
		}
	}
	return nil
}

func (c *Compiler) resolveSelectedModule(spec *specView, resolved *resolution, moduleID, selectedProvider string, _ map[string][]string, providerNodes map[string][]string) (map[string]any, []string, []string, error) {
	contract := c.catalog.modules[moduleID]
	providerID, err := resolveModuleProvider(moduleID, selectedProvider, contract)
	if err != nil {
		return nil, nil, nil, err
	}
	siteRefs, nodeRefs, err := resolveModuleTargets(moduleID, providerID, contract, spec, providerNodes)
	if err != nil {
		return nil, nil, nil, err
	}
	rawIntent := spec.modules[moduleID]
	if workloadID, isWorkload := resolved.workloadByModule[moduleID]; isWorkload {
		workload := resolved.workloads[workloadID]
		nodeRefs = intersectStrings(nodeRefs, workload.nodeRefs)
		if len(nodeRefs) == 0 {
			return nil, nil, nil, fail(ErrUnresolvedPlacement, "spec.workloads."+workloadID+".placement", "workload placement has no eligible module target")
		}
		var narrowedSites []string
		for _, nodeRef := range nodeRefs {
			narrowedSites = append(narrowedSites, spec.nodeByID[nodeRef].siteRef)
		}
		siteRefs = sortStringsUnique(narrowedSites)
		rawIntent = map[string]any{
			"enabled":    true,
			"settings":   workload.settings,
			"secretRefs": workload.secretRefs,
		}
		workload.siteRefs, workload.nodeRefs = siteRefs, nodeRefs
	}
	provides, err := resolveModuleProvides(moduleID, providerID, contract, resolved)
	if err != nil {
		return nil, nil, nil, err
	}
	capabilitySecretRefs, err := resolveModuleCapabilitySecretRefs(moduleID, providerID, contract, resolved)
	if err != nil {
		return nil, nil, nil, err
	}
	generationTarget, err := stringField(spec.generation, "spec.generation", "target")
	if err != nil {
		return nil, nil, nil, err
	}
	module, err := c.resolveModuleContract(moduleID, providerID, provides, siteRefs, nodeRefs, contract, rawIntent, capabilitySecretRefs, spec.nodes, generationTarget)
	return module, siteRefs, nodeRefs, err
}

func resolveModuleProvider(moduleID, selectedProvider string, contract map[string]any) (string, error) {
	if selectedProvider == "" {
		return "", fail(ErrUnrealizedModule, "catalog.modules."+moduleID, "selected module has no governing selected provider")
	}
	declaredProvider, hasProvider, err := optionalStringField(contract, "catalog.modules."+moduleID, "providerRef")
	if err != nil {
		return "", err
	}
	if hasProvider && selectedProvider != "" && declaredProvider != selectedProvider {
		return "", fail(ErrContractConflict, "catalog.modules."+moduleID+".providerRef", "module belongs to %q but provider realization selected it for %q", declaredProvider, selectedProvider)
	}
	if selectedProvider == "" && hasProvider {
		return declaredProvider, nil
	}
	return selectedProvider, nil
}

func resolveModuleTargets(moduleID, providerID string, contract map[string]any, spec *specView, providerNodes map[string][]string) ([]string, []string, error) {
	return resolveModuleTargetsWithInventoryAttestation(moduleID, providerID, contract, spec, providerNodes, true)
}

func resolveModuleTargetsWithInventoryAttestation(moduleID, providerID string, contract map[string]any, spec *specView, providerNodes map[string][]string, attestInventory bool) ([]string, []string, error) {
	supportedKinds, err := stringListField(contract, "catalog.modules."+moduleID, "supportedSiteKinds", true)
	if err != nil {
		return nil, nil, err
	}
	controlMembers, err := stringListField(spec.controlPlane, "spec.controlPlane", "members", true)
	if err != nil {
		return nil, nil, err
	}
	context := moduleTargetContext{
		authoritySiteRef: spec.authoritySiteRef,
		controlMembers:   moduleTargetStringSet(controlMembers),
		attestInventory:  attestInventory,
	}
	var siteRefs, nodeRefs []string
	for _, node := range spec.nodes {
		if !node.enabled || !contains(supportedKinds, node.siteKind) {
			continue
		}
		if providerID != "" && !contains(providerNodes[providerID], node.id) {
			continue
		}
		matches, err := moduleNodeMatchesContract(moduleID, contract, node, context)
		if err != nil {
			return nil, nil, err
		}
		if !matches {
			continue
		}
		nodeRefs = append(nodeRefs, node.id)
		siteRefs = append(siteRefs, node.siteRef)
	}
	siteRefs, nodeRefs = sortStringsUnique(siteRefs), sortStringsUnique(nodeRefs)
	if len(siteRefs) == 0 || len(nodeRefs) == 0 {
		return nil, nil, fail(ErrUnrealizedModule, "catalog.modules."+moduleID, "module has no eligible target nodes")
	}
	return siteRefs, nodeRefs, nil
}

type moduleTargetContext struct {
	authoritySiteRef string
	controlMembers   map[string]struct{}
	attestInventory  bool
}

//nolint:gocyclo // Eligibility is the conjunction of all site, node, role, inventory, and provider constraints.
func moduleNodeMatchesContract(moduleID string, contract map[string]any, node nodeView, context moduleTargetContext) (bool, error) {
	path := "catalog.modules." + moduleID
	selection, hasSelection, err := optionalObjectField(contract, path, "nodeSelection")
	if err != nil {
		return false, err
	}
	if hasSelection {
		authority, err := stringField(selection, path+".nodeSelection", "authority")
		if err != nil {
			return false, err
		}
		switch authority {
		case "any":
		case "control-authority-site":
			if node.siteRef != context.authoritySiteRef {
				return false, nil
			}
		case "non-control-authority-sites":
			if node.siteRef == context.authoritySiteRef {
				return false, nil
			}
		default:
			return false, fail(ErrContractConflict, path+".nodeSelection.authority", "unsupported authority selector %q", authority)
		}

		membership, err := stringField(selection, path+".nodeSelection", "controlPlaneMembers")
		if err != nil {
			return false, err
		}
		_, isMember := context.controlMembers[node.id]
		switch membership {
		case "any":
		case "only":
			if !isMember {
				return false, nil
			}
		case "exclude":
			if isMember {
				return false, nil
			}
		default:
			return false, fail(ErrContractConflict, path+".nodeSelection.controlPlaneMembers", "unsupported control-plane membership selector %q", membership)
		}

		requiredRoles, err := stringListField(selection, path+".nodeSelection", "requiredRoles", false)
		if err != nil {
			return false, err
		}
		for _, role := range requiredRoles {
			if !contains(node.roles, role) {
				return false, nil
			}
		}
		labels, hasLabels, err := optionalObjectField(selection, path+".nodeSelection", "matchLabels")
		if err != nil {
			return false, err
		}
		if hasLabels {
			nodeLabels, _, err := optionalObjectField(node.object, "spec.nodes."+node.id, "labels")
			if err != nil {
				return false, err
			}
			for key, wanted := range labels {
				if nodeLabels[key] != wanted {
					return false, nil
				}
			}
		}
	}

	requirements, hasRequirements, err := optionalObjectField(contract, path, "runtimeRequirements")
	if err != nil || !hasRequirements {
		return !hasRequirements, err
	}
	return moduleNodeSatisfiesRuntimeRequirements(moduleID, requirements, node, context.attestInventory)
}

//nolint:gocyclo // Runtime eligibility must validate each attested inventory dimension without accepting partial matches.
func moduleNodeSatisfiesRuntimeRequirements(moduleID string, requirements map[string]any, node nodeView, attestInventory bool) (bool, error) {
	path := "catalog.modules." + moduleID + ".runtimeRequirements"
	hardware, err := objectField(node.object, "spec.nodes."+node.id, "hardware")
	if err != nil {
		return false, err
	}
	requiredFacts, err := moduleRuntimeRequiredFacts(requirements, path)
	if err != nil {
		return false, err
	}
	for _, fact := range requiredFacts {
		desired, declared := hardware[fact]
		if !declared {
			return false, fail(ErrProfileMismatch, "spec.nodes."+node.id+".hardware."+fact, "module %q requires the desired runtime fact to be declared", moduleID)
		}
		if !attestInventory {
			continue
		}
		observed, attested := node.inventoryFacts[fact]
		if !attested {
			return false, fail(ErrProfileMismatch, "inventory.nodes."+node.id+"."+fact, "module %q requires an attested runtime fact", moduleID)
		}
		equal, err := canonicalEqual(desired, observed)
		if err != nil {
			return false, err
		}
		if !equal {
			return false, fail(ErrProfileMismatch, "inventory.nodes."+node.id+"."+fact, "attested fact does not match spec.nodes.%s.hardware.%s", node.id, fact)
		}
	}

	if allowed, err := stringListField(requirements, path, "allowedArchitectures", false); err != nil {
		return false, err
	} else if len(allowed) > 0 {
		arch, err := stringField(hardware, "spec.nodes."+node.id+".hardware", "arch")
		if err != nil {
			return false, err
		}
		if !contains(allowed, arch) {
			return false, nil
		}
	}
	if allowed, err := stringListField(requirements, path, "allowedVirtualization", false); err != nil {
		return false, err
	} else if len(allowed) > 0 {
		virtualization, err := stringField(hardware, "spec.nodes."+node.id+".hardware", "virtualization")
		if err != nil {
			return false, err
		}
		if !contains(allowed, virtualization) {
			return false, nil
		}
	}
	for _, minimum := range []struct {
		requirement string
		fact        string
	}{{"minCpuCores", "cpuCores"}, {"minRamGB", "ramGB"}, {"minStorageGB", "storageGB"}} {
		wanted, exists := requirements[minimum.requirement]
		if !exists {
			continue
		}
		minimumValue, err := intField(map[string]any{minimum.requirement: wanted}, path, minimum.requirement)
		if err != nil {
			return false, err
		}
		actualValue, err := intField(hardware, "spec.nodes."+node.id+".hardware", minimum.fact)
		if err != nil {
			return false, err
		}
		if actualValue < minimumValue {
			return false, nil
		}
	}
	return true, nil
}

func moduleRuntimeRequiredFacts(requirements map[string]any, path string) ([]string, error) {
	facts, err := stringListField(requirements, path, "requireInventoryFacts", false)
	if err != nil {
		return nil, err
	}
	if _, exists := requirements["allowedArchitectures"]; exists {
		facts = append(facts, "arch")
	}
	if _, exists := requirements["minCpuCores"]; exists {
		facts = append(facts, "cpuCores")
	}
	if _, exists := requirements["minRamGB"]; exists {
		facts = append(facts, "ramGB")
	}
	if _, exists := requirements["minStorageGB"]; exists {
		facts = append(facts, "storageGB")
	}
	if _, exists := requirements["allowedVirtualization"]; exists {
		facts = append(facts, "virtualization")
	}
	return sortStringsUnique(facts), nil
}

func moduleTargetStringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func resolveModuleProvides(moduleID, providerID string, contract map[string]any, resolved *resolution) ([]string, error) {
	role, err := stringField(contract, "catalog.modules."+moduleID, "role")
	if err != nil {
		return nil, err
	}
	if role == "workload" {
		return []string{}, nil
	}
	contractProvides, err := stringListField(contract, "catalog.modules."+moduleID, "provides", true)
	if err != nil {
		return nil, err
	}
	if len(contractProvides) == 0 {
		hasTypedImplementation, err := moduleProvidesTypedImplementation(moduleID, contract)
		if err != nil {
			return nil, err
		}
		if !hasTypedImplementation {
			return nil, fail(ErrUnrealizedModule, "catalog.modules."+moduleID, "selected module realizes neither a selected capability nor a typed implementation contract")
		}
		return []string{}, nil
	}
	var provides []string
	for _, capability := range resolved.capabilityIDs {
		if contains(contractProvides, capability) && (providerID == "" || resolved.providerByCap[capability] == providerID) {
			provides = append(provides, capability)
		}
	}
	provides = sortStringsUnique(provides)
	if len(provides) == 0 {
		return nil, fail(ErrUnrealizedModule, "catalog.modules."+moduleID, "selected module realizes no selected capability")
	}
	return provides, nil
}

func moduleProvidesTypedImplementation(moduleID string, contract map[string]any) (bool, error) {
	path := "catalog.modules." + moduleID
	if _, exists, err := optionalObjectField(contract, path, "runtimeAdapter"); err != nil {
		return false, err
	} else if exists {
		return true, nil
	}
	if _, exists, err := optionalObjectField(contract, path, "runtimeAdapterAgent"); err != nil {
		return false, err
	} else if exists {
		return true, nil
	}
	support, err := objectField(contract, path, "realizationSupport")
	if err != nil {
		return false, err
	}
	scope, err := stringField(support, path+".realizationSupport", "scope")
	if err != nil {
		return false, err
	}
	level, err := stringField(support, path+".realizationSupport", "level")
	if err != nil {
		return false, err
	}
	if scope != "concrete" || (level != "generation-ready" && level != "apply-ready") {
		return false, nil
	}
	units, err := objectListField(contract, path, "renderUnits")
	if err != nil {
		return false, err
	}
	for index, unit := range units {
		interfaces, err := objectListOptional(unit, "providesInterfaces")
		if err != nil {
			return false, fail(ErrInvalidInput, fmt.Sprintf("%s.renderUnits[%d].providesInterfaces", path, index), "%v", err)
		}
		if len(interfaces) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (c *Compiler) resolveModuleContract(moduleID, providerID string, provides, siteRefs, nodeRefs []string, contract map[string]any, rawIntent any, capabilitySecretRefs map[string]any, nodes []nodeView, generationTarget string) (map[string]any, error) {
	version, err := metadataVersion(contract, "catalog.modules."+moduleID)
	if err != nil {
		return nil, err
	}
	contractHash, err := canonicalHash(contract, true)
	if err != nil {
		return nil, err
	}
	placement := newModuleRenderPlacementContext(siteRefs, nodeRefs, nodes)
	resolvedRuntime, resolvedRenderUnits, resolvedSupport, resolvedVariant, err := resolveModuleRuntimeContracts(moduleID, contract, rawIntent, capabilitySecretRefs, placement, generationTarget)
	if err != nil {
		return nil, err
	}
	module := map[string]any{
		"id": moduleID, "version": version, "contractHash": contractHash,
		"provides": stringSliceAny(provides), "siteRefs": stringSliceAny(siteRefs), "nodeRefs": stringSliceAny(nodeRefs),
		"runtime": resolvedRuntime, "renderTarget": generationTarget, "renderUnits": resolvedRenderUnits, "realizationSupport": resolvedSupport,
	}
	if resolvedVariant != nil {
		module["renderVariant"] = resolvedVariant
	}
	role, err := stringField(contract, "catalog.modules."+moduleID, "role")
	if err != nil {
		return nil, err
	}
	module["role"] = role
	if providerID != "" {
		module["providerRef"] = providerID
	}
	requires, err := stringListField(contract, "catalog.modules."+moduleID, "requires", false)
	if err != nil {
		return nil, err
	}
	if requires = sortStringsUnique(requires); len(requires) > 0 {
		module["requires"] = stringSliceAny(requires)
	}
	for _, field := range []string{"nodeSelection", "runtimeRequirements", "enforcementRequirement", "runtimeOwnerRequirement", "runtimeAdapter", "runtimeAdapterAgent"} {
		contractValue, exists, err := optionalObjectField(contract, "catalog.modules."+moduleID, field)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		resolvedValue, err := cloneObject(contractValue, true)
		if err != nil {
			return nil, err
		}
		module[field] = resolvedValue
	}
	return module, nil
}

func resolveModuleRuntimeContracts(moduleID string, contract map[string]any, rawIntent any, capabilitySecretRefs map[string]any, placement moduleRenderPlacementContext, generationTarget string) (map[string]any, []any, map[string]any, map[string]any, error) {
	runtimeContract, err := objectField(contract, "catalog.modules."+moduleID, "runtime")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	resolvedRuntime, err := cloneObject(runtimeContract, true)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	resolvedRenderUnits, selectedUnitIDs, declared, resolvedVariant, err := resolveModuleRenderUnits(moduleID, contract, rawIntent, capabilitySecretRefs, placement, generationTarget)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	supportContract, err := objectField(contract, "catalog.modules."+moduleID, "realizationSupport")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	resolvedSupport, err := cloneObject(supportContract, true)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := selectModuleRealizationSupport(moduleID, resolvedSupport, selectedUnitIDs, declared, resolvedVariant, generationTarget); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := bindResolvedRenderInstanceOutputs(moduleID, resolvedRenderUnits, resolvedSupport); err != nil {
		return nil, nil, nil, nil, err
	}
	return resolvedRuntime, resolvedRenderUnits, resolvedSupport, resolvedVariant, nil
}

type indexedModuleRenderUnit struct {
	id   string
	unit map[string]any
}

type moduleRenderIntent struct {
	settings   map[string]any
	secretRefs map[string]any
}

type moduleDeclaredInputs struct {
	public      map[string]struct{}
	secret      map[string]struct{}
	plan        map[string]struct{}
	bound       map[string]struct{}
	secretBound map[string]struct{}
}

func selectModuleRenderUnits(moduleID string, units []map[string]any, generationTarget string) ([]map[string]any, map[string]struct{}, error) {
	selected := make([]map[string]any, 0, len(units))
	selectedIDs := make(map[string]struct{}, len(units))
	for index, unit := range units {
		unitPath := fmt.Sprintf("catalog.modules.%s.renderUnits[%d]", moduleID, index)
		targets, err := stringListField(unit, unitPath, "compatibleTargets", false)
		if err != nil {
			return nil, nil, err
		}
		// External catalogs are normalized by CUE, which materializes the
		// default. The fallback keeps direct package-level contract tests honest.
		if len(targets) == 0 {
			targets = []string{"compose", "opentofu"}
		}
		if !contains(targets, generationTarget) {
			continue
		}
		unitID, err := stringField(unit, unitPath, "id")
		if err != nil {
			return nil, nil, err
		}
		selectedUnit, err := cloneObject(unit, true)
		if err != nil {
			return nil, nil, err
		}
		// A ResolvedPlan carries the selected variant, not the catalog's wider
		// compatibility set. This makes the projection exact and hash-visible.
		selectedUnit["compatibleTargets"] = stringSliceAny([]string{generationTarget})
		selected = append(selected, selectedUnit)
		selectedIDs[unitID] = struct{}{}
	}
	if len(units) > 0 && len(selected) == 0 {
		return nil, nil, fail(ErrUnrealizedModule, "catalog.modules."+moduleID+".renderUnits", "module has no render variant for generation target %q", generationTarget)
	}
	return selected, selectedIDs, nil
}

func selectExplicitModuleRenderVariant(moduleID string, contract map[string]any, units []map[string]any, generationTarget string) ([]map[string]any, map[string]struct{}, map[string]any, error) {
	variants, err := objectListOptional(contract, "renderVariants")
	if err != nil {
		return nil, nil, nil, fail(ErrContractConflict, "catalog.modules."+moduleID+".renderVariants", "%v", err)
	}
	if len(variants) == 0 {
		selected, selectedIDs, err := selectModuleRenderUnits(moduleID, units, generationTarget)
		return selected, selectedIDs, nil, err
	}
	matches := make([]map[string]any, 0, 1)
	for index, variant := range variants {
		target, err := stringField(variant, fmt.Sprintf("catalog.modules.%s.renderVariants[%d]", moduleID, index), "target")
		if err != nil {
			return nil, nil, nil, err
		}
		if target == generationTarget {
			matches = append(matches, variant)
		}
	}
	if len(matches) != 1 {
		return nil, nil, nil, fail(ErrUnrealizedModule, "catalog.modules."+moduleID+".renderVariants", "generation target %q resolves %d variants, want exactly one", generationTarget, len(matches))
	}
	variant, err := cloneObject(matches[0], true)
	if err != nil {
		return nil, nil, nil, err
	}
	unitRefs, err := stringListField(variant, "catalog.modules."+moduleID+".renderVariants", "unitRefs", true)
	if err != nil {
		return nil, nil, nil, err
	}
	unitRefSet, err := uniqueStringSet(unitRefs, "catalog.modules."+moduleID+".renderVariants.unitRefs")
	if err != nil {
		return nil, nil, nil, err
	}
	selected := make([]map[string]any, 0, len(unitRefs))
	selectedIDs := make(map[string]struct{}, len(unitRefs))
	for index, unit := range units {
		unitPath := fmt.Sprintf("catalog.modules.%s.renderUnits[%d]", moduleID, index)
		unitID, err := stringField(unit, unitPath, "id")
		if err != nil {
			return nil, nil, nil, err
		}
		if _, exists := unitRefSet[unitID]; !exists {
			continue
		}
		targets, err := stringListField(unit, unitPath, "compatibleTargets", false)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(targets) == 0 {
			targets = []string{"compose", "opentofu"}
		}
		if !contains(targets, generationTarget) {
			return nil, nil, nil, fail(ErrContractConflict, unitPath+".compatibleTargets", "variant selects unit outside target %q", generationTarget)
		}
		selectedUnit, err := cloneObject(unit, true)
		if err != nil {
			return nil, nil, nil, err
		}
		selectedUnit["compatibleTargets"] = stringSliceAny([]string{generationTarget})
		selected = append(selected, selectedUnit)
		selectedIDs[unitID] = struct{}{}
	}
	if len(selectedIDs) != len(unitRefSet) {
		return nil, nil, nil, fail(ErrContractConflict, "catalog.modules."+moduleID+".renderVariants.unitRefs", "variant references unknown render unit")
	}
	return selected, selectedIDs, variant, nil
}

func selectModuleDefaults(defaults map[string]any, declared moduleDeclaredInputs) map[string]any {
	selected := make(map[string]any, len(defaults))
	for key, value := range defaults {
		if _, exists := declared.public[key]; exists {
			selected[key] = value
		}
	}
	return selected
}

func selectModuleRealizationSupport(moduleID string, support map[string]any, selectedUnitIDs map[string]struct{}, declared moduleDeclaredInputs, variant map[string]any, generationTarget string) error {
	path := "catalog.modules." + moduleID + ".realizationSupport"
	if inputs, err := objectField(support, path, "inputs"); err != nil {
		return err
	} else if err := selectSupportRequiredRefs(inputs, path+".inputs", declared.public, declared.secret); err != nil {
		return err
	}
	if planInputs, err := objectField(support, path, "planInputs"); err != nil {
		return err
	} else if err := selectSupportRequiredRefs(planInputs, path+".planInputs", declared.plan); err != nil {
		return err
	}

	artifacts, err := objectField(support, path, "artifacts")
	if err != nil {
		return err
	}
	contracts, err := objectListField(artifacts, path+".artifacts", "contracts")
	if err != nil {
		return err
	}
	selectedContracts := make([]any, 0, len(contracts))
	selectedArtifactIDs := make(map[string]struct{}, len(contracts))
	var explicitArtifactIDs map[string]struct{}
	if variant != nil {
		artifactRefs, err := stringListField(variant, path, "artifactRefs", true)
		if err != nil {
			return err
		}
		explicitArtifactIDs, err = uniqueStringSet(artifactRefs, path+".artifactRefs")
		if err != nil {
			return err
		}
		if err := validateVariantInputRefs(moduleID, variant, declared); err != nil {
			return err
		}
	}
	for index, contract := range contracts {
		contractPath := fmt.Sprintf("%s.artifacts.contracts[%d]", path, index)
		targets, err := stringListField(contract, contractPath, "compatibleTargets", true)
		if err != nil {
			return err
		}
		if !contains(targets, generationTarget) {
			continue
		}
		artifactID, err := stringField(contract, contractPath, "id")
		if err != nil {
			return err
		}
		if explicitArtifactIDs != nil {
			if _, selected := explicitArtifactIDs[artifactID]; !selected {
				continue
			}
		}
		unitRef, err := stringField(contract, contractPath, "unitRef")
		if err != nil {
			return err
		}
		if _, exists := selectedUnitIDs[unitRef]; !exists {
			return fail(ErrContractConflict, contractPath+".unitRef", "artifact selected for target %q belongs to unselected render unit %q", generationTarget, unitRef)
		}
		selectedContracts = append(selectedContracts, contract)
		selectedArtifactIDs[artifactID] = struct{}{}
	}
	if explicitArtifactIDs != nil && len(selectedArtifactIDs) != len(explicitArtifactIDs) {
		return fail(ErrContractConflict, path+".artifacts.contracts", "render variant artifactRefs do not resolve exactly for target %q", generationTarget)
	}
	artifacts["contracts"] = selectedContracts
	if err := retainStringRefs(artifacts, path+".artifacts", "requiredRefs", selectedArtifactIDs); err != nil {
		return err
	}
	bindings, err := objectListField(artifacts, path+".artifacts", "outputBindings")
	if err != nil {
		return err
	}
	selectedBindings := make([]any, 0, len(bindings))
	for index, binding := range bindings {
		bindingPath := fmt.Sprintf("%s.artifacts.outputBindings[%d]", path, index)
		artifactRef, err := stringField(binding, bindingPath, "artifactRef")
		if err != nil {
			return err
		}
		unitRef, err := stringField(binding, bindingPath, "unitRef")
		if err != nil {
			return err
		}
		_, artifactSelected := selectedArtifactIDs[artifactRef]
		_, unitSelected := selectedUnitIDs[unitRef]
		if artifactSelected != unitSelected {
			return fail(ErrContractConflict, bindingPath, "artifact and render-unit target variants do not close over target %q", generationTarget)
		}
		if artifactSelected {
			selectedBindings = append(selectedBindings, binding)
		}
	}
	artifacts["outputBindings"] = selectedBindings
	return nil
}

func validateVariantInputRefs(moduleID string, variant map[string]any, declared moduleDeclaredInputs) error {
	path := "catalog.modules." + moduleID + ".renderVariant"
	for _, field := range []struct {
		name string
		set  map[string]struct{}
	}{{"publicInputRefs", declared.public}, {"secretInputRefs", declared.secret}, {"planInputRefs", declared.plan}} {
		refs, err := stringListField(variant, path, field.name, false)
		if err != nil {
			return err
		}
		refsSet, err := uniqueStringSet(refs, path+"."+field.name)
		if err != nil {
			return err
		}
		if len(refsSet) != len(field.set) {
			return fail(ErrContractConflict, path+"."+field.name, "variant input projection is not exact")
		}
		for ref := range refsSet {
			if _, exists := field.set[ref]; !exists {
				return fail(ErrContractConflict, path+"."+field.name, "variant input projection contains undeclared ref %q", ref)
			}
		}
	}
	return nil
}

func selectSupportRequiredRefs(container map[string]any, path string, allowedSets ...map[string]struct{}) error {
	allowed := map[string]struct{}{}
	for _, values := range allowedSets {
		for value := range values {
			allowed[value] = struct{}{}
		}
	}
	return retainStringRefs(container, path, "requiredRefs", allowed)
}

func retainStringRefs(container map[string]any, path, field string, allowed map[string]struct{}) error {
	refs, err := stringListField(container, path, field, false)
	if err != nil {
		return err
	}
	selected := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, exists := allowed[ref]; exists {
			selected = append(selected, ref)
		}
	}
	container[field] = stringSliceAny(selected)
	return nil
}

func resolveModuleRenderUnits(moduleID string, contract map[string]any, rawIntent any, capabilitySecretRefs map[string]any, placement moduleRenderPlacementContext, generationTarget string) ([]any, map[string]struct{}, moduleDeclaredInputs, map[string]any, error) {
	units, err := objectListField(contract, "catalog.modules."+moduleID, "renderUnits")
	if err != nil {
		return nil, nil, moduleDeclaredInputs{}, nil, err
	}
	units, selectedUnitIDs, resolvedVariant, err := selectExplicitModuleRenderVariant(moduleID, contract, units, generationTarget)
	if err != nil {
		return nil, nil, moduleDeclaredInputs{}, nil, err
	}
	defaults, err := resolveModuleInputDefaults(moduleID, contract)
	if err != nil {
		return nil, nil, moduleDeclaredInputs{}, nil, err
	}
	intent, err := resolveModuleRenderIntent(moduleID, rawIntent)
	if err != nil {
		return nil, nil, moduleDeclaredInputs{}, nil, err
	}
	indexed, declared, err := indexModuleRenderUnits(moduleID, units)
	if err != nil {
		return nil, nil, moduleDeclaredInputs{}, nil, err
	}
	defaults = selectModuleDefaults(defaults, declared)
	if err := validateModuleRenderInputs(moduleID, defaults, intent, declared); err != nil {
		return nil, nil, moduleDeclaredInputs{}, nil, err
	}
	for key, value := range capabilitySecretRefs {
		if _, overridden := intent.secretRefs[key]; overridden {
			return nil, nil, moduleDeclaredInputs{}, nil, fail(ErrUnrealizedModule, "spec.modules."+moduleID+".secretRefs."+key, "compiler-bound capability secret cannot be overridden")
		}
		intent.secretRefs[key] = value
	}
	resolved, err := lowerModuleRenderUnits(moduleID, indexed, mergeModuleValues(defaults, intent.settings), intent.secretRefs, placement)
	return resolved, selectedUnitIDs, declared, resolvedVariant, err
}

func resolveModuleInputDefaults(moduleID string, contract map[string]any) (map[string]any, error) {
	defaults, exists, err := optionalObjectField(contract, "catalog.modules."+moduleID, "inputDefaults")
	if err != nil || !exists {
		return map[string]any{}, err
	}
	return cloneObject(defaults, false)
}

func resolveModuleRenderIntent(moduleID string, rawIntent any) (moduleRenderIntent, error) {
	result := moduleRenderIntent{settings: map[string]any{}, secretRefs: map[string]any{}}
	if rawIntent == nil {
		return result, nil
	}
	intentPath := "spec.modules." + moduleID
	intent, err := asObject(rawIntent, intentPath)
	if err != nil {
		return result, err
	}
	if _, exists := intent["runtimeProfile"]; exists {
		return result, fail(ErrUnrealizedModule, intentPath+".runtimeProfile", "runtime profiles require a governed profile contract")
	}
	if result.settings, err = cloneOptionalModuleIntentObject(intent, intentPath, "settings"); err != nil {
		return result, err
	}
	result.secretRefs, err = cloneOptionalModuleIntentObject(intent, intentPath, "secretRefs")
	return result, err
}

func cloneOptionalModuleIntentObject(intent map[string]any, intentPath, field string) (map[string]any, error) {
	value, exists, err := optionalObjectField(intent, intentPath, field)
	if err != nil || !exists {
		return map[string]any{}, err
	}
	return cloneObject(value, false)
}

func indexModuleRenderUnits(moduleID string, units []map[string]any) ([]indexedModuleRenderUnit, moduleDeclaredInputs, error) {
	indexed := make([]indexedModuleRenderUnit, 0, len(units))
	declared := moduleDeclaredInputs{public: map[string]struct{}{}, secret: map[string]struct{}{}, plan: map[string]struct{}{}, bound: map[string]struct{}{}, secretBound: map[string]struct{}{}}
	seen := make(map[string]struct{}, len(units))
	for index, unit := range units {
		unitPath := fmt.Sprintf("catalog.modules.%s.renderUnits[%d]", moduleID, index)
		unitID, err := stringField(unit, unitPath, "id")
		if err != nil {
			return nil, declared, err
		}
		if _, duplicate := seen[unitID]; duplicate {
			return nil, declared, fail(ErrContractConflict, unitPath+".id", "render unit %q is duplicated", unitID)
		}
		seen[unitID] = struct{}{}
		if err := collectModuleUnitInputs(unit, unitPath, declared); err != nil {
			return nil, declared, err
		}
		indexed = append(indexed, indexedModuleRenderUnit{id: unitID, unit: unit})
	}
	return indexed, declared, nil
}

func collectModuleUnitInputs(unit map[string]any, unitPath string, declared moduleDeclaredInputs) error {
	publicRefs, err := stringListField(unit, unitPath, "publicInputRefs", false)
	if err != nil {
		return err
	}
	secretRefs, err := stringListField(unit, unitPath, "secretInputRefs", false)
	if err != nil {
		return err
	}
	planRefs, err := stringListField(unit, unitPath, "planInputRefs", false)
	if err != nil {
		return err
	}
	planRefs, err = validateModulePlanInputRefs(planRefs, unitPath+".planInputRefs")
	if err != nil {
		return err
	}
	bindings, err := moduleRenderInputBindings(unit, unitPath)
	if err != nil {
		return err
	}
	for _, inputRef := range publicRefs {
		declared.public[inputRef] = struct{}{}
	}
	for _, inputRef := range secretRefs {
		declared.secret[inputRef] = struct{}{}
	}
	for _, inputRef := range planRefs {
		declared.plan[inputRef] = struct{}{}
	}
	for _, binding := range bindings {
		declared.bound[binding.targetRef] = struct{}{}
	}
	secretBindings, err := moduleRenderSecretInputBindings(unit, unitPath)
	if err != nil {
		return err
	}
	for targetRef := range secretBindings {
		declared.secretBound[targetRef] = struct{}{}
	}
	return nil
}

func validateModuleRenderInputs(moduleID string, defaults map[string]any, intent moduleRenderIntent, declared moduleDeclaredInputs) error {
	for inputRef := range declared.public {
		if _, conflict := declared.secret[inputRef]; conflict {
			return fail(ErrContractConflict, "catalog.modules."+moduleID+".renderUnits", "input %q is declared as both public and secret", inputRef)
		}
		if _, conflict := declared.plan[inputRef]; conflict {
			return fail(ErrContractConflict, "catalog.modules."+moduleID+".renderUnits", "input %q is declared as both public and compiler-owned plan input", inputRef)
		}
	}
	for inputRef := range declared.secret {
		if _, conflict := declared.plan[inputRef]; conflict {
			return fail(ErrContractConflict, "catalog.modules."+moduleID+".renderUnits", "input %q is declared as both secret and compiler-owned plan input", inputRef)
		}
	}
	if err := validateModulePublicValues("catalog.modules."+moduleID+".inputDefaults", defaults, declared, ErrContractConflict); err != nil {
		return err
	}
	if err := validateModulePublicValues("spec.modules."+moduleID+".settings", intent.settings, declared, ErrUnrealizedModule); err != nil {
		return err
	}
	for key := range intent.secretRefs {
		if _, bound := declared.secretBound[key]; bound {
			return fail(ErrUnrealizedModule, "spec.modules."+moduleID+".secretRefs."+key, "compiler-bound capability secret cannot be set by module intent")
		}
		if _, exists := declared.secret[key]; !exists {
			return fail(ErrUnrealizedModule, "spec.modules."+moduleID+".secretRefs."+key, "secret input is not declared by any render unit")
		}
	}
	return nil
}

func validateModulePublicValues(valuePath string, values map[string]any, declared moduleDeclaredInputs, code ErrorCode) error {
	for key := range values {
		if _, bound := declared.bound[key]; bound {
			return fail(code, valuePath+"."+key, "compiler-bound public input cannot be set by module defaults or StackSpec settings")
		}
		if _, secret := declared.secret[key]; secret {
			return fail(code, valuePath+"."+key, "secret input must use spec.modules secretRefs")
		}
		if _, exists := declared.public[key]; !exists {
			return fail(code, valuePath+"."+key, "input is not declared as public by any render unit")
		}
	}
	return nil
}

func mergeModuleValues(defaults, settings map[string]any) map[string]any {
	for key, value := range settings {
		defaults[key] = value
	}
	return defaults
}

func lowerModuleRenderUnits(moduleID string, indexed []indexedModuleRenderUnit, values, secretRefs map[string]any, placement moduleRenderPlacementContext) ([]any, error) {
	sort.Slice(indexed, func(i, j int) bool { return indexed[i].id < indexed[j].id })
	resolved := make([]any, 0, len(indexed))
	for _, indexedUnit := range indexed {
		unit, err := resolveModuleRenderUnit(moduleID, indexedUnit.unit, values, secretRefs, placement)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, unit)
	}
	return resolved, nil
}

func resolveModuleRenderUnit(moduleID string, contract, moduleValues, moduleSecretRefs map[string]any, placement moduleRenderPlacementContext) (map[string]any, error) {
	unitID, err := stringField(contract, "catalog.modules."+moduleID+".renderUnits", "id")
	if err != nil {
		return nil, err
	}
	unitPath := "catalog.modules." + moduleID + ".renderUnits." + unitID
	if _, legacy := contract["defaultValues"]; legacy {
		return nil, fail(ErrContractConflict, unitPath+".defaultValues", "per-unit defaults are forbidden; use module inputDefaults")
	}
	resolved, err := cloneObject(contract, true)
	if err != nil {
		return nil, err
	}
	publicInputRefs, err := stringListField(contract, unitPath, "publicInputRefs", false)
	if err != nil {
		return nil, err
	}
	secretInputRefs, err := stringListField(contract, unitPath, "secretInputRefs", false)
	if err != nil {
		return nil, err
	}
	planInputRefs, err := stringListField(contract, unitPath, "planInputRefs", false)
	if err != nil {
		return nil, err
	}
	outputs, err := stringListField(contract, unitPath, "outputs", true)
	if err != nil {
		return nil, err
	}
	publicInputRefs = sortStringsUnique(publicInputRefs)
	secretInputRefs = sortStringsUnique(secretInputRefs)
	planInputRefs, err = validateModulePlanInputRefs(planInputRefs, unitPath+".planInputRefs")
	if err != nil {
		return nil, err
	}
	inputBindings, err := moduleRenderInputBindings(contract, unitPath)
	if err != nil {
		return nil, err
	}
	outputs = sortStringsUnique(outputs)
	resolved["publicInputRefs"] = stringSliceAny(publicInputRefs)
	resolved["secretInputRefs"] = stringSliceAny(secretInputRefs)
	resolved["planInputRefs"] = stringSliceAny(planInputRefs)
	resolved["inputBindings"] = moduleRenderInputBindingsAny(inputBindings)
	// The compiler attaches this closed projection only after bridge, identity,
	// data, and topology resolution. It is never populated from module intent.
	resolved["planInputs"] = map[string]any{}
	resolved["outputs"] = stringSliceAny(outputs)

	values := map[string]any{}
	for _, inputRef := range publicInputRefs {
		if value, exists := moduleValues[inputRef]; exists {
			values[inputRef] = value
		}
	}
	secretRefs := map[string]any{}
	for _, inputRef := range secretInputRefs {
		value, exists := moduleSecretRefs[inputRef]
		if !exists {
			return nil, fail(ErrUnrealizedModule, "spec.modules."+moduleID+".secretRefs", "required secret reference %q for render unit %q is missing", inputRef, unitID)
		}
		secretRefs[inputRef] = value
	}
	resolved["values"] = values
	resolved["secretRefs"] = secretRefs
	resolvedPlacement, siteRefs, nodeRefs, daemonBindings, err := resolveModuleRenderUnitPlacement(moduleID, unitID, contract, placement)
	if err != nil {
		return nil, err
	}
	resolved["placement"] = resolvedPlacement
	resolved["siteRefs"] = stringSliceAny(siteRefs)
	resolved["nodeRefs"] = stringSliceAny(nodeRefs)
	resolved["daemonBindings"] = daemonBindings
	instances, err := resolveModuleRenderUnitInstances(moduleID, unitID, resolvedPlacement, placement, daemonBindings)
	if err != nil {
		return nil, err
	}
	resolved["instances"] = instances
	if err := normalizeResolvedUnitInterfaces(resolved, unitPath); err != nil {
		return nil, err
	}
	return resolved, nil
}

func detectModuleCycles(selected map[string]string, catalog *indexedCatalog) error {
	state := make(map[string]uint8, len(selected))
	var visit func(string, []string) error
	visit = func(id string, stack []string) error {
		switch state[id] {
		case 1:
			return fail(ErrContractConflict, "catalog.modules."+id+".requires", "module dependency cycle: %s", fmt.Sprint(append(stack, id)))
		case 2:
			return nil
		}
		state[id] = 1
		requires, err := stringListField(catalog.modules[id], "catalog.modules."+id, "requires", false)
		if err != nil {
			return err
		}
		for _, dependency := range sortStringsUnique(requires) {
			if err := visit(dependency, append(stack, id)); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, id := range sortedStringMapKeys(selected) {
		if err := visit(id, nil); err != nil {
			return err
		}
	}
	return nil
}

func sortedStringMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func anyStringListContains(value any, wanted string) bool {
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
