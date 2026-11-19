package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type moduleOutputOwner struct{ unitID, outputRef string }

func parseModule(raw json.RawMessage, modulePath string, artifacts map[string]artifactContract, outputRoot string) (renderModule, []outputBinding, error) {
	object, err := decodeRawObject(raw, modulePath)
	if err != nil {
		return renderModule{}, nil, err
	}
	moduleID, err := requiredRawString(object, "id", modulePath+".id")
	if err != nil {
		return renderModule{}, nil, err
	}
	runtime, err := parseModuleRuntime(object, modulePath)
	if err != nil {
		return renderModule{}, nil, err
	}
	planOnly, err := optionalRawBool(object, "planOnly", modulePath+".planOnly")
	if err != nil {
		return renderModule{}, nil, err
	}
	if planOnly {
		if err := validatePlanOnlyModule(object, runtime, modulePath); err != nil {
			return renderModule{}, nil, err
		}
		return renderModule{id: moduleID}, nil, nil
	}
	module, unitIDs, outputs, err := parseModuleUnits(object, moduleID, modulePath)
	if err != nil {
		return renderModule{}, nil, err
	}
	accelerator, err := parseModuleAccelerator(object, modulePath)
	if err != nil {
		return renderModule{}, nil, err
	}
	for index := range module.units {
		module.units[index].runtime = runtime
		module.units[index].accelerator = accelerator
	}
	logicalBindings, err := parseModuleBindings(object, module, unitIDs, outputs, modulePath)
	if err != nil {
		return renderModule{}, nil, err
	}
	bindings, err := bindModuleInstances(module, logicalBindings, artifacts, outputRoot, modulePath)
	return module, bindings, err
}

func validatePlanOnlyModule(object map[string]json.RawMessage, runtime moduleRuntimeContract, modulePath string) error {
	if runtime.execution != "contract-handoff" {
		return fail(ErrInvalidPlan, modulePath+".runtime.execution", "plan-only modules require contract-handoff execution")
	}
	units, err := requiredRawArray(object, "renderUnits", modulePath+".renderUnits")
	if err != nil {
		return err
	}
	if len(units) != 0 {
		return fail(ErrInvalidPlan, modulePath+".renderUnits", "plan-only modules must not carry render units")
	}
	realization, err := requiredRawObject(object, "realizationSupport", modulePath+".realizationSupport")
	if err != nil {
		return err
	}
	renderers, err := requiredRawArray(realization, "compatibleRendererRefs", modulePath+".realizationSupport.compatibleRendererRefs")
	if err != nil {
		return err
	}
	if len(renderers) != 0 {
		return fail(ErrInvalidPlan, modulePath+".realizationSupport.compatibleRendererRefs", "plan-only modules must not select renderers")
	}
	artifactSupport, err := requiredRawObject(realization, "artifacts", modulePath+".realizationSupport.artifacts")
	if err != nil {
		return err
	}
	for _, field := range []string{"requiredRefs", "outputBindings", "contracts"} {
		values, err := requiredRawArray(artifactSupport, field, modulePath+".realizationSupport.artifacts."+field)
		if err != nil {
			return err
		}
		if len(values) != 0 {
			return fail(ErrInvalidPlan, modulePath+".realizationSupport.artifacts."+field, "plan-only modules must not claim artifact authority")
		}
	}
	return nil
}

