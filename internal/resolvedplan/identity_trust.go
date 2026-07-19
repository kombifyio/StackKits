package resolvedplan

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
)

var identityTrustIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type resolvedIdentityTrustOwnerIndex struct {
	providers map[string]map[string]any
	modules   map[string]map[string]any
}

type resolvedIdentityTrustIssuer struct {
	id                        string
	principal                 string
	issuer                    string
	audiences                 []string
	verificationKeySetRef     string
	placement                 map[string]any
	issuanceWithinStackKit    bool
	proofOfPossessionRequired bool
}

type identityTrustIssuerGraph struct {
	id               string
	authorityRef     string
	principal        string
	logicalIssuerRef string
	issuer           string
	audiences        []string
	keySetRef        string
	placement        map[string]any
	owner            map[string]any
}

type identityTrustIssuerPolicy struct {
	issuance      bool
	credentialTTL int
	sessionTTL    int
	possession    bool
	maxStale      int
	enrollment    map[string]any
}

// buildResolvedIdentityTrust is the sole lowering boundary from immutable
// Definition selectors and logical trust references into stack-instance-bound
// issuer, audience, key-set, and exact Site placements. It deliberately runs
// after provider/module selection so every catalog owner is an already selected
// structural owner rather than an inferred runtime.
func buildResolvedIdentityTrust(definition map[string]any, spec *specView, contracts planContracts) (map[string]any, error) {
	if definition == nil {
		return nil, fail(ErrInvalidInput, "definition.identityTrust", "required identity trust contract is missing")
	}
	authorities, err := objectListField(definition, "definition.identityTrust", "authorities")
	if err != nil {
		return nil, err
	}
	issuers, err := objectListField(definition, "definition.identityTrust", "credentialIssuers")
	if err != nil {
		return nil, err
	}
	verifiers, err := objectListField(definition, "definition.identityTrust", "verifierPlacements")
	if err != nil {
		return nil, err
	}
	distributions, err := objectListField(definition, "definition.identityTrust", "verifierDistributions")
	if err != nil {
		return nil, err
	}
	if len(authorities) == 0 || len(issuers) == 0 || len(verifiers) == 0 {
		return nil, fail(ErrInvalidInput, "definition.identityTrust", "authorities, credential issuers, and verifier placements must be non-empty")
	}

	owners, err := indexResolvedIdentityTrustOwners(contracts)
	if err != nil {
		return nil, err
	}
	resolvedAuthorities, authorityByID, err := resolveIdentityTrustAuthorities(authorities, spec, owners)
	if err != nil {
		return nil, err
	}
	resolvedIssuers, issuerByLogicalRef, err := resolveIdentityTrustIssuers(issuers, spec, owners, authorityByID)
	if err != nil {
		return nil, err
	}
	resolvedVerifiers, verifierByID, err := resolveIdentityTrustVerifiers(verifiers, spec, owners, issuerByLogicalRef)
	if err != nil {
		return nil, err
	}
	resolvedDistributions, err := resolveIdentityTrustDistributions(distributions, spec, owners, issuerByLogicalRef, verifierByID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"authorities": resolvedAuthorities, "credentialIssuers": resolvedIssuers,
		"verifierPlacements": resolvedVerifiers, "verifierDistributions": resolvedDistributions,
	}, nil
}

func indexResolvedIdentityTrustOwners(contracts planContracts) (resolvedIdentityTrustOwnerIndex, error) {
	result := resolvedIdentityTrustOwnerIndex{providers: map[string]map[string]any{}, modules: map[string]map[string]any{}}
	for index, raw := range contracts.providers {
		provider, err := asObject(raw, fmt.Sprintf("providers[%d]", index))
		if err != nil {
			return result, err
		}
		id, err := stringField(provider, fmt.Sprintf("providers[%d]", index), "id")
		if err != nil {
			return result, err
		}
		result.providers[id] = provider
	}
	for index, raw := range contracts.modules {
		module, err := asObject(raw, fmt.Sprintf("modules[%d]", index))
		if err != nil {
			return result, err
		}
		id, err := stringField(module, fmt.Sprintf("modules[%d]", index), "id")
		if err != nil {
			return result, err
		}
		result.modules[id] = module
	}
	return result, nil
}

