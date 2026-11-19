package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func parseRenderUnit(raw json.RawMessage, unitPath string) (renderUnitContract, error) {
	var decoded rawRenderUnit
	if err := decodeStrict(raw, &decoded); err != nil {
		return renderUnitContract{}, wrap(ErrInvalidPlan, unitPath, "decode render unit", err)
	}
	if err := validateRenderUnitIdentity(decoded, unitPath); err != nil {
		return renderUnitContract{}, err
	}
	inputBindingsCanonical, err := validateRenderUnitInputBindings(decoded, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	valuesCanonical, secretsCanonical, err := validateRenderUnitInputs(decoded, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	planInputRefs, planInputsCanonical, err := validateRenderUnitPlanInputs(decoded, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	outputs, err := validateRenderUnitOutputs(decoded.Outputs, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	placement, providedInterfaces, requiredInterfaces, placementContract, err := validateRenderUnitImplementation(decoded, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	serviceEndpoints, serviceEndpointsCanonical, err := parseServiceEndpoints(decoded.ServiceEndpoints, placementContract, unitPath+".serviceEndpoints")
	if err != nil {
		return renderUnitContract{}, err
	}
	runtimeListenersCanonical, err := parseRuntimeListeners(decoded.RuntimeListeners, placementContract, decoded.ApplyMode, unitPath+".runtimeListeners")
	if err != nil {
		return renderUnitContract{}, err
	}
	providedContracts, err := parseImplementationInterfaces(decoded.ProvidesInterfaces, "provider", unitPath+".providesInterfaces")
	if err != nil {
		return renderUnitContract{}, err
	}
	requiredContracts, err := parseImplementationInterfaces(decoded.RequiresInterfaces, "consumer", unitPath+".requiresInterfaces")
	if err != nil {
		return renderUnitContract{}, err
	}
	instances, err := validateRenderUnitInstances(decoded, outputs, placementContract, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	if err := validateDirectSocketRequirements(requiredContracts, instances, placementContract, unitPath); err != nil {
		return renderUnitContract{}, err
	}
	return renderUnitContract{
		id: decoded.ID, kind: decoded.Kind, rendererRef: decoded.RendererRef,
		templateRef: decoded.TemplateRef, version: decoded.Version, contractHash: decoded.ContractHash,
		publicInputRefs: append([]string(nil), decoded.PublicInputRefs...), secretInputRefs: append([]string(nil), decoded.SecretInputRefs...),
		planInputRefs: append([]string(nil), planInputRefs...),
		siteRefs:      append([]string(nil), decoded.SiteRefs...), nodeRefs: append([]string(nil), decoded.NodeRefs...),
		valuesCanonical: valuesCanonical, secretsCanonical: secretsCanonical, planInputsCanonical: planInputsCanonical,
		inputBindingsCanonical: inputBindingsCanonical, placementCanonical: placement,
		serviceEndpointsCanonical:   serviceEndpointsCanonical,
		runtimeListenersCanonical:   runtimeListenersCanonical,
		providedInterfacesCanonical: providedInterfaces, requiredInterfacesCanonical: requiredInterfaces,
		serviceEndpoints:   serviceEndpoints,
		providedInterfaces: providedContracts, requiredInterfaces: requiredContracts,
		outputs: outputs, instances: instances,
	}, nil
}

//nolint:gocyclo // Closed binding validation intentionally checks every source/type/cardinality/value branch together.
func validateRenderUnitInputBindings(unit rawRenderUnit, unitPath string) ([]byte, error) {
	if unit.InputBindings == nil {
		return nil, fail(ErrInvalidPlan, unitPath+".inputBindings", "inputBindings is mandatory")
	}
	if len(unit.SecretInputBindings) != 0 {
		return nil, fail(ErrInvalidPlan, unitPath+".secretInputBindings", "renderer model does not accept unresolved secret input binding authority")
	}
	publicSet, err := uniqueContractIDSet(unit.PublicInputRefs, unitPath+".publicInputRefs")
	if err != nil {
		return nil, err
	}
	secretSet, err := uniqueContractIDSet(unit.SecretInputRefs, unitPath+".secretInputRefs")
	if err != nil {
		return nil, err
	}
	planSet := make(map[string]struct{}, len(unit.PlanInputRefs))
	for _, ref := range unit.PlanInputRefs {
		planSet[ref] = struct{}{}
	}
	bindings := make([]rawModuleRenderInputBinding, 0, len(unit.InputBindings))
	seen := make(map[string]struct{}, len(unit.InputBindings))
	for index, raw := range unit.InputBindings {
		path := fmt.Sprintf("%s.inputBindings[%d]", unitPath, index)
		var binding rawModuleRenderInputBinding
		if err := decodeStrict(raw, &binding); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode input binding", err)
		}
		if _, exists := publicSet[binding.TargetRef]; !exists {
			return nil, fail(ErrInvalidPlan, path+".targetRef", "bound target is not a declared public input")
		}
		if _, exists := secretSet[binding.TargetRef]; exists {
			return nil, fail(ErrInvalidPlan, path+".targetRef", "bound target aliases a secret input")
		}
		if _, exists := planSet[binding.TargetRef]; exists {
			return nil, fail(ErrInvalidPlan, path+".targetRef", "bound target aliases a compiler plan input")
		}
		if _, duplicate := seen[binding.TargetRef]; duplicate {
			return nil, fail(ErrDuplicate, path+".targetRef", "duplicate bound target")
		}
		seen[binding.TargetRef] = struct{}{}
		hasDefault := len(binding.DefaultValue) != 0
		if binding.Required == hasDefault {
			return nil, fail(ErrInvalidPlan, path+".defaultValue", "required bindings forbid defaults and optional bindings require one")
		}
		switch binding.SourceRef {
		case "identity.deviceEnrollment":
			if binding.ValueType != "device-enrollment-public-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identity.deviceEnrollment has an invalid type or cardinality")
			}
		case "identityTrust.homeDeviceAuthority":
			if binding.ValueType != "home-device-authority-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identityTrust.homeDeviceAuthority has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Home device-authority value is missing")
			}
			if _, err := decodeHomeDeviceAuthorityInput(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "identityTrust.basementVerification":
			if binding.ValueType != "basement-identity-verification-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identityTrust.basementVerification has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Basement identity-verification value is missing")
			}
		case "identityTrust.cloudAuthority":
			if binding.ValueType != "cloud-identity-authority-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identityTrust.cloudAuthority has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Cloud identity-authority value is missing")
			}
		case "identityTrust.modernHomeAuthority":
			if binding.ValueType != "modern-home-identity-authority-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identityTrust.modernHomeAuthority has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Modern Home identity-authority value is missing")
			}
		case "identityTrust.modernCloudVerification":
			if binding.ValueType != "modern-cloud-identity-verification-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identityTrust.modernCloudVerification has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Modern Cloud identity-verification value is missing")
			}
		case "access.homeEnforcement":
			if binding.ValueType != "home-access-enforcement-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "access.homeEnforcement has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Home access-enforcement value is missing")
			}
		case "localAutonomy.policy":
			if binding.ValueType != "local-autonomy-policy-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "localAutonomy.policy has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound local-autonomy policy value is missing")
			}
		case "network.routes":
			if binding.ValueType != "authority-bound-service-route-list-v4" || binding.Cardinality != "list" {
				return nil, fail(ErrInvalidPlan, path, "network.routes has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound network.routes value is missing")
			}
			if err := validatePublicServiceRouteListV4(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "network.moduleRoute":
			if binding.ValueType != "authority-bound-module-route-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "network.moduleRoute has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound network.moduleRoute value is missing")
			}
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				if binding.Required || !bytes.Equal(bytes.TrimSpace(binding.DefaultValue), []byte("null")) {
					return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "null module route requires the exact optional null default")
				}
				break
			}
			routeList := append([]byte{'['}, value...)
			routeList = append(routeList, ']')
			if err := validatePublicServiceRouteListV4(routeList, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "network.cloudHostSecurity":
			if binding.ValueType != "cloud-host-security-policy-v2" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "network.cloudHostSecurity has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound network.cloudHostSecurity value is missing")
			}
			if _, err := decodeCloudHostSecurityNetworkInput(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "host.bootstrapRuntime":
			if binding.ValueType != "host-bootstrap-runtime-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "host.bootstrapRuntime has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound host.bootstrapRuntime value is missing")
			}
			if _, err := decodeCoreHostBootstrapRuntime(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "storage.hostRoots":
			if binding.ValueType != "host-storage-roots-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "storage.hostRoots has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound storage.hostRoots value is missing")
			}
			if _, err := decodeCoreHostStorageRoots(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "storage.backupRoot":
			if binding.ValueType != "local-backup-root-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "storage.backupRoot has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound storage.backupRoot value is missing")
			}
			if _, err := decodeHomeBackupRoot(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "backup.localKopiaSource":
			if binding.ValueType != "local-kopia-backup-source-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "backup.localKopiaSource has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound backup.localKopiaSource value is missing")
			}
		case "modules.computeProfileBinding":
			if binding.ValueType != "module-compute-profile-v1" || binding.Cardinality != "single" || !binding.Required || len(binding.DefaultValue) != 0 {
				return nil, fail(ErrInvalidPlan, path, "compute profile requires the exact required compiler binding")
			}
			if err := validateModuleComputeProfileInput(unit.Values[binding.TargetRef]); err != nil {
				return nil, wrap(ErrInvalidPlan, path, "validate compiler-bound compute profile", err)
			}
		case workloadCompanionsSourceRef:
			if binding.ValueType != workloadCompanionsValueType || binding.Cardinality != "list" {
				return nil, fail(ErrInvalidPlan, path, "workloads.companions has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound workloads.companions value is missing")
			}
			if _, err := decodeWorkloadCompanions(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		default:
			return nil, fail(ErrInvalidPlan, path+".sourceRef", "unsupported resolved-plan input source")
		}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].TargetRef < bindings[j].TargetRef })
	canonical, err := json.Marshal(bindings)
	if err != nil {
		return nil, wrap(ErrInvalidPlan, unitPath+".inputBindings", "canonicalize input bindings", err)
	}
	return canonical, nil
}

