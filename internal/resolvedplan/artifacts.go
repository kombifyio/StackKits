package resolvedplan

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

var (
	generationArtifactIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	generationArtifactPathPattern = regexp.MustCompile(`^[A-Za-z0-9.][A-Za-z0-9._-]*(/[A-Za-z0-9._-]+)*$`)
	windowsReservedArtifactBase   = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)
)

type generationArtifactRecord struct {
	id       string
	kind     string
	path     string
	format   string
	mode     string
	required bool
	owner    map[string]any
}

type moduleArtifactContract struct {
	generationArtifactRecord
	unitRef   string
	outputRef string
}

type moduleArtifactBinding struct {
	unitRef   string
	outputRef string
}

type moduleRenderUnitOutputs struct {
	kind      string
	outputs   map[string]struct{}
	instances []moduleRenderInstance
}

type moduleRenderInstance struct {
	id      string
	scope   string
	outputs map[string]moduleRenderInstanceOutput
}

type moduleRenderInstanceOutput struct {
	ref         string
	artifactRef string
	path        string
}

// bindResolvedRenderInstanceOutputs projects immutable logical unit outputs
// and their governed artifact bindings onto exact executable instances. The
// logical catalog contracts remain unchanged; only this compiler-owned plan
// projection may create instance-scoped artifact identities and paths.
func bindResolvedRenderInstanceOutputs(moduleID string, rawUnits []any, support map[string]any) error {
	modulePath := "modules." + moduleID
	artifactByUnitOutput, err := indexResolvedArtifactBindings(modulePath, support)
	if err != nil {
		return err
	}

	usedBindings := make(map[string]struct{}, len(artifactByUnitOutput))
	concreteArtifactRefs := make(map[string]struct{})
	concretePaths := make(map[string]struct{})
	for unitIndex, rawUnit := range rawUnits {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", modulePath, unitIndex)
		if err := bindResolvedRenderUnitInstanceOutputs(
			moduleID, unitPath, rawUnit, artifactByUnitOutput,
			usedBindings, concreteArtifactRefs, concretePaths,
		); err != nil {
			return err
		}
	}
	if len(usedBindings) != len(artifactByUnitOutput) {
		return fail(ErrContractConflict, modulePath+".realizationSupport.artifacts.outputBindings", "artifact binding references no selected render-unit output")
	}
	return nil
}

func indexResolvedArtifactBindings(modulePath string, support map[string]any) (map[string]string, error) {
	artifacts, err := objectField(support, modulePath+".realizationSupport", "artifacts")
	if err != nil {
		return nil, err
	}
	rawBindings, err := objectListField(artifacts, modulePath+".realizationSupport.artifacts", "outputBindings")
	if err != nil {
		return nil, err
	}
	artifactByUnitOutput := make(map[string]string, len(rawBindings))
	for index, rawBinding := range rawBindings {
		bindingPath := fmt.Sprintf("%s.realizationSupport.artifacts.outputBindings[%d]", modulePath, index)
		artifactRef, err := stringField(rawBinding, bindingPath, "artifactRef")
		if err != nil {
			return nil, err
		}
		unitRef, err := stringField(rawBinding, bindingPath, "unitRef")
		if err != nil {
			return nil, err
		}
		outputRef, err := stringField(rawBinding, bindingPath, "outputRef")
		if err != nil {
			return nil, err
		}
		key := moduleUnitOutputKey(unitRef, outputRef)
		if _, duplicate := artifactByUnitOutput[key]; duplicate {
			return nil, fail(ErrContractConflict, bindingPath, "logical unit output %q/%q is bound more than once", unitRef, outputRef)
		}
		artifactByUnitOutput[key] = artifactRef
	}
	return artifactByUnitOutput, nil
}

func bindResolvedRenderUnitInstanceOutputs(
	moduleID, unitPath string,
	rawUnit any,
	artifactByUnitOutput map[string]string,
	usedBindings, concreteArtifactRefs, concretePaths map[string]struct{},
) error {
	unit, err := asObject(rawUnit, unitPath)
	if err != nil {
		return err
	}
	unitID, err := stringField(unit, unitPath, "id")
	if err != nil {
		return err
	}
	logicalOutputs, err := stringListField(unit, unitPath, "outputs", true)
	if err != nil {
		return err
	}
	placement, err := objectField(unit, unitPath, "placement")
	if err != nil {
		return err
	}
	placementScope, err := stringField(placement, unitPath+".placement", "scope")
	if err != nil {
		return err
	}
	instances, err := objectListField(unit, unitPath, "instances")
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return fail(ErrUnresolvedPlacement, unitPath+".instances", "render unit has no executable instances")
	}
	for instanceIndex, instance := range instances {
		instancePath := fmt.Sprintf("%s.instances[%d]", unitPath, instanceIndex)
		if err := bindResolvedInstanceOutputProjection(
			moduleID, unitID, unitPath, instancePath, placementScope,
			logicalOutputs, instances, instance, artifactByUnitOutput,
			usedBindings, concreteArtifactRefs, concretePaths,
		); err != nil {
			return err
		}
	}
	return nil
}

func bindResolvedInstanceOutputProjection(
	moduleID, unitID, unitPath, instancePath, placementScope string,
	logicalOutputs []string,
	instances []map[string]any,
	instance map[string]any,
	artifactByUnitOutput map[string]string,
	usedBindings, concreteArtifactRefs, concretePaths map[string]struct{},
) error {
	instanceID, err := stringField(instance, instancePath, "id")
	if err != nil {
		return err
	}
	instanceScope, err := stringField(instance, instancePath, "scope")
	if err != nil {
		return err
	}
	if err := validateResolvedInstanceProjectionScope(instanceID, instanceScope, placementScope, unitID, instancePath, len(instances)); err != nil {
		return err
	}
	resolvedOutputs, err := projectResolvedInstanceOutputs(
		moduleID, unitID, unitPath, instanceID, instanceScope, instancePath,
		logicalOutputs, artifactByUnitOutput, usedBindings, concreteArtifactRefs, concretePaths,
	)
	if err != nil {
		return err
	}
	instance["outputs"] = resolvedOutputs
	return nil
}

