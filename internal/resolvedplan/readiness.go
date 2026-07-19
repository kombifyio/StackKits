package resolvedplan

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

const executionReadinessContractVersion = "1.0.0"

type executionReadinessBlocker struct {
	code string
	refs []string
}

// buildExecutionReadiness reports whether the selected governed contracts are
// implementable by this compiler's renderer. It never turns a support gap into
// a resolve failure: contract-only plans remain useful for shadow planning,
// while generators and apply runtimes get a signed fail-closed decision.
func buildExecutionReadiness(providers, modules, artifacts []any, evidence []string, rendererID, outputRoot string, bridges ...map[string]any) (map[string]any, error) {
	artifactIDs, err := indexReadinessArtifacts(artifacts)
	if err != nil {
		return nil, err
	}
	evidenceRefs := stringSet(evidence)
	var generationBlockers, applyOnlyBlockers []executionReadinessBlocker
	artifactOwnershipBlockers, err := moduleArtifactOwnershipBlockers(modules)
	if err != nil {
		return nil, err
	}
	generationBlockers = append(generationBlockers, artifactOwnershipBlockers...)
	for index, raw := range providers {
		generation, applyOnly, err := providerExecutionReadiness(raw, index, artifactIDs, evidenceRefs, rendererID)
		if err != nil {
			return nil, err
		}
		generationBlockers = append(generationBlockers, generation...)
		applyOnlyBlockers = append(applyOnlyBlockers, applyOnly...)
	}
	for index, raw := range modules {
		generation, applyOnly, err := moduleExecutionReadiness(raw, index, artifactIDs, evidenceRefs, rendererID, outputRoot)
		if err != nil {
			return nil, err
		}
		generationBlockers = append(generationBlockers, generation...)
		applyOnlyBlockers = append(applyOnlyBlockers, applyOnly...)
	}
	if len(bridges) > 1 {
		return nil, fail(ErrInvalidInput, "bridge", "execution readiness accepts at most one resolved bridge")
	}
	if len(bridges) == 1 && bridges[0] != nil {
		bridgeBlockers, err := bridgeExecutionReadinessBlockers(bridges[0])
		if err != nil {
			return nil, err
		}
		generationBlockers = append(generationBlockers, bridgeBlockers...)
	}

	generationBlockers = normalizeExecutionReadinessBlockers(generationBlockers)
	applyBlockers := normalizeExecutionReadinessBlockers(append(append([]executionReadinessBlocker{}, generationBlockers...), applyOnlyBlockers...))
	return map[string]any{
		"contractVersion": executionReadinessContractVersion,
		"generation":      executionReadinessPhase(generationBlockers),
		"apply":           executionReadinessPhase(applyBlockers),
	}, nil
}

func bridgeExecutionReadinessBlockers(bridge map[string]any) ([]executionReadinessBlocker, error) {
	publications, err := objectListOptional(bridge, "publications")
	if err != nil {
		return nil, err
	}
	// These seams exist for every bridge, including a management-only or empty
	// bridge. Publication-specific blockers below are additive and must never be
	// the only reason a federated plan remains fail-closed.
	blockers := []executionReadinessBlocker{
		{code: "bridge-overlay-unverified", refs: []string{"bridge:overlay"}},
		{code: "bridge-control-agent-unverified", refs: []string{"bridge:control-agent"}},
		{code: "policy-enforcement-unverified", refs: []string{"bridge:policy"}},
		{code: "partition-policy-enforcement-unverified", refs: []string{"bridge:partition-policy"}},
		{code: "device-verifier-unbound", refs: []string{"identity:edge-device-verifier"}},
	}
	for index, publication := range publications {
		publicationPath := fmt.Sprintf("bridge.publications[%d]", index)
		serviceRef, err := stringField(publication, publicationPath, "serviceRef")
		if err != nil {
			return nil, err
		}
		origin, err := objectField(publication, publicationPath, "origin")
		if err != nil {
			return nil, err
		}
		identityRef, err := stringField(origin, publicationPath+".origin", "identityRef")
		if err != nil {
			return nil, err
		}
		healthGateRef, err := stringField(publication, publicationPath, "healthGateRef")
		if err != nil {
			return nil, err
		}
		publicationRef := "publication:" + serviceRef
		blockers = append(blockers,
			executionReadinessBlocker{code: "bridge-renderer-missing", refs: []string{publicationRef, "renderer:bridge-edge"}},
			executionReadinessBlocker{code: "origin-identity-unbound", refs: []string{publicationRef, "identity:" + identityRef}},
			executionReadinessBlocker{code: "tls-profile-unbound", refs: []string{publicationRef, "tls:" + serviceRef}},
			executionReadinessBlocker{code: "health-gate-not-executable", refs: []string{publicationRef, "health:" + healthGateRef}},
		)
	}
	return blockers, nil
}

