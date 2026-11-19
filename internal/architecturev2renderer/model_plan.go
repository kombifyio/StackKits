package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

func parseVerifiedPlan(plan generationartifact.VerifiedPlan) (renderPlan, error) {
	canonical := plan.Canonical()
	projection, err := parsePlanCanonical(canonical)
	if err != nil {
		return renderPlan{}, err
	}
	if projection.outputRoot != plan.OutputRoot() {
		return renderPlan{}, fail(ErrInvalidPlan, "resolvedPlan.generation.outputRoot", "projection does not match the verified plan output root")
	}
	return projection, nil
}

// parsePlanCanonical is deliberately unexported. Production callers can only
// enter through parseVerifiedPlan with generationartifact.VerifiedPlan; unit
// tests use this byte-level seam to exercise fail-closed lowering before a
// generation-ready authority fixture exists.
func parsePlanCanonical(canonical []byte) (renderPlan, error) {
	top, err := decodeRawObject(canonical, "resolvedPlan")
	if err != nil {
		return renderPlan{}, err
	}
	dataProfile, offlineBundle, err := parseDataProfile(top)
	if err != nil {
		return renderPlan{}, err
	}
	localNetwork, err := parseLocalNetwork(top, dataProfile)
	if err != nil {
		return renderPlan{}, err
	}
	generation, err := requiredRawObject(top, "generation", "resolvedPlan.generation")
	if err != nil {
		return renderPlan{}, err
	}
	outputRoot, err := requiredRawString(generation, "outputRoot", "resolvedPlan.generation.outputRoot")
	if err != nil {
		return renderPlan{}, err
	}
	if outputRoot != "." {
		if _, err := validatePortablePath(outputRoot); err != nil {
			return renderPlan{}, wrap(ErrInvalidPath, "resolvedPlan.generation.outputRoot", "invalid managed output root", err)
		}
	}
	networkDomainBase, networkSubdomainPrefix, err := parseOptionalNetworkDomain(top)
	if err != nil {
		return renderPlan{}, err
	}

	artifacts, err := parseArtifacts(generation, outputRoot)
	if err != nil {
		return renderPlan{}, err
	}
	nodeSites, err := parseNodeSiteIndex(top)
	if err != nil {
		return renderPlan{}, err
	}
	runtimeNetworks, err := parseRuntimeNetworks(top, nodeSites)
	if err != nil {
		return renderPlan{}, err
	}
	rawModules, err := requiredRawArray(top, "modules", "resolvedPlan.modules")
	if err != nil {
		return renderPlan{}, err
	}
	if len(rawModules) == 0 {
		return renderPlan{}, fail(ErrInvalidPlan, "resolvedPlan.modules", "at least one renderable module is required")
	}

	var network struct {
		RuntimeListeners []struct {
			ID          string `json:"id"`
			BindAddress string `json:"bindAddress"`
		} `json:"runtimeListeners"`
	}
	if raw, ok := top["network"]; ok {
		if err := json.Unmarshal(raw, &network); err != nil {
			return renderPlan{}, err
		}
	}
	addresses := map[string]string{}
	for _, listener := range network.RuntimeListeners {
		addresses[listener.ID] = listener.BindAddress
	}
	result := renderPlan{
		localNetwork: localNetwork,
		dataProfile:  dataProfile, offlineBundle: offlineBundle,
		listenerAddresses: addresses,
		outputRoot:        outputRoot, networkDomainBase: networkDomainBase, networkSubdomainPrefix: networkSubdomainPrefix,
		artifacts: artifacts,
		bindings:  make(map[instanceOutputKey]outputBinding),
	}
	moduleIDs := make(map[string]struct{}, len(rawModules))
	boundArtifacts := make(map[string]instanceOutputKey)
	for moduleIndex, rawModule := range rawModules {
		modulePath := fmt.Sprintf("resolvedPlan.modules[%d]", moduleIndex)
		module, bindings, err := parseModule(rawModule, modulePath, artifacts, outputRoot)
		if err != nil {
			return renderPlan{}, err
		}
		if err := validateModuleInstanceNodeSites(module, nodeSites, modulePath); err != nil {
			return renderPlan{}, err
		}
		if _, exists := moduleIDs[module.id]; exists {
			return renderPlan{}, fail(ErrDuplicate, modulePath+".id", "duplicate module ID %q", module.id)
		}
		moduleIDs[module.id] = struct{}{}
		for _, binding := range bindings {
			key := instanceOutputKey{moduleID: module.id, unitID: binding.unitRef, instanceID: binding.instanceRef, output: binding.outputRef}
			if _, exists := result.bindings[key]; exists {
				return renderPlan{}, fail(ErrDuplicate, modulePath+".renderUnits.instances.outputs", "instance output %s/%s/%s is bound more than once", binding.unitRef, binding.instanceRef, binding.outputRef)
			}
			if previous, exists := boundArtifacts[binding.artifactID]; exists {
				return renderPlan{}, fail(ErrDuplicate, modulePath+".renderUnits.instances.outputs", "artifact %q is already owned by %s/%s/%s/%s", binding.artifactID, previous.moduleID, previous.unitID, previous.instanceID, previous.output)
			}
			boundArtifacts[binding.artifactID] = key
			result.bindings[key] = binding
		}
		result.modules = append(result.modules, module)
	}

	return finalizeRenderPlan(top, result, artifacts, boundArtifacts, runtimeNetworks)
}

