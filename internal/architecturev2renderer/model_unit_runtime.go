package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"path"
	"sort"
	"strings"
)

func validateRenderUnitImplementation(unit rawRenderUnit, unitPath string) ([]byte, []byte, []byte, rawRenderUnitPlacement, error) {
	if unit.SiteRefs == nil || unit.NodeRefs == nil || unit.Placement == nil || unit.DaemonBindings == nil || unit.ServiceEndpoints == nil || unit.RuntimeListeners == nil || unit.ProvidesInterfaces == nil || unit.RequiresInterfaces == nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, fail(ErrInvalidPlan, unitPath, "siteRefs, nodeRefs, placement, daemonBindings, serviceEndpoints, runtimeListeners, providesInterfaces, and requiresInterfaces are mandatory")
	}
	if len(unit.SiteRefs) == 0 || len(unit.NodeRefs) == 0 {
		return nil, nil, nil, rawRenderUnitPlacement{}, fail(ErrInvalidPlan, unitPath, "siteRefs and nodeRefs must contain resolved placement")
	}
	if _, err := uniqueContractIDSet(unit.SiteRefs, unitPath+".siteRefs"); err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	if _, err := uniqueContractIDSet(unit.NodeRefs, unitPath+".nodeRefs"); err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	placementObject, err := decodeRawObject(unit.Placement, unitPath+".placement")
	if err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	var placementContract rawRenderUnitPlacement
	if err := decodeStrict(unit.Placement, &placementContract); err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, wrap(ErrInvalidPlan, unitPath+".placement", "decode placement contract", err)
	}
	if err := validatePlacementContract(placementContract, unitPath+".placement"); err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	placement, err := canonicalObject(placementObject)
	if err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, wrap(ErrInvalidPlan, unitPath+".placement", "canonicalize render-unit placement", err)
	}
	provided, err := canonicalImplementationInterfaces(unit.ProvidesInterfaces, unitPath+".providesInterfaces")
	if err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	required, err := canonicalImplementationInterfaces(unit.RequiresInterfaces, unitPath+".requiresInterfaces")
	if err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	return placement, provided, required, placementContract, nil
}

func parseRuntimeListeners(values []json.RawMessage, placement rawRenderUnitPlacement, applyMode, valuePath string) ([]byte, error) {
	if len(values) > 0 && placement.Scope != "node-local" {
		return nil, fail(ErrInvalidPlan, valuePath, "physical runtime listeners require node-local placement")
	}
	if len(values) > 0 && applyMode == "artifact-only" {
		return nil, fail(ErrInvalidPlan, valuePath, "artifact-only render units cannot own physical runtime listeners")
	}
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		listenerPath := fmt.Sprintf("%s[%d]", valuePath, index)
		var listener rawModuleRuntimeListener
		if err := decodeStrict(raw, &listener); err != nil {
			return nil, wrap(ErrInvalidPlan, listenerPath, "decode runtime listener", err)
		}
		if _, duplicate := seen[listener.ID]; duplicate {
			return nil, fail(ErrDuplicate, listenerPath+".id", "duplicate runtime listener %q", listener.ID)
		}
		seen[listener.ID] = struct{}{}
		if !oneOf(listener.Transport, "tcp", "udp") {
			return nil, fail(ErrInvalidPlan, listenerPath+".transport", "unsupported listener transport %q", listener.Transport)
		}
		if _, err := netip.ParseAddr(listener.BindAddress); err != nil {
			return nil, wrap(ErrInvalidPlan, listenerPath+".bindAddress", "parse listener bind address", err)
		}
		if listener.Port < 1 || listener.Port > 65535 || listener.TargetPort < 1 || listener.TargetPort > 65535 {
			return nil, fail(ErrInvalidPlan, listenerPath, "port and targetPort must be between 1 and 65535")
		}
		if !oneOf(listener.Sharing, "exclusive", "virtual-host") {
			return nil, fail(ErrInvalidPlan, listenerPath+".sharing", "unsupported listener sharing %q", listener.Sharing)
		}
		hasGroupRef := listener.ListenerGroupRef.present
		if listener.Sharing == "exclusive" && hasGroupRef {
			return nil, fail(ErrInvalidPlan, listenerPath+".listenerGroupRef", "exclusive listener forbids listenerGroupRef")
		}
		if listener.Sharing == "virtual-host" {
			if !hasGroupRef {
				return nil, fail(ErrInvalidPlan, listenerPath+".listenerGroupRef", "virtual-host listener requires listenerGroupRef")
			}
		}
		if !oneOf(listener.Exposure, "local", "lan", "remote-private", "public") {
			return nil, fail(ErrInvalidPlan, listenerPath+".exposure", "unsupported listener exposure %q", listener.Exposure)
		}
		if listener.SourceServiceRefs == nil {
			return nil, fail(ErrInvalidPlan, listenerPath+".sourceServiceRefs", "sourceServiceRefs is mandatory")
		}
		if _, err := uniqueContractIDSet(listener.SourceServiceRefs, listenerPath+".sourceServiceRefs"); err != nil {
			return nil, err
		}
	}
	canonical, err := json.Marshal(values)
	if err != nil {
		return nil, wrap(ErrInvalidPlan, valuePath, "canonicalize runtime listeners", err)
	}
	return canonical, nil
}