type readinessProvider struct {
	id      string
	ref     string
	level   string
	scope   string
	support map[string]any
	inputs  map[string]any
}

func providerExecutionReadiness(raw any, index int, artifactIDs map[string]readinessArtifact, evidenceRefs map[string]struct{}, rendererID string) ([]executionReadinessBlocker, []executionReadinessBlocker, error) {
	provider, err := asObject(raw, fmt.Sprintf("providers[%d]", index))
	if err != nil {
		return nil, nil, err
	}
	id, err := stringField(provider, fmt.Sprintf("providers[%d]", index), "id")
	if err != nil {
		return nil, nil, err
	}
	ref := "provider:" + id
	realization, err := objectField(provider, "providers."+id, "realization")
	if err != nil {
		return nil, nil, err
	}
	kind, err := stringField(realization, "providers."+id+".realization", "kind")
	if err != nil {
		return nil, nil, err
	}
	switch kind {
	case "none":
		return []executionReadinessBlocker{{code: "provider-realization-none", refs: []string{ref}}}, nil, nil
	case "modules":
		return nil, nil, nil
	case "host", "external":
	default:
		return nil, nil, fail(ErrInvalidInput, "providers."+id+".realization.kind", "unsupported realization %q", kind)
	}
	owner, err := objectField(provider, "providers."+id, "owner")
	if err != nil {
		return nil, nil, err
	}
	support, err := objectField(owner, "providers."+id+".owner", "realizationSupport")
	if err != nil {
		return nil, nil, err
	}
	contractVersion, err := stringField(support, "providers."+id+".owner.realizationSupport", "contractVersion")
	if err != nil {
		return nil, nil, err
	}
	if contractVersion != executionReadinessContractVersion {
		return nil, nil, fail(ErrContractConflict, "providers."+id+".owner.realizationSupport.contractVersion", "unsupported realization support contract %q", contractVersion)
	}
	inputs, err := objectField(owner, "providers."+id+".owner", "inputs")
	if err != nil {
		return nil, nil, err
	}
	readiness := readinessProvider{id: id, ref: ref, support: support, inputs: inputs}
	readiness.scope, err = stringField(support, "providers."+id+".owner.realizationSupport", "scope")
	if err != nil {
		return nil, nil, err
	}
	readiness.level, err = stringField(support, "providers."+id+".owner.realizationSupport", "level")
	if err != nil {
		return nil, nil, err
	}
	generation, err := providerGenerationBlockers(readiness, artifactIDs, rendererID)
	if err != nil {
		return nil, nil, err
	}
	applyOnly, err := providerApplyBlockers(readiness, evidenceRefs)
	return generation, applyOnly, err
}

func providerGenerationBlockers(provider readinessProvider, artifactIDs map[string]readinessArtifact, rendererID string) ([]executionReadinessBlocker, error) {
	var blockers []executionReadinessBlocker
	if provider.scope == "umbrella" {
		blockers = append(blockers, executionReadinessBlocker{code: "provider-owner-umbrella", refs: []string{provider.ref}})
	}
	if provider.level == "contract-only" {
		blockers = append(blockers, executionReadinessBlocker{code: "provider-owner-contract-only", refs: []string{provider.ref}})
		return blockers, nil
	}
	compatibleRenderers, err := stringListField(provider.support, "providers."+provider.id+".owner.realizationSupport", "compatibleRendererRefs", true)
	if err != nil {
		return nil, err
	}
	if len(compatibleRenderers) > 0 && !contains(compatibleRenderers, rendererID) {
		blockers = append(blockers, executionReadinessBlocker{code: "renderer-incompatible", refs: []string{provider.ref, "renderer:" + rendererID}})
	}
	inputs, err := objectField(provider.support, "providers."+provider.id+".owner.realizationSupport", "inputs")
	if err != nil {
		return nil, err
	}
	complete, err := boolFieldDefault(inputs, "providers."+provider.id+".owner.realizationSupport.inputs", "contractComplete", false)
	if err != nil {
		return nil, err
	}
	if !complete {
		blockers = append(blockers, executionReadinessBlocker{code: "input-contract-incomplete", refs: []string{provider.ref}})
	}
	requiredInputs, err := stringListField(inputs, "providers."+provider.id+".owner.realizationSupport.inputs", "requiredRefs", false)
	if err != nil {
		return nil, err
	}
	values, err := objectField(provider.inputs, "providers."+provider.id+".owner.inputs", "values")
	if err != nil {
		return nil, err
	}
	secretRefs, err := objectField(provider.inputs, "providers."+provider.id+".owner.inputs", "secretRefs")
	if err != nil {
		return nil, err
	}
	for _, inputRef := range requiredInputs {
		_, inValues := values[inputRef]
		_, inSecrets := secretRefs[inputRef]
		if !inValues && !inSecrets {
			blockers = append(blockers, executionReadinessBlocker{code: "required-input-missing", refs: []string{provider.ref, "input:" + provider.id + "/" + inputRef}})
		}
	}
	artifactSupport, err := objectField(provider.support, "providers."+provider.id+".owner.realizationSupport", "artifacts")
	if err != nil {
		return nil, err
	}
	requiredArtifacts, err := stringListField(artifactSupport, "providers."+provider.id+".owner.realizationSupport.artifacts", "requiredRefs", true)
	if err != nil {
		return nil, err
	}
	for _, artifactRef := range requiredArtifacts {
		artifact, exists := artifactIDs[artifactRef]
		if !exists || !artifact.required {
			blockers = append(blockers, executionReadinessBlocker{code: "required-artifact-missing", refs: []string{provider.ref, "artifact:" + artifactRef}})
		}
	}
	return blockers, nil
}