func validateRenderUnitIdentity(unit rawRenderUnit, unitPath string) error {
	if strings.TrimSpace(unit.TemplateRef) == "" || strings.ContainsAny(unit.TemplateRef, "\r\n\t") {
		return fail(ErrInvalidPlan, unitPath+".templateRef", "a non-empty template reference is required")
	}
	if strings.TrimSpace(unit.Version) == "" {
		return fail(ErrInvalidPlan, unitPath+".version", "an exact render-unit version is required")
	}
	if !validSHA256(unit.ContractHash) {
		return fail(ErrInvalidPlan, unitPath+".contractHash", "must be a lowercase sha256 digest")
	}
	for index, target := range unit.CompatibleTargets {
		if !oneOf(target, "compose", "opentofu", "terramate") {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.compatibleTargets[%d]", unitPath, index), "unsupported generation target %q", target)
		}
	}
	return nil
}

func validateRenderUnitInputs(unit rawRenderUnit, unitPath string) ([]byte, []byte, error) {
	if unit.PublicInputRefs == nil || unit.SecretInputRefs == nil || unit.Values == nil || unit.SecretRefs == nil {
		return nil, nil, fail(ErrInvalidPlan, unitPath, "publicInputRefs, secretInputRefs, values, and secretRefs are mandatory")
	}
	publicSet, err := uniqueContractIDSet(unit.PublicInputRefs, unitPath+".publicInputRefs")
	if err != nil {
		return nil, nil, err
	}
	secretSet, err := uniqueContractIDSet(unit.SecretInputRefs, unitPath+".secretInputRefs")
	if err != nil {
		return nil, nil, err
	}
	nullable := make(map[string]struct{})
	for _, raw := range unit.InputBindings {
		var binding rawModuleRenderInputBinding
		if err := decodeStrict(raw, &binding); err == nil && !binding.Required &&
			bytes.Equal(bytes.TrimSpace(binding.DefaultValue), []byte("null")) {
			nullable[binding.TargetRef] = struct{}{}
		}
	}
	if err := requireObjectKeysSubsetAllowingNull(unit.Values, publicSet, nullable, unitPath+".values"); err != nil {
		return nil, nil, err
	}
	if err := requireCompleteSecretRefs(unit.SecretRefs, secretSet, unitPath+".secretRefs"); err != nil {
		return nil, nil, err
	}
	values, err := canonicalObject(unit.Values)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, unitPath+".values", "canonicalize public inputs", err)
	}
	secrets, err := canonicalObject(unit.SecretRefs)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, unitPath+".secretRefs", "canonicalize secret references", err)
	}
	return values, secrets, nil
}

