package resolvedplan

import (
	"fmt"
	"reflect"
	"sort"
)

const (
	modernIdentityProviderRef = "stackkits-modern-identity-trust-policy"
	modernHomeIdentityModule  = "stackkits-modern-home-identity-trust-policy-manifest"
	modernCloudIdentityModule = "stackkits-modern-cloud-identity-verifier-policy-manifest"
	modernIssuerProviderRef   = "stackkits-home-device-authority"
	modernIssuerModuleRef     = "stackkits-home-device-authority-policy-manifest"
)

type modernIdentitySourceProjection struct {
	homeSiteRef    string
	cloudSiteRefs  []string
	partition      map[string]any
	issuers        []any
	homeVerifiers  []any
	cloudVerifiers []any
	distributions  []any
}

func projectPublicModernHomeIdentityAuthority(identityTrust, failurePolicy any, kit map[string]any, sites []any, stackID, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(identityTrust, path)
	if err != nil {
		return nil, err
	}
	if alreadyPublic {
		return exactPublicModernHomeIdentityAuthority(input, kit, sites, stackID, path)
	}
	source, err := projectModernIdentitySource(input, failurePolicy, kit, sites, stackID, path)
	if err != nil {
		return nil, err
	}
	value := map[string]any{
		"homeSiteRef": source.homeSiteRef, "cloudSiteRefs": stringSliceAny(source.cloudSiteRefs),
		"partition": map[string]any{
			"onCloudLoss": source.partition["onCloudLoss"], "onLinkLoss": source.partition["onLinkLoss"],
			"localIdentityAuthorityAvailable": source.partition["localIdentityAuthorityAvailable"],
			"maxStaleVerificationSeconds":     source.partition["maxStaleVerificationSeconds"],
			"denyNewCrossSiteSessions":        source.partition["denyNewCrossSiteSessions"],
		},
		"issuers": source.issuers, "verifiers": source.homeVerifiers, "distributions": source.distributions,
	}
	return exactPublicModernHomeIdentityAuthority(value, kit, sites, stackID, path+".public")
}

func projectPublicModernCloudIdentityVerification(identityTrust, failurePolicy any, kit map[string]any, sites []any, stackID, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(identityTrust, path)
	if err != nil {
		return nil, err
	}
	if alreadyPublic {
		return exactPublicModernCloudIdentityVerification(input, kit, sites, stackID, path)
	}
	source, err := projectModernIdentitySource(input, failurePolicy, kit, sites, stackID, path)
	if err != nil {
		return nil, err
	}
	value := map[string]any{
		"homeSiteRef": source.homeSiteRef, "cloudSiteRefs": stringSliceAny(source.cloudSiteRefs),
		"partition": map[string]any{
			"cloudEdge":                   source.partition["cloudEdge"],
			"maxStaleVerificationSeconds": source.partition["maxStaleVerificationSeconds"],
			"denyNewCrossSiteSessions":    source.partition["denyNewCrossSiteSessions"],
		},
		"verifiers": source.cloudVerifiers, "distributions": source.distributions,
	}
	return exactPublicModernCloudIdentityVerification(value, kit, sites, stackID, path+".public")
}