func resolveIdentityTrustAuthorities(values []map[string]any, spec *specView, owners resolvedIdentityTrustOwnerIndex) ([]any, map[string]map[string]any, error) {
	resolved := make([]any, 0, len(values))
	byID := make(map[string]map[string]any, len(values))
	for index, value := range values {
		path := fmt.Sprintf("definition.identityTrust.authorities[%d]", index)
		id, err := identityTrustID(value, path)
		if err != nil {
			return nil, nil, err
		}
		if _, duplicate := byID[id]; duplicate {
			return nil, nil, fail(ErrContractConflict, path+".id", "duplicate identity authority %q", id)
		}
		principal, err := identityTrustPrincipal(value, path)
		if err != nil {
			return nil, nil, err
		}
		trustDomainRef, err := identityTrustLogicalRef(value, path, "trustDomainRef")
		if err != nil {
			return nil, nil, err
		}
		placement, err := resolveIdentityTrustPlacement(value, path, spec, true)
		if err != nil {
			return nil, nil, err
		}
		owner, err := resolveIdentityTrustOwner(value, path, placement, owners, identityTrustPlacementSiteRefs(placement))
		if err != nil {
			return nil, nil, err
		}
		entry := map[string]any{"id": id, "principal": principal, "trustDomainRef": trustDomainRef, "placement": placement, "owner": owner}
		resolved = append(resolved, entry)
		byID[id] = entry
	}
	return resolved, byID, nil
}

func resolveIdentityTrustIssuers(values []map[string]any, spec *specView, owners resolvedIdentityTrustOwnerIndex, authorities map[string]map[string]any) ([]any, map[string]resolvedIdentityTrustIssuer, error) {
	resolved := make([]any, 0, len(values))
	byID := make(map[string]struct{}, len(values))
	byRef := make(map[string]resolvedIdentityTrustIssuer, len(values))
	for index, value := range values {
		path := fmt.Sprintf("definition.identityTrust.credentialIssuers[%d]", index)
		id, err := identityTrustID(value, path)
		if err != nil {
			return nil, nil, err
		}
		if _, duplicate := byID[id]; duplicate {
			return nil, nil, fail(ErrContractConflict, path+".id", "duplicate credential issuer %q", id)
		}
		byID[id] = struct{}{}
		graph, err := resolveIdentityTrustIssuerGraph(value, path, id, spec, owners, authorities, byRef)
		if err != nil {
			return nil, nil, err
		}
		policy, err := resolveIdentityTrustIssuerPolicy(value, path, graph, spec)
		if err != nil {
			return nil, nil, err
		}
		entry := resolvedIdentityTrustIssuerEntry(graph, policy)
		resolved = append(resolved, entry)
		byRef[graph.logicalIssuerRef] = resolvedIdentityTrustIssuer{
			id: graph.id, principal: graph.principal, issuer: graph.issuer, audiences: graph.audiences,
			verificationKeySetRef: graph.keySetRef, placement: graph.placement, issuanceWithinStackKit: policy.issuance,
			proofOfPossessionRequired: policy.possession,
		}
	}
	return resolved, byRef, nil
}

