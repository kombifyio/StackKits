package resolvedplan

import (
	"fmt"
	"reflect"
	"sort"
)

const (
	cloudIdentityAuthorityProviderRef = "stackkits-cloud-identity-trust-policy"
	cloudIdentityAuthorityModuleRef   = "stackkits-cloud-identity-trust-policy-manifest"
	cloudExternalDeviceContractRef    = "owner-bound-device-authority"
)

func projectPublicCloudIdentityAuthority(value any, kit map[string]any, sites []any, stackID, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(value, path)
	if err != nil {
		return nil, err
	}
	if alreadyPublic {
		return exactPublicCloudIdentityAuthority(input, kit, sites, stackID, path)
	}
	slug, err := stringField(kit, "resolvedPlan.kit", "slug")
	if err != nil || slug != "cloud-kit" {
		return nil, fail(ErrContractConflict, "resolvedPlan.kit.slug", "Cloud identity authority is available only to cloud-kit")
	}
	authorities, err := objectListField(input, path, "authorities")
	if err != nil {
		return nil, err
	}
	issuers, err := objectListField(input, path, "credentialIssuers")
	if err != nil {
		return nil, err
	}
	verifiers, err := objectListField(input, path, "verifierPlacements")
	if err != nil {
		return nil, err
	}
	distributions, err := objectListField(input, path, "verifierDistributions")
	if err != nil {
		return nil, err
	}
	if len(authorities) != 3 || len(issuers) != 3 || len(verifiers) != 3 || len(distributions) != 0 {
		return nil, fail(ErrContractConflict, path, "Cloud identity requires exactly three authorities, issuers, and verifiers with no distribution")
	}
	authorityByID := make(map[string]map[string]any, 3)
	for index, authority := range authorities {
		authorityPath := fmt.Sprintf("%s.authorities[%d]", path, index)
		if len(authority) != 5 {
			return nil, fail(ErrContractConflict, authorityPath, "source authority contains fields outside the closed identity contract")
		}
		id, err := stringField(authority, authorityPath, "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := authorityByID[id]; duplicate {
			return nil, fail(ErrContractConflict, authorityPath+".id", "authority ID is duplicated")
		}
		authorityByID[id] = authority
	}
	issuerByID := make(map[string]map[string]any, 3)
	for index, issuer := range issuers {
		issuerPath := fmt.Sprintf("%s.credentialIssuers[%d]", path, index)
		if len(issuer) != 15 {
			return nil, fail(ErrContractConflict, issuerPath, "source issuer contains material or fields outside the closed identity contract")
		}
		id, err := stringField(issuer, issuerPath, "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := issuerByID[id]; duplicate {
			return nil, fail(ErrContractConflict, issuerPath+".id", "issuer ID is duplicated")
		}
		issuerByID[id] = issuer
	}
	result := map[string]any{"issuers": []any{}, "verifiers": []any{}}
	siteRef := ""
	for index, verifier := range verifiers {
		verifierPath := fmt.Sprintf("%s.verifierPlacements[%d]", path, index)
		if len(verifier) != 10 {
			return nil, fail(ErrContractConflict, verifierPath, "source verifier contains material or fields outside the closed identity contract")
		}
		if err := requireCloudIdentityCatalogOwner(verifier, verifierPath); err != nil {
			return nil, err
		}
		currentSite, err := requireExactCloudIdentityPlacement(verifier, sites, verifierPath)
		if err != nil {
			return nil, err
		}
		if siteRef == "" {
			siteRef = currentSite
		} else if currentSite != siteRef {
			return nil, fail(ErrContractConflict, verifierPath+".placement", "all Cloud verifiers must bind the same Cloud Site")
		}
		issuerRef, err := stringField(verifier, verifierPath, "credentialIssuerRef")
		if err != nil {
			return nil, err
		}
		issuer, exists := issuerByID[issuerRef]
		if !exists {
			return nil, fail(ErrContractConflict, verifierPath+".credentialIssuerRef", "referenced issuer does not exist")
		}
		authorityRef, err := stringField(issuer, verifierPath+".issuer", "authorityRef")
		if err != nil {
			return nil, err
		}
		authority, exists := authorityByID[authorityRef]
		if !exists {
			return nil, fail(ErrContractConflict, verifierPath+".issuer.authorityRef", "source issuer authority does not exist")
		}
		projectedVerifier, projectedIssuer, err := projectCloudIdentityBinding(verifier, issuer, authority, sites, currentSite, stackID, verifierPath)
		if err != nil {
			return nil, err
		}
		result["verifiers"] = append(result["verifiers"].([]any), projectedVerifier)
		if projectedIssuer != nil {
			result["issuers"] = append(result["issuers"].([]any), projectedIssuer)
		}
	}
	result["siteRef"] = siteRef
	return exactPublicCloudIdentityAuthority(result, kit, sites, stackID, path+".public")
}

