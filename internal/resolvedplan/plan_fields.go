package resolvedplan

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

func (c *Compiler) buildInstall(spec *specView, resolved *resolution) (map[string]any, error) {
	install, err := cloneObject(spec.install, true)
	if err != nil {
		return nil, err
	}
	platform, err := objectField(install, "spec.install", "platform")
	if err != nil {
		return nil, err
	}
	management, err := stringField(platform, "spec.install.platform", "management")
	if err != nil {
		return nil, err
	}
	if management == "selected-provider" {
		if providerRef, exists, err := optionalStringField(platform, "spec.install.platform", "providerRef"); err != nil {
			return nil, err
		} else if exists {
			if !contains(resolved.providerIDs, providerRef) {
				return nil, fail(ErrUnrealizedCapability, "spec.install.platform.providerRef", "selected-provider management references provider %q, which is not selected by this resolution", providerRef)
			}
		} else {
			provider, exists := resolved.providerByCap["runtime-paas"]
			if !exists {
				return nil, fail(ErrUnrealizedCapability, "spec.install.platform.providerRef", "selected-provider management requires an explicit providerRef or resolved runtime-paas capability")
			}
			platform["providerRef"] = provider
		}
	}
	return install, nil
}

func (c *Compiler) buildGeneration(profile *profileView, spec *specView, definitionHash string, modules []any) (map[string]any, []any, error) {
	strategy, err := stringField(spec.generation, "spec.generation", "strategy")
	if err != nil {
		return nil, nil, err
	}
	target, err := stringField(spec.generation, "spec.generation", "target")
	if err != nil {
		return nil, nil, err
	}
	outputRoot, err := stringField(spec.generation, "spec.generation", "outputRoot")
	if err != nil {
		return nil, nil, err
	}
	contractVersion, err := stringField(profile.generation, "definition.generation", "contractVersion")
	if err != nil {
		return nil, nil, err
	}
	artifacts, err := deriveGenerationArtifacts(c.catalog.planArtifacts, modules, target, outputRoot)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"contractVersion": contractVersion, "strategy": strategy, "target": target,
		"outputRoot": outputRoot, "profileContractHash": definitionHash,
		"renderer":  map[string]any{"id": c.options.RendererID, "version": c.options.RendererVersion},
		"artifacts": artifacts, "rawSpecFallback": false,
	}, artifacts, nil
}

func (c *Compiler) buildCompatibility() map[string]any {
	return map[string]any{
		"minCLI": c.options.MinimumCLIVersion, "minRuntime": c.options.MinimumRuntimeVersion,
		"minGenerator":   c.options.MinimumGeneratorVersion,
		"specAPIVersion": "stackkit/v2alpha1", "planAPIVersion": "stackkit.resolved-plan/v1",
	}
}

func buildSource(spec *specView, sourceIntentHash, specHash, inventoryHash, definitionHash string) (map[string]any, error) {
	source, err := cloneObject(spec.source, true)
	if err != nil {
		return nil, err
	}
	kind, err := stringField(source, "spec.source", "kind")
	if err != nil {
		return nil, err
	}
	resolved := map[string]any{
		"kind":              kind,
		"intent":            map[string]any{"apiVersion": "stackkit/v2alpha1", "hash": sourceIntentHash},
		"normalizedSpec":    map[string]any{"apiVersion": "stackkit/v2alpha1", "hash": specHash},
		"inventory":         map[string]any{"apiVersion": "stackkit.inventory/v1", "hash": inventoryHash},
		"kitDefinitionHash": definitionHash,
	}
	if ref, exists, err := optionalStringField(source, "spec.source", "ref"); err != nil {
		return nil, err
	} else if exists {
		resolved["ref"] = ref
	}
	if migration, exists, err := optionalObjectField(source, "spec.source", "migration"); err != nil {
		return nil, err
	} else if exists {
		clone, err := cloneObject(migration, true)
		if err != nil {
			return nil, err
		}
		resolved["migration"] = clone
	}
	return resolved, nil
}

func buildSystem(spec *specView) (map[string]any, error) {
	host, err := cloneObject(spec.system, true)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"host": host}
	if spec.container != nil {
		container, err := cloneObject(spec.container, true)
		if err != nil {
			return nil, err
		}
		result["container"] = container
	}
	return result, nil
}

