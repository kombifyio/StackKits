package resolvedplan

import (
	"fmt"
)

func safeModuleBridgeOriginMTLS(moduleID string, bridge, identityTrust map[string]any) (map[string]any, error) {
	if moduleID != "stackkits-bridge-origin-mtls-runtime" {
		return nil, fmt.Errorf("module %q cannot consume bridge origin mTLS authority", moduleID)
	}
	if bridge == nil || identityTrust == nil {
		return nil, fmt.Errorf("bridge origin mTLS requires resolved bridge and identity trust")
	}
	publications, err := objectListField(bridge, "bridge", "publications")
	if err != nil {
		return nil, err
	}
	issuers, err := objectListField(identityTrust, "identityTrust", "credentialIssuers")
	if err != nil {
		return nil, err
	}
	verifiers, err := objectListField(identityTrust, "identityTrust", "verifierPlacements")
	if err != nil {
		return nil, err
	}
	distributions, err := objectListField(identityTrust, "identityTrust", "verifierDistributions")
	if err != nil {
		return nil, err
	}
	projected := make([]any, 0, len(publications))
	for index, publication := range publications {
		path := fmt.Sprintf("bridge.publications[%d]", index)
		serviceRef, err := stringField(publication, path, "serviceRef")
		if err != nil {
			return nil, err
		}
		sourceSiteRef, err := stringField(publication, path, "sourceSiteRef")
		if err != nil {
			return nil, err
		}
		edgeSiteRef, err := stringField(publication, path, "edgeSiteRef")
		if err != nil {
			return nil, err
		}
		origin, err := objectField(publication, path, "origin")
		if err != nil {
			return nil, err
		}
		identityRef, err := stringField(origin, path+".origin", "identityRef")
		if err != nil {
			return nil, err
		}
		if identityRef != serviceRef+"-origin" {
			return nil, fmt.Errorf("%s.origin.identityRef must equal the compiler-bound service origin identity", path)
		}
		issuer, err := exactWorkloadIssuerForSite(issuers, sourceSiteRef, path)
		if err != nil {
			return nil, err
		}
		issuerRef, err := stringField(issuer, "identityTrust.credentialIssuers", "id")
		if err != nil {
			return nil, err
		}
		verifier, err := exactWorkloadVerifierForSite(verifiers, issuerRef, edgeSiteRef, path)
		if err != nil {
			return nil, err
		}
		distribution, err := exactVerifierDistribution(distributions, issuerRef, sourceSiteRef, edgeSiteRef, path)
		if err != nil {
			return nil, err
		}
		audiences, err := stringListField(issuer, "identityTrust.credentialIssuers."+issuerRef, "audiences", true)
		if err != nil || len(audiences) != 1 {
			return nil, fmt.Errorf("%s workload issuer must bind exactly one audience", path)
		}
		issuerURI, err := stringField(issuer, "identityTrust.credentialIssuers."+issuerRef, "issuer")
		if err != nil {
			return nil, err
		}
		keySetRef, err := stringField(issuer, "identityTrust.credentialIssuers."+issuerRef, "verificationKeySetRef")
		if err != nil {
			return nil, err
		}
		credentialTTLSeconds, err := intField(issuer, "identityTrust.credentialIssuers."+issuerRef, "credentialTTLSeconds")
		if err != nil {
			return nil, err
		}
		revocationMaxStalenessSeconds, err := intField(issuer, "identityTrust.credentialIssuers."+issuerRef, "revocationMaxStalenessSeconds")
		if err != nil {
			return nil, err
		}
		proofRequired, err := boolFieldDefault(issuer, "identityTrust.credentialIssuers."+issuerRef, "proofOfPossessionRequired", false)
		if err != nil || !proofRequired {
			return nil, fmt.Errorf("%s workload issuer must require proof of possession", path)
		}
		verifierRef, err := stringField(verifier, "identityTrust.verifierPlacements", "id")
		if err != nil {
			return nil, err
		}
		distributionRef, err := stringField(distribution, "identityTrust.verifierDistributions", "id")
		if err != nil {
			return nil, err
		}
		maxStaleness, err := intField(distribution, "identityTrust.verifierDistributions."+distributionRef, "maxStalenessSeconds")
		if err != nil {
			return nil, err
		}
		moduleRef, err := stringField(publication, path, "moduleRef")
		if err != nil {
			return nil, err
		}
		unitRef, err := stringField(publication, path, "unitRef")
		if err != nil {
			return nil, err
		}
		nodeRefs, err := stringListField(publication, path, "originNodeRefs", true)
		if err != nil {
			return nil, err
		}
		instanceRefs, err := stringListField(publication, path, "originInstanceRefs", true)
		if err != nil {
			return nil, err
		}
		targetObjects, err := objectListField(publication, path, "originTargets")
		if err != nil {
			return nil, err
		}
		originTargets := make([]any, 0, len(targetObjects))
		for targetIndex, target := range targetObjects {
			targetPath := fmt.Sprintf("%s.originTargets[%d]", path, targetIndex)
			nodeRef, err := stringField(target, targetPath, "nodeRef")
			if err != nil {
				return nil, err
			}
			instanceRef, err := stringField(target, targetPath, "instanceRef")
			if err != nil {
				return nil, err
			}
			originTargets = append(originTargets, map[string]any{"nodeRef": nodeRef, "instanceRef": instanceRef})
		}
		upstreamProtocol, err := stringField(publication, path, "upstreamProtocol")
		if err != nil {
			return nil, err
		}
		targetPort, err := intField(publication, path, "targetPort")
		if err != nil {
			return nil, err
		}
		projected = append(projected, map[string]any{
			"serviceRef": serviceRef, "identityRef": identityRef, "sourceSiteRef": sourceSiteRef, "edgeSiteRef": edgeSiteRef,
			"moduleRef": moduleRef, "unitRef": unitRef, "originNodeRefs": stringSliceAny(nodeRefs), "originInstanceRefs": stringSliceAny(instanceRefs),
			"originTargets":    originTargets,
			"upstreamProtocol": upstreamProtocol, "targetPort": targetPort,
			"transport": map[string]any{
				"mode": "mtls-origin-proxy", "minimumTLSVersion": "TLS1.3",
				"serverName": serviceRef + ".origin.stackkit.internal", "outboundOnly": true, "generalLANAccess": false,
			},
			"workloadIdentity": map[string]any{
				"credentialIssuerRef": issuerRef, "issuer": issuerURI, "audience": audiences[0],
				"verificationKeySetRef": keySetRef, "proofOfPossessionRequired": true,
				"credentialTTLSeconds": credentialTTLSeconds, "revocationMaxStalenessSeconds": revocationMaxStalenessSeconds,
			},
			"edgeVerifier": map[string]any{
				"verifierRef": verifierRef, "distributionRef": distributionRef, "verificationKeySetRef": keySetRef,
				"maxStalenessSeconds": maxStaleness, "includesPrivateKeyMaterial": false,
				"includesSigningAuthority": false, "reverseAllowed": false,
			},
		})
	}
	if len(projected) == 0 {
		return nil, fmt.Errorf("bridge origin mTLS requires at least one publication")
	}
	return normalizedObject(map[string]any{"publications": projected}, "bridgeOriginMTLS")
}

