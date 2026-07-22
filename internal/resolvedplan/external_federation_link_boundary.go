package resolvedplan

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"time"
)

const (
	federationLinkRequirementAPIVersion = "stackkit.federation-link-requirement/v1"
	externalFederationLinkAPIVersion    = "stackkit.external-federation-link-binding/v1"
	federationLinkCapability            = "inter-site-link"
	maxExternalFederationLinkValidity   = 24 * time.Hour
)

var (
	federationLinkBindingRefPattern = regexp.MustCompile(`^federation-link-binding://sha256/[a-f0-9]{64}$`)
	federationLinkFabricRefPattern  = regexp.MustCompile(`^federation-link-fabric://sha256/[a-f0-9]{64}$`)
	federationLinkCustodyRefPattern = regexp.MustCompile(`^federation-link-custody-attestation://sha256/[a-f0-9]{64}$`)
)

func ComputeFederationLinkRequirementHash(requirement FederationLinkRequirement) (string, error) {
	clone, err := cloneObject(map[string]any(requirement), false)
	if err != nil {
		return "", fmt.Errorf("clone federation link requirement: %w", err)
	}
	delete(clone, "requirementsHash")
	return canonicalHash(clone, false)
}

func ComputeExternalFederationLinkBindingHash(binding ExternalFederationLinkBinding) (string, error) {
	clone, err := cloneObject(map[string]any(binding), false)
	if err != nil {
		return "", fmt.Errorf("clone external federation link binding: %w", err)
	}
	delete(clone, "bindingHash")
	return canonicalHash(clone, false)
}

func buildFederationLinkProjection(spec *specView, specHash string, sites, nodes, capabilities, modules []any, bridge map[string]any) (map[string]any, map[string]any, error) {
	requirements, err := buildFederationLinkRequirements(spec.stackID, specHash, sites, nodes, capabilities, modules, bridge)
	if err != nil {
		return nil, nil, err
	}
	bindings, err := projectExternalFederationLinkBindings(spec.originalInventory, requirements)
	if err != nil {
		return nil, nil, err
	}
	return requirements, bindings, nil
}

