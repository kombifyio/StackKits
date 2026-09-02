package resolvedplan

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

type moduleResourceBudget struct {
	cpu, ram, storage          float64
	hasCPU, hasRAM, hasStorage bool
}

// moduleProfileSelection is the compiler-owned binding between one selected
// module and one catalog profile. The catalog body is retained so the exact
// bytes that drove admission and resource aggregation are visible in the
// ResolvedPlan.
type moduleProfileSelection struct {
	tier                   string
	hash                   string
	source                 string
	profile                map[string]any
	runtimeRequirements    map[string]any
	storageProfile         string
	storageProfileHash     string
	storageBinding         map[string]any
	acceleratorProfile     string
	acceleratorProfileHash string
	acceleratorBinding     map[string]any
}

// validateCatalogModuleProfileComponents closes the catalog-to-renderer seam.
// A compute profile may project the module's exact fixed component closure, but
// it cannot describe a different graph because profile selection does not
// rewrite runtime.components. Orthogonal profiles may name only components
// already present in that closure.
func validateCatalogModuleProfileComponents(catalog Catalog) error {
	for index, module := range catalog.Modules {
		moduleID, err := metadataID(module, fmt.Sprintf("catalog.modules[%d]", index))
		if err != nil {
			return err
		}
		runtime, err := objectField(module, "catalog.modules."+moduleID, "runtime")
		if err != nil {
			return err
		}
		components, err := objectListOptional(runtime, "components")
		if err != nil {
			return fail(ErrContractConflict, "catalog.modules."+moduleID+".runtime.components", "%v", err)
		}
		materialized := make([]string, 0, len(components))
		for componentIndex, component := range components {
			id, err := stringField(component, fmt.Sprintf("catalog.modules.%s.runtime.components[%d]", moduleID, componentIndex), "id")
			if err != nil {
				return err
			}
			materialized = append(materialized, id)
		}
		materialized = sortStringsUnique(materialized)
		for _, dimension := range []struct {
			field string
			exact bool
		}{
			{"computeProfiles", true},
			{"storageProfiles", false},
			{"acceleratorProfiles", false},
		} {
			profiles, declared, err := optionalObjectField(module, "catalog.modules."+moduleID, dimension.field)
			if err != nil {
				return err
			}
			if !declared {
				continue
			}
			for _, profileID := range sortedStringMapKeys(profiles) {
				profile, err := asObject(profiles[profileID], "catalog.modules."+moduleID+"."+dimension.field+"."+profileID)
				if err != nil {
					return err
				}
				refs, err := stringListField(profile, "catalog.modules."+moduleID+"."+dimension.field+"."+profileID, "components", false)
				if err != nil {
					return err
				}
				if len(refs) == 0 {
					continue
				}
				refs = sortStringsUnique(refs)
				if dimension.exact {
					if !equalStringSlices(refs, materialized) {
						return fail(ErrContractConflict, "catalog.modules."+moduleID+"."+dimension.field+"."+profileID+".components", "compute profile components must exactly match the module runtime component closure")
					}
					continue
				}
				for _, ref := range refs {
					if !containsString(materialized, ref) {
						return fail(ErrContractConflict, "catalog.modules."+moduleID+"."+dimension.field+"."+profileID+".components", "orthogonal profile component %q is not materialized by the module", ref)
					}
				}
			}
		}
	}
	return nil
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	index := sort.SearchStrings(values, want)
	return index < len(values) && values[index] == want
}