func projectCloudIdentityBinding(verifier, issuer, authority map[string]any, sites []any, siteRef, stackID, path string) (map[string]any, map[string]any, error) {
	principal, err := stringField(verifier, path, "principal")
	if err != nil || principal != "device" && principal != "human" && principal != "workload" {
		return nil, nil, fail(ErrContractConflict, path+".principal", "unsupported Cloud verifier principal")
	}
	for _, field := range []string{"principal", "issuer", "verificationKeySetRef"} {
		verifierValue, err := stringField(verifier, path, field)
		if err != nil {
			return nil, nil, err
		}
		issuerValue, err := stringField(issuer, path+".issuer", field)
		if err != nil || verifierValue != issuerValue {
			return nil, nil, fail(ErrContractConflict, path+"."+field, "verifier must exactly rebound its issuer")
		}
	}
	authorityPrincipal, err := stringField(authority, path+".authority", "principal")
	if err != nil || authorityPrincipal != principal {
		return nil, nil, fail(ErrContractConflict, path+".authority.principal", "authority, issuer, and verifier principal must match")
	}
	issuerAudiences, err := stringListField(issuer, path+".issuer", "audiences", true)
	if err != nil {
		return nil, nil, err
	}
	verifierAudiences, err := stringListField(verifier, path, "audiences", true)
	if err != nil || !reflect.DeepEqual(issuerAudiences, verifierAudiences) || len(verifierAudiences) == 0 || !reflect.DeepEqual(verifierAudiences, sortStringsUnique(verifierAudiences)) {
		return nil, nil, fail(ErrContractConflict, path+".audiences", "verifier audiences must exactly rebound its sorted issuer audiences")
	}
	for index, audience := range verifierAudiences {
		if err := requireResolvedIdentityURN(audience, stackID, "audience", fmt.Sprintf("%s.audiences[%d]", path, index)); err != nil {
			return nil, nil, err
		}
	}
	issuerURN, _ := stringField(verifier, path, "issuer")
	keyset, _ := stringField(verifier, path, "verificationKeySetRef")
	if err := requireResolvedIdentityURN(issuerURN, stackID, "issuer", path+".issuer"); err != nil {
		return nil, nil, err
	}
	if err := requireResolvedIdentityURN(keyset, stackID, "keyset", path+".verificationKeySetRef"); err != nil {
		return nil, nil, err
	}
	for _, field := range []string{"proofOfPossessionRequired", "revocationMaxStalenessSeconds"} {
		if !reflect.DeepEqual(verifier[field], issuer[field]) {
			return nil, nil, fail(ErrContractConflict, path+"."+field, "verifier policy must exactly rebound its issuer")
		}
	}
	if supported, ok := issuer["revocationSupported"].(bool); !ok || !supported {
		return nil, nil, fail(ErrContractConflict, path+".issuer.revocationSupported", "source issuer must support revocation")
	}
	if err := requireNoCloudEnrollment(issuer, path+".issuer"); err != nil {
		return nil, nil, err
	}
	id, err := stringField(verifier, path, "id")
	if err != nil {
		return nil, nil, err
	}
	issuerRef, err := stringField(verifier, path, "credentialIssuerRef")
	if err != nil {
		return nil, nil, err
	}
	projectedVerifier := map[string]any{
		"id": id, "principal": principal, "issuerRef": issuerRef, "issuer": issuerURN,
		"audiences": stringSliceAny(verifierAudiences), "verificationKeySetRef": keyset,
		"proofOfPossessionRequired":     verifier["proofOfPossessionRequired"],
		"revocationMaxStalenessSeconds": verifier["revocationMaxStalenessSeconds"],
	}
	if principal == "device" {
		if err := requireExternalDeviceIdentityOwnerAndPlacement(issuer, path+".issuer"); err != nil {
			return nil, nil, err
		}
		if err := requireExternalDeviceIdentityOwnerAndPlacement(authority, path+".authority"); err != nil {
			return nil, nil, err
		}
		if owned, ok := issuer["issuanceWithinStackKit"].(bool); !ok || owned {
			return nil, nil, fail(ErrContractConflict, path+".issuer.issuanceWithinStackKit", "Cloud must not own device issuance")
		}
		return projectedVerifier, nil, nil
	}
	if err := requireCloudIdentityCatalogOwner(issuer, path+".issuer"); err != nil {
		return nil, nil, err
	}
	issuerSite, err := requireExactCloudIdentityPlacement(issuer, sites, path+".issuer")
	if err != nil || issuerSite != siteRef {
		return nil, nil, fail(ErrContractConflict, path+".issuer.placement", "owned issuer must bind the exact Cloud Site")
	}
	if err := requireCloudIdentityCatalogOwner(authority, path+".authority"); err != nil {
		return nil, nil, err
	}
	authoritySite, err := requireExactCloudIdentityPlacement(authority, sites, path+".authority")
	if err != nil || authoritySite != siteRef {
		return nil, nil, fail(ErrContractConflict, path+".authority.placement", "owned authority must bind the exact Cloud Site")
	}
	if owned, ok := issuer["issuanceWithinStackKit"].(bool); !ok || !owned {
		return nil, nil, fail(ErrContractConflict, path+".issuer.issuanceWithinStackKit", "Cloud human/workload issuer must remain StackKit-owned")
	}
	authorityRef, _ := stringField(issuer, path+".issuer", "authorityRef")
	authorityID, _ := stringField(authority, path+".authority", "id")
	if authorityRef != authorityID {
		return nil, nil, fail(ErrContractConflict, path+".issuer.authorityRef", "owned issuer must bind the exact Cloud authority")
	}
	lifetime, err := intField(issuer, path+".issuer", "credentialTTLSeconds")
	if err != nil {
		return nil, nil, err
	}
	sessionTTL, err := intField(issuer, path+".issuer", "sessionTTLSeconds")
	if err != nil {
		return nil, nil, err
	}
	projectedIssuer := map[string]any{
		"id": issuerRef, "authorityRef": authorityRef, "principal": principal, "issuer": issuerURN,
		"audiences": stringSliceAny(issuerAudiences), "verificationKeySetRef": keyset,
		"proofOfPossessionRequired":     issuer["proofOfPossessionRequired"],
		"revocationMaxStalenessSeconds": issuer["revocationMaxStalenessSeconds"],
		"lifetimeSeconds":               lifetime, "sessionTTLSeconds": sessionTTL,
	}
	return projectedVerifier, projectedIssuer, nil
}