//nolint:gocyclo // Endpoint parsing validates the complete closed schema and security policy before admitting any routable contract.
func parseServiceEndpoints(values []json.RawMessage, placement rawRenderUnitPlacement, valuePath string) ([]serviceEndpointContract, []byte, error) {
	if len(values) > 0 && placement.Scope != "node-local" {
		return nil, nil, fail(ErrInvalidPlan, valuePath, "routable service endpoints require node-local placement")
	}
	result := make([]serviceEndpointContract, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		endpointPath := fmt.Sprintf("%s[%d]", valuePath, index)
		var endpoint rawModuleServiceEndpoint
		if err := decodeStrict(raw, &endpoint); err != nil {
			return nil, nil, wrap(ErrInvalidPlan, endpointPath, "decode service endpoint", err)
		}
		if _, duplicate := seen[endpoint.ServiceRef]; duplicate {
			return nil, nil, fail(ErrDuplicate, endpointPath+".serviceRef", "duplicate service endpoint %q", endpoint.ServiceRef)
		}
		seen[endpoint.ServiceRef] = struct{}{}
		if !oneOf(endpoint.UpstreamProtocol, "tcp", "udp", "http", "https") {
			return nil, nil, fail(ErrInvalidPlan, endpointPath+".upstreamProtocol", "unsupported network protocol %q", endpoint.UpstreamProtocol)
		}
		if endpoint.TargetPort < 1 || endpoint.TargetPort > 65535 {
			return nil, nil, fail(ErrInvalidPlan, endpointPath+".targetPort", "must be between 1 and 65535")
		}
		if !oneOf(endpoint.RequiredPrivilege, "user", "sign-in", "admin", "identity", "secrets", "vault", "recovery") {
			return nil, nil, fail(ErrInvalidPlan, endpointPath+".requiredPrivilege", "unsupported service privilege %q", endpoint.RequiredPrivilege)
		}
		if endpoint.IngressAuth == "" {
			endpoint.IngressAuth = "native"
		}
		if !oneOf(endpoint.IngressAuth, "none", "native", "forward-auth") {
			return nil, nil, fail(ErrInvalidPlan, endpointPath+".ingressAuth", "unsupported ingress auth mode %q", endpoint.IngressAuth)
		}
		if err := validateUniqueEnumList(endpoint.AllowedIngressProtocols, []string{"tcp", "udp", "http", "https"}, endpointPath+".allowedIngressProtocols"); err != nil {
			return nil, nil, err
		}
		if err := validateUniqueEnumList(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}, endpointPath+".allowedExposures"); err != nil {
			return nil, nil, err
		}
		if !oneOf(endpoint.OriginSelector, "single-site", "control-authority-site", "multi-zone", "edge-pool") {
			return nil, nil, fail(ErrInvalidPlan, endpointPath+".originSelector", "unsupported origin selector %q", endpoint.OriginSelector)
		}
		hasOriginSelection := len(endpoint.OriginSelection) != 0 && string(endpoint.OriginSelection) != "null"
		if endpoint.OriginSelector == "single-site" || endpoint.OriginSelector == "control-authority-site" {
			if hasOriginSelection {
				return nil, nil, fail(ErrInvalidPlan, endpointPath+".originSelection", "single-Site selectors forbid an origin selection policy")
			}
		} else {
			if !hasOriginSelection {
				return nil, nil, fail(ErrInvalidPlan, endpointPath+".originSelection", "selector %q requires an explicit origin selection policy", endpoint.OriginSelector)
			}
			var selection rawServiceEndpointOriginSelectionV2
			if err := decodeStrict(endpoint.OriginSelection, &selection); err != nil {
				return nil, nil, wrap(ErrInvalidPlan, endpointPath+".originSelection", "decode origin selection policy", err)
			}
			if err := validateEndpointOriginSelection(selection, endpoint.OriginSelector, endpointPath+".originSelection", false); err != nil {
				return nil, nil, err
			}
		}
		if endpoint.Data != nil {
			if bytes.Equal(bytes.TrimSpace(endpoint.Data), []byte("null")) {
				return nil, nil, fail(ErrInvalidPlan, endpointPath+".data", "must be an object, not null")
			}
			var data rawModuleServiceEndpointData
			if err := decodeStrict(endpoint.Data, &data); err != nil {
				return nil, nil, wrap(ErrInvalidPlan, endpointPath+".data", "decode service data contract", err)
			}
			if err := validateUniqueEnumList(data.RequiredClasses, []string{"public", "internal", "personal", "sensitive", "secret"}, endpointPath+".data.requiredClasses"); err != nil {
				return nil, nil, err
			}
			if !oneOf(data.Locality, "primary-site", "primary-or-replica") {
				return nil, nil, fail(ErrInvalidPlan, endpointPath+".data.locality", "unsupported data locality %q", data.Locality)
			}
		}
		result = append(result, serviceEndpointContract{serviceRef: endpoint.ServiceRef})
	}
	canonical, err := json.Marshal(values)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, valuePath, "canonicalize service endpoints", err)
	}
	return result, canonical, nil
}

