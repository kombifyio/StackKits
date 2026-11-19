package resolvedplan

import (
	"fmt"
	"sort"
)

func safeModuleInternalPKI(moduleID string, module map[string]any, source modulePlanInputSource) (map[string]any, error) {
	const capabilityID = "internal-pki"
	provided, err := stringListField(module, "modules."+moduleID, "provides", true)
	if err != nil {
		return nil, err
	}
	if len(provided) != 1 || provided[0] != capabilityID {
		return nil, fmt.Errorf("module %q internalPKI projection requires exactly capability %q", moduleID, capabilityID)
	}
	providerRef, err := stringField(module, "modules."+moduleID, "providerRef")
	if err != nil {
		return nil, err
	}
	capability, err := resolvedPlanObjectByID(source.capabilities, capabilityID, "capabilities")
	if err != nil {
		return nil, err
	}
	capabilityProviderRef, err := stringField(capability, "capabilities."+capabilityID, "providerRef")
	if err != nil || capabilityProviderRef != providerRef {
		return nil, fmt.Errorf("module %q provider %q does not own resolved capability %q", moduleID, providerRef, capabilityID)
	}
	profile, err := objectField(capability, "capabilities."+capabilityID, "tlsProfile")
	if err != nil {
		return nil, err
	}
	profileProjection, err := selectObjectFields(profile, "capabilities."+capabilityID+".tlsProfile", []string{"id", "capabilityRef", "mode", "trustDomain", "minimumVersion", "allowedIssuerKinds"})
	if err != nil {
		return nil, err
	}
	if mode, fieldErr := stringField(profileProjection, "internalPKI.profile", "mode"); fieldErr != nil || mode != "internal" {
		return nil, fmt.Errorf("internal PKI profile must use internal TLS mode")
	}
	allowedKinds, err := stringListField(profileProjection, "internalPKI.profile", "allowedIssuerKinds", true)
	if err != nil {
		return nil, err
	}
	provider, err := resolvedPlanObjectByID(source.providers, providerRef, "providers")
	if err != nil {
		return nil, err
	}
	issuerContracts, err := objectListField(provider, "providers."+providerRef, "certificateIssuers")
	if err != nil {
		return nil, err
	}
	var issuer map[string]any
	for index, candidate := range issuerContracts {
		path := fmt.Sprintf("providers.%s.certificateIssuers[%d]", providerRef, index)
		candidateCapability, fieldErr := stringField(candidate, path, "capabilityRef")
		if fieldErr != nil {
			return nil, fieldErr
		}
		candidateKind, fieldErr := stringField(candidate, path, "kind")
		if fieldErr != nil {
			return nil, fieldErr
		}
		if candidateCapability != capabilityID || !contains(allowedKinds, candidateKind) {
			continue
		}
		if issuer != nil {
			return nil, fmt.Errorf("provider %q has multiple internal PKI issuers", providerRef)
		}
		issuer = candidate
	}
	if issuer == nil {
		return nil, fmt.Errorf("provider %q has no internal PKI issuer", providerRef)
	}
	issuerProjection, err := selectObjectFields(issuer, "internalPKI.issuer", []string{"id", "capabilityRef", "kind", "challenge", "supportedSiteKinds", "validitySeconds", "requiredInputSlotIDs", "materialSlots", "renewal"})
	if err != nil {
		return nil, err
	}
	authoritySiteRef, err := stringField(source.controlPlane, "controlPlane", "authoritySiteRef")
	if err != nil {
		return nil, err
	}
	controlMembers, err := stringListField(source.controlPlane, "controlPlane", "members", true)
	if err != nil || len(controlMembers) != 1 {
		return nil, fmt.Errorf("internal PKI requires exactly one explicit Home CA authority node")
	}
	authorityNodeRef := controlMembers[0]
	site, err := resolvedPlanObjectByID(source.sites, authoritySiteRef, "sites")
	if err != nil {
		return nil, err
	}
	siteKind, err := stringField(site, "sites."+authoritySiteRef, "kind")
	if err != nil || siteKind != "home" {
		return nil, fmt.Errorf("internal PKI authority Site %q must be Home", authoritySiteRef)
	}
	nodeByID := make(map[string]map[string]any, len(source.nodes))
	for index, rawNode := range source.nodes {
		node, fieldErr := asObject(rawNode, fmt.Sprintf("nodes[%d]", index))
		if fieldErr != nil {
			return nil, fieldErr
		}
		id, fieldErr := stringField(node, fmt.Sprintf("nodes[%d]", index), "id")
		if fieldErr != nil {
			return nil, fieldErr
		}
		nodeByID[id] = node
	}
	targets := make([]any, 0, len(source.nodes))
	authorityFound := false
	for _, nodeRef := range sortedStringMapKeys(nodeByID) {
		node, exists := nodeByID[nodeRef]
		if !exists {
			return nil, fmt.Errorf("internal PKI target node %q is absent from resolved topology", nodeRef)
		}
		targetSiteRef, fieldErr := stringField(node, "nodes."+nodeRef, "siteRef")
		if fieldErr != nil {
			return nil, fieldErr
		}
		targetSite, fieldErr := resolvedPlanObjectByID(source.sites, targetSiteRef, "sites")
		if fieldErr != nil {
			return nil, fieldErr
		}
		targetKind, fieldErr := stringField(targetSite, "sites."+targetSiteRef, "kind")
		if fieldErr != nil {
			return nil, fieldErr
		}
		if targetKind != "home" {
			continue
		}
		if nodeRef == authorityNodeRef && targetSiteRef == authoritySiteRef {
			authorityFound = true
		}
		targets = append(targets, map[string]any{"siteRef": targetSiteRef, "nodeRef": nodeRef})
	}
	if !authorityFound {
		return nil, fmt.Errorf("internal PKI CA authority %q is not an exact module target", authorityNodeRef)
	}
	routes := []map[string]any{}
	if source.network != nil {
		if _, exists := source.network["routes"]; exists {
			routes, err = objectListField(source.network, "network", "routes")
			if err != nil {
				return nil, err
			}
		}
	}
	identities := make([]any, 0)
	for index, route := range routes {
		routePath := fmt.Sprintf("network.routes[%d]", index)
		tls, fieldErr := objectField(route, routePath, "tls")
		if fieldErr != nil {
			return nil, fieldErr
		}
		mode, fieldErr := stringField(tls, routePath+".tls", "mode")
		if fieldErr != nil {
			return nil, fieldErr
		}
		if mode != "internal" {
			continue
		}
		profileRef, fieldErr := stringField(tls, routePath+".tls", "profileRef")
		if fieldErr != nil {
			return nil, fieldErr
		}
		issuerRef, fieldErr := stringField(tls, routePath+".tls", "issuerRef")
		if fieldErr != nil {
			return nil, fieldErr
		}
		profileID, fieldErr := stringField(profileProjection, "internalPKI.profile", "id")
		if fieldErr != nil {
			return nil, fieldErr
		}
		issuerID, fieldErr := stringField(issuerProjection, "internalPKI.issuer", "id")
		if fieldErr != nil {
			return nil, fieldErr
		}
		if profileRef != profileID || issuerRef != issuerID {
			return nil, fmt.Errorf("%s internal TLS authority does not match the selected internal PKI contract", routePath)
		}
		routeID, fieldErr := stringField(route, routePath, "id")
		if fieldErr != nil {
			return nil, fieldErr
		}
		serviceRef, fieldErr := stringField(route, routePath, "serviceRef")
		if fieldErr != nil {
			return nil, fieldErr
		}
		moduleRef, fieldErr := stringField(route, routePath, "moduleRef")
		if fieldErr != nil {
			return nil, fieldErr
		}
		host, fieldErr := stringField(route, routePath, "host")
		if fieldErr != nil {
			return nil, fmt.Errorf("%s internal TLS route requires one compiler-owned DNS SAN: %w", routePath, fieldErr)
		}
		originNodeRefs, fieldErr := stringListField(route, routePath, "originNodeRefs", true)
		if fieldErr != nil {
			return nil, fieldErr
		}
		for _, nodeRef := range sortStringsUnique(originNodeRefs) {
			node, exists := nodeByID[nodeRef]
			if !exists {
				return nil, fmt.Errorf("%s origin node %q is absent from resolved topology", routePath, nodeRef)
			}
			siteRef, fieldErr := stringField(node, "nodes."+nodeRef, "siteRef")
			if fieldErr != nil {
				return nil, fieldErr
			}
			identities = append(identities, map[string]any{
				"id": routeID + "-" + nodeRef, "routeRef": routeID, "serviceRef": serviceRef,
				"moduleRef": moduleRef, "siteRef": siteRef, "nodeRef": nodeRef,
				"subjectRef": moduleRef + "-" + serviceRef + "-" + nodeRef,
				"dnsSANs":    []any{host}, "ipSANs": []any{},
			})
		}
	}
	// Routes reach this projection in resolver order at compile time and in
	// canonical order when a plan is verified; one leaf identity order keeps
	// both projections exact for any number of routes.
	sort.Slice(identities, func(i, j int) bool {
		return identities[i].(map[string]any)["id"].(string) < identities[j].(map[string]any)["id"].(string)
	})
	return normalizedObject(map[string]any{
		"capabilityRef": capabilityID,
		"providerRef":   providerRef,
		"authority": map[string]any{
			"id": "stackkits-home-root-ca", "role": "root-ca",
			"siteRef": authoritySiteRef, "nodeRef": authorityNodeRef,
			"trustDomainRef": source.stackID, "subjectRef": "stackkits-home-root-ca",
			"keyAlgorithm":     "ecdsa-p256",
			"basicConstraints": map[string]any{"ca": true, "pathLen": -1},
			"keyUsage":         []any{"cert-sign", "crl-sign", "digital-signature"},
		},
		"trustDistribution": map[string]any{
			"scope":   "product-local-owner-root",
			"targets": targets,
			"materialSlot": map[string]any{
				"id": "trust-root", "purpose": "trust-root", "sensitivity": "public",
			},
		},
		"leafIssuance": map[string]any{
			"status": "bound", "subjectAuthority": "compiler-derived-service",
			"sanAuthority": "compiler-derived-route", "ca": false, "keyAlgorithm": "acme-owner-selected",
			"keyUsage":         []any{"digital-signature"},
			"extendedKeyUsage": []any{"server-auth"},
			"requiredObservationFields": []any{
				"certificate-fingerprint", "public-key-fingerprint", "trust-root-fingerprint",
				"serial", "not-before", "not-after", "observed-at",
			},
			"identities": identities,
		},
		"profile": profileProjection,
		"issuer":  issuerProjection,
	}, "internalPKI")
}