func validateResolvedInstanceProjectionScope(instanceID, instanceScope, placementScope, unitID, instancePath string, instanceCount int) error {
	if instanceScope != placementScope {
		return fail(ErrContractConflict, instancePath+".scope", "instance scope %q does not match placement scope %q", instanceScope, placementScope)
	}
	if instanceScope == "module" && (instanceCount != 1 || instanceID != unitID+"-logical") {
		return fail(ErrContractConflict, instancePath, "module-scoped unit must have exactly the canonical logical instance %q", unitID+"-logical")
	}
	if instanceScope != "module" && instanceScope != "node-local" {
		return fail(ErrContractConflict, instancePath+".scope", "unsupported render instance scope %q", instanceScope)
	}
	return nil
}

func projectResolvedInstanceOutputs(
	moduleID, unitID, unitPath, instanceID, instanceScope, instancePath string,
	logicalOutputs []string,
	artifactByUnitOutput map[string]string,
	usedBindings, concreteArtifactRefs, concretePaths map[string]struct{},
) ([]any, error) {
	resolvedOutputs := make([]any, 0, len(logicalOutputs))
	for _, logicalOutput := range logicalOutputs {
		bindingKey := moduleUnitOutputKey(unitID, logicalOutput)
		logicalArtifactRef, exists := artifactByUnitOutput[bindingKey]
		if !exists {
			return nil, fail(ErrContractConflict, unitPath+".outputs", "logical output %q has no governed artifact binding", logicalOutput)
		}
		usedBindings[bindingKey] = struct{}{}
		artifactRef, outputPath := projectResolvedInstanceOutput(moduleID, instanceID, instanceScope, logicalArtifactRef, logicalOutput)
		if err := claimResolvedInstanceOutput(instancePath, artifactRef, outputPath, concreteArtifactRefs, concretePaths); err != nil {
			return nil, err
		}
		resolvedOutputs = append(resolvedOutputs, map[string]any{
			"ref": logicalOutput, "artifactRef": artifactRef, "path": outputPath,
		})
	}
	return resolvedOutputs, nil
}

func projectResolvedInstanceOutput(moduleID, instanceID, instanceScope, logicalArtifactRef, logicalOutput string) (string, string) {
	if instanceScope == "node-local" {
		return logicalArtifactRef + "-instance-" + instanceID, path.Join("instances", moduleID, instanceID, logicalOutput)
	}
	return logicalArtifactRef, logicalOutput
}

func claimResolvedInstanceOutput(instancePath, artifactRef, outputPath string, concreteArtifactRefs, concretePaths map[string]struct{}) error {
	if !generationArtifactIDPattern.MatchString(artifactRef) {
		return fail(ErrContractConflict, instancePath+".outputs", "projected artifact ID %q is not portable", artifactRef)
	}
	if !validRelativeGenerationArtifactPath(outputPath) || isStackKitControlNamespacePath(outputPath) {
		return fail(ErrContractConflict, instancePath+".outputs", "projected artifact path %q is not portable", outputPath)
	}
	if _, duplicate := concreteArtifactRefs[artifactRef]; duplicate {
		return fail(ErrContractConflict, instancePath+".outputs", "projected artifact ID %q is owned more than once in module", artifactRef)
	}
	pathKey := strings.ToLower(outputPath)
	if _, duplicate := concretePaths[pathKey]; duplicate {
		return fail(ErrContractConflict, instancePath+".outputs", "projected artifact path %q is owned more than once in module", outputPath)
	}
	concreteArtifactRefs[artifactRef] = struct{}{}
	concretePaths[pathKey] = struct{}{}
	return nil
}

func moduleUnitOutputKey(unitRef, outputRef string) string {
	return unitRef + "\x00" + outputRef
}