func providerApplyBlockers(provider readinessProvider, evidenceRefs map[string]struct{}) ([]executionReadinessBlocker, error) {
	return realizationApplyBlockers(
		provider.level,
		provider.ref,
		provider.support,
		"providers."+provider.id+".owner.realizationSupport",
		"provider-owner-apply-support-missing",
		evidenceRefs,
	)
}

type readinessArtifact struct {
	path     string
	required bool
}

func indexReadinessArtifacts(artifacts []any) (map[string]readinessArtifact, error) {
	artifactIDs := make(map[string]readinessArtifact, len(artifacts))
	artifactPaths := make(map[string]string, len(artifacts))
	for index, raw := range artifacts {
		path := fmt.Sprintf("generation.artifacts[%d]", index)
		artifact, err := asObject(raw, path)
		if err != nil {
			return nil, err
		}
		id, err := stringField(artifact, path, "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := artifactIDs[id]; duplicate {
			return nil, fail(ErrContractConflict, path+".id", "generation artifact %q is duplicated", id)
		}
		required, err := boolFieldDefault(artifact, path, "required", true)
		if err != nil {
			return nil, err
		}
		artifactPath, err := stringField(artifact, path, "path")
		if err != nil {
			return nil, err
		}
		if previousID, duplicate := artifactPaths[artifactPath]; duplicate {
			return nil, fail(ErrContractConflict, path+".path", "generation artifact path %q is already owned by %q", artifactPath, previousID)
		}
		artifactIDs[id] = readinessArtifact{path: artifactPath, required: required}
		artifactPaths[artifactPath] = id
	}
	return artifactIDs, nil
}

type readinessModule struct {
	id      string
	ref     string
	level   string
	scope   string
	support map[string]any
	units   []readinessRenderUnit
}

// moduleArtifactOwnershipBlockers is the Go-side parity check for the CUE
// catalog and ResolvedPlan global artifactRef uniqueness constraints. Artifact
// references in blockers are module-namespaced so every conflicting owner is
// unambiguous even though the invalid plan reused one global artifact ID.
func moduleArtifactOwnershipBlockers(modules []any) ([]executionReadinessBlocker, error) {
	type owner struct {
		moduleID    string
		unitRef     string
		artifactRef string
		outputRef   string
	}
	artifactOwners := make(map[string][]owner)
	outputOwners := make(map[string][]owner)
	for moduleIndex, raw := range modules {
		modulePath := fmt.Sprintf("modules[%d]", moduleIndex)
		module, err := asObject(raw, modulePath)
		if err != nil {
			return nil, err
		}
		moduleID, err := stringField(module, modulePath, "id")
		if err != nil {
			return nil, err
		}
		support, err := objectField(module, "modules."+moduleID, "realizationSupport")
		if err != nil {
			return nil, err
		}
		artifacts, err := objectField(support, "modules."+moduleID+".realizationSupport", "artifacts")
		if err != nil {
			return nil, err
		}
		bindings, err := objectListField(artifacts, "modules."+moduleID+".realizationSupport.artifacts", "outputBindings")
		if err != nil {
			return nil, err
		}
		for bindingIndex, binding := range bindings {
			bindingPath := fmt.Sprintf("modules.%s.realizationSupport.artifacts.outputBindings[%d]", moduleID, bindingIndex)
			artifactRef, err := stringField(binding, bindingPath, "artifactRef")
			if err != nil {
				return nil, err
			}
			unitRef, err := stringField(binding, bindingPath, "unitRef")
			if err != nil {
				return nil, err
			}
			outputRef, err := stringField(binding, bindingPath, "outputRef")
			if err != nil {
				return nil, err
			}
			candidate := owner{moduleID: moduleID, unitRef: unitRef, artifactRef: artifactRef, outputRef: outputRef}
			artifactOwners[artifactRef] = append(artifactOwners[artifactRef], candidate)
			outputOwners[outputRef] = append(outputOwners[outputRef], candidate)
		}
	}

	var blockers []executionReadinessBlocker
	appendConflict := func(owners []owner) {
		moduleIDs := make(map[string]struct{}, len(owners))
		for _, owner := range owners {
			moduleIDs[owner.moduleID] = struct{}{}
		}
		if len(moduleIDs) < 2 {
			return
		}
		refs := make([]string, 0, len(owners)*3)
		for _, owner := range owners {
			refs = append(refs,
				"module:"+owner.moduleID,
				"unit:"+owner.moduleID+"/"+owner.unitRef,
				moduleArtifactRef(owner.moduleID, owner.artifactRef),
			)
		}
		blockers = append(blockers, executionReadinessBlocker{code: "artifact-output-mismatch", refs: refs})
	}
	for _, owners := range artifactOwners {
		appendConflict(owners)
	}
	for _, owners := range outputOwners {
		appendConflict(owners)
	}
	return blockers, nil
}