func validateRenderUnitPlanInputs(unit rawRenderUnit, unitPath string) ([]string, []byte, error) {
	if unit.PlanInputRefs == nil || unit.PlanInputs == nil {
		return nil, nil, fail(ErrInvalidPlan, unitPath, "planInputRefs and planInputs are mandatory")
	}
	declared := make(map[string]struct{}, len(unit.PlanInputRefs))
	refs := append([]string(nil), unit.PlanInputRefs...)
	for index, ref := range refs {
		if _, allowed := allowedRendererPlanInputRefs[ref]; !allowed {
			return nil, nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.planInputRefs[%d]", unitPath, index), "unsupported compiler plan input %q", ref)
		}
		if _, duplicate := declared[ref]; duplicate {
			return nil, nil, fail(ErrDuplicate, fmt.Sprintf("%s.planInputRefs[%d]", unitPath, index), "duplicate plan input %q", ref)
		}
		declared[ref] = struct{}{}
	}
	for _, ref := range append(append([]string(nil), unit.PublicInputRefs...), unit.SecretInputRefs...) {
		if _, conflict := declared[ref]; conflict {
			return nil, nil, fail(ErrInvalidPlan, unitPath+".planInputRefs", "compiler plan input %q aliases a public or secret input", ref)
		}
	}
	if err := requireObjectKeysSubset(unit.PlanInputs, declared, unitPath+".planInputs"); err != nil {
		return nil, nil, err
	}
	for ref := range declared {
		if _, exists := unit.PlanInputs[ref]; !exists {
			return nil, nil, fail(ErrInvalidPlan, unitPath+".planInputs", "declared plan input %q is missing", ref)
		}
	}
	canonical, err := canonicalObject(unit.PlanInputs)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, unitPath+".planInputs", "canonicalize compiler plan inputs", err)
	}
	sort.Strings(refs)
	return refs, canonical, nil
}