func parseOptionalNetworkDomain(top map[string]json.RawMessage) (string, string, error) {
	raw, present := top["network"]
	if !present {
		return "", "", nil
	}
	network, err := decodeRawObject(raw, "resolvedPlan.network")
	if err != nil {
		return "", "", err
	}
	configuration, err := requiredRawObject(network, "configuration", "resolvedPlan.network.configuration")
	if err != nil {
		return "", "", err
	}
	domain, err := requiredRawObject(configuration, "domain", "resolvedPlan.network.configuration.domain")
	if err != nil {
		return "", "", err
	}
	base, err := requiredRawString(domain, "base", "resolvedPlan.network.configuration.domain.base")
	if err != nil {
		return "", "", err
	}
	prefix := ""
	if rawPrefix, present := domain["subdomainPrefix"]; present {
		if err := json.Unmarshal(rawPrefix, &prefix); err != nil || !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(prefix) {
			return "", "", fail(ErrInvalidPlan, "resolvedPlan.network.configuration.domain.subdomainPrefix", "invalid subdomain prefix")
		}
	}
	return base, prefix, nil
}

func finalizeRenderPlan(top map[string]json.RawMessage, result renderPlan, artifacts map[string]artifactContract, boundArtifacts map[string]instanceOutputKey, runtimeNetworks []runtimeNetwork) (renderPlan, error) {
	if err := validateBoundArtifacts(artifacts, boundArtifacts); err != nil {
		return renderPlan{}, err
	}
	if err := validateRuntimeNetworkGraph(result.modules, runtimeNetworks); err != nil {
		return renderPlan{}, err
	}
	if err := bindPlanPrivilegedInterfaceApprovals(top, result.modules); err != nil {
		return renderPlan{}, err
	}
	sort.Slice(result.modules, func(i, j int) bool { return result.modules[i].id < result.modules[j].id })
	return result, nil
}

func bindPlanPrivilegedInterfaceApprovals(top map[string]json.RawMessage, modules []renderModule) error {
	approvals, present, err := parsePrivilegedInterfaceApprovals(top)
	if err != nil {
		return err
	}
	if !present {
		bindEmptyPrivilegedInterfaceApprovals(modules)
		return nil
	}
	return bindPrivilegedInterfaceApprovals(modules, approvals)
}