// resolveModuleProfiles is the only compute-profile selection seam. Native
// v2alpha2 requires a per-module selector and a declared catalog profile;
// v2alpha1 is handled once here by the explicit legacy adapter.
func (c *Compiler) resolveModuleProfiles(spec *specView, selected map[string]string) (map[string]moduleProfileSelection, error) {
	result := make(map[string]moduleProfileSelection, len(selected))
	legacyTier := ""
	if spec.legacyComputeTier {
		var err error
		legacyTier, err = computeTierFromInstall(spec.install)
		if err != nil {
			return nil, err
		}
	}
	for _, moduleID := range sortedStringMapKeys(selected) {
		contract, exists := c.catalog.modules[moduleID]
		if !exists {
			return nil, fail(ErrUnknownModule, "catalog.modules."+moduleID, "selected module contract is not governed")
		}
		intent, hasIntent, err := optionalObjectField(spec.modules, "spec.modules", moduleID)
		if err != nil {
			return nil, err
		}
		if !hasIntent {
			intent = nil
		}
		selection := moduleProfileSelection{source: "catalog"}
		if spec.legacyComputeTier {
			selection.source = "legacy-adapter"
		}
		profiles, hasProfiles, err := optionalObjectField(contract, "catalog.modules."+moduleID, "computeProfiles")
		if err != nil {
			return nil, err
		}
		explicitProfile := ""
		if intent != nil {
			explicitProfile, _, err = optionalStringField(intent, "spec.modules."+moduleID, "computeProfile")
			if err != nil {
				return nil, err
			}
		}
		if !hasProfiles {
			if explicitProfile != "" {
				return nil, fail(ErrUndeclaredComputeProfile, "spec.modules."+moduleID+".computeProfile", "module has no compute-profile dimension")
			}
			if err := c.resolveModuleAxisProfiles(spec, moduleID, contract, intent, &selection); err != nil {
				return nil, err
			}
			if requirements, declared, err := optionalObjectField(contract, "catalog.modules."+moduleID, "runtimeRequirements"); err != nil {
				return nil, err
			} else if declared {
				selection.runtimeRequirements, err = cloneObject(requirements, true)
				if err != nil {
					return nil, err
				}
			}
			result[moduleID] = selection
			continue
		}
		if spec.legacyComputeTier {
			selection.tier = legacyTier
			if intent != nil {
				if explicit, declared, err := optionalStringField(intent, "spec.modules."+moduleID, "computeProfile"); err != nil {
					return nil, err
				} else if declared && explicit != legacyTier {
					return nil, fail(ErrInvalidInput, "spec.modules."+moduleID+".computeProfile", "legacy adapter selects %q for every module; explicit profile %q conflicts", legacyTier, explicit)
				}
			}
		} else {
			if intent == nil {
				return nil, fail(ErrUndeclaredComputeProfile, "spec.modules."+moduleID+".computeProfile", "native v2alpha2 requires an explicit computeProfile for every selected module")
			}
			if explicitProfile == "" {
				return nil, fail(ErrUndeclaredComputeProfile, "spec.modules."+moduleID+".computeProfile", "native v2alpha2 requires an explicit computeProfile for every selected module")
			}
			selection.tier = explicitProfile
		}

		profileValue, exists := profiles[selection.tier]
		if !exists {
			return nil, fail(ErrUndeclaredComputeProfile, "catalog.modules."+moduleID+".computeProfiles."+selection.tier, "module does not declare compute profile %q", selection.tier)
		}
		profile, err := asObject(profileValue, "catalog.modules."+moduleID+".computeProfiles."+selection.tier)
		if err != nil {
			return nil, err
		}
		selection.profile, err = cloneObject(profile, true)
		if err != nil {
			return nil, err
		}
		selection.hash, err = moduleProfileHash(moduleID, selection.profile)
		if err != nil {
			return nil, err
		}
		selection.profile["profileHash"] = selection.hash
		if !spec.legacyComputeTier {
			if err := requireExecutableModuleProfile(selection.profile, "catalog.modules."+moduleID+".computeProfiles."+selection.tier, true); err != nil {
				return nil, err
			}
		}
		if management, declared, err := optionalStringField(selection.profile, "catalog.modules."+moduleID+".computeProfiles."+selection.tier, "platformManagement"); err != nil {
			return nil, err
		} else if declared && !spec.legacyComputeTier {
			platform, err := objectField(spec.install, "spec.install", "platform")
			if err != nil {
				return nil, err
			}
			if platform["management"] != management {
				return nil, fail(ErrContractConflict, "spec.install.platform.management", "selected module %q profile %q requires platform management %q", moduleID, selection.tier, management)
			}
		}
		selection.runtimeRequirements, err = runtimeRequirementsForProfile(moduleID, contract, selection.profile)
		if err != nil {
			return nil, err
		}
		if err := c.resolveModuleAxisProfiles(spec, moduleID, contract, intent, &selection); err != nil {
			return nil, err
		}
		result[moduleID] = selection
	}
	return result, nil
}