func validateEndpointOriginSelection(selection rawServiceEndpointOriginSelectionV2, selector, path string, resolved bool) error {
	if err := validateUniqueEnumList(selection.SiteKinds, []string{"home", "cloud"}, path+".siteKinds"); err != nil {
		return err
	}
	if selection.MinSites < 1 || selection.SiteFailureDomainSpread < 1 || selection.NodeFailureDomainSpread < 1 {
		return fail(ErrInvalidPlan, path, "selection thresholds must be positive")
	}
	if err := validateUniqueEnumList(selection.RequiredRoles, []string{"controller", "worker", "storage", "edge"}, path+".requiredRoles"); err != nil && len(selection.RequiredRoles) > 0 {
		return err
	}
	if selector == "multi-zone" && (selection.NodeFailureDomainSpread < 2 || len(selection.RequiredRoles) != 0) {
		return fail(ErrInvalidPlan, path, "multi-zone requires node failure-domain spread >= 2 and no role filter")
	}
	if selector == "edge-pool" && !oneOf("edge", selection.RequiredRoles...) {
		return fail(ErrInvalidPlan, path+".requiredRoles", "edge-pool requires the edge role")
	}
	if !resolved {
		if selection.SiteFailureDomains != nil || selection.NodeFailureDomains != nil {
			return fail(ErrInvalidPlan, path, "catalog selection policies cannot contain compiler-resolved failure domains")
		}
		return nil
	}
	if len(selection.SiteFailureDomains) < selection.SiteFailureDomainSpread || len(selection.NodeFailureDomains) < selection.NodeFailureDomainSpread {
		return fail(ErrInvalidPlan, path, "resolved failure-domain evidence is below the declared spread")
	}
	seenSiteDomains := map[string]struct{}{}
	for index, domain := range selection.SiteFailureDomains {
		if domain == "" {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.siteFailureDomains[%d]", path, index), "failure domain is empty")
		}
		if _, duplicate := seenSiteDomains[domain]; duplicate {
			return fail(ErrDuplicate, path+".siteFailureDomains", "duplicate Site failure domain")
		}
		seenSiteDomains[domain] = struct{}{}
	}
	seenNodeDomains := map[string]struct{}{}
	for index, domain := range selection.NodeFailureDomains {
		if domain.FailureDomain == "" {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.nodeFailureDomains[%d].failureDomain", path, index), "failure domain is empty")
		}
		key := domain.SiteRef + "\x00" + domain.FailureDomain
		if _, duplicate := seenNodeDomains[key]; duplicate {
			return fail(ErrDuplicate, path+".nodeFailureDomains", "duplicate site-scoped node failure domain")
		}
		seenNodeDomains[key] = struct{}{}
	}
	return nil
}