func parsePrivilegedInterfaceApprovals(top map[string]json.RawMessage) ([]privilegedInterfaceApproval, bool, error) {
	raw, present := top["privilegedInterfaceApprovals"]
	if !present {
		return nil, false, nil
	}
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		if err == nil {
			err = fmt.Errorf("must be an array")
		}
		return nil, true, wrap(ErrInvalidPlan, "resolvedPlan.privilegedInterfaceApprovals", "decode approvals", err)
	}
	result := make([]privilegedInterfaceApproval, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		approvalPath := fmt.Sprintf("resolvedPlan.privilegedInterfaceApprovals[%d]", index)
		var decoded rawPrivilegedInterfaceApproval
		if err := decodeStrict(value, &decoded); err != nil {
			return nil, true, wrap(ErrInvalidPlan, approvalPath, "decode approval", err)
		}
		if decoded.Kind != dockerSocketDirectInterfaceKind {
			return nil, true, fail(ErrInvalidPlan, approvalPath+".kind", "must be %q", dockerSocketDirectInterfaceKind)
		}
		if decoded.ReasonCode != "provider-backing" && decoded.ReasonCode != "lifecycle-owner" {
			return nil, true, fail(ErrInvalidPlan, approvalPath+".reasonCode", "unsupported direct-socket approval reason")
		}
		if strings.TrimSpace(decoded.EvidenceRef) == "" {
			return nil, true, fail(ErrInvalidPlan, approvalPath+".evidenceRef", "must be non-empty")
		}
		if len(decoded.SiteRefs) == 0 || len(decoded.NodeRefs) == 0 {
			return nil, true, fail(ErrInvalidPlan, approvalPath, "siteRefs and nodeRefs must contain exact approved placement")
		}
		if _, err := uniqueContractIDSet(decoded.SiteRefs, approvalPath+".siteRefs"); err != nil {
			return nil, true, err
		}
		if _, err := uniqueContractIDSet(decoded.NodeRefs, approvalPath+".nodeRefs"); err != nil {
			return nil, true, err
		}
		if _, duplicate := seen[decoded.ID]; duplicate {
			return nil, true, fail(ErrDuplicate, approvalPath+".id", "duplicate privileged approval %q", decoded.ID)
		}
		seen[decoded.ID] = struct{}{}
		result = append(result, privilegedInterfaceApproval{raw: append([]byte(nil), value...), contract: decoded})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].contract.ID < result[j].contract.ID })
	return result, true, nil
}

func bindPrivilegedInterfaceApprovals(modules []renderModule, approvals []privilegedInterfaceApproval) error {
	for moduleIndex := range modules {
		for unitIndex := range modules[moduleIndex].units {
			unit := &modules[moduleIndex].units[unitIndex]
			bound := make([]json.RawMessage, 0)
			for approvalIndex := range approvals {
				approval := &approvals[approvalIndex]
				if approval.contract.ModuleRef != modules[moduleIndex].id || approval.contract.UnitRef != unit.id {
					continue
				}
				approval.boundUnit = true
				bound = append(bound, approval.raw)
			}
			canonical, err := json.Marshal(bound)
			if err != nil {
				return wrap(ErrInvalidPlan, "resolvedPlan.privilegedInterfaceApprovals", "canonicalize unit approvals", err)
			}
			unit.privilegedApprovalsCanonical = canonical
		}
	}
	for _, approval := range approvals {
		if !approval.boundUnit {
			return fail(ErrInvalidPlan, "resolvedPlan.privilegedInterfaceApprovals."+approval.contract.ID, "approval references an unknown render unit")
		}
	}
	return nil
}

func bindEmptyPrivilegedInterfaceApprovals(modules []renderModule) {
	for moduleIndex := range modules {
		for unitIndex := range modules[moduleIndex].units {
			modules[moduleIndex].units[unitIndex].privilegedApprovalsCanonical = []byte("[]")
		}
	}
}

func validateBoundArtifacts(artifacts map[string]artifactContract, _ map[string]instanceOutputKey) error {
	for artifactID, artifact := range artifacts {
		if artifact.owner.kind == "plan" {
			if artifactID != "resolved-plan" && artifactID != terramatestackgraph.ArtifactID {
				return fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "plan-owned artifact %q has no governed renderer producer", artifactID)
			}
			continue
		}
	}
	return nil
}