func exactWorkloadIssuerForSite(issuers []map[string]any, siteRef, path string) (map[string]any, error) {
	return exactIdentityObjectForSite(issuers, "credential issuer", siteRef, func(candidate map[string]any) (bool, error) {
		principal, err := stringField(candidate, "identityTrust.credentialIssuers", "principal")
		return principal == "workload", err
	}, path)
}

func exactWorkloadVerifierForSite(verifiers []map[string]any, issuerRef, siteRef, path string) (map[string]any, error) {
	return exactIdentityObjectForSite(verifiers, "verifier", siteRef, func(candidate map[string]any) (bool, error) {
		principal, err := stringField(candidate, "identityTrust.verifierPlacements", "principal")
		if err != nil || principal != "workload" {
			return false, err
		}
		ref, err := stringField(candidate, "identityTrust.verifierPlacements", "credentialIssuerRef")
		return ref == issuerRef, err
	}, path)
}

func exactIdentityObjectForSite(values []map[string]any, kind, siteRef string, predicate func(map[string]any) (bool, error), path string) (map[string]any, error) {
	var match map[string]any
	for _, candidate := range values {
		ok, err := predicate(candidate)
		if err != nil || !ok {
			if err != nil {
				return nil, err
			}
			continue
		}
		placement, err := objectField(candidate, "identityTrust."+kind, "placement")
		if err != nil {
			return nil, err
		}
		siteRefs, err := stringListField(placement, "identityTrust."+kind+".placement", "siteRefs", true)
		if err != nil || !contains(siteRefs, siteRef) {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("%s has multiple workload %s bindings for Site %q", path, kind, siteRef)
		}
		match = candidate
	}
	if match == nil {
		return nil, fmt.Errorf("%s has no workload %s binding for Site %q", path, kind, siteRef)
	}
	return match, nil
}