func validatePlacementContract(placement rawRenderUnitPlacement, placementPath string) error {
	switch placement.Scope {
	case "module":
		if placement.Cardinality != "single" || placement.DaemonRef.present {
			return fail(ErrInvalidPlan, placementPath, "module placement requires single cardinality and no daemonRef")
		}
	case "node-local":
		switch placement.Cardinality {
		case "single", "one-per-node":
			if placement.DaemonRef.present {
				return fail(ErrInvalidPlan, placementPath+".daemonRef", "is only allowed for one-per-daemon placement")
			}
		case "one-per-daemon":
			if !placement.DaemonRef.present {
				return fail(ErrInvalidPlan, placementPath+".daemonRef", "is required for one-per-daemon placement")
			}
		default:
			return fail(ErrInvalidPlan, placementPath+".cardinality", "unsupported node-local cardinality %q", placement.Cardinality)
		}
	default:
		return fail(ErrInvalidPlan, placementPath+".scope", "must be module or node-local")
	}
	return nil
}

func validateRenderUnitInstances(unit rawRenderUnit, logicalOutputs []string, placement rawRenderUnitPlacement, unitPath string) ([]renderUnitInstance, error) {
	if len(unit.Instances) == 0 {
		return nil, fail(ErrInvalidPlan, unitPath+".instances", "at least one explicit render instance is required")
	}
	logicalSet := make(map[string]struct{}, len(logicalOutputs))
	for _, output := range logicalOutputs {
		logicalSet[output] = struct{}{}
	}
	siteSet, _ := uniqueContractIDSet(unit.SiteRefs, unitPath+".siteRefs")
	nodeSet, _ := uniqueContractIDSet(unit.NodeRefs, unitPath+".nodeRefs")
	daemonBindings, err := indexRuntimeDaemonBindings(unit.DaemonBindings, unitPath+".daemonBindings")
	if err != nil {
		return nil, err
	}
	if err := validateRenderUnitDaemonBindings(placement, daemonBindings, unitPath); err != nil {
		return nil, err
	}
	instances := make([]renderUnitInstance, 0, len(unit.Instances))
	seenIDs := make(map[string]struct{}, len(unit.Instances))
	seenNodes := make(map[string]struct{}, len(unit.Instances))
	for index, raw := range unit.Instances {
		instancePath := fmt.Sprintf("%s.instances[%d]", unitPath, index)
		if _, exists := seenIDs[raw.ID]; exists {
			return nil, fail(ErrDuplicate, instancePath+".id", "duplicate render instance %q", raw.ID)
		}
		seenIDs[raw.ID] = struct{}{}
		instance, err := validateRenderUnitInstanceIdentity(raw, placement, siteSet, nodeSet, instancePath)
		if err != nil {
			return nil, err
		}
		if err := trackRenderInstanceNode(instance, seenNodes, instancePath); err != nil {
			return nil, err
		}
		outputs, err := validateRenderInstanceOutputs(raw.Outputs, logicalSet, instancePath)
		if err != nil {
			return nil, err
		}
		networkBindings, networkCanonical, err := validateRuntimeNetworkBindings(raw.NetworkBindings, instance, instancePath)
		if err != nil {
			return nil, err
		}
		if instance.scope == "module" && len(networkBindings) != 0 {
			return nil, fail(ErrInvalidPlan, instancePath+".networkBindings", "module-scoped render instances cannot join runtime networks")
		}
		instance.outputs = outputs
		instance.networkBindings = networkBindings
		instance.networkCanonical = networkCanonical
		instances = append(instances, instance)
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].id < instances[j].id })
	return instances, nil
}

