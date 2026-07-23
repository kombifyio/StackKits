package resolvedplan

import (
	"fmt"
	"sort"
)

// projectPublicHomeAccessEnforcement lowers the resolved local-route graph into
// the complete operation-shaped authority of the Home access enforcer. Kit
// identity, Site topology, enrollment configuration, discovery, endpoints,
// credentials, provider lifecycle, and fixed local/default-deny invariants do
// not cross this boundary.
func projectPublicHomeAccessEnforcement(stackID string, network map[string]any, sites []any, path string) (map[string]any, error) {
	if stackID == "" {
		return nil, fail(ErrContractConflict, path+".stackId", "Home access enforcement requires a stack identity")
	}
	local, err := buildLocalReachability(network, sites)
	if err != nil {
		return nil, err
	}
	rawRoutes, err := objectListField(local, path, "routes")
	if err != nil {
		return nil, err
	}
	routes := make([]any, 0, len(rawRoutes))
	for index, route := range rawRoutes {
		routePath := fmt.Sprintf("%s.routes[%d]", path, index)
		access, err := objectField(route, routePath, "access")
		if err != nil {
			return nil, err
		}
		tls, err := objectField(route, routePath, "tls")
		if err != nil {
			return nil, err
		}
		exposure, err := stringField(route, routePath, "exposure")
		if err != nil || exposure != "local" {
			return nil, fail(ErrContractConflict, routePath+".exposure", "Home access projection accepts only local routes")
		}
		defaultClosed, err := requiredBoolField(access, routePath+".access", "defaultClosed")
		if err != nil || !defaultClosed {
			return nil, fail(ErrContractConflict, routePath+".access.defaultClosed", "Home access projection requires default-closed decisions")
		}
		projected := map[string]any{}
		for _, field := range []string{
			"id", "serviceRef", "moduleRef", "originSiteRef", "originNodeRefs",
			"protocol", "upstreamProtocol", "port", "targetPort", "host", "path",
		} {
			if value, present := route[field]; present {
				projected[field] = value
			}
		}
		for _, field := range []string{
			"policyRef", "policyExposure", "authentication", "privilege",
			"enrolledDeviceRequired", "ownerStepUpRequired", "lanStepDown",
			"allowedSiteRefs", "allowedMethods",
		} {
			if value, present := access[field]; present {
				projected[field] = value
			}
		}
		required, err := requiredBoolField(tls, routePath+".tls", "required")
		if err != nil {
			return nil, err
		}
		mode, err := stringField(tls, routePath+".tls", "mode")
		if err != nil {
			return nil, err
		}
		projected["tlsRequired"] = required
		projected["tlsMode"] = mode
		if minVersion, present, err := optionalStringField(tls, routePath+".tls", "minVersion"); err != nil {
			return nil, err
		} else if present {
			projected["tlsMinVersion"] = minVersion
		}
		routes = append(routes, projected)
	}
	return normalizedObject(map[string]any{"stackId": stackID, "routes": routes}, path)
}

