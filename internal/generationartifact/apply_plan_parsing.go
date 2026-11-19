package generationartifact

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type applyModule struct {
	id, version, contractHash, providerRef, providerVersion, providerContractHash, executionClass, runtimeKind, runtimeDelivery, runtimeEngine, imageRef, imageDigest string
	runtimeAdapterID, runtimeAdapterAgentID, runtimeAdapterAgentRef                                                                                                   string
	runtimeAdapterAgentRefs                                                                                                                                           []string
	runtimeAdapterCapabilities                                                                                                                                        []string
	evidenceRefs, provides                                                                                                                                            []string
	siteRefs, nodeRefs                                                                                                                                                []string
	units                                                                                                                                                             []applyUnit
}

type applyProviderContract struct {
	version      string
	contractHash string
}

type applyUnit struct {
	id, contractHash string
	applyMode        string
	planInputRefs    []string
	secretRefs       map[string]string
	secretBindings   map[string]applySecretInputBinding
	instances        []applyInstance
	daemonBindings   map[string][]ApplyDaemonRequirement
}

type applySecretInputBinding struct {
	capabilityRef string
	key           string
}

type applyInstance struct {
	id, siteRef, nodeRef string
	outputs              []applyInstanceOutput
}

type applyInstanceOutput struct {
	artifactRef, outputRef string
}

func parseApplyProviderContracts(plan resolvedplan.ResolvedPlan) (map[string]applyProviderContract, error) {
	rawProviders, ok := plan["providers"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.providers", "must be an array")
	}
	result := make(map[string]applyProviderContract, len(rawProviders))
	for index, raw := range rawProviders {
		path := fmt.Sprintf("resolvedPlan.providers[%d]", index)
		provider, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(provider, "id", path+".id")
		if err != nil {
			return nil, err
		}
		contractHash, err := requiredString(provider, "contractHash", path+".contractHash")
		if err != nil {
			return nil, err
		}
		version, err := requiredString(provider, "version", path+".version")
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fail(ErrInvalidPlan, path+".id", "duplicate provider %q", id)
		}
		result[id] = applyProviderContract{version: version, contractHash: contractHash}
	}
	return result, nil
}

func parseApplyModules(plan resolvedplan.ResolvedPlan, providerContracts map[string]applyProviderContract) (map[string]applyModule, error) {
	rawModules, ok := plan["modules"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.modules", "must be an array")
	}
	result := make(map[string]applyModule, len(rawModules))
	for index, raw := range rawModules {
		path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		module, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(module, "id", path+".id")
		if err != nil {
			return nil, err
		}
		providerRef, err := requiredString(module, "providerRef", path+".providerRef")
		if err != nil {
			return nil, err
		}
		contractHash, err := requiredString(module, "contractHash", path+".contractHash")
		if err != nil {
			return nil, err
		}
		version, err := requiredString(module, "version", path+".version")
		if err != nil {
			return nil, err
		}
		runtimeContract, err := requiredObject(module, "runtime", path+".runtime")
		if err != nil {
			return nil, err
		}
		providerContract, exists := providerContracts[providerRef]
		if !exists {
			return nil, fail(ErrInvalidPlan, path+".providerRef", "provider %q is absent", providerRef)
		}
		parsed := applyModule{
			id: id, version: version, contractHash: contractHash, providerRef: providerRef,
			providerVersion: providerContract.version, providerContractHash: providerContract.contractHash,
		}
		parsed.executionClass = ApplyExecutionClassExecutable
		if rawExecution, exists := runtimeContract["execution"]; exists {
			execution, ok := rawExecution.(string)
			if !ok || execution != ApplyExecutionClassExecutable && execution != ApplyExecutionClassContractHandoff {
				return nil, fail(ErrInvalidPlan, path+".runtime.execution", "must be executable or contract-handoff")
			}
			parsed.executionClass = execution
		}
		if parsed.runtimeKind, err = requiredString(runtimeContract, "kind", path+".runtime.kind"); err != nil {
			return nil, err
		}
		if parsed.runtimeDelivery, err = requiredString(runtimeContract, "delivery", path+".runtime.delivery"); err != nil {
			return nil, err
		}
		parsed.runtimeEngine, _ = runtimeContract["engine"].(string)
		if runtimeAdapter, ok := module["runtimeAdapter"].(map[string]any); ok {
			if parsed.runtimeAdapterID, err = requiredString(runtimeAdapter, "id", path+".runtimeAdapter.id"); err != nil {
				return nil, err
			}
			if parsed.runtimeAdapterAgentRefs, err = applyStringList(runtimeAdapter, "agentRefs", path+".runtimeAdapter.agentRefs"); err != nil {
				return nil, err
			}
			if parsed.runtimeAdapterCapabilities, err = applyStringList(runtimeAdapter, "capabilities", path+".runtimeAdapter.capabilities"); err != nil {
				return nil, err
			}
		}
		if runtimeAdapterAgent, ok := module["runtimeAdapterAgent"].(map[string]any); ok {
			if parsed.runtimeAdapterAgentID, err = requiredString(runtimeAdapterAgent, "id", path+".runtimeAdapterAgent.id"); err != nil {
				return nil, err
			}
			if parsed.runtimeAdapterAgentRef, err = requiredString(runtimeAdapterAgent, "adapterRef", path+".runtimeAdapterAgent.adapterRef"); err != nil {
				return nil, err
			}
		}
		if image, ok := runtimeContract["image"].(map[string]any); ok {
			parsed.imageRef, _ = image["ref"].(string)
			parsed.imageDigest, _ = image["digest"].(string)
		}
		parsed.siteRefs, err = applyStringList(module, "siteRefs", path+".siteRefs")
		if err != nil {
			return nil, err
		}
		parsed.nodeRefs, err = applyStringList(module, "nodeRefs", path+".nodeRefs")
		if err != nil {
			return nil, err
		}
		parsed.evidenceRefs, err = applyRealizationEvidenceRefs(module, path)
		if err != nil {
			return nil, err
		}
		parsed.provides, err = applyStringList(module, "provides", path+".provides")
		if err != nil {
			return nil, err
		}
		parsed.units, err = parseApplyUnits(module, path)
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fail(ErrInvalidPlan, path+".id", "duplicate module %q", id)
		}
		result[id] = parsed
	}
	return result, nil
}