func resolveIdentityTrustIssuerGraph(value map[string]any, path, id string, spec *specView, owners resolvedIdentityTrustOwnerIndex, authorities map[string]map[string]any, existing map[string]resolvedIdentityTrustIssuer) (identityTrustIssuerGraph, error) {
	var graph identityTrustIssuerGraph
	graph.id = id
	var err error
	if graph.authorityRef, err = identityTrustLogicalRef(value, path, "authorityRef"); err != nil {
		return graph, err
	}
	authority, exists := authorities[graph.authorityRef]
	if !exists {
		return graph, fail(ErrContractConflict, path+".authorityRef", "unknown identity authority %q", graph.authorityRef)
	}
	if graph.principal, err = identityTrustPrincipal(value, path); err != nil {
		return graph, err
	}
	if authority["principal"] != graph.principal {
		return graph, fail(ErrContractConflict, path+".principal", "issuer principal does not match authority %q", graph.authorityRef)
	}
	if graph.logicalIssuerRef, err = identityTrustLogicalRef(value, path, "issuerRef"); err != nil {
		return graph, err
	}
	if _, duplicate := existing[graph.logicalIssuerRef]; duplicate {
		return graph, fail(ErrContractConflict, path+".issuerRef", "duplicate credential issuer ref %q", graph.logicalIssuerRef)
	}
	graph.issuer = identityTrustURN(spec.stackID, "issuer", graph.logicalIssuerRef)
	audienceRefs, err := identityTrustLogicalRefs(value, path, "audienceRefs")
	if err != nil {
		return graph, err
	}
	graph.audiences = identityTrustURNs(spec.stackID, "audience", audienceRefs)
	logicalKeySetRef, err := identityTrustLogicalRef(value, path, "verificationKeySetRef")
	if err != nil {
		return graph, err
	}
	graph.keySetRef = identityTrustURN(spec.stackID, "keyset", logicalKeySetRef)
	if graph.placement, err = resolveIdentityTrustPlacement(value, path, spec, true); err != nil {
		return graph, err
	}
	if !reflect.DeepEqual(graph.placement, authority["placement"]) {
		return graph, fail(ErrContractConflict, path+".placement", "issuer placement must exactly match authority %q", graph.authorityRef)
	}
	graph.owner, err = resolveIdentityTrustOwner(value, path, graph.placement, owners, identityTrustPlacementSiteRefs(graph.placement))
	return graph, err
}

func resolveIdentityTrustIssuerPolicy(value map[string]any, path string, graph identityTrustIssuerGraph, spec *specView) (identityTrustIssuerPolicy, error) {
	var policy identityTrustIssuerPolicy
	var err error
	if policy.issuance, err = requiredBoolField(value, path, "issuanceWithinStackKit"); err != nil {
		return policy, err
	}
	if policy.credentialTTL, err = intField(value, path, "credentialTTLSeconds"); err != nil {
		return policy, err
	}
	if policy.sessionTTL, err = intField(value, path, "sessionTTLSeconds"); err != nil {
		return policy, err
	}
	if policy.possession, err = requiredBoolField(value, path, "proofOfPossessionRequired"); err != nil {
		return policy, err
	}
	revocation, err := requiredBoolField(value, path, "revocationSupported")
	if err != nil {
		return policy, err
	}
	if policy.maxStale, err = intField(value, path, "revocationMaxStalenessSeconds"); err != nil {
		return policy, err
	}
	if policy.enrollment, err = identityTrustEnrollment(value, path); err != nil {
		return policy, err
	}
	return policy, validateIdentityTrustIssuerPolicy(path, graph, policy, revocation, spec)
}

func validateIdentityTrustIssuerPolicy(path string, graph identityTrustIssuerGraph, policy identityTrustIssuerPolicy, revocation bool, spec *specView) error {
	if err := validateIdentityTrustIssuerLifetime(path, policy, revocation); err != nil {
		return err
	}
	if err := validateIdentityTrustIssuerPossession(path, graph.principal, policy.possession); err != nil {
		return err
	}
	return validateIdentityTrustIssuerAuthority(path, graph, policy, spec)
}

func validateIdentityTrustIssuerLifetime(path string, policy identityTrustIssuerPolicy, revocation bool) error {
	if policy.credentialTTL < 300 || policy.credentialTTL > 86400 || policy.sessionTTL < 60 || policy.sessionTTL > 86400 || policy.maxStale < 0 || !revocation {
		return fail(ErrContractConflict, path, "issuer TTL and revocation contract is invalid")
	}
	return nil
}