//nolint:gocyclo // This is the single closed source-to-owner split for the Modern identity graph.
func projectModernIdentitySource(input map[string]any, failurePolicy any, kit map[string]any, sites []any, stackID, path string) (modernIdentitySourceProjection, error) {
	slug, err := stringField(kit, "resolvedPlan.kit", "slug")
	if err != nil || slug != "modern-homelab" {
		return modernIdentitySourceProjection{}, fail(ErrContractConflict, "resolvedPlan.kit.slug", "Modern identity projections are available only to modern-homelab")
	}
	partition, err := asObject(failurePolicy, path+".failurePolicy")
	if err != nil || len(partition) != 6 {
		return modernIdentitySourceProjection{}, fail(ErrContractConflict, path+".failurePolicy", "Modern partition source must be exact")
	}
	if partition["onCloudLoss"] != "local-continues" || partition["onLinkLoss"] != "local-continues" || partition["cloudEdge"] != "fail-closed" ||
		partition["localIdentityAuthorityAvailable"] != true || partition["denyNewCrossSiteSessions"] != true {
		return modernIdentitySourceProjection{}, fail(ErrContractConflict, path+".failurePolicy", "Modern partition must keep Home authority available and fail Cloud closed")
	}
	maxStale, err := intField(partition, path+".failurePolicy", "maxStaleVerificationSeconds")
	if err != nil || maxStale < 0 || maxStale > 86400 {
		return modernIdentitySourceProjection{}, fail(ErrContractConflict, path+".failurePolicy.maxStaleVerificationSeconds", "Modern verifier staleness must be bounded")
	}
	homeSiteRef, cloudSiteRefs, err := exactModernSitePartition(sites, path+".sites")
	if err != nil {
		return modernIdentitySourceProjection{}, err
	}
	authorities, err := objectListField(input, path, "authorities")
	if err != nil {
		return modernIdentitySourceProjection{}, err
	}
	issuers, err := objectListField(input, path, "credentialIssuers")
	if err != nil {
		return modernIdentitySourceProjection{}, err
	}
	verifiers, err := objectListField(input, path, "verifierPlacements")
	if err != nil {
		return modernIdentitySourceProjection{}, err
	}
	distributions, err := objectListField(input, path, "verifierDistributions")
	if err != nil {
		return modernIdentitySourceProjection{}, err
	}
	if len(authorities) != 3 || len(issuers) != 3 || len(verifiers) != 6 || len(distributions) != 3 {
		return modernIdentitySourceProjection{}, fail(ErrContractConflict, path, "Modern identity requires exactly three authorities, issuers, distributions and two verifier sets")
	}
	authorityByID := map[string]map[string]any{}
	for index, authority := range authorities {
		itemPath := fmt.Sprintf("%s.authorities[%d]", path, index)
		if len(authority) != 5 || requireModernOwner(authority, modernIssuerProviderRef, modernIssuerModuleRef, itemPath) != nil ||
			requireModernPlacement(authority, []string{homeSiteRef}, itemPath) != nil {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath, "Modern authority must remain exactly Home-owned")
		}
		id, err := stringField(authority, itemPath, "id")
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		authorityByID[id] = authority
	}
	issuerByID := map[string]map[string]any{}
	projectedIssuers := make([]any, 0, 3)
	principalIssuers := map[string]int{}
	for index, issuer := range issuers {
		itemPath := fmt.Sprintf("%s.credentialIssuers[%d]", path, index)
		if len(issuer) != 15 || requireModernOwner(issuer, modernIssuerProviderRef, modernIssuerModuleRef, itemPath) != nil ||
			requireModernPlacement(issuer, []string{homeSiteRef}, itemPath) != nil {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath, "Modern issuer must remain exactly Home-owned")
		}
		id, err := stringField(issuer, itemPath, "id")
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		authorityRef, err := stringField(issuer, itemPath, "authorityRef")
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		authority, exists := authorityByID[authorityRef]
		if !exists {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath+".authorityRef", "Modern issuer authority is absent")
		}
		principal, err := stringField(issuer, itemPath, "principal")
		if err != nil || !modernPrincipal(principal) || authority["principal"] != principal {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath+".principal", "Modern authority and issuer principal must match")
		}
		issuerStale, staleErr := intField(issuer, itemPath, "revocationMaxStalenessSeconds")
		if staleErr != nil || issuer["issuanceWithinStackKit"] != true || issuer["revocationSupported"] != true || issuerStale != maxStale {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath, "Modern Home issuer authority or partition staleness changed")
		}
		enrollment, err := objectField(issuer, itemPath, "enrollment")
		if err != nil || len(enrollment) != 2 {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath+".enrollment", "Modern enrollment source must be exact")
		}
		expectedMode, expectedExposure := "none", "none"
		if principal == "device" {
			expectedMode, expectedExposure = "local-only", "lan"
		}
		if enrollment["mode"] != expectedMode || enrollment["exposure"] != expectedExposure {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath+".enrollment", "Modern enrollment must remain local-only for devices and absent otherwise")
		}
		projected, err := projectModernIssuer(issuer, stackID, itemPath)
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		projectedIssuers = append(projectedIssuers, projected)
		principalIssuers[principal]++
		issuerByID[id] = issuer
	}
	if !exactModernPrincipalCounts(principalIssuers) {
		return modernIdentitySourceProjection{}, fail(ErrContractConflict, path+".credentialIssuers", "Modern requires one issuer per principal")
	}
	homeVerifiers, cloudVerifiers := []any{}, []any{}
	homeCounts, cloudCounts := map[string]int{}, map[string]int{}
	for index, verifier := range verifiers {
		itemPath := fmt.Sprintf("%s.verifierPlacements[%d]", path, index)
		if len(verifier) != 10 {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath, "Modern verifier contains fields outside the closed source contract")
		}
		issuerRef, err := stringField(verifier, itemPath, "credentialIssuerRef")
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		issuer, exists := issuerByID[issuerRef]
		if !exists {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath+".credentialIssuerRef", "Modern verifier issuer is absent")
		}
		principal, err := validateModernIssuerRebound(verifier, issuer, stackID, itemPath)
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		projected, err := projectModernVerifier(verifier, stackID, itemPath)
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		placement, _ := objectField(verifier, itemPath, "placement")
		refs, _ := stringListField(placement, itemPath+".placement", "siteRefs", true)
		switch {
		case reflect.DeepEqual(refs, []string{homeSiteRef}):
			verifierStale, staleErr := intField(verifier, itemPath, "revocationMaxStalenessSeconds")
			if err := requireModernOwner(verifier, modernIdentityProviderRef, modernHomeIdentityModule, itemPath); err != nil || staleErr != nil || verifierStale != 0 {
				return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath, "Home verifier owner or zero-stale policy changed")
			}
			homeVerifiers, homeCounts[principal] = append(homeVerifiers, projected), homeCounts[principal]+1
		case reflect.DeepEqual(refs, cloudSiteRefs):
			verifierStale, staleErr := intField(verifier, itemPath, "revocationMaxStalenessSeconds")
			if err := requireModernOwner(verifier, modernIdentityProviderRef, modernCloudIdentityModule, itemPath); err != nil || staleErr != nil || verifierStale != maxStale {
				return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath, "Cloud verifier owner or partition staleness changed")
			}
			cloudVerifiers, cloudCounts[principal] = append(cloudVerifiers, projected), cloudCounts[principal]+1
		default:
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath+".placement", "verifier crossed the exact Home/Cloud Site partition")
		}
	}
	if !exactModernPrincipalCounts(homeCounts) || !exactModernPrincipalCounts(cloudCounts) {
		return modernIdentitySourceProjection{}, fail(ErrContractConflict, path+".verifierPlacements", "Modern requires one Home and one Cloud verifier per principal")
	}
	projectedDistributions, distributionCounts := []any{}, map[string]int{}
	for index, distribution := range distributions {
		itemPath := fmt.Sprintf("%s.verifierDistributions[%d]", path, index)
		if len(distribution) != 13 || requireModernOwner(distribution, modernIdentityProviderRef, modernHomeIdentityModule, itemPath) != nil ||
			requireModernPlacementField(distribution, "from", []string{homeSiteRef}, itemPath) != nil ||
			requireModernPlacementField(distribution, "to", cloudSiteRefs, itemPath) != nil {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath, "Modern distribution must remain exact Home-to-Cloud authority")
		}
		issuerRef, err := stringField(distribution, itemPath, "credentialIssuerRef")
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		issuer, exists := issuerByID[issuerRef]
		if !exists {
			return modernIdentitySourceProjection{}, fail(ErrContractConflict, itemPath+".credentialIssuerRef", "distribution issuer is absent")
		}
		principal, err := validateModernDistributionRebound(distribution, issuer, maxStale, stackID, itemPath)
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		projected, err := projectModernDistribution(distribution, principal, stackID, itemPath)
		if err != nil {
			return modernIdentitySourceProjection{}, err
		}
		projectedDistributions, distributionCounts[principal] = append(projectedDistributions, projected), distributionCounts[principal]+1
	}
	if !exactModernPrincipalCounts(distributionCounts) {
		return modernIdentitySourceProjection{}, fail(ErrContractConflict, path+".verifierDistributions", "Modern requires one one-way distribution per principal")
	}
	sortModernProjection(projectedIssuers)
	sortModernProjection(homeVerifiers)
	sortModernProjection(cloudVerifiers)
	sortModernProjection(projectedDistributions)
	return modernIdentitySourceProjection{
		homeSiteRef: homeSiteRef, cloudSiteRefs: cloudSiteRefs, partition: partition,
		issuers: projectedIssuers, homeVerifiers: homeVerifiers, cloudVerifiers: cloudVerifiers, distributions: projectedDistributions,
	}, nil
}