func parseNodeSiteIndex(top map[string]json.RawMessage) (map[string]string, error) {
	rawNodes, err := requiredRawArray(top, "nodes", "resolvedPlan.nodes")
	if err != nil {
		return nil, err
	}
	if len(rawNodes) == 0 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.nodes", "at least one resolved node is required")
	}
	nodeSites := make(map[string]string, len(rawNodes))
	for index, rawNode := range rawNodes {
		nodePath := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		node, err := decodeRawObject(rawNode, nodePath)
		if err != nil {
			return nil, err
		}
		nodeRef, err := requiredRawString(node, "id", nodePath+".id")
		if err != nil {
			return nil, err
		}
		siteRef, err := requiredRawString(node, "siteRef", nodePath+".siteRef")
		if err != nil {
			return nil, err
		}
		if _, exists := nodeSites[nodeRef]; exists {
			return nil, fail(ErrDuplicate, nodePath+".id", "duplicate top-level node ID %q", nodeRef)
		}
		nodeSites[nodeRef] = siteRef
	}
	return nodeSites, nil
}

func validateModuleInstanceNodeSites(module renderModule, nodeSites map[string]string, modulePath string) error {
	for _, unit := range module.units {
		for _, instance := range unit.instances {
			if instance.scope != "node-local" {
				continue
			}
			instancePath := fmt.Sprintf("%s.renderUnits.%s.instances.%s", modulePath, unit.id, instance.id)
			siteRef, exists := nodeSites[instance.nodeRef]
			if !exists {
				return fail(ErrInvalidPlan, instancePath+".nodeRef", "references node %q outside top-level resolved nodes", instance.nodeRef)
			}
			if instance.siteRef != siteRef {
				return fail(ErrInvalidPlan, instancePath+".siteRef", "site %q does not match top-level node %q siteRef %q", instance.siteRef, instance.nodeRef, siteRef)
			}
		}
	}
	return nil
}

func parseArtifacts(generation map[string]json.RawMessage, outputRoot string) (map[string]artifactContract, error) {
	rawArtifacts, err := requiredRawArray(generation, "artifacts", "resolvedPlan.generation.artifacts")
	if err != nil {
		return nil, err
	}
	if len(rawArtifacts) == 0 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "at least one governed artifact is required")
	}
	result := make(map[string]artifactContract, len(rawArtifacts))
	paths := make(map[string]string, len(rawArtifacts))
	resolvedPlanCount := 0
	for index, raw := range rawArtifacts {
		artifactPath := fmt.Sprintf("resolvedPlan.generation.artifacts[%d]", index)
		artifact, err := parseArtifactContract(raw, artifactPath)
		if err != nil {
			return nil, err
		}
		if _, exists := result[artifact.id]; exists {
			return nil, fail(ErrDuplicate, artifactPath+".id", "duplicate artifact ID %q", artifact.id)
		}
		pathKey := portableKey(artifact.path)
		if previous, exists := paths[pathKey]; exists {
			return nil, fail(ErrDuplicate, artifactPath+".path", "artifact path %q duplicates artifact %q", artifact.path, previous)
		}
		paths[pathKey] = artifact.id
		result[artifact.id] = artifact
		isResolvedPlan, err := validateArtifactGovernance(artifact, artifactPath, outputRoot)
		if err != nil {
			return nil, err
		}
		if isResolvedPlan {
			resolvedPlanCount++
		}
	}
	if resolvedPlanCount == 0 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "a resolved-plan artifact is required")
	}
	return result, nil
}