func requireExactCloudIdentityPlacement(value map[string]any, sites []any, path string) (string, error) {
	placement, err := objectField(value, path, "placement")
	if err != nil || len(placement) != 2 {
		return "", fail(ErrContractConflict, path+".placement", "Cloud placement must contain exactly kind and siteRefs")
	}
	kind, err := stringField(placement, path+".placement", "kind")
	if err != nil || kind != "sites" {
		return "", fail(ErrContractConflict, path+".placement.kind", "Cloud identity placement must be Site-owned")
	}
	refs, err := stringListField(placement, path+".placement", "siteRefs", true)
	if err != nil || len(refs) != 1 {
		return "", fail(ErrContractConflict, path+".placement.siteRefs", "Cloud identity placement requires exactly one Site")
	}
	matches := 0
	for index, rawSite := range sites {
		site, err := asObject(rawSite, fmt.Sprintf("resolvedPlan.sites[%d]", index))
		if err != nil {
			return "", err
		}
		id, _ := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "id")
		if id != refs[0] {
			continue
		}
		siteKind, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "kind")
		if err != nil || siteKind != "cloud" {
			return "", fail(ErrContractConflict, path+".placement.siteRefs", "Cloud identity Site %q must have kind cloud", id)
		}
		matches++
	}
	if matches != 1 {
		return "", fail(ErrContractConflict, path+".placement.siteRefs", "Cloud identity Site %q must exist exactly once", refs[0])
	}
	return refs[0], nil
}