func exactModernSitePartition(sites []any, path string) (string, []string, error) {
	homeRefs, cloudRefs := []string{}, []string{}
	for index, raw := range sites {
		site, err := asObject(raw, fmt.Sprintf("%s[%d]", path, index))
		if err != nil {
			return "", nil, err
		}
		id, err := stringField(site, path, "id")
		if err != nil {
			return "", nil, err
		}
		kind, err := stringField(site, path, "kind")
		if err != nil {
			return "", nil, err
		}
		if kind == "home" {
			homeRefs = append(homeRefs, id)
		} else if kind == "cloud" {
			cloudRefs = append(cloudRefs, id)
		}
	}
	sort.Strings(homeRefs)
	sort.Strings(cloudRefs)
	if len(homeRefs) != 1 || len(cloudRefs) == 0 {
		return "", nil, fail(ErrContractConflict, path, "Modern identity requires one Home authority Site and at least one Cloud Site")
	}
	return homeRefs[0], cloudRefs, nil
}

func requireModernOwner(value map[string]any, providerRef, moduleRef, path string) error {
	owner, err := objectField(value, path, "owner")
	if err != nil || len(owner) != 3 || owner["kind"] != "catalog" || owner["providerRef"] != providerRef || owner["moduleRef"] != moduleRef {
		return fail(ErrContractConflict, path+".owner", "identity owner must remain exact")
	}
	return nil
}