func buildNetwork(profile *profileView, spec *specView, resolved *resolution, modules []any, providerSites map[string][]string, catalog *indexedCatalog) (map[string]any, []any, error) {
	configuration, err := cloneObject(spec.network, true)
	if err != nil {
		return nil, nil, err
	}
	serviceEndpoints, err := indexResolvedServiceEndpoints(modules)
	if err != nil {
		return nil, nil, fmt.Errorf("index resolved service endpoints: %w", err)
	}
	if err := bindServiceEndpointHealthContracts(serviceEndpoints, catalog.modules); err != nil {
		return nil, nil, fmt.Errorf("bind service endpoint health contracts: %w", err)
	}
	routes, backendPools, routeHealth, err := buildRoutes(profile, spec, resolved, modules, providerSites, serviceEndpoints, catalog)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{"configuration": configuration, "routes": routes, "backendPools": backendPools}, routeHealth, nil
}

func buildRoutes(profile *profileView, spec *specView, resolved *resolution, modules []any, providerSites map[string][]string, serviceEndpoints resolvedServiceEndpointIndex, catalog *indexedCatalog) ([]any, []any, []any, error) {
	if len(spec.routes) == 0 {
		return []any{}, []any{}, []any{}, nil
	}
	routes := make([]any, 0, len(spec.routes))
	backendPools := make([]any, 0, len(spec.routes))
	routeHealth := make([]any, 0, len(spec.routes))
	for _, routeID := range sortedStringMapKeys(spec.routes) {
		route, backendPool, healthGate, err := buildRoute(profile, spec, resolved, modules, providerSites, serviceEndpoints, catalog, routeID)
		if err != nil {
			return nil, nil, nil, err
		}
		routes = append(routes, route)
		backendPools = append(backendPools, backendPool)
		routeHealth = append(routeHealth, healthGate)
	}
	return routes, backendPools, routeHealth, nil
}