func requireExecutableModuleProfile(profile map[string]any, path string, compute bool) error {
	if profile["realization"] != "apply-ready" || (compute && profile["executable"] != true) {
		return fail(ErrUnrealizedModule, path, "selected profile has no executable apply-ready realization")
	}
	return nil
}

func moduleProfileHash(moduleID string, profile map[string]any) (string, error) {
	withoutHash, err := cloneObject(profile, true)
	if err != nil {
		return "", err
	}
	delete(withoutHash, "profileHash")
	hash, err := canonicalHash(withoutHash, true)
	if err != nil {
		return "", fmt.Errorf("hash compute profile %s: %w", moduleID, err)
	}
	if declared, exists := profile["profileHash"]; exists {
		value, ok := declared.(string)
		if !ok || value != hash {
			return "", fail(ErrContractConflict, "catalog.modules."+moduleID+".computeProfiles.profileHash", "declared profile hash %v does not match canonical profile hash %s", declared, hash)
		}
	}
	return hash, nil
}

func runtimeRequirementsForProfile(moduleID string, contract, profile map[string]any) (map[string]any, error) {
	requirements := map[string]any{}
	if value, exists, err := optionalObjectField(contract, "catalog.modules."+moduleID, "runtimeRequirements"); err != nil {
		return nil, err
	} else if exists {
		requirements, err = cloneObject(value, true)
		if err != nil {
			return nil, err
		}
	}
	path := "catalog.modules." + moduleID + ".computeProfile"
	if floor, exists, err := optionalObjectField(profile, path, "hostFloor"); err != nil {
		return nil, err
	} else if exists {
		for field, value := range floor {
			switch field {
			case "allowedArchitectures", "allowedVirtualization", "requireInventoryFacts":
				values, err := stringListField(floor, path+".hostFloor", field, true)
				if err != nil {
					return nil, err
				}
				if err := constrainProfileRuntimeFacts(requirements, field, values, path); err != nil {
					return nil, err
				}
			default:
				// A profile owns its capacity floor, but cannot erase the
				// module's architecture, virtualization or attestation rules.
				requirements[field] = value
			}
		}
	}
	for _, axis := range []struct {
		profileField, requirementField string
	}{
		{"architectures", "allowedArchitectures"},
		{"virtualization", "allowedVirtualization"},
	} {
		values, err := stringListField(profile, "catalog.modules."+moduleID+".computeProfile", axis.profileField, false)
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			continue
		}
		if err := constrainProfileRuntimeFacts(requirements, axis.requirementField, values, path); err != nil {
			return nil, err
		}
	}
	if len(requirements) == 0 {
		return nil, nil
	}
	return requirements, nil
}

func constrainProfileRuntimeFacts(requirements map[string]any, field string, values []string, path string) error {
	if existing, exists := requirements[field]; exists {
		existingValues, ok := anyStringSlice(existing)
		if !ok {
			return fail(ErrContractConflict, path+"."+field, "module runtime constraint is not a string list")
		}
		if field == "requireInventoryFacts" {
			values = sortStringsUnique(append(existingValues, values...))
		} else {
			values = intersectStrings(existingValues, values)
			if len(values) == 0 {
				return fail(ErrContractConflict, path+"."+field, "profile and module runtime constraints have no common value")
			}
		}
	}
	requirements[field] = stringSliceAny(values)
	return nil
}

// The selected profile refines the existing target selector. Both compilation
// and persisted-plan validation use this projection, never a second placer.
func moduleContractWithRuntimeRequirements(contract, requirements map[string]any) map[string]any {
	if requirements == nil {
		return contract
	}
	projected := make(map[string]any, len(contract))
	for field, value := range contract {
		projected[field] = value
	}
	projected["runtimeRequirements"] = requirements
	return projected
}