func requireModernPlacement(value map[string]any, refs []string, path string) error {
	return requireModernPlacementField(value, "placement", refs, path)
}

func requireModernPlacementField(value map[string]any, field string, refs []string, path string) error {
	placement, err := objectField(value, path, field)
	if err != nil || len(placement) != 2 || placement["kind"] != "sites" {
		return fail(ErrContractConflict, path+"."+field, "identity placement must be exact Site placement")
	}
	actual, err := stringListField(placement, path+"."+field, "siteRefs", true)
	if err != nil || !reflect.DeepEqual(actual, refs) {
		return fail(ErrContractConflict, path+"."+field+".siteRefs", "identity placement crossed the exact Site partition")
	}
	return nil
}

func projectModernIssuer(value map[string]any, stackID, path string) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range []string{"id", "authorityRef", "principal", "issuer", "verificationKeySetRef"} {
		item, err := stringField(value, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = item
	}
	if err := validateModernURNFields(value, result, stackID, path); err != nil {
		return nil, err
	}
	lifetime, err := intField(value, path, "credentialTTLSeconds")
	if err != nil {
		return nil, err
	}
	session, err := intField(value, path, "sessionTTLSeconds")
	if err != nil {
		return nil, err
	}
	stale, err := intField(value, path, "revocationMaxStalenessSeconds")
	if err != nil || lifetime < 300 || lifetime > 86400 || session < 60 || session > 86400 || stale < 0 || stale > lifetime {
		return nil, fail(ErrContractConflict, path, "Modern issuer lifetime policy is outside closed bounds")
	}
	enrollment, _ := objectField(value, path, "enrollment")
	result["lifetimeSeconds"], result["sessionTTLSeconds"] = lifetime, session
	result["revocationMaxStalenessSeconds"] = stale
	result["proofOfPossessionRequired"] = value["proofOfPossessionRequired"]
	result["enrollmentMode"], result["enrollmentExposure"] = enrollment["mode"], enrollment["exposure"]
	return result, nil
}

func projectModernVerifier(value map[string]any, stackID, path string) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range []string{"id", "principal", "issuer", "verificationKeySetRef"} {
		item, err := stringField(value, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = item
	}
	issuerRef, err := stringField(value, path, "credentialIssuerRef")
	if err != nil {
		return nil, err
	}
	result["issuerRef"] = issuerRef
	if err := validateModernURNFields(value, result, stackID, path); err != nil {
		return nil, err
	}
	stale, err := intField(value, path, "revocationMaxStalenessSeconds")
	if err != nil || stale < 0 || stale > 86400 {
		return nil, fail(ErrContractConflict, path+".revocationMaxStalenessSeconds", "Modern verifier staleness is outside closed bounds")
	}
	result["revocationMaxStalenessSeconds"] = stale
	result["proofOfPossessionRequired"] = value["proofOfPossessionRequired"]
	return result, nil
}