func validateIdentityTrustIssuerPossession(path, principal string, possession bool) error {
	if principal != "human" && !possession {
		return fail(ErrContractConflict, path+".proofOfPossessionRequired", "%s credentials must be possession-bound", principal)
	}
	return nil
}

func validateIdentityTrustIssuerAuthority(path string, graph identityTrustIssuerGraph, policy identityTrustIssuerPolicy, spec *specView) error {
	external := graph.placement["kind"] == "external"
	if external && policy.issuance || external && !identityTrustEnrollmentDisabled(policy.enrollment) {
		return fail(ErrContractConflict, path, "external authorities cannot issue or enroll within StackKits")
	}
	if graph.principal == "device" && identityTrustPlacementHasKind(graph.placement, spec, "cloud") && (policy.issuance || !identityTrustEnrollmentDisabled(policy.enrollment)) {
		return fail(ErrContractConflict, path, "Cloud placements cannot issue or enroll device credentials")
	}
	if graph.principal == "device" && policy.issuance && !policy.possession || graph.principal == "device" && policy.issuance && !identityTrustEnrollmentLocal(policy.enrollment) {
		return fail(ErrContractConflict, path, "StackKits device issuance requires possession binding and local-only LAN enrollment")
	}
	if graph.principal != "device" && !identityTrustEnrollmentDisabled(policy.enrollment) {
		return fail(ErrContractConflict, path+".enrollment", "non-device issuers cannot enroll devices")
	}
	return nil
}

func resolvedIdentityTrustIssuerEntry(graph identityTrustIssuerGraph, policy identityTrustIssuerPolicy) map[string]any {
	return map[string]any{
		"id": graph.id, "authorityRef": graph.authorityRef, "principal": graph.principal, "issuer": graph.issuer,
		"audiences": stringSliceAny(graph.audiences), "verificationKeySetRef": graph.keySetRef, "placement": graph.placement, "owner": graph.owner,
		"issuanceWithinStackKit": policy.issuance, "credentialTTLSeconds": policy.credentialTTL, "sessionTTLSeconds": policy.sessionTTL,
		"proofOfPossessionRequired": policy.possession, "revocationSupported": true,
		"revocationMaxStalenessSeconds": policy.maxStale, "enrollment": policy.enrollment,
	}
}