func parseModuleRuntime(object map[string]json.RawMessage, modulePath string) (moduleRuntimeContract, error) {
	raw, exists := object["runtime"]
	if !exists {
		return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime", "is required")
	}
	var decoded rawModuleRuntime
	if err := decodeStrict(raw, &decoded); err != nil {
		return moduleRuntimeContract{}, wrap(ErrInvalidPlan, modulePath+".runtime", "decode module runtime", err)
	}
	if !containsStringValue([]string{"container", "native", "host", "external", "control-plane"}, decoded.Kind) {
		return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.kind", "unsupported module runtime kind %q", decoded.Kind)
	}
	if !containsStringValue([]string{"stackkit", "application-adapter", "selected-paas", "external-control-plane"}, decoded.Delivery) {
		return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.delivery", "unsupported module delivery %q", decoded.Delivery)
	}
	if !containsStringValue([]string{"executable", "contract-handoff"}, decoded.Execution) {
		return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.execution", "unsupported module execution class %q", decoded.Execution)
	}
	runtime := moduleRuntimeContract{execution: decoded.Execution, kind: decoded.Kind, delivery: decoded.Delivery, componentsCanonical: []byte("[]")}
	if decoded.Settings == nil {
		decoded.Settings = map[string]json.RawMessage{}
	}
	settings, err := json.Marshal(decoded.Settings)
	if err != nil {
		return moduleRuntimeContract{}, wrap(ErrInvalidPlan, modulePath+".runtime.settings", "canonicalize runtime settings", err)
	}
	runtime.settingsCanonical = settings
	if decoded.Engine.present {
		if !containsStringValue([]string{"docker", "podman", "systemd", "binary", "api"}, decoded.Engine.value) {
			return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.engine", "unsupported module runtime engine %q", decoded.Engine.value)
		}
		runtime.engine = decoded.Engine.value
	}
	if decoded.Image != nil {
		if strings.TrimSpace(decoded.Image.Ref) == "" || strings.ContainsAny(decoded.Image.Ref, "\r\n\t ") {
			return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.image.ref", "must be a non-empty image reference")
		}
		runtime.imageRef = decoded.Image.Ref
		if decoded.Image.Digest.present {
			if !validSHA256(decoded.Image.Digest.value) {
				return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.image.digest", "must be a lowercase sha256 digest")
			}
			runtime.imageDigest = decoded.Image.Digest.value
		}
	}
	if decoded.EntryComponentRef.present {
		runtime.entryComponentRef = decoded.EntryComponentRef.value
	}
	if decoded.Components != nil {
		canonical, err := json.Marshal(decoded.Components)
		if err != nil {
			return moduleRuntimeContract{}, wrap(ErrInvalidPlan, modulePath+".runtime.components", "canonicalize component graph", err)
		}
		runtime.componentsCanonical = canonical
	}
	if runtime.kind == "container" {
		if runtime.engine != "docker" && runtime.engine != "podman" {
			return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.engine", "container runtime requires docker or podman")
		}
		if runtime.imageRef == "" {
			return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.image", "container runtime requires an image")
		}
	}
	return runtime, nil
}