func buildFederationLinkRequirements(stackID, specHash string, sites, nodes, capabilities, modules []any, bridge map[string]any) (map[string]any, error) {
	if bridge == nil {
		return map[string]any{}, nil
	}
	siteKinds := map[string]string{}
	for index, raw := range sites {
		site, err := asObject(raw, fmt.Sprintf("resolvedPlan.sites[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "kind")
		if err != nil {
			return nil, err
		}
		siteKinds[id] = kind
	}
	nodeSites := map[string]string{}
	for index, raw := range nodes {
		node, err := asObject(raw, fmt.Sprintf("resolvedPlan.nodes[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(node, fmt.Sprintf("resolvedPlan.nodes[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		siteRef, err := stringField(node, fmt.Sprintf("resolvedPlan.nodes[%d]", index), "siteRef")
		if err != nil {
			return nil, err
		}
		nodeSites[id] = siteRef
	}
	var capability map[string]any
	for index, raw := range capabilities {
		candidate, err := asObject(raw, fmt.Sprintf("resolvedPlan.capabilities[%d]", index))
		if err != nil {
			return nil, err
		}
		if candidate["id"] == federationLinkCapability {
			capability = candidate
		}
	}
	var linkModule map[string]any
	for index, raw := range modules {
		path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		module, err := asObject(raw, path)
		if err != nil {
			return nil, err
		}
		provides, err := stringListField(module, path, "provides", false)
		if err != nil {
			return nil, err
		}
		if slices.Contains(provides, federationLinkCapability) {
			if linkModule != nil {
				return nil, fail(ErrContractConflict, path+".provides", "federation link capability has multiple runtime modules")
			}
			linkModule = module
		}
	}
	if capability == nil && linkModule == nil {
		return map[string]any{}, nil
	}
	if capability == nil || linkModule == nil {
		return nil, fail(ErrUnrealizedCapability, "resolvedPlan.capabilities."+federationLinkCapability, "selected federation link capability lacks one exact runtime module")
	}
	ownerRef, err := stringField(capability, "resolvedPlan.capabilities."+federationLinkCapability, "providerRef")
	if err != nil {
		return nil, err
	}
	contractHash, err := stringField(capability, "resolvedPlan.capabilities."+federationLinkCapability, "contractHash")
	if err != nil {
		return nil, err
	}
	siteRefs, err := stringListField(linkModule, "resolvedPlan.modules.federation-link", "siteRefs", true)
	if err != nil {
		return nil, err
	}
	nodeRefs, err := stringListField(linkModule, "resolvedPlan.modules.federation-link", "nodeRefs", true)
	if err != nil {
		return nil, err
	}
	sort.Strings(siteRefs)
	sort.Strings(nodeRefs)
	homeSiteRefs, cloudSiteRefs := []string{}, []string{}
	for _, siteRef := range siteRefs {
		switch siteKinds[siteRef] {
		case "home":
			homeSiteRefs = append(homeSiteRefs, siteRef)
		case "cloud":
			cloudSiteRefs = append(cloudSiteRefs, siteRef)
		default:
			return nil, fail(ErrProfileMismatch, "resolvedPlan.modules.federation-link.siteRefs", "federation link targets unsupported Site %q", siteRef)
		}
	}
	if len(homeSiteRefs) == 0 || len(cloudSiteRefs) == 0 {
		return nil, fail(ErrProfileMismatch, "resolvedPlan.modules.federation-link.siteRefs", "federation link requires at least one exact Home and Cloud Site")
	}
	targetNodes := make([]any, 0, len(nodeRefs))
	for _, nodeRef := range nodeRefs {
		siteRef, ok := nodeSites[nodeRef]
		if !ok || !slices.Contains(siteRefs, siteRef) {
			return nil, fail(ErrUnresolvedPlacement, "resolvedPlan.modules.federation-link.nodeRefs", "node %q is outside the exact federation Site set", nodeRef)
		}
		targetNodes = append(targetNodes, map[string]any{"siteRef": siteRef, "nodeRef": nodeRef})
	}
	bridgeHash, err := canonicalHash(bridge, false)
	if err != nil {
		return nil, err
	}
	overlay, err := objectField(bridge, "resolvedPlan.bridge", "overlay")
	if err != nil {
		return nil, err
	}
	trafficMode, err := stringField(overlay, "resolvedPlan.bridge.overlay", "trafficMode")
	if err != nil {
		return nil, err
	}
	requirement := FederationLinkRequirement{
		"apiVersion": federationLinkRequirementAPIVersion, "kind": "FederationLinkRequirement",
		"stackId": stackID, "capabilityRef": federationLinkCapability,
		"contractOwnerRef": ownerRef, "capabilityContractHash": contractHash,
		"homeSiteRefs": homeSiteRefs, "cloudSiteRefs": cloudSiteRefs, "targetNodes": targetNodes,
		"bridgeContractHash": bridgeHash,
		"policy": map[string]any{
			"defaultDeny": true, "initiation": "home-outbound", "trafficMode": trafficMode,
			"routeScope": "declared-flows-only", "allowDefaultRoute": false, "allowBroadLAN": false,
			"credentialCustody": "external", "fabricLifecycle": "external",
		},
		"specHash": specHash,
	}
	hash, err := ComputeFederationLinkRequirementHash(requirement)
	if err != nil {
		return nil, err
	}
	requirement["requirementsHash"] = hash
	return map[string]any{federationLinkCapability: map[string]any(requirement)}, nil
}

func projectExternalFederationLinkBindings(inventory, requirements map[string]any) (map[string]any, error) {
	raw, exists := inventory["externalFederationLinkBindings"]
	if !exists || raw == nil {
		return map[string]any{}, nil
	}
	bindings, err := asObject(raw, "inventory.externalFederationLinkBindings")
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, capabilityRef := range sortedStringMapKeys(bindings) {
		requirement, err := federationLinkRequirementAt(requirements, capabilityRef)
		if err != nil {
			return nil, fail(ErrExternalFederationLinkBindingMismatch, "inventory.externalFederationLinkBindings."+capabilityRef, "binding has no exact selected federation link requirement")
		}
		binding, err := asObject(bindings[capabilityRef], "inventory.externalFederationLinkBindings."+capabilityRef)
		if err != nil {
			return nil, err
		}
		if err := validateExternalFederationLinkBinding(binding, requirement, "inventory.externalFederationLinkBindings."+capabilityRef); err != nil {
			return nil, err
		}
		result[capabilityRef], err = cloneObject(binding, false)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func federationLinkRequirementAt(requirements map[string]any, capabilityRef string) (map[string]any, error) {
	return asObject(requirements[capabilityRef], "resolvedPlan.federationLinkRequirements."+capabilityRef)
}

func safeModuleFederationLinkProjection(moduleID string, module, projection map[string]any, required bool) (map[string]any, error) {
	provides, err := stringListField(module, "modules."+moduleID, "provides", false)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(provides, federationLinkCapability) {
		return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputRefs", "module requests a federation link projection without owning %q", federationLinkCapability)
	}
	value, exists := projection[federationLinkCapability]
	if !exists {
		if required {
			return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputs", "required federation link projection is missing")
		}
		return map[string]any{}, nil
	}
	object, err := asObject(value, "federationLinkProjection."+federationLinkCapability)
	if err != nil {
		return nil, err
	}
	clone, err := cloneObject(object, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{federationLinkCapability: clone}, nil
}

func validateExternalFederationLinkBinding(binding, requirement map[string]any, path string) error {
	allowed := map[string]struct{}{
		"apiVersion": {}, "kind": {}, "bindingRef": {}, "fabricRef": {}, "custodyAttestationRef": {},
		"stackId": {}, "capabilityRef": {}, "contractOwnerRef": {}, "capabilityContractHash": {},
		"homeSiteRefs": {}, "cloudSiteRefs": {}, "targetNodes": {}, "bridgeContractHash": {}, "requirementsHash": {},
		"stackkitsVersion": {}, "candidateDigest": {}, "specHash": {}, "issuedAt": {}, "validUntil": {}, "bindingHash": {},
	}
	for key := range binding {
		if _, ok := allowed[key]; !ok {
			return fail(ErrExternalFederationLinkBindingMismatch, path+"."+key, "field is outside the closed external federation link binding contract")
		}
	}
	if binding["apiVersion"] != externalFederationLinkAPIVersion || binding["kind"] != "ExternalFederationLinkBinding" {
		return fail(ErrExternalFederationLinkBindingMismatch, path+".apiVersion", "unsupported external federation link binding contract")
	}
	for _, ref := range []struct {
		field string
		match *regexp.Regexp
	}{
		{"bindingRef", federationLinkBindingRefPattern}, {"fabricRef", federationLinkFabricRefPattern}, {"custodyAttestationRef", federationLinkCustodyRefPattern},
	} {
		value, err := stringField(binding, path, ref.field)
		if err != nil || !ref.match.MatchString(value) {
			return fail(ErrExternalFederationLinkBindingMismatch, path+"."+ref.field, "must be an opaque sha256 reference")
		}
	}
	for _, field := range []string{"stackId", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "bridgeContractHash", "requirementsHash", "specHash", "homeSiteRefs", "cloudSiteRefs", "targetNodes"} {
		equal, err := canonicalEqual(binding[field], requirement[field])
		if err != nil || !equal {
			return fail(ErrExternalFederationLinkBindingMismatch, path+"."+field, "binding does not match exact federation link requirement")
		}
	}
	version, err := stringField(binding, path, "stackkitsVersion")
	if err != nil || !externalHostSemVerPattern.MatchString(version) {
		return fail(ErrExternalFederationLinkBindingMismatch, path+".stackkitsVersion", "must be a semantic version")
	}
	for _, field := range []string{"candidateDigest", "bindingHash"} {
		value, err := stringField(binding, path, field)
		if err != nil || !externalHostContentHashPattern.MatchString(value) {
			return fail(ErrExternalFederationLinkBindingMismatch, path+"."+field, "must be a sha256 content hash")
		}
	}
	issuedAt, err := externalHostTimestamp(binding, path, "issuedAt")
	if err != nil {
		return err
	}
	validUntil, err := externalHostTimestamp(binding, path, "validUntil")
	if err != nil {
		return err
	}
	if !issuedAt.Before(validUntil) || validUntil.Sub(issuedAt) > maxExternalFederationLinkValidity {
		return fail(ErrExternalFederationLinkBindingMismatch, path+".validUntil", "must be after issuedAt with validity no greater than %s", maxExternalFederationLinkValidity)
	}
	wantHash, err := ComputeExternalFederationLinkBindingHash(ExternalFederationLinkBinding(binding))
	if err != nil {
		return err
	}
	if binding["bindingHash"] != wantHash {
		return fail(ErrExternalFederationLinkBindingMismatch, path+".bindingHash", "declared binding hash does not match canonical binding body")
	}
	return nil
}

func validateFederationLinkPlanProjection(plan ResolvedPlan) error {
	top := map[string]any(plan)
	stackID, err := stringField(top, "resolvedPlan", "stackId")
	if err != nil {
		return err
	}
	specHash, err := stringField(top, "resolvedPlan", "specHash")
	if err != nil {
		return err
	}
	sites, err := objectListField(top, "resolvedPlan", "sites")
	if err != nil {
		return err
	}
	nodes, err := objectListField(top, "resolvedPlan", "nodes")
	if err != nil {
		return err
	}
	capabilities, err := objectListField(top, "resolvedPlan", "capabilities")
	if err != nil {
		return err
	}
	modules, err := objectListField(top, "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	bridge, _ := top["bridge"].(map[string]any)
	want, err := buildFederationLinkRequirements(stackID, specHash, objectMapsAsAny(sites), objectMapsAsAny(nodes), objectMapsAsAny(capabilities), objectMapsAsAny(modules), bridge)
	if err != nil {
		return err
	}
	have, err := objectField(top, "resolvedPlan", "federationLinkRequirements")
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(have, want)
	if err != nil || !equal {
		return fail(ErrExternalFederationLinkBindingMismatch, "resolvedPlan.federationLinkRequirements", "projection is not exactly compiler-derived")
	}
	bindings, err := objectField(top, "resolvedPlan", "externalFederationLinkBindings")
	if err != nil {
		return err
	}
	for _, capabilityRef := range sortedStringMapKeys(bindings) {
		requirement, err := federationLinkRequirementAt(want, capabilityRef)
		if err != nil {
			return fail(ErrExternalFederationLinkBindingMismatch, "resolvedPlan.externalFederationLinkBindings."+capabilityRef, "binding has no exact compiler-derived requirement")
		}
		binding, err := asObject(bindings[capabilityRef], "resolvedPlan.externalFederationLinkBindings."+capabilityRef)
		if err != nil {
			return err
		}
		if err := validateExternalFederationLinkBinding(binding, requirement, "resolvedPlan.externalFederationLinkBindings."+capabilityRef); err != nil {
			return err
		}
	}
	return nil
}

func ValidateExternalFederationLinkBindingsFreshness(plan ResolvedPlan, at time.Time) error {
	if at.IsZero() {
		return fail(ErrInvalidInput, "resolvedPlan.externalFederationLinkBindings", "execution time is required")
	}
	top := map[string]any(plan)
	requirementsRaw, hasRequirements := top["federationLinkRequirements"]
	bindingsRaw, hasBindings := top["externalFederationLinkBindings"]
	if !hasRequirements && !hasBindings {
		return nil
	}
	if !hasRequirements || !hasBindings {
		return fail(ErrExternalFederationLinkBindingMismatch, "resolvedPlan.externalFederationLinkBindings", "federation link requirement and binding projections must appear together")
	}
	requirements, err := asObject(requirementsRaw, "resolvedPlan.federationLinkRequirements")
	if err != nil {
		return err
	}
	bindings, err := asObject(bindingsRaw, "resolvedPlan.externalFederationLinkBindings")
	if err != nil {
		return err
	}
	for _, capabilityRef := range sortedStringMapKeys(bindings) {
		requirement, err := federationLinkRequirementAt(requirements, capabilityRef)
		if err != nil {
			return fail(ErrExternalFederationLinkBindingMismatch, "resolvedPlan.externalFederationLinkBindings."+capabilityRef, "binding has no exact requirement")
		}
		path := "resolvedPlan.externalFederationLinkBindings." + capabilityRef
		binding, err := asObject(bindings[capabilityRef], path)
		if err != nil {
			return err
		}
		if err := validateExternalFederationLinkBinding(binding, requirement, path); err != nil {
			return err
		}
		issuedAt, err := externalHostTimestamp(binding, path, "issuedAt")
		if err != nil {
			return err
		}
		validUntil, err := externalHostTimestamp(binding, path, "validUntil")
		if err != nil {
			return err
		}
		if at.Before(issuedAt) || !at.Before(validUntil) {
			return fail(ErrExternalFederationLinkBindingStale, path+".validUntil", "binding is outside its validity window")
		}
	}
	return nil
}