func resolveIdentityTrustVerifiers(values []map[string]any, spec *specView, owners resolvedIdentityTrustOwnerIndex, issuers map[string]resolvedIdentityTrustIssuer) ([]any, map[string]map[string]any, error) {
	resolved := make([]any, 0, len(values))
	byID := make(map[string]map[string]any, len(values))
	for index, value := range values {
		path := fmt.Sprintf("definition.identityTrust.verifierPlacements[%d]", index)
		id, err := identityTrustID(value, path)
		if err != nil {
			return nil, nil, err
		}
		if _, duplicate := byID[id]; duplicate {
			return nil, nil, fail(ErrContractConflict, path+".id", "duplicate verifier placement %q", id)
		}
		logicalIssuerRef, err := identityTrustLogicalRef(value, path, "issuerRef")
		if err != nil {
			return nil, nil, err
		}
		issuer, exists := issuers[logicalIssuerRef]
		if !exists {
			return nil, nil, fail(ErrContractConflict, path+".issuerRef", "unknown credential issuer %q", logicalIssuerRef)
		}
		principal, err := identityTrustPrincipal(value, path)
		if err != nil {
			return nil, nil, err
		}
		if principal != issuer.principal {
			return nil, nil, fail(ErrContractConflict, path+".principal", "verifier principal does not match issuer")
		}
		audienceRefs, err := identityTrustLogicalRefs(value, path, "audienceRefs")
		if err != nil {
			return nil, nil, err
		}
		audiences := identityTrustURNs(spec.stackID, "audience", audienceRefs)
		if !reflect.DeepEqual(audiences, issuer.audiences) {
			return nil, nil, fail(ErrContractConflict, path+".audienceRefs", "verifier audiences must exactly match issuer audiences")
		}
		logicalKeySetRef, err := identityTrustLogicalRef(value, path, "verificationKeySetRef")
		if err != nil {
			return nil, nil, err
		}
		keySetRef := identityTrustURN(spec.stackID, "keyset", logicalKeySetRef)
		if keySetRef != issuer.verificationKeySetRef {
			return nil, nil, fail(ErrContractConflict, path+".verificationKeySetRef", "verifier key set must exactly match issuer")
		}
		placement, err := resolveIdentityTrustPlacement(value, path, spec, false)
		if err != nil {
			return nil, nil, err
		}
		owner, err := resolveIdentityTrustOwner(value, path, placement, owners, identityTrustPlacementSiteRefs(placement))
		if err != nil {
			return nil, nil, err
		}
		possession, err := requiredBoolField(value, path, "proofOfPossessionRequired")
		if err != nil {
			return nil, nil, err
		}
		if possession != issuer.proofOfPossessionRequired || principal != "human" && !possession {
			return nil, nil, fail(ErrContractConflict, path+".proofOfPossessionRequired", "verifier possession policy must exactly match issuer")
		}
		maxStale, err := intField(value, path, "revocationMaxStalenessSeconds")
		if err != nil {
			return nil, nil, err
		}
		if maxStale < 0 {
			return nil, nil, fail(ErrContractConflict, path+".revocationMaxStalenessSeconds", "verifier revocation freshness must be non-negative")
		}
		entry := map[string]any{
			"id": id, "credentialIssuerRef": issuer.id, "issuer": issuer.issuer, "principal": principal, "audiences": stringSliceAny(audiences),
			"verificationKeySetRef": keySetRef, "placement": placement, "owner": owner,
			"proofOfPossessionRequired": possession, "revocationMaxStalenessSeconds": maxStale,
		}
		resolved = append(resolved, entry)
		byID[id] = entry
	}
	return resolved, byID, nil
}

func resolveIdentityTrustDistributions(values []map[string]any, spec *specView, owners resolvedIdentityTrustOwnerIndex, issuers map[string]resolvedIdentityTrustIssuer, verifiers map[string]map[string]any) ([]any, error) {
	resolved := make([]any, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		path := fmt.Sprintf("definition.identityTrust.verifierDistributions[%d]", index)
		id, err := identityTrustID(value, path)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fail(ErrContractConflict, path+".id", "duplicate verifier distribution %q", id)
		}
		seen[id] = struct{}{}
		logicalIssuerRef, err := identityTrustLogicalRef(value, path, "issuerRef")
		if err != nil {
			return nil, err
		}
		issuer, exists := issuers[logicalIssuerRef]
		if !exists || !issuer.issuanceWithinStackKit || issuer.placement["kind"] != "sites" {
			return nil, fail(ErrContractConflict, path+".issuerRef", "distribution requires a StackKits-owned Site issuer")
		}
		from, err := resolveIdentityTrustNamedPlacement(value, path, "from", spec, false)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(from, issuer.placement) {
			return nil, fail(ErrContractConflict, path+".from", "distribution source must exactly match issuer placement")
		}
		to, err := resolveIdentityTrustNamedPlacement(value, path, "to", spec, false)
		if err != nil {
			return nil, err
		}
		if identityTrustSiteSetsOverlap(identityTrustPlacementSiteRefs(from), identityTrustPlacementSiteRefs(to)) {
			return nil, fail(ErrContractConflict, path+".to", "reverse or same-authority verifier distribution is forbidden")
		}
		ownerSites := append(identityTrustPlacementSiteRefs(from), identityTrustPlacementSiteRefs(to)...)
		owner, err := resolveIdentityTrustOwner(value, path, map[string]any{"kind": "sites", "siteRefs": stringSliceAny(sortStringsUnique(ownerSites))}, owners, ownerSites)
		if err != nil {
			return nil, err
		}
		if err := validateIdentityTrustDistributionContents(value, path); err != nil {
			return nil, err
		}
		maxStale, err := intField(value, path, "maxStalenessSeconds")
		if err != nil {
			return nil, err
		}
		if maxStale < 0 {
			return nil, fail(ErrContractConflict, path+".maxStalenessSeconds", "distribution freshness must be non-negative")
		}
		if err := validateIdentityTrustDistributionTargets(path, issuer.id, issuer.issuer, to, maxStale, verifiers); err != nil {
			return nil, err
		}
		resolved = append(resolved, map[string]any{
			"id": id, "credentialIssuerRef": issuer.id, "issuer": issuer.issuer, "from": from, "to": to,
			"materials":                []any{"revocation-state", "verification-key-reference"},
			"includesSigningAuthority": false, "includesEnrollmentAuthority": false,
			"includesPrivateKeyMaterial": false, "includesCredentialMaterial": false,
			"reverseAllowed": false, "maxStalenessSeconds": maxStale, "owner": owner,
		})
	}
	return resolved, nil
}