// validateCatalogGenerationArtifactUniqueness covers every governed module,
// including optional modules that are not selected in the current spec. CUE
// enforces the same catalog-wide ownership with exact and portable-case path
// uniqueness and file-tree closure; this Go check prevents a future alternate
// catalog loader from weakening that boundary.
func validateCatalogGenerationArtifactUniqueness(catalog Catalog) error {
	records := make([]generationArtifactRecord, 0, len(catalog.PlanArtifacts))
	for index, raw := range catalog.PlanArtifacts {
		record, _, err := parseGenerationArtifactMetadata(map[string]any(raw), fmt.Sprintf("catalog.planArtifacts[%d]", index), "path")
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	for moduleIndex, module := range catalog.Modules {
		modulePath := fmt.Sprintf("catalog.modules[%d]", moduleIndex)
		support, err := objectField(map[string]any(module), modulePath, "realizationSupport")
		if err != nil {
			return err
		}
		artifacts, err := objectField(support, modulePath+".realizationSupport", "artifacts")
		if err != nil {
			return err
		}
		contracts, err := objectListField(artifacts, modulePath+".realizationSupport.artifacts", "contracts")
		if err != nil {
			return err
		}
		moduleRecords, err := parseCatalogModuleArtifactRecords(contracts, modulePath)
		if err != nil {
			return err
		}
		records = append(records, moduleRecords...)
	}
	return validateGenerationArtifactUniqueness(records)
}

func parseCatalogModuleArtifactRecords(contracts []map[string]any, modulePath string) ([]generationArtifactRecord, error) {
	records := make([]generationArtifactRecord, 0, len(contracts))
	for index, raw := range contracts {
		contractPath := fmt.Sprintf("%s.realizationSupport.artifacts.contracts[%d]", modulePath, index)
		contract, _, err := parseModuleArtifactContract(raw, contractPath)
		if err != nil {
			return nil, err
		}
		contract.path = contract.outputRef
		records = append(records, contract.generationArtifactRecord)
	}
	return records, nil
}

// deriveGenerationArtifacts lowers only CUE-normalized plan authority and the
// contracts carried by selected resolved modules. It has no caller-supplied or
// target-indexed Go metadata fallback.
func deriveGenerationArtifacts(planContracts []map[string]any, modules []any, target, outputRoot string) ([]any, error) {
	if !validGenerationOutputRoot(outputRoot) {
		return nil, fail(ErrContractConflict, "generation.outputRoot", "output root is not a canonical portable relative path")
	}
	records, err := derivePlanArtifactRecords(planContracts, target, outputRoot)
	if err != nil {
		return nil, err
	}
	for index, rawModule := range modules {
		module, err := asObject(rawModule, fmt.Sprintf("modules[%d]", index))
		if err != nil {
			return nil, err
		}
		moduleRecords, err := deriveModuleArtifactRecords(module, fmt.Sprintf("modules[%d]", index), target, outputRoot)
		if err != nil {
			return nil, err
		}
		records = append(records, moduleRecords...)
	}
	if err := validateReservedGenerationControlPaths(records, outputRoot); err != nil {
		return nil, err
	}
	if err := validateGenerationArtifactUniqueness(records); err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].id < records[j].id })
	artifacts := make([]any, 0, len(records))
	for _, record := range records {
		if record.owner == nil {
			return nil, fail(ErrContractConflict, "generation.artifacts", "generation artifact %q has no exact owner", record.id)
		}
		artifacts = append(artifacts, record.document())
	}
	return artifacts, nil
}

func derivePlanArtifactRecords(contracts []map[string]any, target, outputRoot string) ([]generationArtifactRecord, error) {
	if len(contracts) != 1 {
		return nil, fail(ErrContractConflict, "catalog.planArtifacts", "exactly one CUE-governed plan artifact is required")
	}
	record, targets, err := parseGenerationArtifactMetadata(contracts[0], "catalog.planArtifacts[0]", "path")
	if err != nil {
		return nil, err
	}
	if record.id != "resolved-plan" || record.kind != "metadata" || record.path != ".stackkit/resolved-plan.json" || record.format != "json" || record.mode != "0600" || !record.required {
		return nil, fail(ErrContractConflict, "catalog.planArtifacts[0]", "only the exact CUE-governed resolved-plan artifact is allowed at plan scope")
	}
	if !contains(targets, target) {
		return nil, fail(ErrContractConflict, "catalog.planArtifacts[0].compatibleTargets", "resolved-plan does not support generation target %q", target)
	}
	record.path = path.Join(outputRoot, record.path)
	record.owner = map[string]any{"kind": "plan"}
	return []generationArtifactRecord{record}, nil
}

func deriveModuleArtifactRecords(module map[string]any, modulePath, target, outputRoot string) ([]generationArtifactRecord, error) {
	moduleID, err := stringField(module, modulePath, "id")
	if err != nil {
		return nil, err
	}
	artifacts, err := resolvedModuleArtifactAuthority(module, modulePath)
	if err != nil {
		return nil, err
	}
	contracts, err := objectListField(artifacts, modulePath+".realizationSupport.artifacts", "contracts")
	if err != nil {
		return nil, err
	}
	requiredRefs, bindings, units, err := indexModuleArtifactOwnership(module, artifacts, modulePath)
	if err != nil {
		return nil, err
	}
	records := make([]generationArtifactRecord, 0, len(contracts))
	seenContracts := make(map[string]struct{}, len(contracts))
	for index, rawContract := range contracts {
		contractPath := fmt.Sprintf("%s.realizationSupport.artifacts.contracts[%d]", modulePath, index)
		contract, targets, err := parseModuleArtifactContract(rawContract, contractPath)
		if err != nil {
			return nil, err
		}
		if err := validateModuleArtifactOwnership(contract, contractPath, target, targets, requiredRefs, bindings, units); err != nil {
			return nil, err
		}
		seenContracts[contract.id] = struct{}{}
		unit := units[contract.unitRef]
		for _, instance := range unit.instances {
			instanceOutput, exists := instance.outputs[contract.outputRef]
			if !exists {
				return nil, fail(ErrContractConflict, contractPath+".outputRef", "render instance %q does not own logical output %q", instance.id, contract.outputRef)
			}
			expectedArtifactRef, expectedPath, err := projectModuleInstanceArtifact(moduleID, contract.id, contract.outputRef, instance)
			if err != nil {
				return nil, err
			}
			if instanceOutput.artifactRef != expectedArtifactRef || instanceOutput.path != expectedPath {
				return nil, fail(ErrContractConflict, contractPath+".outputRef", "render instance %q output projection drifts from governed artifact %q", instance.id, contract.id)
			}
			concrete := contract.generationArtifactRecord
			concrete.id = instanceOutput.artifactRef
			concrete.path = path.Join(outputRoot, instanceOutput.path)
			concrete.owner = map[string]any{
				"kind": "render-instance", "moduleRef": moduleID, "unitRef": contract.unitRef,
				"instanceRef": instance.id, "outputRef": contract.outputRef,
			}
			records = append(records, concrete)
		}
	}
	if len(seenContracts) != len(requiredRefs) || len(seenContracts) != len(bindings) {
		return nil, fail(ErrContractConflict, modulePath+".realizationSupport.artifacts", "module %q has orphan artifact references or output bindings", moduleID)
	}
	return records, nil
}