func moduleArtifactRef(moduleID, artifactRef string) string {
	return "artifact:" + moduleID + "/" + artifactRef
}

type readinessRenderUnit struct {
	id              string
	ref             string
	rendererRef     string
	publicInputRefs []string
	secretInputRefs []string
	planInputRefs   []string
	values          map[string]any
	secretRefs      map[string]any
	planInputs      map[string]any
	outputs         []string
	instances       []readinessRenderInstance
}

type readinessRenderInstance struct {
	id      string
	outputs map[string]readinessRenderInstanceOutput
}

type readinessRenderInstanceOutput struct {
	artifactRef string
	path        string
}

func moduleExecutionReadiness(raw any, index int, artifactIDs map[string]readinessArtifact, evidenceRefs map[string]struct{}, rendererID, outputRoot string) ([]executionReadinessBlocker, []executionReadinessBlocker, error) {
	module, err := readReadinessModule(raw, index)
	if err != nil {
		return nil, nil, err
	}
	generation, err := moduleGenerationBlockers(module, artifactIDs, rendererID, outputRoot)
	if err != nil {
		return nil, nil, err
	}
	applyOnly, err := moduleApplyBlockers(module, evidenceRefs)
	return generation, applyOnly, err
}

func readReadinessModule(raw any, index int) (readinessModule, error) {
	var result readinessModule
	module, err := asObject(raw, fmt.Sprintf("modules[%d]", index))
	if err != nil {
		return result, err
	}
	result.id, err = stringField(module, fmt.Sprintf("modules[%d]", index), "id")
	if err != nil {
		return result, err
	}
	result.ref = "module:" + result.id
	result.support, err = objectField(module, "modules."+result.id, "realizationSupport")
	if err != nil {
		return result, err
	}
	contractVersion, err := stringField(result.support, "modules."+result.id+".realizationSupport", "contractVersion")
	if err != nil {
		return result, err
	}
	if contractVersion != executionReadinessContractVersion {
		return result, fail(ErrContractConflict, "modules."+result.id+".realizationSupport.contractVersion", "unsupported realization support contract %q", contractVersion)
	}
	result.scope, err = stringField(result.support, "modules."+result.id+".realizationSupport", "scope")
	if err != nil {
		return result, err
	}
	result.level, err = stringField(result.support, "modules."+result.id+".realizationSupport", "level")
	if err != nil {
		return result, err
	}
	rawUnits, err := objectListField(module, "modules."+result.id, "renderUnits")
	if err != nil {
		return result, err
	}
	seen := make(map[string]struct{}, len(rawUnits))
	for index, rawUnit := range rawUnits {
		unitPath := fmt.Sprintf("modules.%s.renderUnits[%d]", result.id, index)
		unit, err := readReadinessRenderUnit(result.id, rawUnit, unitPath)
		if err != nil {
			return result, err
		}
		if _, duplicate := seen[unit.id]; duplicate {
			return result, fail(ErrContractConflict, unitPath+".id", "render unit %q is duplicated", unit.id)
		}
		seen[unit.id] = struct{}{}
		result.units = append(result.units, unit)
	}
	sort.Slice(result.units, func(i, j int) bool { return result.units[i].id < result.units[j].id })
	return result, nil
}