var allowedRendererPlanInputRefs = map[string]struct{}{
	"backupPolicy": {},
	"stackId":      {}, "kit": {}, "sites": {}, "controlPlane": {},
	"bridge": {}, "bridgePublications": {}, "bridgeOriginMTLS": {}, "identity": {}, "data": {}, "failurePolicy": {},
	"federationPolicy": {}, "federationLinkPolicy": {}, "federationControlActions": {}, "federationBackupPolicy": {}, "federationObservability": {},
	"identityTrust": {}, "localReachability": {}, "homeLANDiscovery": {},
	"homeAccessRequirements": {}, "externalHomeAccessBindings": {},
	"backupTargetRequirements": {}, "externalBackupTargetBindings": {},
	"homeBackupTargetRequirements": {}, "externalHomeBackupTargetBindings": {},
	"homeAccessHandoff": {}, "homeOffsiteBackup": {}, "cloudOffsiteBackup": {},
	"federationLinkRequirements": {}, "externalFederationLinkBindings": {},
	"availability":  {},
	"moduleTargets": {}, "moduleCapabilities": {}, "hostRuntimePolicy": {},
	"storagePolicy": {}, "localNetworkPolicy": {}, "cloudNetworkPolicy": {}, "publicEdge": {}, "publicTLS": {},
	"internalPKI": {}, "cloudAdminMesh": {},
}

func requireCompleteSecretRefs(refs map[string]json.RawMessage, declared map[string]struct{}, valuePath string) error {
	if err := requireObjectKeysSubset(refs, declared, valuePath); err != nil {
		return err
	}
	if len(refs) != len(declared) {
		return fail(ErrInvalidPlan, valuePath, "every declared secret input requires an opaque secret reference")
	}
	for key := range declared {
		if _, exists := refs[key]; !exists {
			return fail(ErrInvalidPlan, valuePath, "declared secret input %q has no opaque reference", key)
		}
	}
	return requireOpaqueSecretReferences(refs, valuePath)
}

func validateRenderUnitOutputs(declared []string, unitPath string) ([]string, error) {
	if len(declared) == 0 {
		return nil, fail(ErrInvalidPlan, unitPath+".outputs", "at least one output is required")
	}
	outputs := append([]string(nil), declared...)
	seen := make(map[string]struct{}, len(outputs))
	for index, output := range outputs {
		if _, err := validatePortablePath(output); err != nil {
			return nil, wrap(ErrInvalidPath, fmt.Sprintf("%s.outputs[%d]", unitPath, index), "invalid render output", err)
		}
		key := portableKey(output)
		if _, exists := seen[key]; exists {
			return nil, fail(ErrDuplicate, fmt.Sprintf("%s.outputs[%d]", unitPath, index), "duplicate output %q", output)
		}
		seen[key] = struct{}{}
	}
	sort.Strings(outputs)
	return outputs, nil
}