func parseArtifactContract(raw json.RawMessage, artifactPath string) (artifactContract, error) {
	var decoded rawArtifact
	if err := decodeStrict(raw, &decoded); err != nil {
		return artifactContract{}, wrap(ErrInvalidPlan, artifactPath, "decode artifact contract", err)
	}
	if _, err := validatePortablePath(decoded.Path); err != nil {
		return artifactContract{}, wrap(ErrInvalidPath, artifactPath+".path", "invalid artifact path", err)
	}
	if decoded.Kind == "" || decoded.Format == "" || decoded.Mode == "" || decoded.Required == nil || decoded.Owner == nil {
		return artifactContract{}, fail(ErrInvalidPlan, artifactPath, "kind, format, mode, required, and owner are mandatory")
	}
	owner, err := parseArtifactOwner(decoded.Owner, artifactPath+".owner")
	if err != nil {
		return artifactContract{}, err
	}
	return artifactContract{
		id: decoded.ID, path: decoded.Path, kind: decoded.Kind, format: decoded.Format,
		mode: decoded.Mode, required: *decoded.Required, owner: owner,
	}, nil
}

func validateArtifactGovernance(artifact artifactContract, artifactPath, outputRoot string) (bool, error) {
	if artifact.id == "resolved-plan" {
		want := joinOutputPath(outputRoot, ".stackkit/resolved-plan.json")
		if !artifact.required || artifact.path != want || artifact.owner.kind != "plan" {
			return true, fail(ErrInvalidPlan, artifactPath, "resolved-plan must be a required plan-owned artifact at %q", want)
		}
		return true, nil
	}
	if artifact.id == terramatestackgraph.ArtifactID {
		want := joinOutputPath(outputRoot, terramatestackgraph.ArtifactPath)
		if !artifact.required || artifact.path != want || artifact.owner.kind != "plan" || artifact.kind != "metadata" || artifact.format != "json" {
			return false, fail(ErrInvalidPlan, artifactPath, "the Terramate stack graph must be a required plan-owned JSON metadata artifact at %q", want)
		}
		return false, nil
	}
	if artifact.owner.kind != "render-instance" {
		return false, fail(ErrInvalidPlan, artifactPath+".owner.kind", "non-plan artifact must be owned by one render instance")
	}
	return false, nil
}

func parseArtifactOwner(raw json.RawMessage, ownerPath string) (artifactOwner, error) {
	var decoded rawArtifactOwner
	if err := decodeStrict(raw, &decoded); err != nil {
		return artifactOwner{}, wrap(ErrInvalidPlan, ownerPath, "decode artifact owner", err)
	}
	switch decoded.Kind {
	case "plan":
		if decoded.ModuleRef.present || decoded.UnitRef.present || decoded.InstanceRef.present || decoded.OutputRef.present {
			return artifactOwner{}, fail(ErrInvalidPlan, ownerPath, "plan owner must not carry render-instance identity")
		}
		return artifactOwner{kind: decoded.Kind}, nil
	case "render-instance":
		fields := []struct {
			name  string
			value optionalStringField
		}{
			{name: "moduleRef", value: decoded.ModuleRef},
			{name: "unitRef", value: decoded.UnitRef},
			{name: "instanceRef", value: decoded.InstanceRef},
		}
		for _, field := range fields {
			if !field.value.present {
				return artifactOwner{}, fail(ErrInvalidPlan, ownerPath+"."+field.name, "is required for a render-instance owner")
			}
		}
		if !decoded.OutputRef.present {
			return artifactOwner{}, fail(ErrInvalidPlan, ownerPath+".outputRef", "is required for a render-instance owner")
		}
		if _, err := validatePortablePath(decoded.OutputRef.value); err != nil {
			return artifactOwner{}, wrap(ErrInvalidPath, ownerPath+".outputRef", "invalid logical output reference", err)
		}
		return artifactOwner{
			kind: decoded.Kind, moduleRef: decoded.ModuleRef.value, unitRef: decoded.UnitRef.value,
			instanceRef: decoded.InstanceRef.value, outputRef: decoded.OutputRef.value,
		}, nil
	default:
		return artifactOwner{}, fail(ErrInvalidPlan, ownerPath+".kind", "must be plan or render-instance")
	}
}