//nolint:gocyclo // Route lowering validates the complete reachability, endpoint, access, TLS, and backend-pool contract atomically.
func buildRoute(profile *profileView, spec *specView, resolved *resolution, modules []any, providerSites map[string][]string, serviceEndpoints resolvedServiceEndpointIndex, catalog *indexedCatalog, routeID string) (map[string]any, map[string]any, map[string]any, error) {
	routePath := "spec.routes." + routeID
	intent, err := asObject(spec.routes[routeID], routePath)
	if err != nil {
		return nil, nil, nil, err
	}
	moduleRef, exists, err := optionalStringField(intent, routePath, "moduleRef")
	if err != nil {
		return nil, nil, nil, err
	}
	if !exists {
		return nil, nil, nil, fail(ErrUnresolvedPlacement, routePath+".moduleRef", "route must name a governed resolved module")
	}
	serviceRef, err := stringField(intent, routePath, "serviceRef")
	if err != nil {
		return nil, nil, nil, err
	}
	nodeSites := make(map[string]string, len(spec.nodeByID))
	for nodeID, node := range spec.nodeByID {
		nodeSites[nodeID] = node.siteRef
	}
	endpoint, err := resolveRouteServiceEndpoint(serviceEndpoints, moduleRef, serviceRef, spec.authoritySiteRef, nodeSites, routePath, spec)
	if err != nil {
		return nil, nil, nil, err
	}
	exposure, err := stringField(intent, routePath, "exposure")
	if err != nil {
		return nil, nil, nil, err
	}
	for _, originSiteRef := range endpoint.siteRefs {
		if err := validateRouteReachability(profile, spec, resolved, routeID, exposure, originSiteRef); err != nil {
			return nil, nil, nil, err
		}
	}
	realizations, err := buildRouteCapabilityRealizations(profile.reachability.routes[exposure].requiredRealizations, resolved, modules, providerSites, nodeSites, catalog, routePath+".capabilityRealizations")
	if err != nil {
		return nil, nil, nil, err
	}
	protocol, err := stringField(intent, routePath, "protocol")
	if err != nil {
		return nil, nil, nil, err
	}
	if exposure == "public" {
		host, err := stringField(intent, routePath, "host")
		if err != nil {
			return nil, nil, nil, err
		}
		if _, err := netip.ParseAddr(host); err == nil {
			return nil, nil, nil, fail(ErrProfileMismatch, routePath+".host", "public WebPKI routes require a DNS hostname, not an IP address")
		}
	}
	port, err := intField(intent, routePath, "port")
	if err != nil {
		return nil, nil, nil, err
	}
	targetPort := endpoint.targetPort
	if rawTarget, hasTarget := intent["targetPort"]; hasTarget {
		targetPort, err = intField(map[string]any{"value": rawTarget}, routePath, "value")
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if err := validateRouteEndpointContract(endpoint, spec, routePath, exposure, protocol, targetPort); err != nil {
		return nil, nil, nil, err
	}
	policyRef, err := stringField(intent, routePath, "accessPolicyRef")
	if err != nil {
		return nil, nil, nil, err
	}
	policy, exists := spec.access[policyRef]
	if !exists {
		return nil, nil, nil, fail(ErrProfileMismatch, routePath+".accessPolicyRef", "access policy %q does not exist", policyRef)
	}
	resolvedAccess, err := resolveRouteAccess(routeID, exposure, policyRef, policy)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := validateServiceEndpointAccess(endpoint, resolvedAccess, routePath+".access"); err != nil {
		return nil, nil, nil, err
	}
	resolvedTLS, err := resolveRouteTLS(routeID, protocol, exposure, spec.network, catalog, resolved.providerByCap, profile.reachability.routes[exposure].requiredRealizations)
	if err != nil {
		return nil, nil, nil, err
	}
	backendPool, err := buildRouteBackendPool(routeID, endpoint)
	if err != nil {
		return nil, nil, nil, err
	}
	healthGate, err := buildRouteHealthGate(routeID, backendPool, endpoint)
	if err != nil {
		return nil, nil, nil, err
	}
	route := map[string]any{
		"id": routeID, "serviceRef": serviceRef, "moduleRef": moduleRef,
		"originSiteRefs": stringSliceAny(endpoint.siteRefs), "originNodeRefs": stringSliceAny(endpoint.nodeRefs),
		"originSelector": endpoint.originSelector,
		"exposure":       exposure, "protocol": protocol, "upstreamProtocol": endpoint.upstreamProtocol, "port": port, "targetPort": targetPort,
		"access": resolvedAccess, "tls": resolvedTLS,
		"capabilityRealizations": realizations,
		"healthGateRef":          healthGate["id"],
		"backendPoolRef":         backendPool["id"],
	}
	if endpoint.originSelector == "single-site" || endpoint.originSelector == "control-authority-site" {
		route["originSiteRef"] = endpoint.siteRefs[0]
	} else {
		selection, err := cloneObject(endpoint.resolvedOriginSelection, true)
		if err != nil {
			return nil, nil, nil, err
		}
		route["originSelection"] = selection
	}
	for _, optional := range []string{"host", "path"} {
		if value, present := intent[optional]; present {
			route[optional] = value
		}
	}
	return route, backendPool, healthGate, nil
}

func validateRouteReachability(profile *profileView, spec *specView, resolved *resolution, routeID, exposure, originSiteRef string) error {
	path := "spec.routes." + routeID
	rule, exists := profile.reachability.routes[exposure]
	if !exists {
		return fail(ErrProfileMismatch, path+".exposure", "route exposure %q has no kit reachability rule", exposure)
	}
	if !rule.allowed {
		return fail(ErrProfileMismatch, path+".exposure", "route exposure %q is denied by the kit reachability contract", exposure)
	}
	resolvedCapabilities := stringSet(resolved.capabilityIDs)
	for _, requirement := range rule.requiredRealizations {
		if _, enabled := resolvedCapabilities[requirement.capabilityRef]; !enabled {
			return fail(ErrProfileMismatch, path+".exposure", "route exposure %q requires resolved capability %q for role %q", exposure, requirement.capabilityRef, requirement.role)
		}
	}
	origin, exists := spec.siteByID[originSiteRef]
	if !exists {
		return fail(ErrUnresolvedPlacement, path+".moduleRef", "route origin site %q is not part of the resolved topology", originSiteRef)
	}
	if !contains(rule.allowedOriginKinds, origin.kind) {
		return fail(ErrProfileMismatch, path+".moduleRef", "route exposure %q cannot originate from site kind %q", exposure, origin.kind)
	}
	return nil
}

func resolveRouteAccess(routeID, exposure, policyRef string, rawPolicy any) (map[string]any, error) {
	return resolveAccessDecision("spec.routes."+routeID+".accessPolicyRef", exposure, policyRef, rawPolicy)
}

//nolint:gocyclo // Access resolution normalizes optional policy fields while enforcing the full exposure and enrollment profile.
func resolveAccessDecision(referencePath, exposure, policyRef string, rawPolicy any) (map[string]any, error) {
	policy, err := asObject(rawPolicy, "access."+policyRef)
	if err != nil {
		return nil, err
	}
	policyExposure, err := stringField(policy, "access."+policyRef, "exposure")
	if err != nil {
		return nil, err
	}
	validExposure := exposure == "public" && policyExposure == "public" || exposure == "remote-private" && policyExposure == "private" || exposure == "local" && (policyExposure == "private" || policyExposure == "lan")
	if !validExposure {
		return nil, fail(ErrProfileMismatch, referencePath, "service exposure %q is incompatible with policy exposure %q", exposure, policyExposure)
	}
	authentication, err := stringField(policy, "access."+policyRef, "authentication")
	if err != nil {
		return nil, err
	}
	if (exposure == "public" || exposure == "remote-private") && authentication == "none" {
		return nil, fail(ErrProfileMismatch, referencePath, "service exposure %q requires authenticated access", exposure)
	}
	privilege, err := stringField(policy, "access."+policyRef, "privilege")
	if err != nil {
		return nil, err
	}
	enrolledDeviceRequired, err := boolFieldDefault(policy, "access."+policyRef, "enrolledDeviceRequired", false)
	if err != nil {
		return nil, err
	}
	ownerStepUpRequired, err := boolFieldDefault(policy, "access."+policyRef, "ownerStepUpRequired", false)
	if err != nil {
		return nil, err
	}
	lanStepDown, err := boolFieldDefault(policy, "access."+policyRef, "lanStepDown", false)
	if err != nil {
		return nil, err
	}
	if isStepUpPrivilege(privilege) &&
		(authentication != "human+device" || !enrolledDeviceRequired || !ownerStepUpRequired) {
		return nil, fail(ErrProfileMismatch, referencePath, "privileged access requires human+device, enrolled-device, and owner step-up controls")
	}
	resolved := map[string]any{
		"exposure": exposure, "defaultClosed": true, "policyRef": policyRef,
		"policyExposure": policyExposure, "lanStepDown": lanStepDown,
		"authentication": authentication, "privilege": privilege,
		"enrolledDeviceRequired": enrolledDeviceRequired, "ownerStepUpRequired": ownerStepUpRequired,
	}
	if _, exists := policy["allowedSiteRefs"]; exists {
		siteRefs, err := stringListField(policy, "access."+policyRef, "allowedSiteRefs", true)
		if err != nil {
			return nil, err
		}
		resolved["allowedSiteRefs"] = stringSliceAny(sortStringsUnique(siteRefs))
	}
	if _, exists := policy["allowedMethods"]; exists {
		methods, err := stringListField(policy, "access."+policyRef, "allowedMethods", true)
		if err != nil {
			return nil, err
		}
		resolved["allowedMethods"] = stringSliceAny(sortStringsUnique(methods))
	}
	return resolved, nil
}

func isStepUpPrivilege(privilege string) bool {
	switch privilege {
	case "admin", "identity", "secrets", "vault", "recovery":
		return true
	default:
		return false
	}
}

func resolveRouteTLS(routeID, protocol, exposure string, network map[string]any, catalog *indexedCatalog, capabilityProviders map[string]string, routeRequirements []routeCapabilityRealizationRequirement) (map[string]any, error) {
	tls, err := objectField(network, "spec.network", "tls")
	if err != nil {
		return nil, err
	}
	required := protocol == "https" || exposure == "public"
	if !required {
		return map[string]any{"required": false, "mode": "off"}, nil
	}
	requestedMinimum, err := stringField(tls, "spec.network.tls", "minVersion")
	if err != nil {
		return nil, err
	}
	if exposure == "public" {
		egressCapability := ""
		for _, requirement := range routeRequirements {
			if requirement.role != "egress" {
				continue
			}
			if egressCapability != "" {
				return nil, fail(ErrContractConflict, "spec.routes."+routeID+".tls", "public route has multiple egress TLS custody owners")
			}
			egressCapability = requirement.capabilityRef
		}
		if egressCapability != "" {
			if _, selected := capabilityProviders[egressCapability]; !selected {
				return nil, fail(ErrProfileMismatch, "spec.routes."+routeID+".tls", "external TLS custody requires resolved egress capability %q", egressCapability)
			}
			return map[string]any{
				"required": true, "mode": "external", "minVersion": requestedMinimum,
				"ownerCapabilityRef": egressCapability,
			}, nil
		}
	}
	defaultMode, err := stringField(tls, "spec.network.tls", "defaultMode")
	if err != nil {
		return nil, err
	}
	mode := ""
	requiredCapability := ""
	switch defaultMode {
	case "internal":
		if exposure == "public" {
			return nil, fail(ErrProfileMismatch, "spec.routes."+routeID+".tls", "public route requires catalog-owned public TLS termination")
		}
		mode = "internal"
		requiredCapability = "internal-pki"
	case "public":
		mode = "terminate-at-edge"
		requiredCapability = "public-tls"
	default:
		return nil, fail(ErrProfileMismatch, "spec.routes."+routeID, "TLS-required route cannot use network TLS mode %q", defaultMode)
	}
	providerID, exists := capabilityProviders[requiredCapability]
	if !exists {
		return nil, fail(ErrProfileMismatch, "spec.routes."+routeID+".tls", "TLS mode %q requires resolved capability %q", defaultMode, requiredCapability)
	}
	capability := catalog.capabilities[requiredCapability]
	profile, err := objectField(capability, "catalog.capabilities."+requiredCapability, "tlsProfile")
	if err != nil {
		return nil, fail(ErrContractConflict, "catalog.capabilities."+requiredCapability+".tlsProfile", "TLS capability has no governed profile")
	}
	profileCapability, err := stringField(profile, "catalog.capabilities."+requiredCapability+".tlsProfile", "capabilityRef")
	if err != nil || profileCapability != requiredCapability {
		return nil, fail(ErrContractConflict, "catalog.capabilities."+requiredCapability+".tlsProfile.capabilityRef", "TLS profile does not bind its capability")
	}
	profileMode, err := stringField(profile, "catalog.capabilities."+requiredCapability+".tlsProfile", "mode")
	if err != nil || profileMode != mode {
		return nil, fail(ErrContractConflict, "catalog.capabilities."+requiredCapability+".tlsProfile.mode", "TLS profile mode does not match resolved route mode %q", mode)
	}
	profileMinimum, err := stringField(profile, "catalog.capabilities."+requiredCapability+".tlsProfile", "minimumVersion")
	if err != nil {
		return nil, err
	}
	if tlsVersionRank(requestedMinimum) < tlsVersionRank(profileMinimum) {
		return nil, fail(ErrProfileMismatch, "spec.network.tls.minVersion", "requested minimum %q is weaker than governed TLS profile minimum %q", requestedMinimum, profileMinimum)
	}
	allowedKinds, err := stringListField(profile, "catalog.capabilities."+requiredCapability+".tlsProfile", "allowedIssuerKinds", true)
	if err != nil {
		return nil, err
	}
	provider := catalog.providers[providerID]
	capabilitySiteKinds, err := stringListField(capability, "catalog.capabilities."+requiredCapability, "supportedSiteKinds", true)
	if err != nil {
		return nil, err
	}
	providerSiteKinds, err := stringListField(provider, "catalog.providers."+providerID, "supportedSiteKinds", true)
	if err != nil {
		return nil, err
	}
	issuerValues, err := objectListField(provider, "catalog.providers."+providerID, "certificateIssuers")
	if err != nil {
		return nil, err
	}
	var issuer map[string]any
	for index, candidate := range issuerValues {
		candidatePath := fmt.Sprintf("catalog.providers.%s.certificateIssuers[%d]", providerID, index)
		candidateCapability, candidateErr := stringField(candidate, candidatePath, "capabilityRef")
		if candidateErr != nil {
			return nil, candidateErr
		}
		candidateKind, candidateErr := stringField(candidate, candidatePath, "kind")
		if candidateErr != nil {
			return nil, candidateErr
		}
		if candidateCapability == requiredCapability && contains(allowedKinds, candidateKind) {
			candidateSiteKinds, candidateErr := stringListField(candidate, candidatePath, "supportedSiteKinds", true)
			if candidateErr != nil {
				return nil, candidateErr
			}
			if !containsAll(providerSiteKinds, candidateSiteKinds) {
				return nil, fail(ErrContractConflict, candidatePath+".supportedSiteKinds", "issuer site kinds must be contained by provider %q", providerID)
			}
			if !containsAll(candidateSiteKinds, capabilitySiteKinds) {
				return nil, fail(ErrContractConflict, candidatePath+".supportedSiteKinds", "issuer site kinds must cover capability %q", requiredCapability)
			}
			if issuer != nil {
				return nil, fail(ErrContractConflict, "catalog.providers."+providerID+".certificateIssuers", "TLS capability %q has multiple compatible issuers", requiredCapability)
			}
			issuer = candidate
		}
	}
	if issuer == nil {
		return nil, fail(ErrContractConflict, "catalog.providers."+providerID+".certificateIssuers", "TLS capability %q has no compatible issuer", requiredCapability)
	}
	profileID, err := stringField(profile, "catalog.capabilities."+requiredCapability+".tlsProfile", "id")
	if err != nil {
		return nil, err
	}
	issuerID, err := stringField(issuer, "catalog.providers."+providerID+".certificateIssuers", "id")
	if err != nil {
		return nil, err
	}
	return map[string]any{"required": true, "mode": mode, "minVersion": requestedMinimum, "profileRef": profileID, "issuerRef": issuerID}, nil
}

func tlsVersionRank(version string) int {
	switch version {
	case "TLS1.3":
		return 13
	case "TLS1.2":
		return 12
	default:
		return 0
	}
}

func containsAll(values, required []string) bool {
	for _, value := range required {
		if !contains(values, value) {
			return false
		}
	}
	return true
}

func buildGates(profile *profileView, resolved *resolution, catalog *indexedCatalog, providerSites, providerNodes, moduleSites, moduleNodes map[string][]string, nodeSites map[string]string, artifacts, routeHealth []any) (map[string]any, error) {
	var health []any
	var healthIDs []string
	appendHealth := func(targetKind, targetRef string, contract map[string]any, siteRefs, nodeRefs []string) error {
		gates, err := materializeContractHealthGates(targetKind, targetRef, contract, siteRefs, nodeRefs, nodeSites)
		if err != nil {
			return err
		}
		for _, gate := range gates {
			gateID := gate["id"].(string)
			health = append(health, gate)
			healthIDs = append(healthIDs, gateID)
		}
		return nil
	}
	for _, capability := range resolved.capabilityIDs {
		providerID := resolved.providerByCap[capability]
		if err := appendHealth("capability", capability, catalog.capabilities[capability], providerSites[providerID], providerNodes[providerID]); err != nil {
			return nil, err
		}
	}
	for _, provider := range resolved.providerIDs {
		if err := appendHealth("provider", provider, catalog.providers[provider], providerSites[provider], providerNodes[provider]); err != nil {
			return nil, err
		}
	}
	for _, module := range sortedStringMapKeys(moduleSites) {
		if err := appendHealth("module", module, catalog.modules[module], moduleSites[module], moduleNodes[module]); err != nil {
			return nil, err
		}
	}
	for index, rawGate := range routeHealth {
		gate, err := asObject(rawGate, fmt.Sprintf("routeHealth[%d]", index))
		if err != nil {
			return nil, err
		}
		gateID, err := stringField(gate, fmt.Sprintf("routeHealth[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		health = append(health, gate)
		healthIDs = append(healthIDs, gateID)
	}
	if len(health) == 0 {
		return nil, fail(ErrUnrealizedCapability, "gates.health", "selected contracts provide no governed health gate")
	}
	sort.Slice(health, func(i, j int) bool {
		return health[i].(map[string]any)["id"].(string) < health[j].(map[string]any)["id"].(string)
	})
	healthIDs = sortStringsUnique(healthIDs)
	artifactIDs := make([]string, 0, len(artifacts))
	for _, rawArtifact := range artifacts {
		artifact := rawArtifact.(map[string]any)
		artifactIDs = append(artifactIDs, fmt.Sprint(artifact["id"]))
	}
	artifactIDs = sortStringsUnique(artifactIDs)
	scenarios := sortStringsUnique(append(profile.evidence, collectContractEvidence(resolved, catalog, sortedStringMapKeys(moduleSites))...))
	evidence := make([]any, 0, len(scenarios))
	for i, scenario := range scenarios {
		evidence = append(evidence, map[string]any{
			"id": fmt.Sprintf("evidence-%03d", i+1), "scenario": scenario,
			"phase": "verify", "producer": "evidence-runner", "required": true,
			"healthGateRefs": stringSliceAny(healthIDs), "artifactRefs": stringSliceAny(artifactIDs),
		})
	}
	return map[string]any{
		"health": health, "evidence": evidence,
		"apply": map[string]any{
			"requireFreshPlanHash": true, "requireCompatibleCLI": true, "requireCompatibleRuntime": true,
			"requireGenerationArtifacts": true, "requireResolvedSecrets": true,
		},
	}, nil
}

func materializeContractHealthGates(targetKind, targetRef string, contract map[string]any, siteRefs, nodeRefs []string, nodeSites map[string]string) ([]map[string]any, error) {
	healthContracts, err := objectListOptional(contract, "health")
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	for _, healthContract := range healthContracts {
		healthID, err := stringField(healthContract, "health", "id")
		if err != nil {
			return nil, err
		}
		contractHash, err := canonicalHash(healthContract, true)
		if err != nil {
			return nil, err
		}
		scope, _ := healthContract["scope"].(string)
		if scope == "each-node" {
			if targetKind != "module" || len(nodeRefs) == 0 {
				return nil, fail(ErrContractConflict, "health."+healthID+".scope", "each-node health is valid only for a placed module")
			}
			for _, nodeRef := range sortStringsUnique(nodeRefs) {
				siteRef := nodeSites[nodeRef]
				if siteRef == "" || !contains(siteRefs, siteRef) {
					return nil, fail(ErrUnresolvedPlacement, "health."+healthID+".nodeRefs", "node %q has no exact module Site binding", nodeRef)
				}
				gate, err := cloneObject(healthContract, true)
				if err != nil {
					return nil, err
				}
				gateID := contractID(targetKind + "-" + targetRef + "-" + healthID + "-node-" + nodeRef)
				gate["id"], gate["sourceRef"], gate["contractHash"], gate["targetKind"], gate["targetRef"] = gateID, healthID, contractHash, targetKind, targetRef
				gate["siteRefs"], gate["nodeRefs"], gate["required"] = []any{siteRef}, []any{nodeRef}, true
				result = append(result, gate)
			}
			continue
		}
		gate, err := cloneObject(healthContract, true)
		if err != nil {
			return nil, err
		}
		gateID := contractID(targetKind + "-" + targetRef + "-" + healthID)
		gate["id"], gate["sourceRef"], gate["contractHash"], gate["targetKind"], gate["targetRef"] = gateID, healthID, contractHash, targetKind, targetRef
		gate["siteRefs"], gate["required"] = stringSliceAny(siteRefs), true
		if len(nodeRefs) > 0 {
			gate["nodeRefs"] = stringSliceAny(nodeRefs)
		}
		result = append(result, gate)
	}
	return result, nil
}

func collectContractEvidence(resolved *resolution, catalog *indexedCatalog, moduleIDs []string) []string {
	var evidence []string
	for _, capability := range resolved.capabilityIDs {
		values, _ := stringListField(catalog.capabilities[capability], "catalog.capabilities."+capability, "evidence", false)
		evidence = append(evidence, values...)
	}
	for _, provider := range resolved.providerIDs {
		values, _ := stringListField(catalog.providers[provider], "catalog.providers."+provider, "evidence", false)
		evidence = append(evidence, values...)
	}
	for _, addon := range resolved.addonIDs {
		values, _ := stringListField(catalog.addons[addon], "catalog.addons."+addon, "evidence", false)
		evidence = append(evidence, values...)
	}
	evidence = append(evidence, collectModuleEvidence(catalog, moduleIDs)...)
	return evidence
}

func collectModuleEvidence(catalog *indexedCatalog, moduleIDs []string) []string {
	var evidence []string
	for _, module := range moduleIDs {
		values, _ := stringListField(catalog.modules[module], "catalog.modules."+module, "evidence", false)
		evidence = append(evidence, values...)
	}
	return evidence
}

func objectListOptional(object map[string]any, name string) ([]map[string]any, error) {
	if _, exists := object[name]; !exists {
		return nil, nil
	}
	return objectListField(object, "contract", name)
}

func contractID(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	previousDash := false
	for _, character := range value {
		valid := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if valid {
			builder.WriteRune(character)
			previousDash = false
		} else if !previousDash && builder.Len() > 0 {
			builder.WriteByte('-')
			previousDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