//nolint:gocyclo // Readiness must decode every render-unit authority class together before deriving blockers or generation support.
func readReadinessRenderUnit(moduleID string, rawUnit map[string]any, unitPath string) (readinessRenderUnit, error) {
	var result readinessRenderUnit
	var err error
	result.id, err = stringField(rawUnit, unitPath, "id")
	if err != nil {
		return result, err
	}
	result.ref = "unit:" + moduleID + "/" + result.id
	for _, field := range []string{"kind", "templateRef", "version", "contractHash"} {
		if _, err := stringField(rawUnit, unitPath, field); err != nil {
			return result, err
		}
	}
	result.rendererRef, err = stringField(rawUnit, unitPath, "rendererRef")
	if err != nil {
		return result, err
	}
	result.publicInputRefs, err = stringListField(rawUnit, unitPath, "publicInputRefs", false)
	if err != nil {
		return result, err
	}
	result.secretInputRefs, err = stringListField(rawUnit, unitPath, "secretInputRefs", false)
	if err != nil {
		return result, err
	}
	result.planInputRefs, err = stringListField(rawUnit, unitPath, "planInputRefs", false)
	if err != nil {
		return result, err
	}
	result.outputs, err = stringListField(rawUnit, unitPath, "outputs", true)
	if err != nil {
		return result, err
	}
	rawInstances, err := objectListField(rawUnit, unitPath, "instances")
	if err != nil {
		return result, err
	}
	seenInstances := make(map[string]struct{}, len(rawInstances))
	for instanceIndex, rawInstance := range rawInstances {
		instancePath := fmt.Sprintf("%s.instances[%d]", unitPath, instanceIndex)
		instanceID, err := stringField(rawInstance, instancePath, "id")
		if err != nil {
			return result, err
		}
		if _, duplicate := seenInstances[instanceID]; duplicate {
			return result, fail(ErrContractConflict, instancePath+".id", "render instance %q is duplicated", instanceID)
		}
		seenInstances[instanceID] = struct{}{}
		instance := readinessRenderInstance{id: instanceID, outputs: map[string]readinessRenderInstanceOutput{}}
		rawOutputs, err := objectListField(rawInstance, instancePath, "outputs")
		if err != nil {
			return result, err
		}
		for outputIndex, rawOutput := range rawOutputs {
			outputPath := fmt.Sprintf("%s.outputs[%d]", instancePath, outputIndex)
			ref, err := stringField(rawOutput, outputPath, "ref")
			if err != nil {
				return result, err
			}
			artifactRef, err := stringField(rawOutput, outputPath, "artifactRef")
			if err != nil {
				return result, err
			}
			materializedPath, err := stringField(rawOutput, outputPath, "path")
			if err != nil {
				return result, err
			}
			if _, duplicate := instance.outputs[ref]; duplicate {
				return result, fail(ErrContractConflict, outputPath+".ref", "logical output %q is projected more than once", ref)
			}
			instance.outputs[ref] = readinessRenderInstanceOutput{artifactRef: artifactRef, path: materializedPath}
		}
		result.instances = append(result.instances, instance)
	}
	sort.Slice(result.instances, func(i, j int) bool { return result.instances[i].id < result.instances[j].id })
	result.values, err = objectField(rawUnit, unitPath, "values")
	if err != nil {
		return result, err
	}
	result.secretRefs, err = objectField(rawUnit, unitPath, "secretRefs")
	if err != nil {
		return result, err
	}
	result.planInputs, err = objectField(rawUnit, unitPath, "planInputs")
	if err != nil {
		return result, err
	}
	return result, nil
}

func moduleGenerationBlockers(module readinessModule, artifactIDs map[string]readinessArtifact, rendererID, outputRoot string) ([]executionReadinessBlocker, error) {
	var blockers []executionReadinessBlocker
	if module.scope == "umbrella" {
		blockers = append(blockers, executionReadinessBlocker{code: "module-umbrella", refs: []string{module.ref}})
	}
	if module.level == "contract-only" {
		blockers = append(blockers, executionReadinessBlocker{code: "module-contract-only", refs: []string{module.ref}})
	}
	if len(module.units) == 0 {
		blockers = append(blockers, executionReadinessBlocker{code: "module-render-units-missing", refs: []string{module.ref}})
	}
	if module.level == "contract-only" {
		return blockers, nil
	}
	supported, err := supportedGenerationBlockers(module, artifactIDs, rendererID, outputRoot)
	return append(blockers, supported...), err
}

func supportedGenerationBlockers(module readinessModule, artifactIDs map[string]readinessArtifact, rendererID, outputRoot string) ([]executionReadinessBlocker, error) {
	var blockers []executionReadinessBlocker
	compatibleRenderers, err := stringListField(module.support, "modules."+module.id+".realizationSupport", "compatibleRendererRefs", true)
	if err != nil {
		return nil, err
	}
	for _, unit := range module.units {
		if unit.rendererRef != rendererID || !contains(compatibleRenderers, unit.rendererRef) {
			blockers = append(blockers, executionReadinessBlocker{code: "renderer-incompatible", refs: []string{module.ref, unit.ref, "renderer:" + rendererID}})
		}
	}
	inputBlockers, err := moduleInputBlockers(module)
	if err != nil {
		return nil, err
	}
	planInputBlockers, err := modulePlanInputBlockers(module)
	if err != nil {
		return nil, err
	}
	artifactBlockers, err := moduleArtifactBlockers(module, artifactIDs, outputRoot)
	if err != nil {
		return nil, err
	}
	blockers = append(blockers, inputBlockers...)
	blockers = append(blockers, planInputBlockers...)
	return append(blockers, artifactBlockers...), nil
}