func validateRenderUnitDaemonBindings(placement rawRenderUnitPlacement, bindings map[string]rawRuntimeDaemonBinding, unitPath string) error {
	if placement.Cardinality == "one-per-daemon" {
		return nil
	}
	if len(bindings) != 0 {
		return fail(ErrInvalidPlan, unitPath+".daemonBindings", "is only allowed for one-per-daemon placement")
	}
	return nil
}

func trackRenderInstanceNode(instance renderUnitInstance, seenNodes map[string]struct{}, instancePath string) error {
	if instance.nodeRef == "" {
		return nil
	}
	if _, exists := seenNodes[instance.nodeRef]; exists {
		return fail(ErrDuplicate, instancePath+".nodeRef", "node %q has more than one instance for this logical render unit", instance.nodeRef)
	}
	seenNodes[instance.nodeRef] = struct{}{}
	return nil
}

func validateRenderUnitInstanceIdentity(raw rawRenderUnitInstance, placement rawRenderUnitPlacement, siteSet, nodeSet map[string]struct{}, instancePath string) (renderUnitInstance, error) {
	if raw.Scope != placement.Scope {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".scope", "instance scope %q does not match logical placement scope %q", raw.Scope, placement.Scope)
	}
	if placement.Scope == "module" {
		return validateModuleRenderUnitInstanceIdentity(raw, instancePath)
	}
	instance, err := validateNodeLocalRenderUnitInstanceIdentity(raw, siteSet, nodeSet, instancePath)
	if err != nil {
		return renderUnitInstance{}, err
	}
	if placement.Cardinality != "one-per-daemon" {
		return validateNonDaemonRenderUnitInstanceIdentity(raw, instance, instancePath)
	}
	return validateDaemonRenderUnitInstanceIdentity(raw, instance, placement, instancePath)
}

func validateModuleRenderUnitInstanceIdentity(raw rawRenderUnitInstance, instancePath string) (renderUnitInstance, error) {
	if raw.SiteRef.present || raw.NodeRef.present || raw.DaemonRef.present || raw.DaemonInstanceRef.present || raw.DaemonEngine.present || raw.DaemonSocketPath.present {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath, "module instance must not carry site, node, or daemon locality")
	}
	return renderUnitInstance{id: raw.ID, scope: raw.Scope}, nil
}

func validateNodeLocalRenderUnitInstanceIdentity(raw rawRenderUnitInstance, siteSet, nodeSet map[string]struct{}, instancePath string) (renderUnitInstance, error) {
	if !raw.SiteRef.present || !raw.NodeRef.present {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath, "node-local instance requires exact siteRef and nodeRef")
	}
	if _, exists := siteSet[raw.SiteRef.value]; !exists {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".siteRef", "references site %q outside the logical render unit", raw.SiteRef.value)
	}
	if _, exists := nodeSet[raw.NodeRef.value]; !exists {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".nodeRef", "references node %q outside the logical render unit", raw.NodeRef.value)
	}
	return renderUnitInstance{
		id: raw.ID, scope: raw.Scope, siteRef: raw.SiteRef.value, nodeRef: raw.NodeRef.value,
	}, nil
}

func validateNonDaemonRenderUnitInstanceIdentity(raw rawRenderUnitInstance, instance renderUnitInstance, instancePath string) (renderUnitInstance, error) {
	if raw.DaemonRef.present || raw.DaemonInstanceRef.present || raw.DaemonEngine.present || raw.DaemonSocketPath.present {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath, "non-daemon instance must not carry daemon identity or socket metadata")
	}
	return instance, nil
}

func validateDaemonRenderUnitInstanceIdentity(raw rawRenderUnitInstance, instance renderUnitInstance, placement rawRenderUnitPlacement, instancePath string) (renderUnitInstance, error) {
	if !raw.DaemonRef.present || !raw.DaemonInstanceRef.present || !raw.DaemonEngine.present || !raw.DaemonSocketPath.present {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath, "one-per-daemon instance requires daemonRef, daemonInstanceRef, daemonEngine, and daemonSocketPath")
	}
	if raw.DaemonEngine.value != "docker" {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".daemonEngine", "must be docker")
	}
	if err := validateDockerSocketPath(raw.DaemonSocketPath.value); err != nil {
		return renderUnitInstance{}, wrap(ErrInvalidPlan, instancePath+".daemonSocketPath", "invalid Docker socket path", err)
	}
	if !placement.DaemonRef.present || raw.DaemonRef.value != placement.DaemonRef.value {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".daemonRef", "does not match logical placement daemonRef")
	}
	instance.daemonRef, instance.daemonInstanceRef = raw.DaemonRef.value, raw.DaemonInstanceRef.value
	instance.daemonEngine, instance.daemonSocketPath = raw.DaemonEngine.value, raw.DaemonSocketPath.value
	return instance, nil
}