func parseApplyUnits(module map[string]any, modulePath string) ([]applyUnit, error) {
	rawUnits, ok := module["renderUnits"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, modulePath+".renderUnits", "must be an array")
	}
	units := make([]applyUnit, 0, len(rawUnits))
	for index, raw := range rawUnits {
		path := fmt.Sprintf("%s.renderUnits[%d]", modulePath, index)
		unit, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(unit, "id", path+".id")
		if err != nil {
			return nil, err
		}
		contractHash, err := requiredString(unit, "contractHash", path+".contractHash")
		if err != nil {
			return nil, err
		}
		applyMode := "runtime"
		if _, exists := unit["applyMode"]; exists {
			applyMode, err = requiredString(unit, "applyMode", path+".applyMode")
			if err != nil {
				return nil, err
			}
		}
		if applyMode != "runtime" && applyMode != ApplyExecutionClassArtifactOnly {
			return nil, fail(ErrInvalidPlan, path+".applyMode", "must be runtime or artifact-only")
		}
		secretRefs, err := applyStringMap(unit, "secretRefs", path+".secretRefs")
		if err != nil {
			return nil, err
		}
		secretBindings, err := parseApplySecretInputBindings(unit, secretRefs, path)
		if err != nil {
			return nil, err
		}
		planInputRefs, err := applyStringList(unit, "planInputRefs", path+".planInputRefs")
		if err != nil {
			return nil, err
		}
		instances, err := parseApplyInstances(unit, path)
		if err != nil {
			return nil, err
		}
		daemons, err := parseApplyDaemonBindings(unit, path)
		if err != nil {
			return nil, err
		}
		units = append(units, applyUnit{id: id, contractHash: contractHash, applyMode: applyMode, planInputRefs: planInputRefs, secretRefs: secretRefs, secretBindings: secretBindings, instances: instances, daemonBindings: daemons})
	}
	return units, nil
}

func parseApplySecretInputBindings(unit map[string]any, secretRefs map[string]string, path string) (map[string]applySecretInputBinding, error) {
	raw, ok := unit["secretInputBindings"].(map[string]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, path+".secretInputBindings", "must be an object")
	}
	result := make(map[string]applySecretInputBinding, len(raw))
	for targetRef, rawBinding := range raw {
		bindingPath := path + ".secretInputBindings." + targetRef
		if _, declared := secretRefs[targetRef]; !declared {
			return nil, fail(ErrInvalidPlan, bindingPath, "target is not a materialized secret input")
		}
		binding, ok := rawBinding.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, bindingPath, "must be an object")
		}
		source, err := requiredString(binding, "source", bindingPath+".source")
		if err != nil {
			return nil, err
		}
		if source != "capability-secret" {
			return nil, fail(ErrInvalidPlan, bindingPath+".source", "unsupported secret source %q", source)
		}
		capabilityRef, err := requiredString(binding, "capabilityRef", bindingPath+".capabilityRef")
		if err != nil {
			return nil, err
		}
		key, err := requiredString(binding, "key", bindingPath+".key")
		if err != nil {
			return nil, err
		}
		result[targetRef] = applySecretInputBinding{capabilityRef: capabilityRef, key: key}
	}
	return result, nil
}