func (c *Compiler) resolveModuleAxisProfiles(spec *specView, moduleID string, contract, intent map[string]any, selection *moduleProfileSelection) error {
	for _, axis := range []struct {
		intentField, contractField string
		profileID, profileHash     *string
		binding                    *map[string]any
	}{
		{"storageProfile", "storageProfiles", &selection.storageProfile, &selection.storageProfileHash, &selection.storageBinding},
		{"acceleratorProfile", "acceleratorProfiles", &selection.acceleratorProfile, &selection.acceleratorProfileHash, &selection.acceleratorBinding},
	} {
		profiles, declared, err := optionalObjectField(contract, "catalog.modules."+moduleID, axis.contractField)
		if err != nil {
			return err
		}
		explicit := ""
		if intent != nil {
			explicit, _, err = optionalStringField(intent, "spec.modules."+moduleID, axis.intentField)
			if err != nil {
				return err
			}
		}
		if !declared {
			if explicit != "" {
				return fail(ErrUndeclaredComputeProfile, "spec.modules."+moduleID+"."+axis.intentField, "module has no %s dimension", axis.contractField)
			}
			continue
		}
		if explicit == "" {
			if spec.legacyComputeTier {
				continue
			}
			return fail(ErrUndeclaredComputeProfile, "spec.modules."+moduleID+"."+axis.intentField, "native v2alpha2 requires an explicit %s for modules that declare %s", axis.intentField, axis.contractField)
		}
		value, exists := profiles[explicit]
		if !exists {
			return fail(ErrUndeclaredComputeProfile, "catalog.modules."+moduleID+"."+axis.contractField+"."+explicit, "module does not declare profile %q", explicit)
		}
		profile, err := asObject(value, "catalog.modules."+moduleID+"."+axis.contractField+"."+explicit)
		if err != nil {
			return err
		}
		cloned, err := cloneObject(profile, true)
		if err != nil {
			return err
		}
		if !spec.legacyComputeTier {
			if err := requireExecutableModuleProfile(cloned, "catalog.modules."+moduleID+"."+axis.contractField+"."+explicit, false); err != nil {
				return err
			}
		}
		hash, err := moduleProfileHash(moduleID+"."+axis.contractField+"."+explicit, cloned)
		if err != nil {
			return err
		}
		cloned["profileHash"] = hash
		*axis.profileID, *axis.profileHash, *axis.binding = explicit, hash, cloned
	}
	return nil
}

func anyStringSlice(value any) ([]string, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, len(values))
	for i, item := range values {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result[i] = text
	}
	return result, true
}