func validateRenderInstanceOutputs(rawOutputs []rawRenderInstanceOutput, logicalSet map[string]struct{}, instancePath string) ([]renderInstanceOutput, error) {
	outputs := make([]renderInstanceOutput, 0, len(rawOutputs))
	seenRefs := make(map[string]struct{}, len(rawOutputs))
	seenArtifacts := make(map[string]struct{}, len(rawOutputs))
	seenPaths := make(map[string]struct{}, len(rawOutputs))
	for index, raw := range rawOutputs {
		outputPath := fmt.Sprintf("%s.outputs[%d]", instancePath, index)
		if _, exists := logicalSet[raw.Ref]; !exists {
			return nil, fail(ErrInvalidPlan, outputPath+".ref", "references undeclared logical output %q", raw.Ref)
		}
		if _, exists := seenRefs[raw.Ref]; exists {
			return nil, fail(ErrDuplicate, outputPath+".ref", "logical output %q is materialized more than once", raw.Ref)
		}
		if _, err := validatePortablePath(raw.Path); err != nil {
			return nil, wrap(ErrInvalidPath, outputPath+".path", "invalid materialized output path", err)
		}
		if _, exists := seenArtifacts[raw.ArtifactRef]; exists {
			return nil, fail(ErrDuplicate, outputPath+".artifactRef", "artifact %q is materialized more than once in the instance", raw.ArtifactRef)
		}
		pathKey := portableKey(raw.Path)
		if _, exists := seenPaths[pathKey]; exists {
			return nil, fail(ErrDuplicate, outputPath+".path", "path %q is materialized more than once in the instance", raw.Path)
		}
		seenRefs[raw.Ref], seenArtifacts[raw.ArtifactRef], seenPaths[pathKey] = struct{}{}, struct{}{}, struct{}{}
		outputs = append(outputs, renderInstanceOutput{ref: raw.Ref, artifactID: raw.ArtifactRef, path: raw.Path})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].ref < outputs[j].ref })
	return outputs, nil
}

func indexRuntimeDaemonBindings(rawBindings []json.RawMessage, bindingPath string) (map[string]rawRuntimeDaemonBinding, error) {
	result := make(map[string]rawRuntimeDaemonBinding, len(rawBindings))
	for index, raw := range rawBindings {
		var binding rawRuntimeDaemonBinding
		path := fmt.Sprintf("%s[%d]", bindingPath, index)
		if err := decodeStrict(raw, &binding); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode runtime daemon binding", err)
		}
		if binding.Engine != "docker" {
			return nil, fail(ErrInvalidPlan, path+".engine", "runtime daemon binding engine must be docker")
		}
		if err := validateDockerSocketPath(binding.SocketPath); err != nil {
			return nil, wrap(ErrInvalidPlan, path+".socketPath", "invalid runtime daemon socket path", err)
		}
		if _, exists := result[binding.NodeRef]; exists {
			return nil, fail(ErrDuplicate, path+".nodeRef", "node %q has more than one daemon binding", binding.NodeRef)
		}
		result[binding.NodeRef] = binding
	}
	return result, nil
}

func validateDirectSocketRequirements(requirements []implementationInterface, instances []renderUnitInstance, placement rawRenderUnitPlacement, unitPath string) error {
	for _, requirement := range requirements {
		if requirement.kind != dockerSocketDirectInterfaceKind {
			continue
		}
		requirementPath := unitPath + ".requiresInterfaces." + requirement.id
		if placement.Scope != "node-local" || placement.Cardinality != "one-per-daemon" {
			return fail(ErrInvalidPlan, requirementPath, "direct Docker socket access requires one-per-daemon placement")
		}
		// The Docker engine remains a privileged trust boundary; only the
		// compiler-derived ref/path equality below it was dropped.
		for _, instance := range instances {
			if instance.daemonEngine != "docker" {
				return fail(ErrInvalidPlan, requirementPath, "direct socket access requires a Docker daemon binding on every render instance")
			}
		}
	}
	return nil
}

