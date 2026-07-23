package resolvedplan

import (
	"fmt"
	"reflect"
	"sort"
)

const (
	basementVerificationProviderRef = "stackkits-basement-identity-trust-policy"
	basementVerificationModuleRef   = "stackkits-basement-identity-trust-policy-manifest"
)

func projectPublicBasementIdentityVerification(value any, kit map[string]any, sites []any, stackID, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(value, path)
	if err != nil {
		return nil, err
	}
	if alreadyPublic {
		return exactPublicBasementIdentityVerification(input, kit, sites, stackID, path)
	}
	slug, err := stringField(kit, "resolvedPlan.kit", "slug")
	if err != nil || slug != "basement-kit" {
		return nil, fail(ErrContractConflict, "resolvedPlan.kit.slug", "Basement identity verification is available only to basement-kit")
	}
	verifiers, err := objectListField(input, path, "verifierPlacements")
	if err != nil {
		return nil, err
	}
	issuers, err := objectListField(input, path, "credentialIssuers")
	if err != nil {
		return nil, err
	}
	authorities, err := objectListField(input, path, "authorities")
	if err != nil {
		return nil, err
	}
	distributions, err := objectListField(input, path, "verifierDistributions")
	if err != nil {
		return nil, err
	}
	if len(authorities) != 3 || len(issuers) != 3 || len(verifiers) != 3 || len(distributions) != 0 {
		return nil, fail(ErrContractConflict, path, "Basement verification requires exactly three Home authorities, issuers, and verifiers with no distribution")
	}
	authorityByID := make(map[string]map[string]any, len(authorities))
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
	issuerByID := make(map[string]map[string]any, len(issuers))
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
	projected := map[string]any{"verifiers": []any{}}
	siteRef := ""
	for index, verifier := range verifiers {
		verifierPath := fmt.Sprintf("%s.verifierPlacements[%d]", path, index)
		if len(verifier) != 10 {
			return nil, fail(ErrContractConflict, verifierPath, "source verifier contains material or fields outside the closed identity contract")
		}
		if err := requireBasementVerifierOwner(verifier, verifierPath); err != nil {
			return nil, err
		}
		currentSite, err := requireHomeAuthorityPlacement(verifier, sites, verifierPath)
		if err != nil {
			return nil, err
		}
		if siteRef == "" {
			siteRef = currentSite
		} else if currentSite != siteRef {
			return nil, fail(ErrContractConflict, verifierPath+".placement", "all Basement verifiers must bind the same Home Site")
		}
		issuerRef, err := stringField(verifier, verifierPath, "credentialIssuerRef")
		if err != nil {
			return nil, err
		}
		issuer, exists := issuerByID[issuerRef]
		if !exists {
			return nil, fail(ErrContractConflict, verifierPath+".credentialIssuerRef", "referenced issuer does not exist")
		}
		issuerSite, err := requireHomeAuthorityPlacement(issuer, sites, verifierPath+".issuer")
		if err != nil || issuerSite != currentSite {
			return nil, fail(ErrContractConflict, verifierPath+".issuer.placement", "source issuer must bind the exact verifier Home Site")
		}
		if err := requireHomeAuthorityOwner(issuer, verifierPath+".issuer"); err != nil {
			return nil, err
		}
		authorityRef, err := stringField(issuer, verifierPath+".issuer", "authorityRef")
		if err != nil {
			return nil, err
		}
		authority, exists := authorityByID[authorityRef]
		if !exists {
			return nil, fail(ErrContractConflict, verifierPath+".issuer.authorityRef", "source issuer authority does not exist")
		}
		authoritySite, err := requireHomeAuthorityPlacement(authority, sites, verifierPath+".issuer.authority")
		if err != nil || authoritySite != currentSite {
			return nil, fail(ErrContractConflict, verifierPath+".issuer.authority.placement", "source authority must bind the exact verifier Home Site")
		}
		if err := requireHomeAuthorityOwner(authority, verifierPath+".issuer.authority"); err != nil {
			return nil, err
		}
		for _, field := range []string{"principal"} {
			authorityValue, err := stringField(authority, verifierPath+".issuer.authority", field)
			if err != nil {
				return nil, err
			}
			issuerValue, err := stringField(issuer, verifierPath+".issuer", field)
			if err != nil || authorityValue != issuerValue {
				return nil, fail(ErrContractConflict, verifierPath+".issuer."+field, "source authority, issuer, and verifier must rebound")
			}
		}
		projectedVerifier, err := projectBasementVerifier(verifier, issuer, stackID, verifierPath)
		if err != nil {
			return nil, err
		}
		projected["verifiers"] = append(projected["verifiers"].([]any), projectedVerifier)
	}
	if siteRef == "" {
		return nil, fail(ErrContractConflict, path+".verifierPlacements", "Basement verification requires exact verifier placements")
	}
	projected["siteRef"] = siteRef
	return exactPublicBasementIdentityVerification(projected, kit, sites, stackID, path+".public")
}