func projectModernDistribution(value map[string]any, principal, stackID, path string) (map[string]any, error) {
	id, err := stringField(value, path, "id")
	if err != nil {
		return nil, err
	}
	issuerRef, err := stringField(value, path, "credentialIssuerRef")
	if err != nil {
		return nil, err
	}
	issuer, err := stringField(value, path, "issuer")
	if err != nil {
		return nil, err
	}
	if err := requireResolvedIdentityURN(issuer, stackID, "issuer", path+".issuer"); err != nil {
		return nil, err
	}
	stale, err := intField(value, path, "maxStalenessSeconds")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "principal": principal, "issuerRef": issuerRef, "issuer": issuer,
		"materials": []any{"revocation-state", "verification-key-reference"}, "maxStalenessSeconds": stale,
	}, nil
}

func validateModernURNFields(source, result map[string]any, stackID, path string) error {
	issuer := result["issuer"].(string)
	keyset := result["verificationKeySetRef"].(string)
	if err := requireResolvedIdentityURN(issuer, stackID, "issuer", path+".issuer"); err != nil {
		return err
	}
	if err := requireResolvedIdentityURN(keyset, stackID, "keyset", path+".verificationKeySetRef"); err != nil {
		return err
	}
	audiences, err := stringListField(source, path, "audiences", true)
	if err != nil || len(audiences) == 0 || !reflect.DeepEqual(audiences, sortStringsUnique(audiences)) {
		return fail(ErrContractConflict, path+".audiences", "Modern audiences must be non-empty, sorted, and unique")
	}
	for index, audience := range audiences {
		if err := requireResolvedIdentityURN(audience, stackID, "audience", fmt.Sprintf("%s.audiences[%d]", path, index)); err != nil {
			return err
		}
	}
	result["audiences"] = stringSliceAny(audiences)
	return nil
}

func validateModernIssuerRebound(verifier, issuer map[string]any, stackID, path string) (string, error) {
	principal, err := stringField(verifier, path, "principal")
	if err != nil || !modernPrincipal(principal) || issuer["principal"] != principal {
		return "", fail(ErrContractConflict, path+".principal", "Modern verifier principal must rebound its issuer")
	}
	for _, field := range []string{"issuer", "verificationKeySetRef", "audiences", "proofOfPossessionRequired"} {
		if !reflect.DeepEqual(verifier[field], issuer[field]) {
			return "", fail(ErrContractConflict, path+"."+field, "Modern verifier must exactly rebound its issuer")
		}
	}
	if _, err := projectModernVerifier(verifier, stackID, path); err != nil {
		return "", err
	}
	return principal, nil
}

func validateModernDistributionRebound(distribution, issuer map[string]any, maxStale int, stackID, path string) (string, error) {
	principal, err := stringField(issuer, path+".issuer", "principal")
	distributionStale, staleErr := intField(distribution, path, "maxStalenessSeconds")
	if err != nil || staleErr != nil || distribution["issuer"] != issuer["issuer"] || distributionStale != maxStale {
		return "", fail(ErrContractConflict, path, "Modern distribution must rebound issuer and partition staleness")
	}
	materials, err := stringListField(distribution, path, "materials", true)
	if err != nil || !reflect.DeepEqual(materials, []string{"revocation-state", "verification-key-reference"}) {
		return "", fail(ErrContractConflict, path+".materials", "Modern distribution materials must be exact references")
	}
	for _, field := range []string{"includesSigningAuthority", "includesEnrollmentAuthority", "includesPrivateKeyMaterial", "includesCredentialMaterial", "reverseAllowed"} {
		if distribution[field] != false {
			return "", fail(ErrContractConflict, path+"."+field, "Modern distribution cannot carry authority, material, or reverse flow")
		}
	}
	_, err = projectModernDistribution(distribution, principal, stackID, path)
	return principal, err
}