func validateIdentityTrustDistributionContents(value map[string]any, path string) error {
	materials, err := identityTrustLogicalRefs(value, path, "materials")
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(materials, []string{"revocation-state", "verification-key-reference"}) {
		return fail(ErrContractConflict, path+".materials", "distribution may contain only verification-key-reference and revocation-state")
	}
	for _, name := range []string{"includesSigningAuthority", "includesEnrollmentAuthority", "includesPrivateKeyMaterial", "includesCredentialMaterial", "reverseAllowed"} {
		flag, err := requiredBoolField(value, path, name)
		if err != nil {
			return err
		}
		if flag {
			return fail(ErrContractConflict, path+"."+name, "verifier distribution must remain one-way verification material only")
		}
	}
	return nil
}

func resolveIdentityTrustPlacement(value map[string]any, path string, spec *specView, allowExternal bool) (map[string]any, error) {
	return resolveIdentityTrustNamedPlacement(value, path, "placement", spec, allowExternal)
}

func resolveIdentityTrustNamedPlacement(value map[string]any, path, field string, spec *specView, allowExternal bool) (map[string]any, error) {
	placement, err := objectField(value, path, field)
	if err != nil {
		return nil, err
	}
	selector, err := stringField(placement, path+"."+field, "selector")
	if err != nil {
		return nil, err
	}
	switch selector {
	case "control-authority-site":
		if _, exists := spec.siteByID[spec.authoritySiteRef]; !exists {
			return nil, fail(ErrUnresolvedPlacement, path+"."+field, "control authority Site %q is absent", spec.authoritySiteRef)
		}
		return map[string]any{"kind": "sites", "siteRefs": []any{spec.authoritySiteRef}}, nil
	case "cloud-sites":
		var siteRefs []string
		for _, site := range spec.sites {
			if site.kind == "cloud" {
				siteRefs = append(siteRefs, site.id)
			}
		}
		siteRefs = sortStringsUnique(siteRefs)
		if len(siteRefs) == 0 {
			return nil, fail(ErrUnresolvedPlacement, path+"."+field, "cloud-sites selector resolved no Sites")
		}
		return map[string]any{"kind": "sites", "siteRefs": stringSliceAny(siteRefs)}, nil
	case "external":
		if !allowExternal {
			return nil, fail(ErrUnresolvedPlacement, path+"."+field, "external placement is not valid for this identity contract")
		}
		contractRef, err := identityTrustLogicalRef(placement, path+"."+field, "contractRef")
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": "external", "contractRef": contractRef}, nil
	default:
		return nil, fail(ErrUnresolvedPlacement, path+"."+field+".selector", "unsupported identity placement selector %q", selector)
	}
}