// projectPublicLocalAutonomyPolicy atomically derives the only policy document
// available to the local-autonomy owner. Full identity enrollment, data-class,
// Cloud-copy, provider, transport, credential, and general-LAN authority are
// validated upstream and narrowed to finite enforcement decisions here.
func projectPublicLocalAutonomyPolicy(
	stackID string,
	kit map[string]any,
	sites []any,
	controlPlane, identity, data, failurePolicy map[string]any,
	path string,
) (map[string]any, error) {
	if stackID == "" {
		return nil, fail(ErrContractConflict, path+".stackId", "local-autonomy policy requires a stack identity")
	}
	kitSlug, err := stringField(kit, "resolvedPlan.kit", "slug")
	if err != nil {
		return nil, err
	}
	if kitSlug != "basement-kit" && kitSlug != "modern-homelab" {
		return nil, fail(ErrContractConflict, path+".kitSlug", "local-autonomy policy is unavailable to kit %q", kitSlug)
	}

	siteKinds := make(map[string]string, len(sites))
	cloudSiteRefs := make([]string, 0, len(sites))
	for index, rawSite := range sites {
		site, err := asObject(rawSite, fmt.Sprintf("resolvedPlan.sites[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "kind")
		if err != nil || kind != "home" && kind != "cloud" {
			return nil, fail(ErrContractConflict, fmt.Sprintf("resolvedPlan.sites[%d].kind", index), "local autonomy accepts only Home and Cloud Sites")
		}
		if _, exists := siteKinds[id]; exists {
			return nil, fail(ErrContractConflict, fmt.Sprintf("resolvedPlan.sites[%d].id", index), "duplicate Site %q", id)
		}
		siteKinds[id] = kind
		if kind == "cloud" {
			cloudSiteRefs = append(cloudSiteRefs, id)
		}
	}
	sort.Strings(cloudSiteRefs)

	mode, err := stringField(controlPlane, "resolvedPlan.controlPlane", "mode")
	if err != nil {
		return nil, err
	}
	authoritySiteRef, err := stringField(controlPlane, "resolvedPlan.controlPlane", "authoritySiteRef")
	if err != nil {
		return nil, err
	}
	if siteKinds[authoritySiteRef] != "home" {
		return nil, fail(ErrContractConflict, "resolvedPlan.controlPlane.authoritySiteRef", "local autonomy requires one Home authority Site")
	}
	memberNodeRefs, err := stringListField(controlPlane, "resolvedPlan.controlPlane", "members", true)
	if err != nil {
		return nil, err
	}
	memberNodeRefs = sortStringsUnique(memberNodeRefs)
	if err := validateLocalAutonomyControlCardinality(mode, memberNodeRefs, path+".control"); err != nil {
		return nil, err
	}

	humanAuthoritySiteRef, err := stringField(identity, "resolvedPlan.identity", "humanAuthoritySiteRef")
	if err != nil {
		return nil, err
	}
	deviceAuthoritySiteRef, err := stringField(identity, "resolvedPlan.identity", "deviceAuthoritySiteRef")
	if err != nil {
		return nil, err
	}
	if humanAuthoritySiteRef != authoritySiteRef || deviceAuthoritySiteRef != authoritySiteRef {
		return nil, fail(ErrContractConflict, "resolvedPlan.identity", "human and device authority must remain on the exact Home control authority Site")
	}
	possessionBound, err := requiredBoolField(identity, "resolvedPlan.identity", "possessionBoundSessions")
	if err != nil {
		return nil, err
	}
	lanIsIdentity, err := requiredBoolField(identity, "resolvedPlan.identity", "lanLocationIsIdentity")
	if err != nil {
		return nil, err
	}
	enrollment, err := objectField(identity, "resolvedPlan.identity", "deviceEnrollment")
	if err != nil {
		return nil, err
	}
	enrollmentMode, err := stringField(enrollment, "resolvedPlan.identity.deviceEnrollment", "mode")
	if err != nil {
		return nil, err
	}
	enrollmentAuthority, err := stringField(enrollment, "resolvedPlan.identity.deviceEnrollment", "authoritySiteRef")
	if err != nil {
		return nil, err
	}
	if !possessionBound || lanIsIdentity || enrollmentMode != "local-only" || enrollmentAuthority != authoritySiteRef {
		return nil, fail(ErrContractConflict, "resolvedPlan.identity", "local autonomy requires possession-bound, LAN-independent, Home-local identity")
	}
	edgeVerifierSiteRefs, err := stringListField(identity, "resolvedPlan.identity", "edgeVerifierSiteRefs", false)
	if err != nil {
		return nil, err
	}
	edgeVerifierSiteRefs = sortStringsUnique(edgeVerifierSiteRefs)

	defaultAuthoritySiteRef, err := stringField(data, "resolvedPlan.data", "defaultAuthority")
	if err != nil {
		return nil, err
	}
	if defaultAuthoritySiteRef != authoritySiteRef {
		return nil, fail(ErrContractConflict, "resolvedPlan.data.defaultAuthority", "local autonomy requires the Home control authority as default data authority")
	}
	dataBindings, err := projectLocalAutonomyDataBindings(data, siteKinds, authoritySiteRef, path+".data")
	if err != nil {
		return nil, err
	}

	onCloudLoss, err := stringField(failurePolicy, "resolvedPlan.failurePolicy", "onCloudLoss")
	if err != nil {
		return nil, err
	}
	onLinkLoss, err := stringField(failurePolicy, "resolvedPlan.failurePolicy", "onLinkLoss")
	if err != nil {
		return nil, err
	}
	cloudEdge, err := stringField(failurePolicy, "resolvedPlan.failurePolicy", "cloudEdge")
	if err != nil {
		return nil, err
	}
	localIdentityAvailable, err := requiredBoolField(failurePolicy, "resolvedPlan.failurePolicy", "localIdentityAuthorityAvailable")
	if err != nil {
		return nil, err
	}
	maxStale, err := intField(failurePolicy, "resolvedPlan.failurePolicy", "maxStaleVerificationSeconds")
	if err != nil {
		return nil, err
	}
	denyCrossSite, err := requiredBoolField(failurePolicy, "resolvedPlan.failurePolicy", "denyNewCrossSiteSessions")
	if err != nil {
		return nil, err
	}
	if !localIdentityAvailable || !denyCrossSite || onLinkLoss != "local-continues" || maxStale < 0 {
		return nil, fail(ErrContractConflict, "resolvedPlan.failurePolicy", "local autonomy must remain locally authoritative and deny new cross-Site sessions")
	}
	if kitSlug == "basement-kit" {
		if len(cloudSiteRefs) != 0 || len(edgeVerifierSiteRefs) != 0 || onCloudLoss != "not-applicable" ||
			cloudEdge != "not-applicable" || maxStale != 0 {
			return nil, fail(ErrContractConflict, path, "Basement local autonomy cannot acquire Cloud or stale-verification authority")
		}
	} else {
		if len(cloudSiteRefs) == 0 || !sameStringSetFromSlices(edgeVerifierSiteRefs, cloudSiteRefs) ||
			onCloudLoss != "local-continues" || cloudEdge != "fail-closed" {
			return nil, fail(ErrContractConflict, path, "Modern local autonomy requires exact Cloud verifier coverage and a fail-closed Cloud edge")
		}
	}

	return normalizedObject(map[string]any{
		"stackId": stackID,
		"kitSlug": kitSlug,
		"topology": map[string]any{
			"authorityHomeSiteRef": authoritySiteRef,
			"cloudSiteRefs":        stringSliceAny(cloudSiteRefs),
		},
		"control": map[string]any{
			"mode": mode, "authoritySiteRef": authoritySiteRef, "memberNodeRefs": stringSliceAny(memberNodeRefs),
		},
		"identity": map[string]any{
			"authoritySiteRef": authoritySiteRef, "enrollmentMode": enrollmentMode,
			"edgeVerifierSiteRefs":    stringSliceAny(edgeVerifierSiteRefs),
			"possessionBoundSessions": possessionBound, "lanLocationIsIdentity": lanIsIdentity,
			"availableDuringPartition": localIdentityAvailable,
		},
		"data": map[string]any{
			"defaultAuthoritySiteRef": defaultAuthoritySiteRef, "bindings": dataBindings,
		},
		"failure": map[string]any{
			"onCloudLoss": onCloudLoss, "onLinkLoss": onLinkLoss, "cloudEdge": cloudEdge,
			"maxStaleVerificationSeconds": maxStale, "denyNewCrossSiteSessions": denyCrossSite,
		},
	}, path)
}

func projectLocalAutonomyDataBindings(data map[string]any, siteKinds map[string]string, authoritySiteRef, path string) ([]any, error) {
	rawBindings, err := objectField(data, "resolvedPlan.data", "bindings")
	if err != nil {
		return nil, err
	}
	refs := sortedStringMapKeys(rawBindings)
	result := make([]any, 0, len(refs))
	for _, ref := range refs {
		binding, err := asObject(rawBindings[ref], "resolvedPlan.data.bindings."+ref)
		if err != nil {
			return nil, err
		}
		primary, err := stringField(binding, "resolvedPlan.data.bindings."+ref, "primarySiteRef")
		if err != nil {
			return nil, err
		}
		replicas, err := stringListField(binding, "resolvedPlan.data.bindings."+ref, "replicaSiteRefs", false)
		if err != nil {
			return nil, err
		}
		replicas = sortStringsUnique(replicas)
		cloudCopyAllowed, err := requiredBoolField(binding, "resolvedPlan.data.bindings."+ref, "cloudCopyAllowed")
		if err != nil {
			return nil, err
		}
		cloudPlacement := siteKinds[primary] == "cloud"
		for _, replica := range replicas {
			cloudPlacement = cloudPlacement || siteKinds[replica] == "cloud"
		}
		projected := map[string]any{
			"bindingRef": ref, "primarySiteRef": primary, "replicaSiteRefs": stringSliceAny(replicas),
			"cloudPlacement": "denied",
		}
		policy, hasPolicy, err := optionalObjectField(binding, "resolvedPlan.data.bindings."+ref, "cloudCopyPolicy")
		if err != nil {
			return nil, err
		}
		if cloudPlacement || cloudCopyAllowed {
			if !cloudCopyAllowed || !hasPolicy {
				return nil, fail(ErrContractConflict, path+".bindings."+ref, "Cloud placement requires an explicit compiler-validated policy")
			}
			policyRef, err := stringField(policy, "resolvedPlan.data.bindings."+ref+".cloudCopyPolicy", "policyRef")
			if err != nil {
				return nil, err
			}
			allowPrimary, err := requiredBoolField(policy, "resolvedPlan.data.bindings."+ref+".cloudCopyPolicy", "allowPrimary")
			if err != nil {
				return nil, err
			}
			allowReplicas, err := requiredBoolField(policy, "resolvedPlan.data.bindings."+ref+".cloudCopyPolicy", "allowReplicas")
			if err != nil {
				return nil, err
			}
			if siteKinds[primary] == "cloud" && !allowPrimary {
				return nil, fail(ErrContractConflict, path+".bindings."+ref, "Cloud primary is not authorized")
			}
			for _, replica := range replicas {
				if siteKinds[replica] == "cloud" && !allowReplicas {
					return nil, fail(ErrContractConflict, path+".bindings."+ref, "Cloud replica is not authorized")
				}
			}
			projected["cloudPlacement"] = "policy-authorized"
			projected["cloudCopyPolicyRef"] = policyRef
		} else if hasPolicy {
			return nil, fail(ErrContractConflict, path+".bindings."+ref, "Home-only placement cannot carry an unused Cloud-copy policy")
		}
		if siteKinds[primary] == "" || primary != authoritySiteRef && siteKinds[primary] != "cloud" {
			return nil, fail(ErrContractConflict, path+".bindings."+ref+".primarySiteRef", "data primary must be the Home authority or an explicitly authorized Cloud Site")
		}
		result = append(result, projected)
	}
	return result, nil
}

func validateLocalAutonomyControlCardinality(mode string, members []string, path string) error {
	valid := mode == "single" && len(members) == 1 ||
		mode == "warm-standby" && len(members) >= 2 ||
		mode == "quorum" && (len(members) == 3 || len(members) == 5 || len(members) == 7)
	if !valid {
		return fail(ErrContractConflict, path, "control-plane member cardinality does not match mode %q", mode)
	}
	return nil
}

func sameStringSetFromSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