// aggregateModuleResourceDemand computes only declared resource axes. Host
// floors are maxima because every module must fit; reservations, recommended
// and headroom are additive because modules share a node's capacity.
func aggregateModuleResourceDemand(modules []any) ([]any, error) {
	type budget = moduleResourceBudget
	type demand struct {
		modules, unverified                      []string
		host, reservation, recommended, headroom budget
	}
	byNode := make(map[string]*demand)
	for _, raw := range modules {
		module, err := asObject(raw, "resolvedPlan.modules")
		if err != nil {
			return nil, err
		}
		moduleID, err := stringField(module, "resolvedPlan.modules", "id")
		if err != nil {
			return nil, err
		}
		binding, exists, err := optionalObjectField(module, "resolvedPlan.modules."+moduleID, "computeProfileBinding")
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		nodeRefs, err := stringListField(module, "resolvedPlan.modules."+moduleID, "nodeRefs", true)
		if err != nil {
			return nil, err
		}
		host, err := profileBudget(binding, "hostFloor", true)
		if err != nil {
			return nil, err
		}
		reservation, err := profileBudget(binding, "reservation", false)
		if err != nil {
			return nil, err
		}
		recommended, err := profileBudget(binding, "recommended", false)
		if err != nil {
			return nil, err
		}
		headroom, err := profileBudget(binding, "headroom", false)
		if err != nil {
			return nil, err
		}
		recommended = maxBudget(recommended, reservation)
		minimum := maxBudget(host, reservation)
		complete := minimum.hasCPU && minimum.hasRAM && minimum.hasStorage
		for _, axisField := range []string{"storageProfileBinding", "acceleratorProfileBinding"} {
			axis, declared, err := optionalObjectField(module, "resolvedPlan.modules."+moduleID, axisField)
			if err != nil {
				return nil, err
			}
			if !declared {
				continue
			}
			axisReservation, err := profileBudget(axis, "reservation", false)
			if err != nil {
				return nil, err
			}
			reservation = addBudget(reservation, axisReservation)
			recommended = addBudget(recommended, axisReservation)
			// A missing axis is unknown, never an implicit zero. Orthogonal
			// profiles therefore need a complete declared vector before the
			// module can leave the unverified set.
			complete = complete && axisReservation.hasCPU && axisReservation.hasRAM && axisReservation.hasStorage
		}
		for _, nodeRef := range nodeRefs {
			entry := byNode[nodeRef]
			if entry == nil {
				entry = &demand{}
				byNode[nodeRef] = entry
			}
			entry.modules = append(entry.modules, moduleID)
			if !complete {
				entry.unverified = append(entry.unverified, moduleID)
			}
			entry.host = maxBudget(entry.host, host)
			entry.reservation = addBudget(entry.reservation, reservation)
			entry.recommended = addBudget(entry.recommended, recommended)
			entry.headroom = addBudget(entry.headroom, headroom)
		}
	}
	nodes := make([]string, 0, len(byNode))
	for nodeRef := range byNode {
		nodes = append(nodes, nodeRef)
	}
	sort.Strings(nodes)
	result := make([]any, 0, len(nodes))
	for _, nodeRef := range nodes {
		entry := byNode[nodeRef]
		minimum := maxBudget(entry.host, entry.reservation)
		entry.recommended = maxBudget(minimum, addBudget(entry.recommended, entry.headroom))
		moduleRefs := sortStringsUnique(entry.modules)
		value := map[string]any{
			"nodeRef": nodeRef, "moduleRefs": stringSliceAny(moduleRefs),
			"unverifiedModuleRefs": stringSliceAny(sortStringsUnique(entry.unverified)),
		}
		for _, axis := range []struct {
			name  string
			value budget
		}{
			{"hostFloor", entry.host}, {"reservation", entry.reservation},
			{"recommended", entry.recommended}, {"headroom", entry.headroom},
		} {
			if encoded := encodeBudget(axis.value); encoded != nil {
				value[axis.name] = encoded
			}
		}
		result = append(result, value)
	}
	return result, nil
}

func profileBudget(profile map[string]any, field string, hostFloor bool) (moduleResourceBudget, error) {
	type budget = moduleResourceBudget
	value, exists, err := optionalObjectField(profile, "resolvedPlan.computeProfile", field)
	if err != nil || !exists {
		return budget{}, err
	}
	result := budget{}
	for _, axis := range []struct {
		name string
		dest *float64
		set  *bool
	}{
		{"cpuCores", &result.cpu, &result.hasCPU}, {"ramGB", &result.ram, &result.hasRAM}, {"storageGB", &result.storage, &result.hasStorage},
	} {
		if _, exists := value[axis.name]; exists {
			parsed, err := resourceNumberField(value, "resolvedPlan.computeProfile."+field, axis.name)
			if err != nil {
				return budget{}, err
			}
			*axis.dest, *axis.set = parsed, true
		} else if hostFloor {
			legacyName := map[string]string{"cpuCores": "minCpuCores", "ramGB": "minRamGB", "storageGB": "minStorageGB"}[axis.name]
			if legacyName != "" {
				if _, exists := value[legacyName]; exists {
					parsed, err := resourceNumberField(value, "resolvedPlan.computeProfile."+field, legacyName)
					if err != nil {
						return budget{}, err
					}
					*axis.dest, *axis.set = parsed, true
				}
			}
		}
	}
	return result, nil
}

func maxBudget(left, right moduleResourceBudget) moduleResourceBudget {
	result := left
	if right.hasCPU && (!result.hasCPU || right.cpu > result.cpu) {
		result.cpu, result.hasCPU = right.cpu, true
	}
	if right.hasRAM && (!result.hasRAM || right.ram > result.ram) {
		result.ram, result.hasRAM = right.ram, true
	}
	if right.hasStorage && (!result.hasStorage || right.storage > result.storage) {
		result.storage, result.hasStorage = right.storage, true
	}
	return result
}