func resolveIdentityTrustOwner(value map[string]any, path string, placement map[string]any, owners resolvedIdentityTrustOwnerIndex, requiredSites []string) (map[string]any, error) {
	owner, err := objectField(value, path, "owner")
	if err != nil {
		return nil, err
	}
	kind, err := stringField(owner, path+".owner", "kind")
	if err != nil {
		return nil, err
	}
	if kind == "external" {
		if placement["kind"] != "external" {
			return nil, fail(ErrContractConflict, path+".owner", "external owner requires external placement")
		}
		contractRef, err := identityTrustLogicalRef(owner, path+".owner", "contractRef")
		if err != nil {
			return nil, err
		}
		if contractRef != placement["contractRef"] {
			return nil, fail(ErrContractConflict, path+".owner.contractRef", "external owner must match placement contract")
		}
		if _, present := owner["providerRef"]; present {
			return nil, fail(ErrContractConflict, path+".owner", "external owner cannot carry catalog provider authority")
		}
		if _, present := owner["moduleRef"]; present {
			return nil, fail(ErrContractConflict, path+".owner", "external owner cannot carry catalog module authority")
		}
		return map[string]any{"kind": "external", "contractRef": contractRef}, nil
	}
	if kind != "catalog" || placement["kind"] != "sites" {
		return nil, fail(ErrContractConflict, path+".owner", "Site identity contracts require a catalog owner")
	}
	providerRef, err := identityTrustLogicalRef(owner, path+".owner", "providerRef")
	if err != nil {
		return nil, err
	}
	moduleRef, err := identityTrustLogicalRef(owner, path+".owner", "moduleRef")
	if err != nil {
		return nil, err
	}
	provider, providerSelected := owners.providers[providerRef]
	module, moduleSelected := owners.modules[moduleRef]
	if !providerSelected || !moduleSelected {
		return nil, fail(ErrContractConflict, path+".owner", "catalog owner %s/%s is not selected", providerRef, moduleRef)
	}
	moduleProviderRef, err := stringField(module, "modules."+moduleRef, "providerRef")
	if err != nil {
		return nil, err
	}
	if moduleProviderRef != providerRef {
		return nil, fail(ErrContractConflict, path+".owner", "module %q is not governed by provider %q", moduleRef, providerRef)
	}
	if err := identityTrustOwnerCoversSites(provider, module, requiredSites, path+".owner"); err != nil {
		return nil, err
	}
	return map[string]any{"kind": "catalog", "providerRef": providerRef, "moduleRef": moduleRef}, nil
}

func identityTrustOwnerCoversSites(provider, module map[string]any, siteRefs []string, path string) error {
	providerSites, err := stringListField(provider, path, "siteRefs", true)
	if err != nil {
		return err
	}
	moduleSites, err := stringListField(module, path, "siteRefs", true)
	if err != nil {
		return err
	}
	for _, siteRef := range sortStringsUnique(siteRefs) {
		if !contains(providerSites, siteRef) || !contains(moduleSites, siteRef) {
			return fail(ErrContractConflict, path, "catalog owner does not cover identity Site %q", siteRef)
		}
	}
	return nil
}

func identityTrustID(value map[string]any, path string) (string, error) {
	return identityTrustLogicalRef(value, path, "id")
}

func identityTrustLogicalRef(value map[string]any, path, field string) (string, error) {
	ref, err := stringField(value, path, field)
	if err != nil {
		return "", err
	}
	if !identityTrustIDPattern.MatchString(ref) {
		return "", fail(ErrInvalidInput, path+"."+field, "identity trust reference %q is not a contract ID", ref)
	}
	return ref, nil
}