func projectModuleInstanceArtifact(moduleID, logicalArtifactRef, logicalOutputRef string, instance moduleRenderInstance) (string, string, error) {
	switch instance.scope {
	case "module":
		return logicalArtifactRef, logicalOutputRef, nil
	case "node-local":
		artifactRef := logicalArtifactRef + "-instance-" + instance.id
		outputPath := path.Join("instances", moduleID, instance.id, logicalOutputRef)
		if !generationArtifactIDPattern.MatchString(artifactRef) {
			return "", "", fail(ErrContractConflict, "modules."+moduleID+".renderUnits.instances", "projected artifact ID %q is not portable", artifactRef)
		}
		if !validRelativeGenerationArtifactPath(outputPath) || isStackKitControlNamespacePath(outputPath) {
			return "", "", fail(ErrContractConflict, "modules."+moduleID+".renderUnits.instances", "projected artifact path %q is not portable", outputPath)
		}
		return artifactRef, outputPath, nil
	default:
		return "", "", fail(ErrContractConflict, "modules."+moduleID+".renderUnits.instances", "unsupported render instance scope %q", instance.scope)
	}
}

func resolvedModuleArtifactAuthority(module map[string]any, modulePath string) (map[string]any, error) {
	support, err := objectField(module, modulePath, "realizationSupport")
	if err != nil {
		return nil, err
	}
	return objectField(support, modulePath+".realizationSupport", "artifacts")
}

func indexModuleArtifactOwnership(module, artifacts map[string]any, modulePath string) (map[string]struct{}, map[string]moduleArtifactBinding, map[string]moduleRenderUnitOutputs, error) {
	requiredRefs, err := stringListField(artifacts, modulePath+".realizationSupport.artifacts", "requiredRefs", false)
	if err != nil {
		return nil, nil, nil, err
	}
	required, err := uniqueStringSet(requiredRefs, modulePath+".realizationSupport.artifacts.requiredRefs")
	if err != nil {
		return nil, nil, nil, err
	}
	bindings, err := indexModuleArtifactBindings(artifacts, modulePath)
	if err != nil {
		return nil, nil, nil, err
	}
	units, err := indexModuleRenderUnitOutputs(module, modulePath)
	return required, bindings, units, err
}

func indexModuleArtifactBindings(artifacts map[string]any, modulePath string) (map[string]moduleArtifactBinding, error) {
	rawBindings, err := objectListField(artifacts, modulePath+".realizationSupport.artifacts", "outputBindings")
	if err != nil {
		return nil, err
	}
	bindings := make(map[string]moduleArtifactBinding, len(rawBindings))
	for index, raw := range rawBindings {
		bindingPath := fmt.Sprintf("%s.realizationSupport.artifacts.outputBindings[%d]", modulePath, index)
		artifactRef, err := stringField(raw, bindingPath, "artifactRef")
		if err != nil {
			return nil, err
		}
		unitRef, err := stringField(raw, bindingPath, "unitRef")
		if err != nil {
			return nil, err
		}
		outputRef, err := stringField(raw, bindingPath, "outputRef")
		if err != nil {
			return nil, err
		}
		if _, duplicate := bindings[artifactRef]; duplicate {
			return nil, fail(ErrContractConflict, bindingPath+".artifactRef", "artifact %q is bound more than once", artifactRef)
		}
		bindings[artifactRef] = moduleArtifactBinding{unitRef: unitRef, outputRef: outputRef}
	}
	return bindings, nil
}

func indexModuleRenderUnitOutputs(module map[string]any, modulePath string) (map[string]moduleRenderUnitOutputs, error) {
	rawUnits, err := objectListField(module, modulePath, "renderUnits")
	if err != nil {
		return nil, err
	}
	units := make(map[string]moduleRenderUnitOutputs, len(rawUnits))
	for index, raw := range rawUnits {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", modulePath, index)
		unitID, err := stringField(raw, unitPath, "id")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(raw, unitPath, "kind")
		if err != nil {
			return nil, err
		}
		outputs, err := stringListField(raw, unitPath, "outputs", true)
		if err != nil {
			return nil, err
		}
		outputSet, err := uniqueStringSet(outputs, unitPath+".outputs")
		if err != nil {
			return nil, err
		}
		if _, duplicate := units[unitID]; duplicate {
			return nil, fail(ErrContractConflict, unitPath+".id", "render unit %q is declared more than once", unitID)
		}
		instances, err := parseModuleRenderInstances(raw, unitPath, unitID, outputSet)
		if err != nil {
			return nil, err
		}
		units[unitID] = moduleRenderUnitOutputs{kind: kind, outputs: outputSet, instances: instances}
	}
	return units, nil
}

type moduleRenderInstancePlacement struct {
	scope          string
	cardinality    string
	nodeRefs       []string
	siteRefs       []string
	daemonBindings map[string]map[string]any
}