func validateDockerSocketPath(value string) error {
	// len(string) is the UTF-8 byte length in Go, which is the Linux ABI bound.
	if byteLength := len(value); byteLength > maxDockerSocketPathBytes {
		return fmt.Errorf("is %d UTF-8 bytes; Linux AF_UNIX paths permit at most %d bytes before the terminating NUL", byteLength, maxDockerSocketPathBytes)
	}
	if !dockerSocketPathPattern.MatchString(value) || !path.IsAbs(value) || path.Clean(value) != value {
		return fmt.Errorf("must be an absolute canonical path with portable ASCII segments and a .sock suffix")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("must not contain dot or parent-directory segments")
		}
	}
	return nil
}

func canonicalImplementationInterfaces(values []json.RawMessage, valuePath string) ([]byte, error) {
	canonical := make([]json.RawMessage, 0, len(values))
	for index, raw := range values {
		object, err := decodeRawObject(raw, fmt.Sprintf("%s[%d]", valuePath, index))
		if err != nil {
			return nil, err
		}
		value, err := canonicalObject(object)
		if err != nil {
			return nil, wrap(ErrInvalidPlan, fmt.Sprintf("%s[%d]", valuePath, index), "canonicalize implementation interface", err)
		}
		canonical = append(canonical, value)
	}
	return json.Marshal(canonical)
}

func parseImplementationInterfaces(values []json.RawMessage, role, valuePath string) ([]implementationInterface, error) {
	result := make([]implementationInterface, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		interfacePath := fmt.Sprintf("%s[%d]", valuePath, index)
		contract, err := parseImplementationInterface(raw, role, interfacePath)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[contract.id]; duplicate {
			return nil, fail(ErrDuplicate, interfacePath+".id", "duplicate %s interface %q", role, contract.id)
		}
		seen[contract.id] = struct{}{}
		result = append(result, contract)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

//nolint:gocyclo // Interface parsing exhaustively enforces mutually exclusive socket and network endpoint forms at the trust boundary.
func parseImplementationInterface(raw json.RawMessage, role, interfacePath string) (implementationInterface, error) {
	object, err := decodeRawObject(raw, interfacePath)
	if err != nil {
		return implementationInterface{}, err
	}
	id, err := requiredRawString(object, "id", interfacePath+".id")
	if err != nil {
		return implementationInterface{}, err
	}
	kind, err := requiredRawString(object, "kind", interfacePath+".kind")
	if err != nil {
		return implementationInterface{}, err
	}
	daemonRef, err := requiredRawString(object, "daemonRef", interfacePath+".daemonRef")
	if err != nil {
		return implementationInterface{}, err
	}
	policyProfile, err := requiredRawString(object, "policyProfile", interfacePath+".policyProfile")
	if err != nil {
		return implementationInterface{}, err
	}
	endpoint, err := requiredRawObject(object, "endpoint", interfacePath+".endpoint")
	if err != nil {
		return implementationInterface{}, err
	}
	networkRef, networkPresent, err := optionalRawContractID(endpoint, "networkRef", interfacePath+".endpoint.networkRef")
	if err != nil {
		return implementationInterface{}, err
	}
	endpointRef, endpointPresent, err := optionalRawContractID(endpoint, "ref", interfacePath+".endpoint.ref")
	if err != nil {
		return implementationInterface{}, err
	}
	if networkPresent != endpointPresent {
		return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".endpoint", "networkRef and ref must be present together")
	}
	var socketPath, socketPathSource string
	if kind == dockerSocketDirectInterfaceKind {
		if networkPresent || endpointPresent {
			return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".endpoint", "direct Docker socket endpoints cannot carry networkRef or ref")
		}
		_, hasPath := endpoint["path"]
		_, hasPathSource := endpoint["pathSource"]
		if hasPath == hasPathSource {
			return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".endpoint", "exactly one of path or pathSource is required")
		}
		if hasPath {
			socketPath, err = requiredRawString(endpoint, "path", interfacePath+".endpoint.path")
			if err != nil {
				return implementationInterface{}, err
			}
			if err := validateDockerSocketPath(socketPath); err != nil {
				return implementationInterface{}, wrap(ErrInvalidPlan, interfacePath+".endpoint.path", "invalid direct Docker socket path", err)
			}
		} else {
			socketPathSource, err = requiredRawString(endpoint, "pathSource", interfacePath+".endpoint.pathSource")
			if err != nil {
				return implementationInterface{}, err
			}
			if socketPathSource != dockerSocketPathSourceDaemonBinding {
				return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".endpoint.pathSource", "must be %q", dockerSocketPathSourceDaemonBinding)
			}
		}
	}
	contract := implementationInterface{
		id: id, kind: kind, daemonRef: daemonRef, networkRef: networkRef,
		policyProfile: policyProfile, endpointRef: endpointRef, socketPath: socketPath, socketPathSource: socketPathSource,
	}
	providerRaw, hasProviderBindings := object["providerBindings"]
	if role == "provider" && hasProviderBindings {
		return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".providerBindings", "provider interfaces cannot carry provider bindings")
	}
	if role == "consumer" && kind == "docker-http-readonly-v1" && !hasProviderBindings {
		return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".providerBindings", "networked required interface must carry exact provider bindings")
	}
	if !hasProviderBindings {
		return contract, nil
	}
	bindings, err := decodeImplementationProviderBindings(providerRaw, kind, interfacePath+".providerBindings")
	if err != nil {
		return implementationInterface{}, err
	}
	contract.providerBindings = bindings
	return contract, nil
}