func requireCloudIdentityCatalogOwner(value map[string]any, path string) error {
	owner, err := objectField(value, path, "owner")
	if err != nil || len(owner) != 3 {
		return fail(ErrContractConflict, path+".owner", "Cloud identity owner must be exact")
	}
	for field, expected := range map[string]string{
		"kind": "catalog", "providerRef": cloudIdentityAuthorityProviderRef, "moduleRef": cloudIdentityAuthorityModuleRef,
	} {
		actual, err := stringField(owner, path+".owner", field)
		if err != nil || actual != expected {
			return fail(ErrContractConflict, path+".owner."+field, "Cloud identity owner requires %q", expected)
		}
	}
	return nil
}

func requireExternalDeviceIdentityOwnerAndPlacement(value map[string]any, path string) error {
	for _, field := range []string{"owner", "placement"} {
		binding, err := objectField(value, path, field)
		if err != nil || len(binding) != 2 {
			return fail(ErrContractConflict, path+"."+field, "external device authority binding must be exact")
		}
		kind, _ := stringField(binding, path+"."+field, "kind")
		contractRef, _ := stringField(binding, path+"."+field, "contractRef")
		if kind != "external" || contractRef != cloudExternalDeviceContractRef {
			return fail(ErrContractConflict, path+"."+field, "device authority must remain owner-bound and external")
		}
	}
	return nil
}

func requireNoCloudEnrollment(issuer map[string]any, path string) error {
	enrollment, err := objectField(issuer, path, "enrollment")
	if err != nil || len(enrollment) != 2 {
		return fail(ErrContractConflict, path+".enrollment", "Cloud identity source enrollment must be exact")
	}
	mode, _ := stringField(enrollment, path+".enrollment", "mode")
	exposure, _ := stringField(enrollment, path+".enrollment", "exposure")
	if mode != "none" || exposure != "none" {
		return fail(ErrContractConflict, path+".enrollment", "Cloud identity projection cannot own enrollment")
	}
	return nil
}