func containsStringValue(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func parseModuleUnits(object map[string]json.RawMessage, moduleID, modulePath string) (renderModule, map[string]struct{}, map[string]moduleOutputOwner, error) {
	rawUnits, err := requiredRawArray(object, "renderUnits", modulePath+".renderUnits")
	if err != nil {
		return renderModule{}, nil, nil, err
	}
	if len(rawUnits) == 0 {
		return renderModule{}, nil, nil, fail(ErrInvalidPlan, modulePath+".renderUnits", "at least one render unit is required")
	}
	module := renderModule{id: moduleID, units: make([]renderUnitContract, 0, len(rawUnits))}
	unitIDs := make(map[string]struct{}, len(rawUnits))
	outputs := make(map[string]moduleOutputOwner)
	serviceOwners := make(map[string]string)
	for index, rawUnit := range rawUnits {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", modulePath, index)
		unit, err := parseRenderUnit(rawUnit, unitPath)
		if err != nil {
			return renderModule{}, nil, nil, err
		}
		if _, exists := unitIDs[unit.id]; exists {
			return renderModule{}, nil, nil, fail(ErrDuplicate, unitPath+".id", "duplicate render-unit ID %q", unit.id)
		}
		unitIDs[unit.id] = struct{}{}
		for _, endpoint := range unit.serviceEndpoints {
			if previous, exists := serviceOwners[endpoint.serviceRef]; exists {
				return renderModule{}, nil, nil, fail(ErrDuplicate, unitPath+".serviceEndpoints", "service %q is already exported by unit %q", endpoint.serviceRef, previous)
			}
			serviceOwners[endpoint.serviceRef] = unit.id
		}
		for _, output := range unit.outputs {
			if previous, exists := outputs[portableKey(output)]; exists {
				return renderModule{}, nil, nil, fail(ErrDuplicate, unitPath+".outputs", "output %q is already owned by unit %q", output, previous.unitID)
			}
			outputs[portableKey(output)] = moduleOutputOwner{unitID: unit.id, outputRef: output}
		}
		module.units = append(module.units, unit)
	}
	sort.Slice(module.units, func(i, j int) bool { return module.units[i].id < module.units[j].id })
	return module, unitIDs, outputs, nil
}

func parseModuleBindings(object map[string]json.RawMessage, module renderModule, unitIDs map[string]struct{}, outputs map[string]moduleOutputOwner, modulePath string) ([]outputBinding, error) {
	artifactSupport, err := moduleArtifactSupport(object, modulePath)
	if err != nil {
		return nil, err
	}
	requiredRefs, err := requiredStringArray(artifactSupport, "requiredRefs", modulePath+".realizationSupport.artifacts.requiredRefs")
	if err != nil {
		return nil, err
	}
	requiredSet, err := uniqueContractIDSet(requiredRefs, modulePath+".realizationSupport.artifacts.requiredRefs")
	if err != nil {
		return nil, err
	}
	rawBindings, err := requiredRawArray(artifactSupport, "outputBindings", modulePath+".realizationSupport.artifacts.outputBindings")
	if err != nil {
		return nil, err
	}
	if len(rawBindings) == 0 {
		return nil, fail(ErrInvalidPlan, modulePath+".realizationSupport.artifacts.outputBindings", "render units require explicit output bindings")
	}
	bindings := make([]outputBinding, 0, len(rawBindings))
	boundOutputs := make(map[unitOutputKey]struct{}, len(rawBindings))
	boundArtifacts := make(map[string]struct{}, len(rawBindings))
	for index, rawBinding := range rawBindings {
		bindingPath := fmt.Sprintf("%s.realizationSupport.artifacts.outputBindings[%d]", modulePath, index)
		binding, err := parseOutputBinding(rawBinding, module.id, unitIDs, outputs, requiredSet, bindingPath)
		if err != nil {
			return nil, err
		}
		key := unitOutputKey{moduleID: module.id, unitID: binding.unitRef, output: binding.outputRef}
		if _, exists := boundOutputs[key]; exists {
			return nil, fail(ErrDuplicate, bindingPath, "unit output is bound more than once")
		}
		if _, exists := boundArtifacts[binding.artifactID]; exists {
			return nil, fail(ErrDuplicate, bindingPath, "artifact is bound more than once")
		}
		boundOutputs[key], boundArtifacts[binding.artifactID] = struct{}{}, struct{}{}
		bindings = append(bindings, binding)
	}
	if err := requireCompleteModuleBindings(module, requiredSet, boundOutputs, boundArtifacts, modulePath); err != nil {
		return nil, err
	}
	return bindings, nil
}

func moduleArtifactSupport(object map[string]json.RawMessage, modulePath string) (map[string]json.RawMessage, error) {
	realization, err := requiredRawObject(object, "realizationSupport", modulePath+".realizationSupport")
	if err != nil {
		return nil, err
	}
	return requiredRawObject(realization, "artifacts", modulePath+".realizationSupport.artifacts")
}

func parseOutputBinding(raw json.RawMessage, moduleID string, unitIDs map[string]struct{}, outputs map[string]moduleOutputOwner, requiredSet map[string]struct{}, bindingPath string) (outputBinding, error) {
	var decoded rawOutputBinding
	if err := decodeStrict(raw, &decoded); err != nil {
		return outputBinding{}, wrap(ErrInvalidPlan, bindingPath, "decode output binding", err)
	}
	if _, err := validatePortablePath(decoded.OutputRef); err != nil {
		return outputBinding{}, wrap(ErrInvalidPath, bindingPath+".outputRef", "invalid output reference", err)
	}
	if _, exists := unitIDs[decoded.UnitRef]; !exists {
		return outputBinding{}, fail(ErrInvalidPlan, bindingPath+".unitRef", "references undeclared render unit %q", decoded.UnitRef)
	}
	owner, exists := outputs[portableKey(decoded.OutputRef)]
	if !exists || owner.unitID != decoded.UnitRef || owner.outputRef != decoded.OutputRef {
		return outputBinding{}, fail(ErrInvalidPlan, bindingPath+".outputRef", "output %q is not declared by unit %q", decoded.OutputRef, decoded.UnitRef)
	}
	if _, exists := requiredSet[decoded.ArtifactRef]; !exists {
		return outputBinding{}, fail(ErrInvalidPlan, bindingPath+".artifactRef", "artifact %q is not declared in this module's requiredRefs", decoded.ArtifactRef)
	}
	return outputBinding{moduleID: moduleID, unitRef: decoded.UnitRef, artifactID: decoded.ArtifactRef, outputRef: decoded.OutputRef}, nil
}

func requireCompleteModuleBindings(module renderModule, requiredSet map[string]struct{}, boundOutputs map[unitOutputKey]struct{}, boundArtifacts map[string]struct{}, modulePath string) error {
	if len(boundArtifacts) != len(requiredSet) {
		return fail(ErrInvalidPlan, modulePath+".realizationSupport.artifacts", "requiredRefs and outputBindings artifactRefs must be identical")
	}
	for _, unit := range module.units {
		for _, output := range unit.outputs {
			if _, exists := boundOutputs[unitOutputKey{moduleID: module.id, unitID: unit.id, output: output}]; !exists {
				return fail(ErrInvalidPlan, modulePath+".renderUnits", "unit output %s/%s has no output binding", unit.id, output)
			}
		}
	}
	return nil
}

func bindModuleInstances(module renderModule, logicalBindings []outputBinding, artifacts map[string]artifactContract, outputRoot, modulePath string) ([]outputBinding, error) {
	logicalArtifacts := make(map[unitOutputKey]string, len(logicalBindings))
	for _, binding := range logicalBindings {
		logicalArtifacts[unitOutputKey{moduleID: module.id, unitID: binding.unitRef, output: binding.outputRef}] = binding.artifactID
	}
	bindings := make([]outputBinding, 0, len(logicalBindings))
	for _, unit := range module.units {
		for _, instance := range unit.instances {
			for _, output := range instance.outputs {
				logicalKey := unitOutputKey{moduleID: module.id, unitID: unit.id, output: output.ref}
				logicalArtifactID, exists := logicalArtifacts[logicalKey]
				if !exists {
					return nil, fail(ErrInvalidPlan, modulePath+".renderUnits."+unit.id+".instances."+instance.id, "output %q has no logical output binding", output.ref)
				}
				artifact, exists := artifacts[output.artifactID]
				if !exists || !artifact.required {
					return nil, fail(ErrInvalidPlan, modulePath+".renderUnits."+unit.id+".instances."+instance.id, "references undeclared or non-required artifact %q", output.artifactID)
				}
				materializedPath := joinOutputPath(outputRoot, output.path)
				if artifact.path != materializedPath {
					return nil, fail(ErrInvalidPlan, modulePath+".renderUnits."+unit.id+".instances."+instance.id, "artifact %q path %q does not match materialized instance path %q", output.artifactID, artifact.path, materializedPath)
				}
				if instance.scope == "module" && output.artifactID != logicalArtifactID {
					return nil, fail(ErrInvalidPlan, modulePath+".renderUnits."+unit.id+".instances."+instance.id, "module instance output %q must retain logical artifact %q", output.ref, logicalArtifactID)
				}
				bindings = append(bindings, outputBinding{
					moduleID: module.id, unitRef: unit.id, instanceRef: instance.id,
					artifactID: output.artifactID, outputRef: output.ref,
				})
			}
		}
	}
	return bindings, nil
}