func moduleInputBlockers(module readinessModule) ([]executionReadinessBlocker, error) {
	inputs, err := objectField(module.support, "modules."+module.id+".realizationSupport", "inputs")
	if err != nil {
		return nil, err
	}
	complete, err := boolFieldDefault(inputs, "modules."+module.id+".realizationSupport.inputs", "contractComplete", false)
	if err != nil {
		return nil, err
	}
	var blockers []executionReadinessBlocker
	if !complete {
		blockers = append(blockers, executionReadinessBlocker{code: "input-contract-incomplete", refs: []string{module.ref}})
	}
	requiredInputs, err := stringListField(inputs, "modules."+module.id+".realizationSupport.inputs", "requiredRefs", false)
	if err != nil {
		return nil, err
	}
	for _, inputRef := range requiredInputs {
		declared := false
		for _, unit := range module.units {
			if !contains(unit.publicInputRefs, inputRef) && !contains(unit.secretInputRefs, inputRef) {
				continue
			}
			declared = true
			_, inValues := unit.values[inputRef]
			_, inSecrets := unit.secretRefs[inputRef]
			if !inValues && !inSecrets {
				blockers = append(blockers, executionReadinessBlocker{code: "required-input-missing", refs: []string{module.ref, unit.ref, "input:" + module.id + "/" + inputRef}})
			}
		}
		if !declared {
			blockers = append(blockers, executionReadinessBlocker{code: "required-input-missing", refs: []string{module.ref, "input:" + module.id + "/" + inputRef}})
		}
	}
	for _, unit := range module.units {
		for _, inputRef := range unit.secretInputRefs {
			if _, exists := unit.secretRefs[inputRef]; !exists {
				blockers = append(blockers, executionReadinessBlocker{code: "required-input-missing", refs: []string{module.ref, unit.ref, "input:" + module.id + "/" + inputRef}})
			}
		}
	}
	return blockers, nil
}

func modulePlanInputBlockers(module readinessModule) ([]executionReadinessBlocker, error) {
	planInputs, exists, err := optionalObjectField(module.support, "modules."+module.id+".realizationSupport", "planInputs")
	if err != nil {
		return nil, err
	}
	if !exists {
		for _, unit := range module.units {
			if len(unit.planInputRefs) > 0 || len(unit.planInputs) > 0 {
				return []executionReadinessBlocker{{code: "input-contract-incomplete", refs: []string{module.ref}}}, nil
			}
		}
		return nil, nil
	}
	complete, err := boolFieldDefault(planInputs, "modules."+module.id+".realizationSupport.planInputs", "contractComplete", false)
	if err != nil {
		return nil, err
	}
	var blockers []executionReadinessBlocker
	if !complete {
		blockers = append(blockers, executionReadinessBlocker{code: "input-contract-incomplete", refs: []string{module.ref}})
	}
	requiredInputs, err := stringListField(planInputs, "modules."+module.id+".realizationSupport.planInputs", "requiredRefs", false)
	if err != nil {
		return nil, err
	}
	for _, inputRef := range requiredInputs {
		declared := false
		for _, unit := range module.units {
			if !contains(unit.planInputRefs, inputRef) {
				continue
			}
			declared = true
			if _, present := unit.planInputs[inputRef]; !present {
				blockers = append(blockers, executionReadinessBlocker{code: "required-input-missing", refs: []string{module.ref, unit.ref, "input:" + module.id + "/" + inputRef}})
			}
		}
		if !declared {
			blockers = append(blockers, executionReadinessBlocker{code: "required-input-missing", refs: []string{module.ref, "input:" + module.id + "/" + inputRef}})
		}
	}
	return blockers, nil
}

func moduleArtifactBlockers(module readinessModule, artifactIDs map[string]readinessArtifact, outputRoot string) ([]executionReadinessBlocker, error) {
	artifactSupport, err := objectField(module.support, "modules."+module.id+".realizationSupport", "artifacts")
	if err != nil {
		return nil, err
	}
	requiredArtifacts, err := stringListField(artifactSupport, "modules."+module.id+".realizationSupport.artifacts", "requiredRefs", true)
	if err != nil {
		return nil, err
	}
	bindings, err := objectListField(artifactSupport, "modules."+module.id+".realizationSupport.artifacts", "outputBindings")
	if err != nil {
		return nil, err
	}
	units := indexReadinessModuleUnitOutputs(module)
	bindingBlockers, boundArtifacts, boundOutputs, err := moduleArtifactBindingBlockers(module, bindings, units, artifactIDs, outputRoot)
	if err != nil {
		return nil, err
	}
	blockers, err := missingRequiredModuleArtifactBlockers(module, requiredArtifacts, bindings, units, artifactIDs)
	if err != nil {
		return nil, err
	}
	blockers = append(blockers, bindingBlockers...)
	blockers = append(blockers, unboundModuleArtifactBlockers(module, requiredArtifacts, boundArtifacts, boundOutputs)...)
	return blockers, nil
}