func modernPrincipal(value string) bool {
	return value == "device" || value == "human" || value == "workload"
}

func exactModernPrincipalCounts(counts map[string]int) bool {
	return counts["device"] == 1 && counts["human"] == 1 && counts["workload"] == 1 && len(counts) == 3
}

func sortModernProjection(values []any) {
	sort.Slice(values, func(i, j int) bool {
		return values[i].(map[string]any)["id"].(string) < values[j].(map[string]any)["id"].(string)
	})
}

func exactPublicModernHomeIdentityAuthority(input map[string]any, kit map[string]any, sites []any, stackID, path string) (map[string]any, error) {
	if len(input) != 6 {
		return nil, fail(ErrContractConflict, path, "Modern Home projection must contain exactly six owner-bounded fields")
	}
	homeSiteRef, cloudSiteRefs, partition, err := exactModernPublicEnvelope(input, kit, sites, path, "home")
	if err != nil {
		return nil, err
	}
	issuers, err := exactModernPublicItems(input, "issuers", 3, 12, stackID, path, projectExactModernIssuer)
	if err != nil {
		return nil, err
	}
	verifiers, err := exactModernPublicItems(input, "verifiers", 3, 8, stackID, path, projectExactModernVerifier)
	if err != nil {
		return nil, err
	}
	distributions, err := exactModernPublicItems(input, "distributions", 3, 6, stackID, path, projectExactModernDistribution)
	if err != nil {
		return nil, err
	}
	return map[string]any{"homeSiteRef": homeSiteRef, "cloudSiteRefs": stringSliceAny(cloudSiteRefs), "partition": partition, "issuers": issuers, "verifiers": verifiers, "distributions": distributions}, nil
}

func exactPublicModernCloudIdentityVerification(input map[string]any, kit map[string]any, sites []any, stackID, path string) (map[string]any, error) {
	if len(input) != 5 {
		return nil, fail(ErrContractConflict, path, "Modern Cloud projection must contain exactly five owner-bounded fields")
	}
	homeSiteRef, cloudSiteRefs, partition, err := exactModernPublicEnvelope(input, kit, sites, path, "cloud")
	if err != nil {
		return nil, err
	}
	verifiers, err := exactModernPublicItems(input, "verifiers", 3, 8, stackID, path, projectExactModernVerifier)
	if err != nil {
		return nil, err
	}
	distributions, err := exactModernPublicItems(input, "distributions", 3, 6, stackID, path, projectExactModernDistribution)
	if err != nil {
		return nil, err
	}
	return map[string]any{"homeSiteRef": homeSiteRef, "cloudSiteRefs": stringSliceAny(cloudSiteRefs), "partition": partition, "verifiers": verifiers, "distributions": distributions}, nil
}

func exactModernPublicEnvelope(input map[string]any, kit map[string]any, sites []any, path, role string) (string, []string, map[string]any, error) {
	if kit != nil {
		slug, err := stringField(kit, "resolvedPlan.kit", "slug")
		if err != nil || slug != "modern-homelab" {
			return "", nil, nil, fail(ErrContractConflict, "resolvedPlan.kit.slug", "Modern identity projection requires modern-homelab")
		}
	}
	homeSiteRef, err := stringField(input, path, "homeSiteRef")
	if err != nil {
		return "", nil, nil, err
	}
	cloudSiteRefs, err := stringListField(input, path, "cloudSiteRefs", true)
	if err != nil || len(cloudSiteRefs) == 0 || !reflect.DeepEqual(cloudSiteRefs, sortStringsUnique(cloudSiteRefs)) {
		return "", nil, nil, fail(ErrContractConflict, path+".cloudSiteRefs", "Modern Cloud Sites must be non-empty, sorted, and unique")
	}
	if sites != nil {
		wantHome, wantCloud, err := exactModernSitePartition(sites, path+".sites")
		if err != nil || homeSiteRef != wantHome || !reflect.DeepEqual(cloudSiteRefs, wantCloud) {
			return "", nil, nil, fail(ErrContractConflict, path, "Modern public projection crossed the exact Site partition")
		}
	}
	partition, err := objectField(input, path, "partition")
	if err != nil {
		return "", nil, nil, err
	}
	maxStale, err := intField(partition, path+".partition", "maxStaleVerificationSeconds")
	if err != nil || maxStale < 0 || maxStale > 86400 || partition["denyNewCrossSiteSessions"] != true {
		return "", nil, nil, fail(ErrContractConflict, path+".partition", "Modern public partition is not fail-closed")
	}
	if role == "home" {
		if len(partition) != 5 || partition["onCloudLoss"] != "local-continues" || partition["onLinkLoss"] != "local-continues" || partition["localIdentityAuthorityAvailable"] != true {
			return "", nil, nil, fail(ErrContractConflict, path+".partition", "Modern Home partition authority changed")
		}
	} else if len(partition) != 3 || partition["cloudEdge"] != "fail-closed" {
		return "", nil, nil, fail(ErrContractConflict, path+".partition", "Modern Cloud partition must fail closed")
	}
	return homeSiteRef, cloudSiteRefs, partition, nil
}