func parseModuleRenderInstances(unit map[string]any, unitPath, unitID string, logicalOutputs map[string]struct{}) ([]moduleRenderInstance, error) {
	placement, err := parseModuleRenderInstancePlacement(unit, unitPath)
	if err != nil {
		return nil, err
	}
	rawInstances, err := objectListField(unit, unitPath, "instances")
	if err != nil {
		return nil, err
	}
	if len(rawInstances) == 0 {
		return nil, fail(ErrContractConflict, unitPath+".instances", "render unit has no executable instances")
	}
	if placement.scope == "node-local" && len(rawInstances) != len(placement.nodeRefs) {
		return nil, fail(ErrContractConflict, unitPath+".instances", "node-local render instances must map one-to-one to the %d resolved nodes", len(placement.nodeRefs))
	}

	instances := make([]moduleRenderInstance, 0, len(rawInstances))
	seenIDs := make(map[string]struct{}, len(rawInstances))
	seenNodes := make(map[string]struct{}, len(rawInstances))
	for instanceIndex, rawInstance := range rawInstances {
		instancePath := fmt.Sprintf("%s.instances[%d]", unitPath, instanceIndex)
		instance, err := parseModuleRenderInstance(
			rawInstance, instancePath, unitID, len(rawInstances), logicalOutputs,
			placement, seenIDs, seenNodes,
		)
		if err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}
	if placement.scope == "node-local" && len(seenNodes) != len(placement.nodeRefs) {
		return nil, fail(ErrContractConflict, unitPath+".instances", "node-local render instances do not cover every resolved unit node")
	}
	if placement.cardinality == "one-per-daemon" && len(placement.daemonBindings) != len(rawInstances) {
		return nil, fail(ErrContractConflict, unitPath+".instances", "daemon render instances do not map one-to-one to resolved daemon bindings")
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].id < instances[j].id })
	return instances, nil
}

func parseModuleRenderInstancePlacement(unit map[string]any, unitPath string) (moduleRenderInstancePlacement, error) {
	placement, err := objectField(unit, unitPath, "placement")
	if err != nil {
		return moduleRenderInstancePlacement{}, err
	}
	placementScope, err := stringField(placement, unitPath+".placement", "scope")
	if err != nil {
		return moduleRenderInstancePlacement{}, err
	}
	cardinality, err := stringField(placement, unitPath+".placement", "cardinality")
	if err != nil {
		return moduleRenderInstancePlacement{}, err
	}
	authority := moduleRenderInstancePlacement{
		scope:          placementScope,
		cardinality:    cardinality,
		daemonBindings: map[string]map[string]any{},
	}
	var nodeRefs, siteRefs []string
	if placementScope == "node-local" {
		nodeRefs, err = stringListField(unit, unitPath, "nodeRefs", true)
		if err != nil {
			return moduleRenderInstancePlacement{}, err
		}
		siteRefs, err = stringListField(unit, unitPath, "siteRefs", true)
		if err != nil {
			return moduleRenderInstancePlacement{}, err
		}
	}
	authority.nodeRefs = nodeRefs
	authority.siteRefs = siteRefs
	if cardinality == "one-per-daemon" {
		authority.daemonBindings, err = indexModuleRenderDaemonBindings(unit, unitPath)
		if err != nil {
			return moduleRenderInstancePlacement{}, err
		}
	}
	return authority, nil
}

func indexModuleRenderDaemonBindings(unit map[string]any, unitPath string) (map[string]map[string]any, error) {
	rawBindings, err := objectListField(unit, unitPath, "daemonBindings")
	if err != nil {
		return nil, err
	}
	daemonBindings := make(map[string]map[string]any, len(rawBindings))
	for index, binding := range rawBindings {
		bindingPath := fmt.Sprintf("%s.daemonBindings[%d]", unitPath, index)
		nodeRef, err := stringField(binding, bindingPath, "nodeRef")
		if err != nil {
			return nil, err
		}
		if _, duplicate := daemonBindings[nodeRef]; duplicate {
			return nil, fail(ErrContractConflict, bindingPath+".nodeRef", "node %q has duplicate daemon bindings", nodeRef)
		}
		daemonBindings[nodeRef] = binding
	}
	return daemonBindings, nil
}

func parseModuleRenderInstance(
	rawInstance map[string]any,
	instancePath, unitID string,
	instanceCount int,
	logicalOutputs map[string]struct{},
	placement moduleRenderInstancePlacement,
	seenIDs, seenNodes map[string]struct{},
) (moduleRenderInstance, error) {
	instanceID, err := stringField(rawInstance, instancePath, "id")
	if err != nil {
		return moduleRenderInstance{}, err
	}
	if _, duplicate := seenIDs[instanceID]; duplicate {
		return moduleRenderInstance{}, fail(ErrContractConflict, instancePath+".id", "render instance %q is duplicated", instanceID)
	}
	seenIDs[instanceID] = struct{}{}
	scope, err := stringField(rawInstance, instancePath, "scope")
	if err != nil {
		return moduleRenderInstance{}, err
	}
	if scope != placement.scope {
		return moduleRenderInstance{}, fail(ErrContractConflict, instancePath+".scope", "render instance scope %q does not match placement scope %q", scope, placement.scope)
	}
	switch scope {
	case "module":
		if err := validateModuleScopedRenderInstance(rawInstance, instancePath, instanceID, unitID, instanceCount); err != nil {
			return moduleRenderInstance{}, err
		}
	case "node-local":
		if err := validateNodeLocalRenderInstance(rawInstance, instancePath, instanceID, unitID, placement, seenNodes); err != nil {
			return moduleRenderInstance{}, err
		}
	default:
		return moduleRenderInstance{}, fail(ErrContractConflict, instancePath+".scope", "unsupported render instance scope %q", scope)
	}
	outputs, err := parseModuleRenderInstanceOutputs(rawInstance, instancePath, logicalOutputs)
	if err != nil {
		return moduleRenderInstance{}, err
	}
	return moduleRenderInstance{id: instanceID, scope: scope, outputs: outputs}, nil
}