func requireBasementVerifierOwner(value map[string]any, path string) error {
	owner, err := objectField(value, path, "owner")
	if err != nil {
		return err
	}
	if len(owner) != 3 {
		return fail(ErrContractConflict, path+".owner", "Basement verifier owner must be exact")
	}
	for field, expected := range map[string]string{
		"kind": "catalog", "providerRef": basementVerificationProviderRef, "moduleRef": basementVerificationModuleRef,
	} {
		actual, err := stringField(owner, path+".owner", field)
		if err != nil || actual != expected {
			return fail(ErrContractConflict, path+".owner."+field, "Basement verifier owner requires %q", expected)
		}
	}
	return nil
}

func projectBasementVerifier(verifier, issuer map[string]any, stackID, path string) (map[string]any, error) {
	result := map[string]any{}
	for sourceField, targetField := range map[string]string{
		"id": "id", "principal": "principal", "credentialIssuerRef": "issuerRef", "issuer": "issuer", "verificationKeySetRef": "verificationKeySetRef",
	} {
		value, err := stringField(verifier, path, sourceField)
		if err != nil {
			return nil, err
		}
		result[targetField] = value
	}
	issuerPath := path + ".issuer"
	for _, field := range []string{"id", "principal", "issuer", "verificationKeySetRef"} {
		sourceField := field
		expected := result[field]
		if field == "id" {
			expected = result["issuerRef"]
		}
		actual, err := stringField(issuer, issuerPath, sourceField)
		if err != nil || actual != expected {
			return nil, fail(ErrContractConflict, path+"."+sourceField, "verifier must exactly rebound its issuer")
		}
	}
	principal := result["principal"].(string)
	if principal != "device" && principal != "human" && principal != "workload" {
		return nil, fail(ErrContractConflict, path+".principal", "unsupported Basement verifier principal")
	}
	if err := requireResolvedIdentityURN(result["issuer"].(string), stackID, "issuer", path+".issuer"); err != nil {
		return nil, err
	}
	if err := requireResolvedIdentityURN(result["verificationKeySetRef"].(string), stackID, "keyset", path+".verificationKeySetRef"); err != nil {
		return nil, err
	}
	audiences, err := stringListField(verifier, path, "audiences", true)
	if err != nil || len(audiences) == 0 || !reflect.DeepEqual(audiences, sortStringsUnique(audiences)) {
		return nil, fail(ErrContractConflict, path+".audiences", "verifier audiences must be non-empty, sorted, and unique")
	}
	issuerAudiences, err := stringListField(issuer, issuerPath, "audiences", true)
	if err != nil || !reflect.DeepEqual(audiences, issuerAudiences) {
		return nil, fail(ErrContractConflict, path+".audiences", "verifier audiences must exactly rebound its issuer")
	}
	for index, audience := range audiences {
		if err := requireResolvedIdentityURN(audience, stackID, "audience", fmt.Sprintf("%s.audiences[%d]", path, index)); err != nil {
			return nil, err
		}
	}
	result["audiences"] = stringSliceAny(audiences)
	for _, field := range []string{"proofOfPossessionRequired", "revocationMaxStalenessSeconds"} {
		if !reflect.DeepEqual(verifier[field], issuer[field]) {
			return nil, fail(ErrContractConflict, path+"."+field, "verifier policy must exactly rebound its issuer")
		}
		result[field] = verifier[field]
	}
	for _, field := range []string{"issuanceWithinStackKit", "revocationSupported"} {
		flag, ok := issuer[field].(bool)
		if !ok || !flag {
			return nil, fail(ErrContractConflict, issuerPath+"."+field, "source issuer requires true")
		}
	}
	lifetime, err := intField(issuer, issuerPath, "credentialTTLSeconds")
	if err != nil {
		return nil, err
	}
	sessionTTL, err := intField(issuer, issuerPath, "sessionTTLSeconds")
	if err != nil {
		return nil, err
	}
	result["lifetimeSeconds"] = lifetime
	result["sessionTTLSeconds"] = sessionTTL
	return result, nil
}