func exactPublicCloudIdentityAuthority(input map[string]any, kit map[string]any, sites []any, stackID, path string) (map[string]any, error) {
	if len(input) != 3 {
		return nil, fail(ErrContractConflict, path, "Cloud identity projection must contain exactly siteRef, issuers, and verifiers")
	}
	if kit != nil {
		slug, err := stringField(kit, "resolvedPlan.kit", "slug")
		if err != nil || slug != "cloud-kit" {
			return nil, fail(ErrContractConflict, "resolvedPlan.kit.slug", "Cloud identity authority is available only to cloud-kit")
		}
	}
	siteRef, err := stringField(input, path, "siteRef")
	if err != nil {
		return nil, err
	}
	if sites != nil {
		synthetic := map[string]any{"placement": map[string]any{"kind": "sites", "siteRefs": []any{siteRef}}}
		if _, err := requireExactCloudIdentityPlacement(synthetic, sites, path); err != nil {
			return nil, err
		}
	}
	rawIssuers, err := objectListField(input, path, "issuers")
	if err != nil || len(rawIssuers) != 2 {
		return nil, fail(ErrContractConflict, path+".issuers", "requires exactly human and workload issuers")
	}
	rawVerifiers, err := objectListField(input, path, "verifiers")
	if err != nil || len(rawVerifiers) != 3 {
		return nil, fail(ErrContractConflict, path+".verifiers", "requires exactly device, human, and workload verifiers")
	}
	issuers := make([]any, 0, 2)
	issuerCounts := map[string]int{}
	issuerIDs := map[string]struct{}{}
	for index, issuer := range rawIssuers {
		issuerPath := fmt.Sprintf("%s.issuers[%d]", path, index)
		if len(issuer) != 10 {
			return nil, fail(ErrContractConflict, issuerPath, "issuer contains authority outside the closed projection")
		}
		validated, err := validatePublicCloudIdentityIssuer(issuer, stackID, issuerPath)
		if err != nil {
			return nil, err
		}
		principal := validated["principal"].(string)
		issuerCounts[principal]++
		id := validated["id"].(string)
		if _, duplicate := issuerIDs[id]; duplicate {
			return nil, fail(ErrContractConflict, issuerPath+".id", "issuer ID is duplicated")
		}
		issuerIDs[id] = struct{}{}
		issuers = append(issuers, validated)
	}
	for _, principal := range []string{"human", "workload"} {
		if issuerCounts[principal] != 1 {
			return nil, fail(ErrContractConflict, path+".issuers", "requires exactly one %s issuer", principal)
		}
	}
	verifiers := make([]any, 0, 3)
	verifierCounts := map[string]int{}
	verifierIDs := map[string]struct{}{}
	for index, verifier := range rawVerifiers {
		verifierPath := fmt.Sprintf("%s.verifiers[%d]", path, index)
		if len(verifier) != 8 {
			return nil, fail(ErrContractConflict, verifierPath, "verifier contains authority outside the closed projection")
		}
		validated, err := validatePublicCloudIdentityVerifier(verifier, stackID, verifierPath)
		if err != nil {
			return nil, err
		}
		principal := validated["principal"].(string)
		verifierCounts[principal]++
		id := validated["id"].(string)
		if _, duplicate := verifierIDs[id]; duplicate {
			return nil, fail(ErrContractConflict, verifierPath+".id", "verifier ID is duplicated")
		}
		verifierIDs[id] = struct{}{}
		verifiers = append(verifiers, validated)
	}
	for _, principal := range []string{"device", "human", "workload"} {
		if verifierCounts[principal] != 1 {
			return nil, fail(ErrContractConflict, path+".verifiers", "requires exactly one %s verifier", principal)
		}
	}
	sort.Slice(issuers, func(i, j int) bool {
		return issuers[i].(map[string]any)["id"].(string) < issuers[j].(map[string]any)["id"].(string)
	})
	sort.Slice(verifiers, func(i, j int) bool {
		return verifiers[i].(map[string]any)["id"].(string) < verifiers[j].(map[string]any)["id"].(string)
	})
	return map[string]any{"siteRef": siteRef, "issuers": issuers, "verifiers": verifiers}, nil
}