func validateModuleScopedRenderInstance(rawInstance map[string]any, instancePath, instanceID, unitID string, instanceCount int) error {
	if instanceCount != 1 || instanceID != unitID+"-logical" {
		return fail(ErrContractConflict, instancePath, "module-scoped render unit must have exactly canonical instance %q", unitID+"-logical")
	}
	for _, forbidden := range []string{"siteRef", "nodeRef", "daemonRef", "daemonInstanceRef", "daemonEngine", "daemonSocketPath"} {
		if _, exists := rawInstance[forbidden]; exists {
			return fail(ErrContractConflict, instancePath+"."+forbidden, "module-scoped render instance cannot fabricate locality")
		}
	}
	return nil
}

func validateNodeLocalRenderInstance(
	rawInstance map[string]any,
	instancePath, instanceID, unitID string,
	placement moduleRenderInstancePlacement,
	seenNodes map[string]struct{},
) error {
	siteRef, err := stringField(rawInstance, instancePath, "siteRef")
	if err != nil {
		return err
	}
	nodeRef, err := stringField(rawInstance, instancePath, "nodeRef")
	if err != nil {
		return err
	}
	if !contains(placement.nodeRefs, nodeRef) || !contains(placement.siteRefs, siteRef) {
		return fail(ErrContractConflict, instancePath, "render instance locality %q/%q is outside its resolved unit placement", siteRef, nodeRef)
	}
	if _, duplicate := seenNodes[nodeRef]; duplicate {
		return fail(ErrContractConflict, instancePath+".nodeRef", "node %q owns more than one render instance", nodeRef)
	}
	seenNodes[nodeRef] = struct{}{}
	expectedID := unitID + "-node-" + nodeRef
	if placement.cardinality == "one-per-daemon" {
		expectedID, err = validateDaemonRenderInstance(rawInstance, instancePath, expectedID, siteRef, nodeRef, placement.daemonBindings)
		if err != nil {
			return err
		}
	} else if err := validateNonDaemonRenderInstance(rawInstance, instancePath); err != nil {
		return err
	}
	if instanceID != expectedID {
		return fail(ErrContractConflict, instancePath+".id", "render instance ID %q does not match exact placement identity %q", instanceID, expectedID)
	}
	return nil
}

func validateDaemonRenderInstance(
	rawInstance map[string]any,
	instancePath, expectedID, siteRef, nodeRef string,
	daemonBindings map[string]map[string]any,
) (string, error) {
	daemonRef, err := stringField(rawInstance, instancePath, "daemonRef")
	if err != nil {
		return "", err
	}
	daemonInstanceRef, err := stringField(rawInstance, instancePath, "daemonInstanceRef")
	if err != nil {
		return "", err
	}
	daemonEngine, err := stringField(rawInstance, instancePath, "daemonEngine")
	if err != nil {
		return "", err
	}
	daemonSocketPath, err := stringField(rawInstance, instancePath, "daemonSocketPath")
	if err != nil {
		return "", err
	}
	if err := validateUnixSocketPath(daemonSocketPath, instancePath+".daemonSocketPath", ErrContractConflict); err != nil {
		return "", err
	}
	expectedID += "-daemon-" + daemonInstanceRef
	binding, exists := daemonBindings[nodeRef]
	if !exists {
		return "", fail(ErrContractConflict, instancePath, "daemon render instance has no resolved daemon binding")
	}
	bindingSiteRef, err := stringField(binding, instancePath+".daemonBinding", "siteRef")
	if err != nil {
		return "", err
	}
	bindingDaemonRef, err := stringField(binding, instancePath+".daemonBinding", "daemonRef")
	if err != nil {
		return "", err
	}
	bindingInstanceRef, err := stringField(binding, instancePath+".daemonBinding", "instanceRef")
	if err != nil {
		return "", err
	}
	bindingEngine, err := stringField(binding, instancePath+".daemonBinding", "engine")
	if err != nil {
		return "", err
	}
	bindingSocketPath, err := stringField(binding, instancePath+".daemonBinding", "socketPath")
	if err != nil {
		return "", err
	}
	if bindingSiteRef != siteRef || bindingDaemonRef != daemonRef || bindingInstanceRef != daemonInstanceRef || bindingEngine != daemonEngine || bindingSocketPath != daemonSocketPath {
		return "", fail(ErrContractConflict, instancePath, "daemon render instance drifts from its exact resolved daemon binding")
	}
	return expectedID, nil
}

func validateNonDaemonRenderInstance(rawInstance map[string]any, instancePath string) error {
	if _, exists := rawInstance["daemonRef"]; exists {
		return fail(ErrContractConflict, instancePath+".daemonRef", "non-daemon render instance cannot select a daemon")
	}
	if _, exists := rawInstance["daemonInstanceRef"]; exists {
		return fail(ErrContractConflict, instancePath+".daemonInstanceRef", "non-daemon render instance cannot select a daemon")
	}
	if _, exists := rawInstance["daemonEngine"]; exists {
		return fail(ErrContractConflict, instancePath+".daemonEngine", "non-daemon render instance cannot select a daemon engine")
	}
	if _, exists := rawInstance["daemonSocketPath"]; exists {
		return fail(ErrContractConflict, instancePath+".daemonSocketPath", "non-daemon render instance cannot select a daemon socket path")
	}
	return nil
}

func parseModuleRenderInstanceOutputs(rawInstance map[string]any, instancePath string, logicalOutputs map[string]struct{}) (map[string]moduleRenderInstanceOutput, error) {
	rawOutputs, err := objectListField(rawInstance, instancePath, "outputs")
	if err != nil {
		return nil, err
	}
	if len(rawOutputs) != len(logicalOutputs) {
		return nil, fail(ErrContractConflict, instancePath+".outputs", "render instance must project every logical unit output exactly once")
	}
	outputs := make(map[string]moduleRenderInstanceOutput, len(rawOutputs))
	for outputIndex, rawOutput := range rawOutputs {
		outputPath := fmt.Sprintf("%s.outputs[%d]", instancePath, outputIndex)
		output, err := parseModuleRenderInstanceOutput(rawOutput, outputPath, logicalOutputs, outputs)
		if err != nil {
			return nil, err
		}
		outputs[output.ref] = output
	}
	return outputs, nil
}