func exactVerifierDistribution(distributions []map[string]any, issuerRef, fromSiteRef, toSiteRef, path string) (map[string]any, error) {
	var match map[string]any
	for _, candidate := range distributions {
		ref, err := stringField(candidate, "identityTrust.verifierDistributions", "credentialIssuerRef")
		if err != nil || ref != issuerRef {
			if err != nil {
				return nil, err
			}
			continue
		}
		from, err := objectField(candidate, "identityTrust.verifierDistributions", "from")
		if err != nil {
			return nil, err
		}
		to, err := objectField(candidate, "identityTrust.verifierDistributions", "to")
		if err != nil {
			return nil, err
		}
		fromRefs, err := stringListField(from, "identityTrust.verifierDistributions.from", "siteRefs", true)
		if err != nil {
			return nil, err
		}
		toRefs, err := stringListField(to, "identityTrust.verifierDistributions.to", "siteRefs", true)
		if err != nil {
			return nil, err
		}
		if !sameStringSet(fromRefs, []string{fromSiteRef}) || !sameStringSet(toRefs, []string{toSiteRef}) {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("%s has multiple workload verifier distributions", path)
		}
		match = candidate
	}
	if match == nil {
		return nil, fmt.Errorf("%s has no exact Home-to-Cloud workload verifier distribution", path)
	}
	return match, nil
}

func safeModuleBridgePublications(moduleID string, module, bridge map[string]any) ([]any, error) {
	const publicationModuleID = "stackkits-bridge-publication-runtime"
	if moduleID != publicationModuleID {
		return nil, fmt.Errorf("module %q cannot consume bridge publications", moduleID)
	}
	provided, err := stringListField(module, "modules."+moduleID, "provides", true)
	if err != nil {
		return nil, err
	}
	if len(provided) != 1 || provided[0] != "service-publication" {
		return nil, fmt.Errorf("module %q bridge publication projection requires exactly capability %q", moduleID, "service-publication")
	}
	if bridge == nil {
		return nil, fmt.Errorf("bridge projection is unavailable")
	}
	publications, err := objectListField(bridge, "bridge", "publications")
	if err != nil {
		return nil, err
	}
	if len(publications) == 0 {
		return nil, fmt.Errorf("bridge publication projection is empty")
	}
	projected := make([]any, 0, len(publications))
	for index, publication := range publications {
		normalized, err := normalizeJSON(publication, false, fmt.Sprintf("bridgePublications[%d]", index))
		if err != nil {
			return nil, err
		}
		projected = append(projected, normalized)
	}
	return projected, nil
}