func validatePublicCloudIdentityIssuer(value map[string]any, stackID, path string) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range []string{"id", "authorityRef", "principal", "issuer", "verificationKeySetRef"} {
		item, err := stringField(value, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = item
	}
	principal := result["principal"].(string)
	if principal != "human" && principal != "workload" {
		return nil, fail(ErrContractConflict, path+".principal", "Cloud may issue only human and workload identity")
	}
	if !identityTrustIDPattern.MatchString(result["id"].(string)) || !identityTrustIDPattern.MatchString(result["authorityRef"].(string)) {
		return nil, fail(ErrContractConflict, path, "issuer and authority references must be canonical")
	}
	if err := requireResolvedIdentityURN(result["issuer"].(string), stackID, "issuer", path+".issuer"); err != nil {
		return nil, err
	}
	if err := requireResolvedIdentityURN(result["verificationKeySetRef"].(string), stackID, "keyset", path+".verificationKeySetRef"); err != nil {
		return nil, err
	}
	audiences, err := stringListField(value, path, "audiences", true)
	if err != nil || len(audiences) == 0 || !reflect.DeepEqual(audiences, sortStringsUnique(audiences)) {
		return nil, fail(ErrContractConflict, path+".audiences", "issuer audiences must be non-empty, sorted, and unique")
	}
	for index, audience := range audiences {
		if err := requireResolvedIdentityURN(audience, stackID, "audience", fmt.Sprintf("%s.audiences[%d]", path, index)); err != nil {
			return nil, err
		}
	}
	result["audiences"] = stringSliceAny(audiences)
	for _, field := range []string{"proofOfPossessionRequired"} {
		flag, ok := value[field].(bool)
		if !ok {
			return nil, fail(ErrContractConflict, path+"."+field, "requires boolean")
		}
		result[field] = flag
	}
	for _, field := range []string{"revocationMaxStalenessSeconds", "lifetimeSeconds", "sessionTTLSeconds"} {
		number, err := intField(value, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = number
	}
	lifetime := result["lifetimeSeconds"].(int)
	sessionTTL := result["sessionTTLSeconds"].(int)
	staleness := result["revocationMaxStalenessSeconds"].(int)
	if lifetime < 300 || lifetime > 86400 || sessionTTL < 60 || sessionTTL > 86400 || staleness < 0 || staleness > lifetime {
		return nil, fail(ErrContractConflict, path, "issuer lifetime, session, and revocation policy are outside closed bounds")
	}
	return result, nil
}

func validatePublicCloudIdentityVerifier(value map[string]any, stackID, path string) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range []string{"id", "principal", "issuerRef", "issuer", "verificationKeySetRef"} {
		item, err := stringField(value, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = item
	}
	principal := result["principal"].(string)
	if principal != "device" && principal != "human" && principal != "workload" {
		return nil, fail(ErrContractConflict, path+".principal", "unsupported Cloud verifier principal")
	}
	if !identityTrustIDPattern.MatchString(result["id"].(string)) || !identityTrustIDPattern.MatchString(result["issuerRef"].(string)) {
		return nil, fail(ErrContractConflict, path, "verifier and issuer references must be canonical")
	}
	if err := requireResolvedIdentityURN(result["issuer"].(string), stackID, "issuer", path+".issuer"); err != nil {
		return nil, err
	}
	if err := requireResolvedIdentityURN(result["verificationKeySetRef"].(string), stackID, "keyset", path+".verificationKeySetRef"); err != nil {
		return nil, err
	}
	audiences, err := stringListField(value, path, "audiences", true)
	if err != nil || len(audiences) == 0 || !reflect.DeepEqual(audiences, sortStringsUnique(audiences)) {
		return nil, fail(ErrContractConflict, path+".audiences", "verifier audiences must be non-empty, sorted, and unique")
	}
	for index, audience := range audiences {
		if err := requireResolvedIdentityURN(audience, stackID, "audience", fmt.Sprintf("%s.audiences[%d]", path, index)); err != nil {
			return nil, err
		}
	}
	result["audiences"] = stringSliceAny(audiences)
	proof, ok := value["proofOfPossessionRequired"].(bool)
	if !ok {
		return nil, fail(ErrContractConflict, path+".proofOfPossessionRequired", "requires boolean")
	}
	result["proofOfPossessionRequired"] = proof
	staleness, err := intField(value, path, "revocationMaxStalenessSeconds")
	if err != nil || staleness < 0 || staleness > 86400 {
		return nil, fail(ErrContractConflict, path+".revocationMaxStalenessSeconds", "requires bounded revocation staleness")
	}
	result["revocationMaxStalenessSeconds"] = staleness
	return result, nil
}