func identityTrustLogicalRefs(value map[string]any, path, field string) ([]string, error) {
	refs, err := stringListField(value, path, field, true)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, fail(ErrInvalidInput, path+"."+field, "identity trust reference list is empty")
	}
	for index, ref := range refs {
		if !identityTrustIDPattern.MatchString(ref) {
			return nil, fail(ErrInvalidInput, fmt.Sprintf("%s.%s[%d]", path, field, index), "identity trust reference %q is not a contract ID", ref)
		}
	}
	sorted := sortStringsUnique(refs)
	if len(sorted) != len(refs) {
		return nil, fail(ErrContractConflict, path+"."+field, "identity trust references must be unique")
	}
	return sorted, nil
}

func identityTrustPrincipal(value map[string]any, path string) (string, error) {
	principal, err := stringField(value, path, "principal")
	if err != nil {
		return "", err
	}
	if principal != "human" && principal != "device" && principal != "workload" {
		return "", fail(ErrInvalidInput, path+".principal", "unsupported identity principal %q", principal)
	}
	return principal, nil
}

func identityTrustURN(stackID, kind, ref string) string {
	return "urn:stackkit:" + stackID + ":" + kind + ":" + ref
}

func identityTrustURNs(stackID, kind string, refs []string) []string {
	values := make([]string, len(refs))
	for index, ref := range refs {
		values[index] = identityTrustURN(stackID, kind, ref)
	}
	sort.Strings(values)
	return values
}

func identityTrustEnrollment(value map[string]any, path string) (map[string]any, error) {
	enrollment, err := objectField(value, path, "enrollment")
	if err != nil {
		return nil, err
	}
	mode, err := stringField(enrollment, path+".enrollment", "mode")
	if err != nil {
		return nil, err
	}
	exposure, err := stringField(enrollment, path+".enrollment", "exposure")
	if err != nil {
		return nil, err
	}
	if mode == "local-only" && exposure == "lan" || mode == "none" && exposure == "none" {
		return map[string]any{"mode": mode, "exposure": exposure}, nil
	}
	return nil, fail(ErrContractConflict, path+".enrollment", "enrollment mode and exposure are inconsistent")
}

func identityTrustEnrollmentDisabled(value map[string]any) bool {
	return value["mode"] == "none" && value["exposure"] == "none"
}

func identityTrustEnrollmentLocal(value map[string]any) bool {
	return value["mode"] == "local-only" && value["exposure"] == "lan"
}

func identityTrustPlacementSiteRefs(placement map[string]any) []string {
	if placement["kind"] != "sites" {
		return nil
	}
	values, _ := stringListField(placement, "identityTrust.placement", "siteRefs", true)
	return values
}

func identityTrustPlacementHasKind(placement map[string]any, spec *specView, kind string) bool {
	for _, siteRef := range identityTrustPlacementSiteRefs(placement) {
		if site, exists := spec.siteByID[siteRef]; exists && site.kind == kind {
			return true
		}
	}
	return false
}

func identityTrustSiteSetsOverlap(left, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, exists := seen[value]; exists {
			return true
		}
	}
	return false
}

func validateIdentityTrustDistributionTargets(path, credentialIssuerRef, issuerURN string, to map[string]any, maxStalenessSeconds int, verifiers map[string]map[string]any) error {
	wanted := identityTrustPlacementSiteRefs(to)
	covered := make(map[string]struct{}, len(wanted))
	for _, verifier := range verifiers {
		if verifier["credentialIssuerRef"] != credentialIssuerRef || verifier["issuer"] != issuerURN {
			continue
		}
		if verifier["revocationMaxStalenessSeconds"] != maxStalenessSeconds {
			continue
		}
		for _, siteRef := range identityTrustPlacementSiteRefs(verifier["placement"].(map[string]any)) {
			if contains(wanted, siteRef) {
				covered[siteRef] = struct{}{}
			}
		}
	}
	if len(covered) != len(wanted) {
		return fail(ErrContractConflict, path+".to", "distribution targets must exactly have verifier coverage for issuer and freshness")
	}
	return nil
}