type readinessModuleUnitOutputs struct {
	ref     string
	outputs map[string][]readinessRenderInstanceOutput
}

type readinessArtifactBinding struct {
	artifactRef string
	unitRef     string
	outputRef   string
}

func indexReadinessModuleUnitOutputs(module readinessModule) map[string]readinessModuleUnitOutputs {
	result := make(map[string]readinessModuleUnitOutputs, len(module.units))
	for _, unit := range module.units {
		outputs := make(map[string][]readinessRenderInstanceOutput, len(unit.outputs))
		for _, output := range unit.outputs {
			outputs[output] = nil
		}
		for _, instance := range unit.instances {
			for outputRef, output := range instance.outputs {
				if _, declared := outputs[outputRef]; declared {
					outputs[outputRef] = append(outputs[outputRef], output)
				}
			}
		}
		result[unit.id] = readinessModuleUnitOutputs{ref: unit.ref, outputs: outputs}
	}
	return result
}

func missingRequiredModuleArtifactBlockers(module readinessModule, requiredArtifacts []string, bindings []map[string]any, units map[string]readinessModuleUnitOutputs, artifactIDs map[string]readinessArtifact) ([]executionReadinessBlocker, error) {
	required := make(map[string]struct{}, len(requiredArtifacts))
	for _, artifactRef := range requiredArtifacts {
		required[artifactRef] = struct{}{}
	}
	var blockers []executionReadinessBlocker
	covered := make(map[string]struct{}, len(requiredArtifacts))
	for index, raw := range bindings {
		binding, err := parseReadinessArtifactBinding(module.id, index, raw)
		if err != nil {
			return nil, err
		}
		if _, isRequired := required[binding.artifactRef]; !isRequired {
			continue
		}
		covered[binding.artifactRef] = struct{}{}
		unit, unitExists := units[binding.unitRef]
		outputs, outputExists := unit.outputs[binding.outputRef]
		if !unitExists || !outputExists || len(outputs) == 0 {
			blockers = append(blockers, executionReadinessBlocker{code: "required-artifact-missing", refs: []string{module.ref, moduleArtifactRef(module.id, binding.artifactRef)}})
			continue
		}
		for _, output := range outputs {
			artifact, exists := artifactIDs[output.artifactRef]
			if !exists || !artifact.required {
				blockers = append(blockers, executionReadinessBlocker{code: "required-artifact-missing", refs: []string{module.ref, unit.ref, moduleArtifactRef(module.id, output.artifactRef)}})
			}
		}
	}
	for _, artifactRef := range requiredArtifacts {
		if _, exists := covered[artifactRef]; !exists {
			blockers = append(blockers, executionReadinessBlocker{code: "required-artifact-missing", refs: []string{module.ref, moduleArtifactRef(module.id, artifactRef)}})
		}
	}
	return blockers, nil
}

func moduleArtifactBindingBlockers(module readinessModule, bindings []map[string]any, units map[string]readinessModuleUnitOutputs, artifactIDs map[string]readinessArtifact, outputRoot string) ([]executionReadinessBlocker, map[string]struct{}, map[string]struct{}, error) {
	var blockers []executionReadinessBlocker
	boundArtifacts := make(map[string]struct{}, len(bindings))
	boundOutputs := make(map[string]struct{}, len(bindings))
	for index, raw := range bindings {
		binding, err := parseReadinessArtifactBinding(module.id, index, raw)
		if err != nil {
			return nil, nil, nil, err
		}
		boundArtifacts[binding.artifactRef] = struct{}{}
		boundOutputs[readinessUnitOutputKey(binding.unitRef, binding.outputRef)] = struct{}{}
		if refs := mismatchedArtifactBindingRefs(module, binding, units, artifactIDs, outputRoot); refs != nil {
			blockers = append(blockers, executionReadinessBlocker{code: "artifact-output-mismatch", refs: refs})
		}
	}
	return blockers, boundArtifacts, boundOutputs, nil
}

func parseReadinessArtifactBinding(moduleID string, index int, raw map[string]any) (readinessArtifactBinding, error) {
	bindingPath := fmt.Sprintf("modules.%s.realizationSupport.artifacts.outputBindings[%d]", moduleID, index)
	artifactRef, err := stringField(raw, bindingPath, "artifactRef")
	if err != nil {
		return readinessArtifactBinding{}, err
	}
	unitRef, err := stringField(raw, bindingPath, "unitRef")
	if err != nil {
		return readinessArtifactBinding{}, err
	}
	outputRef, err := stringField(raw, bindingPath, "outputRef")
	if err != nil {
		return readinessArtifactBinding{}, err
	}
	return readinessArtifactBinding{artifactRef: artifactRef, unitRef: unitRef, outputRef: outputRef}, nil
}