func safeModulePublicTLS(moduleID string, module map[string]any, capabilities, providers []any, network map[string]any) (map[string]any, error) {
	const capabilityID = "public-tls"
	provided, err := stringListField(module, "modules."+moduleID, "provides", true)
	if err != nil {
		return nil, err
	}
	if len(provided) != 1 || provided[0] != capabilityID {
		return nil, fmt.Errorf("module %q publicTLS projection requires exactly capability %q", moduleID, capabilityID)
	}
	providerRef, err := stringField(module, "modules."+moduleID, "providerRef")
	if err != nil {
		return nil, err
	}
	capability, err := resolvedPlanObjectByID(capabilities, capabilityID, "capabilities")
	if err != nil {
		return nil, err
	}
	capabilityProviderRef, err := stringField(capability, "capabilities."+capabilityID, "providerRef")
	if err != nil {
		return nil, err
	}
	if capabilityProviderRef != providerRef {
		return nil, fmt.Errorf("module %q provider %q does not own resolved capability %q", moduleID, providerRef, capabilityID)
	}
	profile, err := objectField(capability, "capabilities."+capabilityID, "tlsProfile")
	if err != nil {
		return nil, err
	}
	profileProjection, err := selectObjectFields(profile, "capabilities."+capabilityID+".tlsProfile", []string{"id", "capabilityRef", "mode", "trustDomain", "minimumVersion", "allowedIssuerKinds"})
	if err != nil {
		return nil, err
	}
	profileID, err := stringField(profileProjection, "publicTLS.profile", "id")
	if err != nil {
		return nil, err
	}
	profileMode, err := stringField(profileProjection, "publicTLS.profile", "mode")
	if err != nil || profileMode != "terminate-at-edge" {
		return nil, fmt.Errorf("public TLS profile must terminate at the edge")
	}
	allowedKinds, err := stringListField(profileProjection, "publicTLS.profile", "allowedIssuerKinds", true)
	if err != nil {
		return nil, err
	}
	provider, err := resolvedPlanObjectByID(providers, providerRef, "providers")
	if err != nil {
		return nil, err
	}
	issuerContracts, err := objectListField(provider, "providers."+providerRef, "certificateIssuers")
	if err != nil {
		return nil, err
	}
	var issuer map[string]any
	for index, candidate := range issuerContracts {
		path := fmt.Sprintf("providers.%s.certificateIssuers[%d]", providerRef, index)
		candidateCapability, fieldErr := stringField(candidate, path, "capabilityRef")
		if fieldErr != nil {
			return nil, fieldErr
		}
		candidateKind, fieldErr := stringField(candidate, path, "kind")
		if fieldErr != nil {
			return nil, fieldErr
		}
		if candidateCapability != capabilityID || !contains(allowedKinds, candidateKind) {
			continue
		}
		if issuer != nil {
			return nil, fmt.Errorf("provider %q has multiple public TLS issuers", providerRef)
		}
		issuer = candidate
	}
	if issuer == nil {
		return nil, fmt.Errorf("provider %q has no public TLS issuer", providerRef)
	}
	issuerProjection, err := selectObjectFields(issuer, "publicTLS.issuer", []string{"id", "capabilityRef", "kind", "challenge", "supportedSiteKinds", "validitySeconds", "requiredInputSlotIDs", "materialSlots", "renewal"})
	if err != nil {
		return nil, err
	}
	issuerID, err := stringField(issuerProjection, "publicTLS.issuer", "id")
	if err != nil {
		return nil, err
	}
	routes, err := objectListField(network, "network", "routes")
	if err != nil {
		return nil, err
	}
	routesByID := map[string]map[string]any{}
	for index, route := range routes {
		path := fmt.Sprintf("network.routes[%d]", index)
		tls, err := objectField(route, path, "tls")
		if err != nil {
			return nil, err
		}
		routeProfileRef, hasProfile, err := optionalStringField(tls, path+".tls", "profileRef")
		if err != nil {
			return nil, err
		}
		routeIssuerRef, hasIssuer, err := optionalStringField(tls, path+".tls", "issuerRef")
		if err != nil {
			return nil, err
		}
		if !hasProfile && !hasIssuer {
			continue
		}
		if routeProfileRef != profileID || routeIssuerRef != issuerID {
			continue
		}
		exposure, err := stringField(route, path, "exposure")
		if err != nil || exposure != "public" {
			return nil, fmt.Errorf("%s bound to public TLS is not public", path)
		}
		protocol, err := stringField(route, path, "protocol")
		if err != nil || protocol != "https" {
			return nil, fmt.Errorf("%s bound to public TLS is not HTTPS", path)
		}
		id, err := stringField(route, path, "id")
		if err != nil {
			return nil, err
		}
		host, err := stringField(route, path, "host")
		if err != nil {
			return nil, err
		}
		port, err := intField(route, path, "port")
		if err != nil {
			return nil, err
		}
		pathValue, err := stringField(route, path, "path")
		if err != nil {
			return nil, err
		}
		minimumVersion, err := stringField(tls, path+".tls", "minVersion")
		if err != nil {
			return nil, err
		}
		routesByID[id] = map[string]any{
			"id": id, "host": host, "port": port, "path": pathValue, "exposure": exposure, "protocol": protocol,
			"tls": map[string]any{"mode": "terminate-at-edge", "minVersion": minimumVersion, "profileRef": profileID, "issuerRef": issuerID},
		}
	}
	projectedRoutes := make([]any, 0, len(routesByID))
	for _, id := range sortedStringMapKeys(routesByID) {
		projectedRoutes = append(projectedRoutes, routesByID[id])
	}
	return normalizedObject(map[string]any{
		"capabilityRef": capabilityID,
		"providerRef":   providerRef,
		"profile":       profileProjection,
		"issuer":        issuerProjection,
		"routes":        projectedRoutes,
	}, "publicTLS")
}