type exactModernProjector func(map[string]any, string, string) (map[string]any, error)

func exactModernPublicItems(input map[string]any, field string, count, fieldCount int, stackID, path string, project exactModernProjector) ([]any, error) {
	items, err := objectListField(input, path, field)
	if err != nil || len(items) != count {
		return nil, fail(ErrContractConflict, path+"."+field, "Modern projection requires exactly %d items", count)
	}
	result, principalCounts := make([]any, 0, count), map[string]int{}
	for index, item := range items {
		itemPath := fmt.Sprintf("%s.%s[%d]", path, field, index)
		if len(item) != fieldCount {
			return nil, fail(ErrContractConflict, itemPath, "item contains fields outside the closed Modern projection")
		}
		validated, err := project(item, stackID, itemPath)
		if err != nil {
			return nil, err
		}
		principalCounts[validated["principal"].(string)]++
		result = append(result, validated)
	}
	if !exactModernPrincipalCounts(principalCounts) {
		return nil, fail(ErrContractConflict, path+"."+field, "Modern projection requires one item per principal")
	}
	sortModernProjection(result)
	return result, nil
}

func projectExactModernIssuer(value map[string]any, stackID, path string) (map[string]any, error) {
	// Public issuers already use lifetimeSeconds; map to the shared validator's source spelling.
	source := make(map[string]any, len(value))
	for key, item := range value {
		source[key] = item
	}
	source["credentialTTLSeconds"] = source["lifetimeSeconds"]
	delete(source, "lifetimeSeconds")
	source["enrollment"] = map[string]any{"mode": source["enrollmentMode"], "exposure": source["enrollmentExposure"]}
	delete(source, "enrollmentMode")
	delete(source, "enrollmentExposure")
	result, err := projectModernIssuer(source, stackID, path)
	if err != nil {
		return nil, err
	}
	if !modernPrincipal(result["principal"].(string)) {
		return nil, fail(ErrContractConflict, path+".principal", "unsupported Modern issuer principal")
	}
	return result, nil
}

func projectExactModernVerifier(value map[string]any, stackID, path string) (map[string]any, error) {
	source := make(map[string]any, len(value))
	for key, item := range value {
		source[key] = item
	}
	source["credentialIssuerRef"] = source["issuerRef"]
	delete(source, "issuerRef")
	result, err := projectModernVerifier(source, stackID, path)
	if err != nil {
		return nil, err
	}
	if !modernPrincipal(result["principal"].(string)) {
		return nil, fail(ErrContractConflict, path+".principal", "unsupported Modern verifier principal")
	}
	return result, nil
}

func projectExactModernDistribution(value map[string]any, stackID, path string) (map[string]any, error) {
	principal, err := stringField(value, path, "principal")
	if err != nil || !modernPrincipal(principal) {
		return nil, fail(ErrContractConflict, path+".principal", "unsupported Modern distribution principal")
	}
	materials, err := stringListField(value, path, "materials", true)
	if err != nil || !reflect.DeepEqual(materials, []string{"revocation-state", "verification-key-reference"}) {
		return nil, fail(ErrContractConflict, path+".materials", "Modern distribution materials changed")
	}
	source := make(map[string]any, len(value))
	for key, item := range value {
		source[key] = item
	}
	source["credentialIssuerRef"] = source["issuerRef"]
	delete(source, "issuerRef")
	return projectModernDistribution(source, principal, stackID, path)
}