func mismatchedArtifactBindingRefs(module readinessModule, binding readinessArtifactBinding, units map[string]readinessModuleUnitOutputs, artifactIDs map[string]readinessArtifact, outputRoot string) []string {
	unit, unitExists := units[binding.unitRef]
	outputs, outputExists := unit.outputs[binding.outputRef]
	if unitExists && outputExists && len(outputs) > 0 {
		for _, output := range outputs {
			artifact, artifactExists := artifactIDs[output.artifactRef]
			if !artifactExists || !artifact.required || artifact.path != path.Join(outputRoot, output.path) {
				return []string{module.ref, unit.ref, moduleArtifactRef(module.id, output.artifactRef)}
			}
		}
		return nil
	}
	refs := []string{module.ref, moduleArtifactRef(module.id, binding.artifactRef)}
	if unitExists {
		return append(refs, unit.ref)
	}
	return append(refs, "unit:"+module.id+"/"+binding.unitRef)
}

func unboundModuleArtifactBlockers(module readinessModule, requiredArtifacts []string, boundArtifacts, boundOutputs map[string]struct{}) []executionReadinessBlocker {
	var blockers []executionReadinessBlocker
	for _, artifactRef := range requiredArtifacts {
		if _, bound := boundArtifacts[artifactRef]; !bound {
			blockers = append(blockers, executionReadinessBlocker{code: "artifact-output-mismatch", refs: []string{module.ref, moduleArtifactRef(module.id, artifactRef)}})
		}
	}
	for _, unit := range module.units {
		for _, outputRef := range unit.outputs {
			if _, bound := boundOutputs[readinessUnitOutputKey(unit.id, outputRef)]; !bound {
				blockers = append(blockers, executionReadinessBlocker{code: "artifact-output-mismatch", refs: []string{module.ref, unit.ref}})
			}
		}
	}
	return blockers
}

func readinessUnitOutputKey(unitRef, outputRef string) string {
	return unitRef + "\x00" + outputRef
}

func moduleApplyBlockers(module readinessModule, evidenceRefs map[string]struct{}) ([]executionReadinessBlocker, error) {
	return realizationApplyBlockers(
		module.level,
		module.ref,
		module.support,
		"modules."+module.id+".realizationSupport",
		"module-apply-support-missing",
		evidenceRefs,
	)
}

func realizationApplyBlockers(level, ref string, support map[string]any, supportPath, missingSupportCode string, evidenceRefs map[string]struct{}) ([]executionReadinessBlocker, error) {
	if level != "apply-ready" {
		return []executionReadinessBlocker{{code: missingSupportCode, refs: []string{ref}}}, nil
	}
	evidenceSupport, err := objectField(support, supportPath, "evidence")
	if err != nil {
		return nil, err
	}
	requiredEvidence, err := stringListField(evidenceSupport, supportPath+".evidence", "requiredRefs", true)
	if err != nil {
		return nil, err
	}
	var blockers []executionReadinessBlocker
	for _, evidenceRef := range requiredEvidence {
		if _, exists := evidenceRefs[evidenceRef]; !exists {
			blockers = append(blockers, executionReadinessBlocker{code: "required-evidence-missing", refs: []string{ref, "evidence:" + evidenceRef}})
		}
	}
	return blockers, nil
}

func normalizeExecutionReadinessBlockers(blockers []executionReadinessBlocker) []executionReadinessBlocker {
	unique := make(map[string]executionReadinessBlocker, len(blockers))
	for _, blocker := range blockers {
		blocker.refs = sortStringsUnique(blocker.refs)
		key := blocker.code + "\x00" + strings.Join(blocker.refs, "\x00")
		unique[key] = blocker
	}
	result := make([]executionReadinessBlocker, 0, len(unique))
	for _, blocker := range unique {
		result = append(result, blocker)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].code != result[j].code {
			return result[i].code < result[j].code
		}
		return strings.Join(result[i].refs, "\x00") < strings.Join(result[j].refs, "\x00")
	})
	return result
}

func executionReadinessPhase(blockers []executionReadinessBlocker) map[string]any {
	status := "ready"
	if len(blockers) > 0 {
		status = "blocked"
	}
	encoded := make([]any, 0, len(blockers))
	for _, blocker := range blockers {
		encoded = append(encoded, map[string]any{"code": blocker.code, "refs": stringSliceAny(blocker.refs)})
	}
	return map[string]any{"status": status, "blockers": encoded}
}