func addBudget(left, right moduleResourceBudget) moduleResourceBudget {
	result := left
	if right.hasCPU {
		result.cpu += right.cpu
		result.hasCPU = true
	}
	if right.hasRAM {
		result.ram += right.ram
		result.hasRAM = true
	}
	if right.hasStorage {
		result.storage += right.storage
		result.hasStorage = true
	}
	return result
}

func encodeBudget(value moduleResourceBudget) map[string]any {
	result := make(map[string]any)
	if value.hasCPU {
		result["cpuCores"] = value.cpu
	}
	if value.hasRAM {
		result["ramGB"] = value.ram
	}
	if value.hasStorage {
		result["storageGB"] = value.storage
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func resourceNumberField(object map[string]any, path, name string) (float64, error) {
	var value float64
	switch number := object[name].(type) {
	case json.Number:
		parsed, err := number.Float64()
		if err != nil {
			return 0, fail(ErrInvalidInput, joinPath(path, name), "invalid resource number")
		}
		value = parsed
	case float64:
		value = number
	case int:
		value = float64(number)
	default:
		return 0, fail(ErrInvalidInput, joinPath(path, name), "expected resource number")
	}
	if math.IsInf(value, 0) || math.IsNaN(value) || value <= 0 {
		return 0, fail(ErrInvalidInput, joinPath(path, name), "resource number must be finite and positive")
	}
	return value, nil
}

// applyModuleResourceAdmission extends the existing inventory admission seam
// with all colocated modules' reserved demand. It changes no profile, placement
// or inventory fact. Unknown declarations cannot authorize Apply; generation
// remains available for review.
func applyModuleResourceAdmission(modules, demands []any, nodes []nodeView) error {
	moduleStatus := map[string]string{}
	for _, raw := range demands {
		demand, err := asObject(raw, "resolvedPlan.resourceDemand")
		if err != nil {
			return err
		}
		nodeRef, err := stringField(demand, "resolvedPlan.resourceDemand", "nodeRef")
		if err != nil {
			return err
		}
		floor, err := profileBudget(demand, "hostFloor", false)
		if err != nil {
			return err
		}
		reservation, err := profileBudget(demand, "reservation", false)
		if err != nil {
			return err
		}
		minimum := encodeBudget(maxBudget(floor, reservation))
		requirements := map[string]any{}
		for axis, field := range map[string]string{"cpuCores": "minCpuCores", "ramGB": "minRamGB", "storageGB": "minStorageGB"} {
			if value, declared := minimum[axis]; declared {
				// Inventory currently attests whole GiB/cores. Round only this
				// comparison threshold, never the persisted module budgets.
				requirements[field] = math.Ceil(value.(float64))
			}
		}
		admission, err := evaluateRuntimeAdmission(requirements, []string{nodeRef}, nodes)
		if err != nil {
			return err
		}
		status := admission["status"].(string)
		unverified, err := stringListField(demand, "resolvedPlan.resourceDemand", "unverifiedModuleRefs", true)
		if err != nil {
			return err
		}
		if len(unverified) > 0 {
			status = mergeRuntimeAdmissionStatus(status, "unverified")
		}
		refs, err := stringListField(demand, "resolvedPlan.resourceDemand", "moduleRefs", true)
		if err != nil {
			return err
		}
		for _, moduleRef := range refs {
			moduleStatus[moduleRef] = mergeRuntimeAdmissionStatus(moduleStatus[moduleRef], status)
		}
	}
	for _, raw := range modules {
		module, err := asObject(raw, "resolvedPlan.modules")
		if err != nil {
			return err
		}
		moduleID, err := stringField(module, "resolvedPlan.modules", "id")
		if err != nil {
			return err
		}
		status, selected := moduleStatus[moduleID]
		if !selected {
			continue
		}
		if existing, ok := module["runtimeAdmission"].(map[string]any); ok {
			status = mergeRuntimeAdmissionStatus(status, existing["status"].(string))
		}
		module["runtimeAdmission"] = map[string]any{"status": status}
	}
	return nil
}