func decodeImplementationProviderBindings(raw json.RawMessage, kind, valuePath string) ([]implementationProviderBinding, error) {
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		if err == nil {
			err = fmt.Errorf("must be an array")
		}
		return nil, wrap(ErrInvalidPlan, valuePath, "decode provider bindings", err)
	}
	if kind == "docker-http-readonly-v1" && len(values) == 0 {
		return nil, fail(ErrInvalidPlan, valuePath, "networked required interface needs at least one provider binding")
	}
	return parseImplementationProviderBindings(values, valuePath)
}

func optionalRawContractID(object map[string]json.RawMessage, name, valuePath string) (string, bool, error) {
	raw, exists := object[name]
	if !exists {
		return "", false, nil
	}
	var value string
	if err := decodeJSON(raw, &value, false); err != nil {
		return "", false, wrap(ErrInvalidPlan, valuePath, "decode contract ID", err)
	}
	return value, true, nil
}

func parseImplementationProviderBindings(values []json.RawMessage, valuePath string) ([]implementationProviderBinding, error) {
	result := make([]implementationProviderBinding, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		bindingPath := fmt.Sprintf("%s[%d]", valuePath, index)
		var decoded rawImplementationProviderBinding
		if err := decodeStrict(raw, &decoded); err != nil {
			return nil, wrap(ErrInvalidPlan, bindingPath, "decode implementation provider binding", err)
		}
		key := decoded.ConsumerInstanceRef
		if _, duplicate := seen[key]; duplicate {
			return nil, fail(ErrDuplicate, bindingPath, "consumer instance has more than one provider network binding")
		}
		seen[key] = struct{}{}
		result = append(result, implementationProviderBinding{
			interfaceRef: decoded.InterfaceRef, moduleRef: decoded.ModuleRef, unitRef: decoded.UnitRef,
			providerInstanceRef: decoded.ProviderInstanceRef, consumerInstanceRef: decoded.ConsumerInstanceRef,
			siteRef: decoded.SiteRef, nodeRef: decoded.NodeRef, daemonRef: decoded.DaemonRef,
			daemonInstanceRef: decoded.DaemonInstanceRef, networkRef: decoded.NetworkRef,
			networkInstanceRef: decoded.NetworkInstanceRef, policyProfile: decoded.PolicyProfile,
			endpointRef: decoded.EndpointRef,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].consumerInstanceRef != result[j].consumerInstanceRef {
			return result[i].consumerInstanceRef < result[j].consumerInstanceRef
		}
		return result[i].networkInstanceRef < result[j].networkInstanceRef
	})
	return result, nil
}