func parseModuleRenderInstanceOutput(
	rawOutput map[string]any,
	outputPath string,
	logicalOutputs map[string]struct{},
	outputs map[string]moduleRenderInstanceOutput,
) (moduleRenderInstanceOutput, error) {
	ref, err := stringField(rawOutput, outputPath, "ref")
	if err != nil {
		return moduleRenderInstanceOutput{}, err
	}
	artifactRef, err := stringField(rawOutput, outputPath, "artifactRef")
	if err != nil {
		return moduleRenderInstanceOutput{}, err
	}
	materializedPath, err := stringField(rawOutput, outputPath, "path")
	if err != nil {
		return moduleRenderInstanceOutput{}, err
	}
	if _, declared := logicalOutputs[ref]; !declared {
		return moduleRenderInstanceOutput{}, fail(ErrContractConflict, outputPath+".ref", "instance output %q is not declared by its logical unit", ref)
	}
	if _, duplicate := outputs[ref]; duplicate {
		return moduleRenderInstanceOutput{}, fail(ErrContractConflict, outputPath+".ref", "logical output %q is projected more than once", ref)
	}
	return moduleRenderInstanceOutput{ref: ref, artifactRef: artifactRef, path: materializedPath}, nil
}

func parseModuleArtifactContract(raw map[string]any, contractPath string) (moduleArtifactContract, []string, error) {
	record, targets, err := parseGenerationArtifactMetadata(raw, contractPath, "outputRef")
	if err != nil {
		return moduleArtifactContract{}, nil, err
	}
	unitRef, err := stringField(raw, contractPath, "unitRef")
	if err != nil {
		return moduleArtifactContract{}, nil, err
	}
	outputRef, err := stringField(raw, contractPath, "outputRef")
	if err != nil {
		return moduleArtifactContract{}, nil, err
	}
	if isStackKitControlNamespacePath(outputRef) {
		return moduleArtifactContract{}, nil, fail(ErrContractConflict, contractPath+".outputRef", "module artifact path %q is inside the generator-owned .stackkit namespace", outputRef)
	}
	return moduleArtifactContract{generationArtifactRecord: record, unitRef: unitRef, outputRef: outputRef}, targets, nil
}

func parseGenerationArtifactMetadata(raw map[string]any, artifactPath, pathField string) (generationArtifactRecord, []string, error) {
	var record generationArtifactRecord
	fields := []struct {
		name   string
		target *string
	}{{"id", &record.id}, {"kind", &record.kind}, {pathField, &record.path}, {"format", &record.format}, {"mode", &record.mode}}
	for _, field := range fields {
		value, err := stringField(raw, artifactPath, field.name)
		if err != nil {
			return generationArtifactRecord{}, nil, err
		}
		*field.target = value
	}
	required, err := boolFieldDefault(raw, artifactPath, "required", false)
	if err != nil || !required {
		if err != nil {
			return generationArtifactRecord{}, nil, err
		}
		return generationArtifactRecord{}, nil, fail(ErrContractConflict, artifactPath+".required", "module and plan generation artifacts must be required")
	}
	targets, err := stringListField(raw, artifactPath, "compatibleTargets", true)
	if err != nil {
		return generationArtifactRecord{}, nil, err
	}
	if _, err := uniqueStringSet(targets, artifactPath+".compatibleTargets"); err != nil {
		return generationArtifactRecord{}, nil, err
	}
	record.required = true
	if err := validateGenerationArtifactMetadata(record, targets, artifactPath); err != nil {
		return generationArtifactRecord{}, nil, err
	}
	return record, targets, nil
}

func validateGenerationArtifactMetadata(record generationArtifactRecord, targets []string, artifactPath string) error {
	if !generationArtifactIDPattern.MatchString(record.id) {
		return fail(ErrContractConflict, artifactPath+".id", "invalid generation artifact ID %q", record.id)
	}
	if !contains([]string{"opentofu", "compose", "metadata", "script", "native-config"}, record.kind) {
		return fail(ErrContractConflict, artifactPath+".kind", "unsupported generation artifact kind %q", record.kind)
	}
	if !contains([]string{"json", "yaml", "hcl", "shell", "text"}, record.format) {
		return fail(ErrContractConflict, artifactPath+".format", "unsupported generation artifact format %q", record.format)
	}
	if !validArtifactMode(record.mode) {
		return fail(ErrContractConflict, artifactPath+".mode", "artifact mode %q must be a four-digit octal mode", record.mode)
	}
	if !validRelativeGenerationArtifactPath(record.path) {
		return fail(ErrContractConflict, artifactPath, "artifact path escapes or is not a portable relative path")
	}
	if isReservedGenerationControlPath(record.path) {
		return fail(ErrContractConflict, artifactPath, "artifact path %q is reserved for generator transaction control", record.path)
	}
	for _, target := range targets {
		if target != "compose" && target != "opentofu" {
			return fail(ErrContractConflict, artifactPath+".compatibleTargets", "unsupported generation target %q", target)
		}
		if (record.kind == "compose" || record.kind == "opentofu") && target != record.kind {
			return fail(ErrContractConflict, artifactPath+".compatibleTargets", "artifact kind %q is incompatible with generation target %q", record.kind, target)
		}
	}
	return nil
}