func parseApplyInstances(unit map[string]any, unitPath string) ([]applyInstance, error) {
	rawInstances, ok := unit["instances"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, unitPath+".instances", "must be an array")
	}
	instances := make([]applyInstance, 0, len(rawInstances))
	for index, raw := range rawInstances {
		path := fmt.Sprintf("%s.instances[%d]", unitPath, index)
		instance, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(instance, "id", path+".id")
		if err != nil {
			return nil, err
		}
		siteRef, _ := instance["siteRef"].(string)
		nodeRef, _ := instance["nodeRef"].(string)
		rawOutputs, ok := instance["outputs"].([]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path+".outputs", "must be an array")
		}
		outputs := make([]applyInstanceOutput, 0, len(rawOutputs))
		for outputIndex, rawOutput := range rawOutputs {
			outputPath := fmt.Sprintf("%s.outputs[%d]", path, outputIndex)
			output, ok := rawOutput.(map[string]any)
			if !ok {
				return nil, fail(ErrInvalidPlan, outputPath, "must be an object")
			}
			artifactRef, err := requiredString(output, "artifactRef", outputPath+".artifactRef")
			if err != nil {
				return nil, err
			}
			outputRef, err := requiredString(output, "ref", outputPath+".ref")
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, applyInstanceOutput{artifactRef: artifactRef, outputRef: outputRef})
		}
		instances = append(instances, applyInstance{id: id, siteRef: siteRef, nodeRef: nodeRef, outputs: outputs})
	}
	return instances, nil
}

func parseApplyDaemonBindings(unit map[string]any, unitPath string) (map[string][]ApplyDaemonRequirement, error) {
	result := map[string][]ApplyDaemonRequirement{}
	rawBindings, ok := unit["daemonBindings"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, unitPath+".daemonBindings", "must be an array")
	}
	for index, raw := range rawBindings {
		path := fmt.Sprintf("%s.daemonBindings[%d]", unitPath, index)
		binding, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		nodeRef, err := requiredString(binding, "nodeRef", path+".nodeRef")
		if err != nil {
			return nil, err
		}
		requirement := ApplyDaemonRequirement{}
		if requirement.DaemonRef, err = requiredString(binding, "daemonRef", path+".daemonRef"); err != nil {
			return nil, err
		}
		if requirement.InstanceRef, err = requiredString(binding, "instanceRef", path+".instanceRef"); err != nil {
			return nil, err
		}
		if requirement.Engine, err = requiredString(binding, "engine", path+".engine"); err != nil {
			return nil, err
		}
		if requirement.SocketPath, err = requiredString(binding, "socketPath", path+".socketPath"); err != nil {
			return nil, err
		}
		result[nodeRef] = append(result[nodeRef], requirement)
	}
	return result, nil
}

func applyRealizationEvidenceRefs(owner map[string]any, path string) ([]string, error) {
	support, err := requiredObject(owner, "realizationSupport", path+".realizationSupport")
	if err != nil {
		return nil, err
	}
	evidence, err := requiredObject(support, "evidence", path+".realizationSupport.evidence")
	if err != nil {
		return nil, err
	}
	return applyStringList(evidence, "requiredRefs", path+".realizationSupport.evidence.requiredRefs")
}

func applyStringList(object map[string]any, field, path string) ([]string, error) {
	raw, ok := object[field].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, path, "must be an array")
	}
	result := make([]string, 0, len(raw))
	for index, value := range raw {
		item, ok := value.(string)
		if !ok || item == "" {
			return nil, fail(ErrInvalidPlan, fmt.Sprintf("%s[%d]", path, index), "must be a non-empty string")
		}
		result = append(result, item)
	}
	return result, nil
}

func applyStringMap(object map[string]any, field, path string) (map[string]string, error) {
	raw, ok := object[field].(map[string]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, path, "must be an object")
	}
	result := make(map[string]string, len(raw))
	for key, value := range raw {
		item, ok := value.(string)
		if !ok || item == "" {
			return nil, fail(ErrInvalidPlan, path+"."+key, "must be a non-empty string")
		}
		result[key] = item
	}
	return result, nil
}