func exactPublicBasementIdentityVerification(input map[string]any, kit map[string]any, sites []any, stackID, path string) (map[string]any, error) {
	if len(input) != 2 {
		return nil, fail(ErrContractConflict, path, "Basement verification projection must contain exactly siteRef and verifiers")
	}
	if kit != nil {
		slug, err := stringField(kit, "resolvedPlan.kit", "slug")
		if err != nil || slug != "basement-kit" {
			return nil, fail(ErrContractConflict, "resolvedPlan.kit.slug", "Basement identity verification is available only to basement-kit")
		}
	}
	siteRef, err := stringField(input, path, "siteRef")
	if err != nil {
		return nil, err
	}
	if sites != nil {
		synthetic := map[string]any{"placement": map[string]any{"kind": "sites", "siteRefs": []any{siteRef}}}
		if _, err := requireHomeAuthorityPlacement(synthetic, sites, path); err != nil {
			return nil, err
		}
	}
	raw, err := objectListField(input, path, "verifiers")
	if err != nil || len(raw) != 3 {
		return nil, fail(ErrContractConflict, path+".verifiers", "requires exactly device, human, and workload verifiers")
	}
	principalCount := map[string]int{}
	ids := map[string]struct{}{}
	projected := make([]any, 0, 3)
	for index, verifier := range raw {
		verifierPath := fmt.Sprintf("%s.verifiers[%d]", path, index)
		if len(verifier) != 10 {
			return nil, fail(ErrContractConflict, verifierPath, "verifier contains authority outside the closed projection")
		}
		id, err := stringField(verifier, verifierPath, "id")
		if err != nil || !identityTrustIDPattern.MatchString(id) {
			return nil, fail(ErrContractConflict, verifierPath+".id", "requires a canonical verifier ID")
		}
		if _, duplicate := ids[id]; duplicate {
			return nil, fail(ErrContractConflict, verifierPath+".id", "verifier ID is duplicated")
		}
		ids[id] = struct{}{}
		principal, err := stringField(verifier, verifierPath, "principal")
		if err != nil || principal != "device" && principal != "human" && principal != "workload" {
			return nil, fail(ErrContractConflict, verifierPath+".principal", "requires device, human, or workload")
		}
		principalCount[principal]++
		issuerRef, err := stringField(verifier, verifierPath, "issuerRef")
		if err != nil || !identityTrustIDPattern.MatchString(issuerRef) {
			return nil, fail(ErrContractConflict, verifierPath+".issuerRef", "requires a canonical issuer reference")
		}
		issuer, err := stringField(verifier, verifierPath, "issuer")
		if err != nil {
			return nil, err
		}
		keyset, err := stringField(verifier, verifierPath, "verificationKeySetRef")
		if err != nil {
			return nil, err
		}
		if err := requireResolvedIdentityURN(issuer, stackID, "issuer", verifierPath+".issuer"); err != nil {
			return nil, err
		}
		if err := requireResolvedIdentityURN(keyset, stackID, "keyset", verifierPath+".verificationKeySetRef"); err != nil {
			return nil, err
		}
		audiences, err := stringListField(verifier, verifierPath, "audiences", true)
		if err != nil || len(audiences) == 0 || !reflect.DeepEqual(audiences, sortStringsUnique(audiences)) {
			return nil, fail(ErrContractConflict, verifierPath+".audiences", "audiences must be non-empty, sorted, and unique")
		}
		for audienceIndex, audience := range audiences {
			if err := requireResolvedIdentityURN(audience, stackID, "audience", fmt.Sprintf("%s.audiences[%d]", verifierPath, audienceIndex)); err != nil {
				return nil, err
			}
		}
		proof, ok := verifier["proofOfPossessionRequired"].(bool)
		if !ok {
			return nil, fail(ErrContractConflict, verifierPath+".proofOfPossessionRequired", "requires boolean")
		}
		staleness, err := intField(verifier, verifierPath, "revocationMaxStalenessSeconds")
		if err != nil {
			return nil, err
		}
		lifetime, err := intField(verifier, verifierPath, "lifetimeSeconds")
		if err != nil {
			return nil, err
		}
		sessionTTL, err := intField(verifier, verifierPath, "sessionTTLSeconds")
		if err != nil {
			return nil, err
		}
		if lifetime < 300 || lifetime > 86400 || sessionTTL < 60 || sessionTTL > 86400 || staleness < 0 || staleness > lifetime {
			return nil, fail(ErrContractConflict, verifierPath, "lifetime, session, and revocation policy are outside closed bounds")
		}
		projected = append(projected, map[string]any{
			"id": id, "principal": principal, "issuerRef": issuerRef, "issuer": issuer,
			"audiences": stringSliceAny(audiences), "verificationKeySetRef": keyset,
			"proofOfPossessionRequired": proof, "revocationMaxStalenessSeconds": staleness,
			"lifetimeSeconds": lifetime, "sessionTTLSeconds": sessionTTL,
		})
	}
	for _, principal := range []string{"device", "human", "workload"} {
		if principalCount[principal] != 1 {
			return nil, fail(ErrContractConflict, path+".verifiers", "requires exactly one %s verifier", principal)
		}
	}
	sort.Slice(projected, func(i, j int) bool {
		return projected[i].(map[string]any)["id"].(string) < projected[j].(map[string]any)["id"].(string)
	})
	return map[string]any{"siteRef": siteRef, "verifiers": projected}, nil
}