func validArtifactMode(mode string) bool {
	if len(mode) != 4 || mode[0] != '0' {
		return false
	}
	for _, digit := range mode[1:] {
		if digit < '0' || digit > '7' {
			return false
		}
	}
	return true
}

func validRelativeGenerationArtifactPath(value string) bool {
	clean := path.Clean(value)
	if value == "" || clean == "." || clean != value || path.IsAbs(clean) || strings.HasPrefix(clean, "../") || !generationArtifactPathPattern.MatchString(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if strings.HasSuffix(segment, ".") {
			return false
		}
		base := segment
		if extension := strings.IndexByte(segment, '.'); extension >= 0 {
			base = segment[:extension]
		}
		if windowsReservedArtifactBase.MatchString(base) {
			return false
		}
	}
	return true
}

func validGenerationOutputRoot(value string) bool {
	return value == "." || (validRelativeGenerationArtifactPath(value) && !isStackKitControlNamespacePath(value))
}

func isReservedGenerationControlPath(value string) bool {
	switch strings.ToLower(value) {
	case ".stackkit/generation-manifest.json", ".stackkit/generation-receipt.json":
		return true
	default:
		return false
	}
}

func isStackKitControlNamespacePath(value string) bool {
	lower := strings.ToLower(value)
	return lower == ".stackkit" || strings.HasPrefix(lower, ".stackkit/")
}

func validateReservedGenerationControlPaths(records []generationArtifactRecord, outputRoot string) error {
	reserved := map[string]struct{}{
		strings.ToLower(path.Join(outputRoot, ".stackkit/generation-manifest.json")): {},
		strings.ToLower(path.Join(outputRoot, ".stackkit/generation-receipt.json")):  {},
	}
	for index, record := range records {
		if _, conflict := reserved[strings.ToLower(record.path)]; conflict {
			return fail(ErrContractConflict, fmt.Sprintf("generation.artifacts[%d].path", index), "generation artifact path %q is reserved for generator transaction control", record.path)
		}
	}
	return nil
}

func validateModuleArtifactOwnership(contract moduleArtifactContract, contractPath, target string, targets []string, required map[string]struct{}, bindings map[string]moduleArtifactBinding, units map[string]moduleRenderUnitOutputs) error {
	if !contains(targets, target) {
		return fail(ErrUnrealizedModule, contractPath+".compatibleTargets", "selected module artifact %q is incompatible with generation target %q", contract.id, target)
	}
	if _, exists := required[contract.id]; !exists {
		return fail(ErrContractConflict, contractPath+".id", "artifact %q is not declared in requiredRefs", contract.id)
	}
	binding, exists := bindings[contract.id]
	if !exists || binding.unitRef != contract.unitRef || binding.outputRef != contract.outputRef {
		return fail(ErrContractConflict, contractPath, "artifact %q ownership drifts from its output binding", contract.id)
	}
	unit, exists := units[contract.unitRef]
	if !exists {
		return fail(ErrContractConflict, contractPath+".unitRef", "artifact %q references unknown render unit %q", contract.id, contract.unitRef)
	}
	if _, exists := unit.outputs[contract.outputRef]; !exists {
		return fail(ErrContractConflict, contractPath+".outputRef", "artifact %q references undeclared output %q", contract.id, contract.outputRef)
	}
	if artifactKindRequiresMatchingUnit(contract.kind) && contract.kind != unit.kind {
		return fail(ErrContractConflict, contractPath+".kind", "artifact kind %q does not match owning render-unit kind %q", contract.kind, unit.kind)
	}
	return nil
}

func validateGenerationArtifactUniqueness(records []generationArtifactRecord) error {
	ids := make(map[string]struct{}, len(records))
	paths := make(map[string]string, len(records))
	pathKeys := make([]string, 0, len(records))
	for index, record := range records {
		artifactPath := fmt.Sprintf("generation.artifacts[%d]", index)
		if _, duplicate := ids[record.id]; duplicate {
			return fail(ErrContractConflict, artifactPath+".id", "generation artifact ID %q is owned more than once", record.id)
		}
		ids[record.id] = struct{}{}
		pathKey := strings.ToLower(path.Clean(record.path))
		if owner, duplicate := paths[pathKey]; duplicate {
			return fail(ErrContractConflict, artifactPath+".path", "generation artifact path %q conflicts with artifact %q", record.path, owner)
		}
		paths[pathKey] = record.id
		pathKeys = append(pathKeys, pathKey)
	}
	sort.Strings(pathKeys)
	for _, descendantPath := range pathKeys {
		segments := strings.Split(descendantPath, "/")
		for boundary := 1; boundary < len(segments); boundary++ {
			ancestorPath := strings.Join(segments[:boundary], "/")
			if owner, conflict := paths[ancestorPath]; conflict {
				return fail(ErrContractConflict, "generation.artifacts", "generation artifact path %q descends from file artifact %q at %q", descendantPath, owner, ancestorPath)
			}
		}
	}
	return nil
}

func uniqueStringSet(values []string, valuePath string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for index, value := range values {
		if _, duplicate := result[value]; duplicate {
			return nil, fail(ErrContractConflict, fmt.Sprintf("%s[%d]", valuePath, index), "value %q is duplicated", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func artifactKindRequiresMatchingUnit(kind string) bool {
	return kind == "compose" || kind == "opentofu" || kind == "native-config"
}

func (record generationArtifactRecord) document() map[string]any {
	document := map[string]any{
		"id": record.id, "kind": record.kind, "path": record.path,
		"format": record.format, "mode": record.mode, "required": record.required,
	}
	document["owner"] = record.owner
	return document
}
